package worker

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/thevalmarch/inoculum/internal/types"
)

func TestDiagnosticSleep(t *testing.T) {
	executor := NewExecutor()
	output, duration, err := executor.Execute("diagnostic_sleep", "20ms")
	if err != nil {
		t.Fatalf("Execute() error: %v", err)
	}
	if output != "slept for 20ms" {
		t.Fatalf("output = %q", output)
	}
	if duration < 20*time.Millisecond {
		t.Fatalf("duration = %s, want at least 20ms", duration)
	}

	if _, _, err := executor.Execute("diagnostic_sleep", "not-a-duration"); err == nil {
		t.Fatal("invalid duration was accepted")
	}
}

func decodeProbeOutput(t *testing.T, output string) types.HTTPProbeOutput {
	t.Helper()
	var result types.HTTPProbeOutput
	if err := json.Unmarshal([]byte(output), &result); err != nil {
		t.Fatalf("decode probe output %q: %v", output, err)
	}
	return result
}

func TestHTTPProbeSuccessUsesHEADAndDoesNotForwardAuthorization(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead {
			t.Errorf("method = %s, want HEAD", request.Method)
		}
		if authorization := request.Header.Get("Authorization"); authorization != "" {
			t.Errorf("Authorization was forwarded: %q", authorization)
		}
		writer.Header().Set("Content-Length", "123")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	output, _, err := NewExecutor().Execute("http_probe", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result := decodeProbeOutput(t, output)
	if result.StatusCode != http.StatusOK || result.FinalURL != server.URL || result.DeclaredContentLength == nil || *result.DeclaredContentLength != 123 {
		t.Fatalf("probe result = %#v", result)
	}
}

func TestHTTPProbeHTTPSUsesNormalCertificateVerification(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	output, _, err := NewExecutor().Execute("http_probe", server.URL)
	if err == nil {
		t.Fatal("untrusted test certificate was accepted")
	}
	if result := decodeProbeOutput(t, output); result.ErrorCategory != "tls" {
		t.Fatalf("TLS failure = %#v", result)
	}

	roots := x509.NewCertPool()
	roots.AddCert(server.Certificate())
	executor := NewExecutor()
	executor.probeTransport = &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	output, _, err = executor.Execute("http_probe", server.URL)
	if err != nil {
		t.Fatal(err)
	}
	result := decodeProbeOutput(t, output)
	if result.StatusCode != http.StatusOK || result.TLSCertificateExpiry == "" {
		t.Fatalf("HTTPS result = %#v", result)
	}
}

func TestHTTPProbeRejectsMalformedUnsafeURLs(t *testing.T) {
	for _, test := range []struct {
		input    string
		category string
	}{
		{input: "not a URL", category: "invalid_url"},
		{input: "ftp://example.com/file", category: "unsupported_scheme"},
		{input: "https://user:password@example.com/", category: "url_credentials"},
	} {
		output, _, err := NewExecutor().Execute("http_probe", test.input)
		if err == nil {
			t.Fatalf("%q was accepted", test.input)
		}
		if result := decodeProbeOutput(t, output); result.ErrorCategory != test.category {
			t.Fatalf("%q result = %#v", test.input, result)
		}
	}
}

func TestHTTPProbeRedirectsAreBounded(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		next := strings.Count(request.URL.Path, "next") + 1
		http.Redirect(writer, request, strings.Repeat("/next", next), http.StatusFound)
	}))
	defer server.Close()

	output, _, err := NewExecutor().Execute("http_probe", server.URL)
	if err == nil {
		t.Fatal("unbounded redirect chain succeeded")
	}
	if result := decodeProbeOutput(t, output); result.ErrorCategory != "redirect_limit" {
		t.Fatalf("redirect result = %#v", result)
	}
}

func TestHTTPProbeTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		time.Sleep(100 * time.Millisecond)
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	executor := NewExecutor()
	executor.probeTimeout = 20 * time.Millisecond

	output, _, err := executor.Execute("http_probe", server.URL)
	if err == nil {
		t.Fatal("timed out probe succeeded")
	}
	if result := decodeProbeOutput(t, output); result.ErrorCategory != "timeout" {
		t.Fatalf("timeout result = %#v", result)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHTTPProbeClassifiesDNSFailure(t *testing.T) {
	executor := NewExecutor()
	executor.probeTransport = roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, &net.DNSError{Err: "not found", Name: "missing.example", IsNotFound: true}
	})
	output, _, err := executor.Execute("http_probe", "https://missing.example/")
	if err == nil {
		t.Fatal("DNS failure succeeded")
	}
	if result := decodeProbeOutput(t, output); result.ErrorCategory != "dns" {
		t.Fatalf("DNS result = %#v", result)
	}
}

type unreadableBody struct {
	read bool
}

func (body *unreadableBody) Read([]byte) (int, error) {
	body.read = true
	return 0, errors.New("body must not be read")
}

func (*unreadableBody) Close() error { return nil }

func TestHTTPProbeDoesNotReadResponseBody(t *testing.T) {
	body := &unreadableBody{}
	executor := NewExecutor()
	executor.probeTransport = roundTripFunc(func(request *http.Request) (*http.Response, error) {
		return &http.Response{
			StatusCode: http.StatusOK,
			Body:       body,
			Request:    request,
			Header:     make(http.Header),
		}, nil
	})
	if _, _, err := executor.Execute("http_probe", "https://example.com/"); err != nil {
		t.Fatal(err)
	}
	if body.read {
		t.Fatal("HTTP probe read the response body")
	}
}

func TestRemovedExecutorsAreNotProductExecutors(t *testing.T) {
	for _, taskType := range []string{"http_fetch", "file_analyze", "dummy"} {
		if _, _, err := NewExecutor().Execute(taskType, "input"); err == nil || !strings.Contains(err.Error(), "unknown task type") {
			t.Fatalf("%s error = %v", taskType, err)
		}
	}
}

var _ io.ReadCloser = (*unreadableBody)(nil)
