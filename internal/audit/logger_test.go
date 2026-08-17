package audit

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAuditLoggingIsOptionalAndSanitizedByCaller(t *testing.T) {
	if err := InitLogger(""); err != nil {
		t.Fatal(err)
	}
	LogEvent("auth_failure", "127.0.0.1", "401", "Authentication failed", map[string]any{"path": "/status"})

	path := filepath.Join(t.TempDir(), "audit.log")
	if err := InitLogger(path); err != nil {
		t.Fatal(err)
	}
	LogEvent("auth_failure", "127.0.0.1", "401", "Authentication failed", map[string]any{"path": "/status"})
	if err := Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	for _, required := range []string{"auth_failure", "127.0.0.1", "/status"} {
		if !strings.Contains(text, required) {
			t.Fatalf("audit log missing %q: %s", required, text)
		}
	}
	for _, forbidden := range []string{"Authorization", "Bearer", "token"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("audit log leaked %q: %s", forbidden, text)
		}
	}
}
