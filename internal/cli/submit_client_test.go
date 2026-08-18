package cli

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	inoculumcrypto "github.com/thevalmarch/inoculum/internal/crypto"
	"github.com/thevalmarch/inoculum/internal/types"
)

func TestJobProgress(t *testing.T) {
	job := types.PullJobResponse{Tasks: []types.PullJobTask{
		{State: "queued"},
		{State: "leased"},
		{State: "completed"},
		{State: "failed"},
	}}

	if got, want := jobProgress(job), "queued=1 leased=1 completed=1 failed=1"; got != want {
		t.Fatalf("jobProgress() = %q, want %q", got, want)
	}
}

func TestClientWaitTimeoutDoesNotCallJobFailed(t *testing.T) {
	err := clientWaitTimeout("job-1", 2*time.Minute, context.DeadlineExceeded)
	message := err.Error()
	for _, fragment := range []string{
		"client stopped waiting",
		"job-1",
		"coordinator job was not marked failed",
	} {
		if !strings.Contains(message, fragment) {
			t.Fatalf("timeout error %q does not contain %q", message, fragment)
		}
	}
}

func TestSubmitClientUsesBearerWithoutReplayHeaders(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if got := request.Header.Get("Authorization"); got != "Bearer submit-secret" {
			t.Errorf("Authorization = %q", got)
		}
		for _, removed := range []string{"X-Inoculum-Token", "X-Inoculum-Nonce", "X-Inoculum-Timestamp"} {
			if got := request.Header.Get(removed); got != "" {
				t.Errorf("removed header %s = %q", removed, got)
			}
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"job_id":"job-1","task_ids":["task-1"]}`))
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
	tlsConfig, err := inoculumcrypto.NewCoordinatorClientConfig(filepath.Join(t.TempDir(), "trusted-coordinator"), identity.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig
	submitClient := &client{address: parsed.Host, token: "submit-secret", http: &http.Client{Transport: transport}}

	response, err := submitClient.submit(context.Background(), types.PullSubmitRequest{TaskType: "diagnostic_sleep", Inputs: []string{"1ms"}})
	if err != nil {
		t.Fatal(err)
	}
	if response.JobID != "job-1" || len(response.TaskIDs) != 1 {
		t.Fatalf("response = %#v", response)
	}
}
