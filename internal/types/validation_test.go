package types

import (
	"strings"
	"testing"
)

func TestOperationalIdentifiersAreBoundedAndLogSafe(t *testing.T) {
	tests := []struct {
		name     string
		validate func(string) error
		valid    string
		tooLong  string
	}{
		{name: "worker", validate: ValidateWorkerID, valid: "linux-worker_1.example", tooLong: strings.Repeat("w", MaxWorkerIDBytes+1)},
		{name: "task", validate: ValidateTaskID, valid: "pull-job-1-task-0", tooLong: strings.Repeat("t", MaxTaskIDBytes+1)},
		{name: "lease", validate: ValidateLeaseID, valid: "lease-123", tooLong: strings.Repeat("l", MaxLeaseIDBytes+1)},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if err := test.validate(test.valid); err != nil {
				t.Fatalf("valid identifier rejected: %v", err)
			}
			for _, invalid := range []string{"", "line\nbreak", "escape\x1bsequence", "space separated", test.tooLong} {
				if err := test.validate(invalid); err == nil {
					t.Fatalf("invalid identifier %q accepted", invalid)
				}
			}
		})
	}
}

func TestResultTextLimits(t *testing.T) {
	if err := ValidateResult(Result{Output: strings.Repeat("o", MaxResultOutputBytes), Error: strings.Repeat("e", MaxResultErrorBytes)}); err != nil {
		t.Fatalf("boundary result rejected: %v", err)
	}
	for _, result := range []Result{
		{Output: strings.Repeat("o", MaxResultOutputBytes+1)},
		{Error: strings.Repeat("e", MaxResultErrorBytes+1)},
		{TaskID: strings.Repeat("t", MaxTaskIDBytes+1)},
	} {
		if err := ValidateResult(result); err == nil {
			t.Fatalf("oversized result accepted")
		}
	}
}
