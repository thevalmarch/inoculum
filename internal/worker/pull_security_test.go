package worker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	inoculumcrypto "github.com/thevalmarch/inoculum/internal/crypto"
	"github.com/thevalmarch/inoculum/internal/types"
)

func TestPullWorkerUsesBearerTokenAndSharedPersistedTrust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		requests.Add(1)
		if got := request.Header.Get("Authorization"); got != "Bearer worker-secret" {
			t.Errorf("Authorization = %q", got)
		}
		for _, removed := range []string{"X-Inoculum-Token", "X-Inoculum-Nonce", "X-Inoculum-Timestamp"} {
			if got := request.Header.Get(removed); got != "" {
				t.Errorf("removed header %s = %q", removed, got)
			}
		}
		json.NewEncoder(writer).Encode(types.PullClaimResponse{Status: types.PullNoTask})
	}))
	identityDir := t.TempDir()
	identity, err := inoculumcrypto.LoadOrCreateCoordinatorIdentity(inoculumcrypto.IdentityOptions{
		CertFile: filepath.Join(identityDir, "cert.pem"),
		KeyFile:  filepath.Join(identityDir, "key.pem"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{identity.Certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	defer server.Close()
	parsed, _ := url.Parse(server.URL)
	trustFile := filepath.Join(t.TempDir(), "trusted-coordinator")

	first, err := NewPullWorker(PullConfig{
		CoordinatorAddr: parsed.Host,
		WorkerID:        "worker-a",
		Token:           "worker-secret",
		Fingerprint:     identity.Fingerprint,
		TrustFile:       trustFile,
		Concurrency:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := first.claim(context.Background()); err != nil || status != types.PullNoTask {
		t.Fatalf("first claim status=%q error=%v", status, err)
	}

	second, err := NewPullWorker(PullConfig{
		CoordinatorAddr: parsed.Host,
		WorkerID:        "worker-b",
		Token:           "worker-secret",
		TrustFile:       trustFile,
		Concurrency:     1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, status, err := second.claim(context.Background()); err != nil || status != types.PullNoTask {
		t.Fatalf("subsequent claim status=%q error=%v", status, err)
	}
	if requests.Load() != 2 {
		t.Fatalf("requests = %d, want 2", requests.Load())
	}
}

func TestPullWorkerWithoutTrustFailsBeforeConnecting(t *testing.T) {
	_, err := NewPullWorker(PullConfig{
		CoordinatorAddr: "127.0.0.1:1",
		WorkerID:        "worker-a",
		Token:           "secret",
		TrustFile:       filepath.Join(t.TempDir(), "trusted-coordinator"),
		Concurrency:     1,
	})
	if err == nil || !strings.Contains(err.Error(), "no coordinator identity is trusted yet") {
		t.Fatalf("error = %v", err)
	}
}
