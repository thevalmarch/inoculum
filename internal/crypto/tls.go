package crypto

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"log"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// IdentityOptions describes the stable coordinator identity and the one
// legacy working-directory pair that may be migrated into it.
type IdentityOptions struct {
	CertFile       string
	KeyFile        string
	LegacyCertFile string
	LegacyKeyFile  string
}

// CoordinatorIdentity is the coordinator's stable TLS identity.
type CoordinatorIdentity struct {
	Certificate tls.Certificate
	Fingerprint string
	CertFile    string
	KeyFile     string
	Created     bool
	Migrated    bool
}

// LoadOrCreateCoordinatorIdentity strictly loads an existing identity,
// safely migrates a complete valid legacy pair, or generates an identity only
// when neither current nor legacy state exists. Partial or corrupt state is
// always an error and is never replaced automatically.
func LoadOrCreateCoordinatorIdentity(options IdentityOptions) (CoordinatorIdentity, error) {
	if options.CertFile == "" || options.KeyFile == "" {
		return CoordinatorIdentity{}, errors.New("coordinator certificate and key paths are required")
	}

	certExists, err := fileExists(options.CertFile)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("inspect coordinator certificate %s: %w", options.CertFile, err)
	}
	keyExists, err := fileExists(options.KeyFile)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("inspect coordinator key %s: %w", options.KeyFile, err)
	}
	if certExists != keyExists {
		return CoordinatorIdentity{}, partialIdentityError(options.CertFile, options.KeyFile)
	}
	if certExists {
		return loadIdentity(options.CertFile, options.KeyFile)
	}

	legacyCertExists, err := optionalFileExists(options.LegacyCertFile)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("inspect legacy coordinator certificate %s: %w", options.LegacyCertFile, err)
	}
	legacyKeyExists, err := optionalFileExists(options.LegacyKeyFile)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("inspect legacy coordinator key %s: %w", options.LegacyKeyFile, err)
	}
	if legacyCertExists != legacyKeyExists {
		return CoordinatorIdentity{}, fmt.Errorf("legacy %w", partialIdentityError(options.LegacyCertFile, options.LegacyKeyFile))
	}
	if legacyCertExists {
		legacy, err := loadIdentity(options.LegacyCertFile, options.LegacyKeyFile)
		if err != nil {
			return CoordinatorIdentity{}, fmt.Errorf("legacy coordinator identity is not safe to migrate: %w", err)
		}
		certPEM, err := os.ReadFile(options.LegacyCertFile)
		if err != nil {
			return CoordinatorIdentity{}, fmt.Errorf("read legacy coordinator certificate: %w", err)
		}
		keyPEM, err := os.ReadFile(options.LegacyKeyFile)
		if err != nil {
			return CoordinatorIdentity{}, fmt.Errorf("read legacy coordinator key: %w", err)
		}
		if err := persistIdentity(options.CertFile, options.KeyFile, certPEM, keyPEM); err != nil {
			return CoordinatorIdentity{}, fmt.Errorf("migrate coordinator identity: %w", err)
		}
		legacy.CertFile = options.CertFile
		legacy.KeyFile = options.KeyFile
		legacy.Migrated = true
		return legacy, nil
	}

	certPEM, keyPEM, err := generateIdentityPEM()
	if err != nil {
		return CoordinatorIdentity{}, err
	}
	if err := persistIdentity(options.CertFile, options.KeyFile, certPEM, keyPEM); err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("persist new coordinator identity: %w", err)
	}
	identity, err := loadIdentity(options.CertFile, options.KeyFile)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("load new coordinator identity: %w", err)
	}
	identity.Created = true
	return identity, nil
}

func loadIdentity(certFile, keyFile string) (CoordinatorIdentity, error) {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("read coordinator certificate %s: %w", certFile, err)
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("read coordinator key %s: %w", keyFile, err)
	}
	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("load coordinator certificate/key pair: %w", err)
	}
	if len(tlsCert.Certificate) == 0 {
		return CoordinatorIdentity{}, errors.New("coordinator certificate contains no leaf certificate")
	}
	cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
	if err != nil {
		return CoordinatorIdentity{}, fmt.Errorf("parse coordinator certificate: %w", err)
	}
	tlsCert.Leaf = cert
	return CoordinatorIdentity{
		Certificate: tlsCert,
		Fingerprint: Fingerprint(cert.Raw),
		CertFile:    certFile,
		KeyFile:     keyFile,
	}, nil
}

func generateIdentityPEM() ([]byte, []byte, error) {
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, fmt.Errorf("generate coordinator private key: %w", err)
	}
	serialLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serial, err := rand.Int(rand.Reader, serialLimit)
	if err != nil {
		return nil, nil, fmt.Errorf("generate certificate serial number: %w", err)
	}
	template := x509.Certificate{
		SerialNumber: serial,
		Subject: pkix.Name{
			Organization: []string{"Inoculum Coordinator"},
		},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("create coordinator certificate: %w", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal coordinator private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), nil
}

func persistIdentity(certFile, keyFile string, certPEM, keyPEM []byte) error {
	if filepath.Dir(certFile) != filepath.Dir(keyFile) {
		return errors.New("coordinator certificate and key must use the same directory")
	}
	if err := os.MkdirAll(filepath.Dir(certFile), 0o700); err != nil {
		return fmt.Errorf("create identity directory %s: %w", filepath.Dir(certFile), err)
	}
	// Write the private key first. If the second write fails, the resulting
	// partial state deliberately fails loudly on the next startup.
	if err := writeExclusive(keyFile, keyPEM, 0o600); err != nil {
		return err
	}
	if err := writeExclusive(certFile, certPEM, 0o644); err != nil {
		return err
	}
	return nil
}

