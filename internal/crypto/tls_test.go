package crypto

import (
	"crypto/tls"
	"crypto/x509"
	"io"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestCoordinatorIdentityFirstCreationAndStableReload(t *testing.T) {
	dir := t.TempDir()
	options := IdentityOptions{
		CertFile: filepath.Join(dir, "coordinator-cert.pem"),
		KeyFile:  filepath.Join(dir, "coordinator-key.pem"),
	}
	created, err := LoadOrCreateCoordinatorIdentity(options)
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Migrated || created.Fingerprint == "" {
		t.Fatalf("created identity = %#v", created)
	}
	certBefore, _ := os.ReadFile(options.CertFile)
	keyBefore, _ := os.ReadFile(options.KeyFile)

	loaded, err := LoadOrCreateCoordinatorIdentity(options)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Created || loaded.Migrated || loaded.Fingerprint != created.Fingerprint {
		t.Fatalf("loaded identity = %#v, created fingerprint %q", loaded, created.Fingerprint)
	}
	certAfter, _ := os.ReadFile(options.CertFile)
	keyAfter, _ := os.ReadFile(options.KeyFile)
	if string(certAfter) != string(certBefore) || string(keyAfter) != string(keyBefore) {
		t.Fatal("stable reload changed coordinator identity files")
	}
	if loaded.Certificate.Leaf == nil || len(loaded.Certificate.Leaf.ExtKeyUsage) != 1 || loaded.Certificate.Leaf.ExtKeyUsage[0] != x509.ExtKeyUsageServerAuth {
		t.Fatalf("unexpected certificate extended key usage: %#v", loaded.Certificate.Leaf)
	}
}

func TestCoordinatorIdentityRejectsPartialCorruptAndMismatchedState(t *testing.T) {
	t.Run("partial", func(t *testing.T) {
		dir := t.TempDir()
		cert := filepath.Join(dir, "coordinator-cert.pem")
		key := filepath.Join(dir, "coordinator-key.pem")
		if err := os.WriteFile(cert, []byte("certificate"), 0o600); err != nil {
			t.Fatal(err)
		}
		_, err := LoadOrCreateCoordinatorIdentity(IdentityOptions{CertFile: cert, KeyFile: key})
		if err == nil || !strings.Contains(err.Error(), "partial") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		dir := t.TempDir()
		cert := filepath.Join(dir, "coordinator-cert.pem")
		key := filepath.Join(dir, "coordinator-key.pem")
		os.WriteFile(cert, []byte("not a certificate"), 0o600)
		os.WriteFile(key, []byte("not a key"), 0o600)
		_, err := LoadOrCreateCoordinatorIdentity(IdentityOptions{CertFile: cert, KeyFile: key})
		if err == nil || !strings.Contains(err.Error(), "load coordinator certificate/key pair") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("mismatched", func(t *testing.T) {
		firstDir, secondDir := t.TempDir(), t.TempDir()
		first := IdentityOptions{CertFile: filepath.Join(firstDir, "cert.pem"), KeyFile: filepath.Join(firstDir, "key.pem")}
		second := IdentityOptions{CertFile: filepath.Join(secondDir, "cert.pem"), KeyFile: filepath.Join(secondDir, "key.pem")}
		if _, err := LoadOrCreateCoordinatorIdentity(first); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateCoordinatorIdentity(second); err != nil {
			t.Fatal(err)
		}
		secondKey, _ := os.ReadFile(second.KeyFile)
		if err := os.WriteFile(first.KeyFile, secondKey, 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateCoordinatorIdentity(first); err == nil {
			t.Fatal("mismatched certificate and key unexpectedly loaded")
		}
	})
}

func TestCoordinatorIdentityMigratesCompleteLegacyPairWithoutOverwrite(t *testing.T) {
	legacyDir, configDir := t.TempDir(), t.TempDir()
	legacyOptions := IdentityOptions{CertFile: filepath.Join(legacyDir, "legacy-cert.pem"), KeyFile: filepath.Join(legacyDir, "legacy-key.pem")}
	legacy, err := LoadOrCreateCoordinatorIdentity(legacyOptions)
	if err != nil {
		t.Fatal(err)
	}
	options := IdentityOptions{
		CertFile:       filepath.Join(configDir, "coordinator-cert.pem"),
		KeyFile:        filepath.Join(configDir, "coordinator-key.pem"),
		LegacyCertFile: legacyOptions.CertFile,
		LegacyKeyFile:  legacyOptions.KeyFile,
	}
	migrated, err := LoadOrCreateCoordinatorIdentity(options)
	if err != nil {
		t.Fatal(err)
	}
	if !migrated.Migrated || migrated.Created || migrated.Fingerprint != legacy.Fingerprint {
		t.Fatalf("migrated identity = %#v, legacy fingerprint %q", migrated, legacy.Fingerprint)
	}
	if _, err := os.Stat(legacyOptions.CertFile); err != nil {
		t.Fatalf("legacy certificate was not preserved: %v", err)
	}

	otherLegacyDir := t.TempDir()
	otherOptions := IdentityOptions{CertFile: filepath.Join(otherLegacyDir, "cert.pem"), KeyFile: filepath.Join(otherLegacyDir, "key.pem")}
	other, err := LoadOrCreateCoordinatorIdentity(otherOptions)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := LoadOrCreateCoordinatorIdentity(IdentityOptions{
		CertFile: options.CertFile, KeyFile: options.KeyFile,
		LegacyCertFile: otherOptions.CertFile, LegacyKeyFile: otherOptions.KeyFile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Fingerprint != legacy.Fingerprint || reloaded.Fingerprint == other.Fingerprint {
		t.Fatal("existing config-directory identity was overwritten by legacy state")
	}
}

func TestCoordinatorIdentityRejectsPartialLegacyState(t *testing.T) {
	dir := t.TempDir()
	legacyCert := filepath.Join(dir, "legacy-cert.pem")
	if err := os.WriteFile(legacyCert, []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := LoadOrCreateCoordinatorIdentity(IdentityOptions{
		CertFile: filepath.Join(dir, "new", "cert.pem"), KeyFile: filepath.Join(dir, "new", "key.pem"),
		LegacyCertFile: legacyCert, LegacyKeyFile: filepath.Join(dir, "legacy-key.pem"),
	})
	if err == nil || !strings.Contains(err.Error(), "legacy coordinator identity is partial") {
		t.Fatalf("error = %v", err)
	}
}

func TestExplicitTrustPersistsAndIsSharedAcrossAddresses(t *testing.T) {
	server, fingerprint, calls := pinnedTestServer(t)
	defer server.Close()
	trustFile := filepath.Join(t.TempDir(), "trusted-coordinator")

	if _, err := NewCoordinatorClientConfig(trustFile, ""); err == nil {
		t.Fatal("client without stored or explicit trust unexpectedly succeeded")
	} else if !strings.Contains(err.Error(), "no coordinator identity is trusted yet") {
		t.Fatalf("unexpected no-trust error: %v", err)
	}

	requestWithTrust(t, server.URL, trustFile, fingerprint, true)
	if got := calls.Load(); got != 1 {
		t.Fatalf("handler calls = %d, want 1", got)
	}
	requestWithTrust(t, server.URL, trustFile, "", true)
	if got := calls.Load(); got != 2 {
		t.Fatalf("handler calls = %d, want 2", got)
	}

	changedAddress := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
	requestWithTrust(t, changedAddress, trustFile, "", true)
	if got := calls.Load(); got != 3 {
		t.Fatalf("handler calls = %d, want 3", got)
	}
}

func TestLegacyTOFURecordIsNeverImportedAsTrust(t *testing.T) {
	dir := t.TempDir()
	legacy := filepath.Join(dir, ".inoculum-worker-known-hosts")
	legacyFingerprint := strings.Repeat("AA", 32)
	if err := os.WriteFile(legacy, []byte("192.168.0.5:8080 "+legacyFingerprint+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := NewCoordinatorClientConfig(filepath.Join(dir, "trusted-coordinator"), "")
	if err == nil || !strings.Contains(err.Error(), "no coordinator identity is trusted yet") {
		t.Fatalf("legacy TOFU state unexpectedly established trust: %v", err)
	}
}

func TestExplicitTrustRecoversInvalidTrustRecordAfterVerification(t *testing.T) {
	server, fingerprint, _ := pinnedTestServer(t)
	defer server.Close()
	trustFile := filepath.Join(t.TempDir(), "trusted-coordinator")
	if err := os.WriteFile(trustFile, []byte("corrupt trust record\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewCoordinatorClientConfig(trustFile, ""); err == nil || !strings.Contains(err.Error(), "is invalid") {
		t.Fatalf("invalid trust record error = %v", err)
	}
	requestWithTrust(t, server.URL, trustFile, fingerprint, true)
	requestWithTrust(t, server.URL, trustFile, "", true)
}

func TestWrongFingerprintAndChangedIdentityAreRejectedBeforeHTTP(t *testing.T) {
	server, _, calls := pinnedTestServer(t)
	defer server.Close()
	other, otherFingerprint, _ := pinnedTestServer(t)
	defer other.Close()
	trustFile := filepath.Join(t.TempDir(), "trusted-coordinator")

	requestWithTrust(t, server.URL, trustFile, otherFingerprint, false)
	if got := calls.Load(); got != 0 {
		t.Fatalf("HTTP handler observed %d requests after fingerprint mismatch", got)
	}

	requestWithTrust(t, other.URL, trustFile, otherFingerprint, true)
	requestWithTrust(t, server.URL, trustFile, "", false)
	if got := calls.Load(); got != 0 {
		t.Fatalf("changed identity reached handler %d times", got)
	}
}

func TestExplicitFingerprintReplacesStoredTrustOnlyAfterVerification(t *testing.T) {
	first, firstFingerprint, _ := pinnedTestServer(t)
	defer first.Close()
	second, secondFingerprint, _ := pinnedTestServer(t)
	defer second.Close()
	trustFile := filepath.Join(t.TempDir(), "trusted-coordinator")

	requestWithTrust(t, first.URL, trustFile, firstFingerprint, true)
	requestWithTrust(t, second.URL, trustFile, secondFingerprint, true)
	requestWithTrust(t, second.URL, trustFile, "", true)
	requestWithTrust(t, first.URL, trustFile, "", false)
}

func TestClientConfigUsesVerifyConnection(t *testing.T) {
	trustFile := filepath.Join(t.TempDir(), "trusted-coordinator")
	if err := os.WriteFile(trustFile, []byte(strings.Repeat("AA:", 31)+"AA\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	config, err := NewCoordinatorClientConfig(trustFile, "")
	if err != nil {
		t.Fatal(err)
	}
	if !config.InsecureSkipVerify || config.VerifyConnection == nil || config.VerifyPeerCertificate != nil {
		t.Fatalf("TLS verification config = %#v", config)
	}
}

func pinnedTestServer(t *testing.T) (*httptest.Server, string, *atomic.Int32) {
	t.Helper()
	calls := &atomic.Int32{}
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
		writer.WriteHeader(http.StatusNoContent)
	}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	identityDir := t.TempDir()
	identity, err := LoadOrCreateCoordinatorIdentity(IdentityOptions{
		CertFile: filepath.Join(identityDir, "cert.pem"),
		KeyFile:  filepath.Join(identityDir, "key.pem"),
	})
	if err != nil {
		t.Fatal(err)
	}
	server.TLS = &tls.Config{Certificates: []tls.Certificate{identity.Certificate}, MinVersion: tls.VersionTLS12}
	server.StartTLS()
	return server, Fingerprint(server.Certificate().Raw), calls
}

func requestWithTrust(t *testing.T, url, trustFile, explicit string, wantSuccess bool) {
	t.Helper()
	config, err := NewCoordinatorClientConfig(trustFile, explicit)
	if err != nil {
		if wantSuccess {
			t.Fatalf("NewCoordinatorClientConfig() error: %v", err)
		}
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = config
	client := &http.Client{Transport: transport}
	response, err := client.Get(url)
	transport.CloseIdleConnections()
	if wantSuccess {
		if err != nil {
			t.Fatalf("trusted request failed: %v", err)
		}
		response.Body.Close()
		return
	}
	if err == nil {
		response.Body.Close()
		t.Fatal("untrusted request unexpectedly succeeded")
	}
	if !strings.Contains(err.Error(), "coordinator identity mismatch") {
		t.Fatalf("unexpected trust error: %v", err)
	}
}
