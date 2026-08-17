// Package appconfig resolves Inoculum's persistent per-user configuration
// paths without exposing platform-specific path rules to runtime packages.
package appconfig

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

const (
	CoordinatorCertName = "coordinator-cert.pem"
	CoordinatorKeyName  = "coordinator-key.pem"
	TrustRecordName     = "trusted-coordinator"
)

// Paths contains all persistent security state used by the pull runtime.
type Paths struct {
	Dir                string
	CoordinatorCert    string
	CoordinatorKey     string
	TrustedCoordinator string
}

// DefaultPaths resolves the current user's platform-appropriate config root.
func DefaultPaths() (Paths, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("determine user configuration directory: %w", err)
	}
	return PathsAt(root, runtime.GOOS), nil
}

// PathsAt is separated from DefaultPaths so path policy can be tested for
// platforms that are not the current build host.
func PathsAt(root, goos string) Paths {
	dir := filepath.Join(root, applicationDirectoryName(goos))
	return Paths{
		Dir:                dir,
		CoordinatorCert:    filepath.Join(dir, CoordinatorCertName),
		CoordinatorKey:     filepath.Join(dir, CoordinatorKeyName),
		TrustedCoordinator: filepath.Join(dir, TrustRecordName),
	}
}

func applicationDirectoryName(goos string) string {
	if goos == "darwin" || goos == "windows" {
		return "Inoculum"
	}
	return "inoculum"
}
