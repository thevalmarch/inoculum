package presentation

import (
	"errors"
	"strings"
	"testing"
)

func TestFriendlySecurityErrorsAreActionableAndSanitized(t *testing.T) {
	tests := []struct {
		name string
		err  string
		want []string
	}{
		{name: "no trust", err: "no coordinator identity is trusted yet", want: []string{"No coordinator identity is trusted yet.", "--coordinator-fingerprint <fingerprint>"}},
		{name: "identity mismatch", err: "coordinator identity mismatch", want: []string{"does not match the trusted fingerprint", "stored trust was not bypassed"}},
		{name: "authentication", err: "authentication rejected by coordinator", want: []string{"Authentication failed.", "INOCULUM_TOKEN or -token"}},
		{name: "coordinator identity state", err: "coordinator identity could not be loaded safely from /config/Inoculum", want: []string{"could not be loaded safely", "reported configuration path"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			message := FriendlyError(errors.New(test.err), false)
			for _, fragment := range test.want {
				if !strings.Contains(message, fragment) {
					t.Fatalf("message %q missing %q", message, fragment)
				}
			}
		})
	}
}
