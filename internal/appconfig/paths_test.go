package appconfig

import (
	"path/filepath"
	"testing"
)

func TestPathsAtUsesPlatformApplicationDirectory(t *testing.T) {
	tests := []struct {
		goos string
		name string
	}{
		{goos: "darwin", name: "Inoculum"},
		{goos: "linux", name: "inoculum"},
		{goos: "windows", name: "Inoculum"},
	}
	for _, test := range tests {
		t.Run(test.goos, func(t *testing.T) {
			paths := PathsAt("config-root", test.goos)
			wantDir := filepath.Join("config-root", test.name)
			if paths.Dir != wantDir {
				t.Fatalf("Dir = %q, want %q", paths.Dir, wantDir)
			}
			if paths.CoordinatorCert != filepath.Join(wantDir, CoordinatorCertName) ||
				paths.CoordinatorKey != filepath.Join(wantDir, CoordinatorKeyName) ||
				paths.TrustedCoordinator != filepath.Join(wantDir, TrustRecordName) {
				t.Fatalf("paths = %#v", paths)
			}
		})
	}
}
