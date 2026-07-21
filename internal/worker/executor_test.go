package worker

import (
	"os"
	"path/filepath"
	"testing"
)

func TestExecuteFileAnalyzePathTraversal(t *testing.T) {
	// Create a temporary directory structure for tests
	tempDir, err := os.MkdirTemp("", "inoculum_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Structure:
	// /tempDir
	//   /allowed
	//     safe_file.txt
	//   /forbidden
	//     secret.txt
	allowedDir := filepath.Join(tempDir, "allowed")
	forbiddenDir := filepath.Join(tempDir, "forbidden")
	if err := os.MkdirAll(allowedDir, 0755); err != nil {
		t.Fatalf("failed to create allowed dir: %v", err)
	}
	if err := os.MkdirAll(forbiddenDir, 0755); err != nil {
		t.Fatalf("failed to create forbidden dir: %v", err)
	}

	safeFile := filepath.Join(allowedDir, "safe_file.txt")
	secretFile := filepath.Join(forbiddenDir, "secret.txt")
	if err := os.WriteFile(safeFile, []byte("safe"), 0644); err != nil {
		t.Fatalf("failed to write safe file: %v", err)
	}
	if err := os.WriteFile(secretFile, []byte("secret"), 0644); err != nil {
		t.Fatalf("failed to write secret file: %v", err)
	}

	// Create a symlink inside the allowed directory pointing to the secret file
	symlinkPath := filepath.Join(allowedDir, "sneaky_link.txt")
	if err := os.Symlink(secretFile, symlinkPath); err != nil {
		t.Fatalf("failed to create symlink: %v", err)
	}

	executor := NewExecutor([]string{allowedDir})

	tests := []struct {
		name      string
		input     string
		expectErr bool
	}{
		{
			name:      "Safe file inside allowed directory",
			input:     safeFile,
			expectErr: false,
		},
		{
			name:      "Direct path to forbidden directory",
			input:     secretFile,
			expectErr: true,
		},
		{
			name:      "Traversal using ../",
			input:     filepath.Join(allowedDir, "..", "forbidden", "secret.txt"),
			expectErr: true,
		},
		{
			name:      "Symlink inside allowed pointing outside",
			input:     symlinkPath,
			expectErr: true,
		},
		{
			name:      "Prefix spoofing (e.g. allowed_evil)",
			input:     filepath.Join(tempDir, "allowed_evil.txt"),
			expectErr: true,
		},
	}

	// Write prefix spoofing file
	spoofFile := filepath.Join(tempDir, "allowed_evil.txt")
	os.WriteFile(spoofFile, []byte("evil"), 0644)

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, _, err := executor.Execute("file_analyze", tc.input)
			if tc.expectErr && err == nil {
				t.Errorf("expected error for input %q, got success", tc.input)
			}
			if !tc.expectErr && err != nil {
				t.Errorf("expected success for input %q, got error: %v", tc.input, err)
			}
		})
	}
}
