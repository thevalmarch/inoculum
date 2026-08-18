package auth

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestBearerAuthentication(t *testing.T) {
	tests := []struct {
		name          string
		authorization string
		legacyToken   string
		wantStatus    int
	}{
		{name: "correct", authorization: "Bearer correct-secret", wantStatus: http.StatusNoContent},
		{name: "case insensitive scheme", authorization: "bearer correct-secret", wantStatus: http.StatusNoContent},
		{name: "wrong", authorization: "Bearer wrong-secret", wantStatus: http.StatusUnauthorized},
		{name: "missing", wantStatus: http.StatusUnauthorized},
		{name: "malformed", authorization: "Bearer", wantStatus: http.StatusUnauthorized},
		{name: "legacy header removed", legacyToken: "correct-secret", wantStatus: http.StatusUnauthorized},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			handler := WithBearerAuth("correct-secret", func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(http.StatusNoContent)
			})
			request := httptest.NewRequest(http.MethodPost, "/worker/claim", nil)
			request.Header.Set("Authorization", test.authorization)
			request.Header.Set("X-Inoculum-Token", test.legacyToken)
			recorder := httptest.NewRecorder()
			handler(recorder, request)
			if recorder.Code != test.wantStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, test.wantStatus)
			}
		})
	}
}

func TestDuplicateAuthorizationHeadersAreRejected(t *testing.T) {
	handler := WithBearerAuth("correct-secret", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodPost, "/worker/claim", nil)
	request.Header.Add("Authorization", "Bearer correct-secret")
	request.Header.Add("Authorization", "Bearer correct-secret")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusUnauthorized)
	}
}

func TestAuthenticationDoesNotRequireNonceTimestampOrClock(t *testing.T) {
	handler := WithBearerAuth("secret", func(writer http.ResponseWriter, request *http.Request) {
		writer.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	request.Header.Set("Authorization", "Bearer secret")
	// Deliberately absurd legacy values have no effect because authentication
	// no longer depends on client wall-clock time or replay headers.
	request.Header.Set("X-Inoculum-Timestamp", "-62135596800")
	request.Header.Set("X-Inoculum-Nonce", "duplicate-or-empty")
	recorder := httptest.NewRecorder()
	handler(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", recorder.Code, http.StatusNoContent)
	}
}

func TestAuthenticationLogsDoNotLeakSecretsOrHeaders(t *testing.T) {
	var output bytes.Buffer
	previousWriter := log.Writer()
	previousFlags := log.Flags()
	log.SetOutput(&output)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(previousWriter)
		log.SetFlags(previousFlags)
	})

	handler := WithBearerAuth("server-secret-value", func(http.ResponseWriter, *http.Request) {})
	request := httptest.NewRequest(http.MethodGet, "/status", nil)
	request.Header.Set("Authorization", "Bearer presented-secret-value")
	recorder := httptest.NewRecorder()
	handler(recorder, request)

	logged := output.String()
	for _, forbidden := range []string{"server-secret-value", "presented-secret-value", "Authorization", "Bearer"} {
		if strings.Contains(logged, forbidden) {
			t.Fatalf("log leaked %q: %s", forbidden, logged)
		}
	}
	if !strings.Contains(logged, "Authentication failed") || !strings.Contains(logged, "/status") {
		t.Fatalf("sanitized failure detail missing: %s", logged)
	}
}