func writeExclusive(path string, data []byte, mode os.FileMode) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
	if err != nil {
		return fmt.Errorf("create %s: %w", path, err)
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return fmt.Errorf("write %s: %w", path, writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close %s: %w", path, closeErr)
	}
	return nil
}

func partialIdentityError(certFile, keyFile string) error {
	return fmt.Errorf("coordinator identity is partial: certificate and key must both exist (%s, %s)", certFile, keyFile)
}

func optionalFileExists(path string) (bool, error) {
	if path == "" {
		return false, nil
	}
	return fileExists(path)
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	return false, err
}

// Fingerprint returns the existing Inoculum SHA-256 certificate fingerprint
// format, preserving compatibility with fingerprints already shown to users.
func Fingerprint(rawCertificate []byte) string {
	hash := sha256.Sum256(rawCertificate)
	return formatFingerprint(strings.ToUpper(hex.EncodeToString(hash[:])))
}

// NoTrustedCoordinatorError means a client refused to connect before sending
// authentication because no explicit or stored identity was available.
type NoTrustedCoordinatorError struct{ TrustFile string }

func (e *NoTrustedCoordinatorError) Error() string {
	return fmt.Sprintf("no coordinator identity is trusted yet (trust record: %s); provide --coordinator-fingerprint <fingerprint>", e.TrustFile)
}

// IdentityMismatchError means TLS reached a peer whose identity did not match
// the explicit or stored coordinator fingerprint.
type IdentityMismatchError struct {
	Expected  string
	Presented string
}

func (e *IdentityMismatchError) Error() string {
	return fmt.Sprintf("coordinator identity mismatch: trusted fingerprint %s, presented fingerprint %s", e.Expected, e.Presented)
}

type trustVerifier struct {
	mu          sync.Mutex
	trustFile   string
	trusted     string
	explicit    string
	needsUpdate bool
}

// NewCoordinatorClientConfig creates a TLS configuration that accepts only an
// explicitly supplied or previously stored coordinator certificate. Unknown
// coordinators are never trusted automatically.
func NewCoordinatorClientConfig(trustFile, explicitFingerprint string) (*tls.Config, error) {
	if trustFile == "" {
		return nil, errors.New("trusted coordinator path is required")
	}
	explicit, err := normalizeFingerprint(explicitFingerprint)
	if err != nil {
		return nil, fmt.Errorf("invalid explicit coordinator fingerprint: %w", err)
	}
	stored, readErr := readTrustRecord(trustFile)
	if readErr != nil && explicit == "" {
		return nil, readErr
	}
	if explicit == "" && stored == "" {
		return nil, &NoTrustedCoordinatorError{TrustFile: trustFile}
	}
	expected := stored
	needsUpdate := false
	if explicit != "" {
		expected = explicit
		needsUpdate = stored != explicit || readErr != nil
	}
	verifier := &trustVerifier{
		trustFile:   trustFile,
		trusted:     expected,
		explicit:    explicit,
		needsUpdate: needsUpdate,
	}
	return &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: true, // Safe only with the mandatory verifier below.
		VerifyConnection:   verifier.verifyConnection,
	}, nil
}

func (v *trustVerifier) verifyConnection(state tls.ConnectionState) error {
	if len(state.PeerCertificates) == 0 {
		return errors.New("coordinator provided no certificate")
	}
	presented := Fingerprint(state.PeerCertificates[0].Raw)
	presentedNormalized, _ := normalizeFingerprint(presented)

	v.mu.Lock()
	defer v.mu.Unlock()
	if presentedNormalized != v.trusted {
		return &IdentityMismatchError{Expected: formatFingerprint(v.trusted), Presented: presented}
	}
	if v.needsUpdate {
		if err := writeTrustRecord(v.trustFile, formatFingerprint(v.trusted)); err != nil {
			return fmt.Errorf("save trusted coordinator identity: %w", err)
		}
		v.needsUpdate = false
		log.Printf("[security] Trusted coordinator identity saved to %s", v.trustFile)
	}
	return nil
}

func readTrustRecord(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read trusted coordinator record %s: %w", path, err)
	}
	fingerprint, err := normalizeFingerprint(strings.TrimSpace(string(data)))
	if err != nil || fingerprint == "" {
		return "", fmt.Errorf("trusted coordinator record %s is invalid; reconnect with an explicit --coordinator-fingerprint to replace it", path)
	}
	return fingerprint, nil
}

func writeTrustRecord(path, fingerprint string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create trust directory %s: %w", filepath.Dir(path), err)
	}
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := file.Chmod(0o600); err != nil {
		file.Close()
		return err
	}
	if _, err := file.WriteString(fingerprint + "\n"); err != nil {
		file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return nil
}

func normalizeFingerprint(value string) (string, error) {
	if value == "" {
		return "", nil
	}
	normalized := strings.ToUpper(strings.ReplaceAll(strings.TrimSpace(value), ":", ""))
	if len(normalized) != sha256.Size*2 {
		return "", fmt.Errorf("expected %d hexadecimal characters", sha256.Size*2)
	}
	if _, err := hex.DecodeString(normalized); err != nil {
		return "", errors.New("fingerprint must contain only hexadecimal characters and optional colons")
	}
	return normalized, nil
}

func formatFingerprint(hexString string) string {
	var parts []string
	for index := 0; index < len(hexString); index += 2 {
		parts = append(parts, hexString[index:index+2])
	}
	return strings.Join(parts, ":")
}
