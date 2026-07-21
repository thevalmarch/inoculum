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
	"fmt"
	"log"
	"math/big"
	"os"
	"strings"
	"sync"
	"time"
)

// GetOrGenerateCert loads a certificate and private key from disk if they exist,
// otherwise generates a new self-signed ECDSA certificate in memory, saves it,
// and returns the tls.Certificate and its SHA-256 fingerprint (hex encoded).
func GetOrGenerateCert(certFile, keyFile string) (tls.Certificate, string, error) {
	// Try to load existing
	tlsCert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err == nil {
		// Loaded successfully, parse the leaf to get fingerprint
		cert, err := x509.ParseCertificate(tlsCert.Certificate[0])
		if err == nil {
			hash := sha256.Sum256(cert.Raw)
			fingerprint := hex.EncodeToString(hash[:])
			return tlsCert, formatFingerprint(fingerprint), nil
		}
	}

	// Generate new
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to generate private key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Inoculum Ephemeral Node"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to create certificate: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	privBytes, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to marshal private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: privBytes})

	// Save to disk
	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to write cert file: %w", err)
	}
	if err := os.WriteFile(keyFile, keyPEM, 0600); err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to write key file: %w", err)
	}

	tlsCert, err = tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return tls.Certificate{}, "", fmt.Errorf("failed to load key pair: %w", err)
	}

	hash := sha256.Sum256(derBytes)
	fingerprint := hex.EncodeToString(hash[:])

	return tlsCert, formatFingerprint(fingerprint), nil
}

// TOFUCache manages the Trust-On-First-Use known hosts file.
type TOFUCache struct {
	mu          sync.Mutex
	peerAddress string
	knownHosts  string
	explicitPin string
}

// NewTOFUClientConfig creates a tls.Config that implements Trust-On-First-Use (TOFU) pinning.
// If explicitFingerprint is provided, it strictly requires that fingerprint.
// Otherwise, it uses knownHostsFile to save/verify the fingerprint for peerAddress.
func NewTOFUClientConfig(peerAddress, explicitFingerprint, knownHostsFile string) *tls.Config {
	cache := &TOFUCache{
		peerAddress: peerAddress,
		knownHosts:  knownHostsFile,
		explicitPin: strings.ToUpper(strings.ReplaceAll(explicitFingerprint, ":", "")),
	}

	return &tls.Config{
		InsecureSkipVerify: true,
		VerifyPeerCertificate: func(rawCerts [][]byte, verifiedChains [][]*x509.Certificate) error {
			if len(rawCerts) == 0 {
				return fmt.Errorf("no certificates provided by peer")
			}

			hash := sha256.Sum256(rawCerts[0])
			presentedFingerprint := strings.ToUpper(hex.EncodeToString(hash[:]))

			cache.mu.Lock()
			defer cache.mu.Unlock()

			if cache.explicitPin != "" {
				if presentedFingerprint != cache.explicitPin {
					log.Printf("🚨 [MITM ALERT] Certificate fingerprint mismatch for %s! Expected %s, got %s", cache.peerAddress, cache.explicitPin, presentedFingerprint)
					return fmt.Errorf("certificate fingerprint mismatch (possible MITM)")
				}
				return nil
			}

			if cache.knownHosts == "" {
				return fmt.Errorf("no known hosts file specified and no explicit pin provided")
			}

			// Read existing file
			b, err := os.ReadFile(cache.knownHosts)
			if err != nil && !os.IsNotExist(err) {
				return fmt.Errorf("failed to read known hosts file: %w", err)
			}

			lines := strings.Split(string(b), "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line == "" {
					continue
				}
				parts := strings.SplitN(line, " ", 2)
				if len(parts) == 2 && parts[0] == cache.peerAddress {
					pinnedFingerprint := parts[1]
					if presentedFingerprint != pinnedFingerprint {
						log.Printf("🚨 [MITM ALERT] Certificate changed for %s since last connection! Expected %s, got %s", cache.peerAddress, pinnedFingerprint, presentedFingerprint)
						return fmt.Errorf("certificate changed since last connection (possible MITM). Refusing to connect.")
					}
					return nil
				}
			}

			// Host not found, Trust On First Use
			log.Printf("[TOFU] First connection to %s. Pinning certificate fingerprint: %s", cache.peerAddress, formatFingerprint(presentedFingerprint))
			f, err := os.OpenFile(cache.knownHosts, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
			if err != nil {
				return fmt.Errorf("failed to open known hosts file: %w", err)
			}
			defer f.Close()

			entry := fmt.Sprintf("%s %s\n", cache.peerAddress, presentedFingerprint)
			if _, err := f.WriteString(entry); err != nil {
				return fmt.Errorf("failed to append to known hosts file: %w", err)
			}

			return nil
		},
	}
}

func formatFingerprint(hexStr string) string {
	var out []string
	for i := 0; i < len(hexStr); i += 2 {
		out = append(out, hexStr[i:i+2])
	}
	return strings.Join(out, ":")
}
