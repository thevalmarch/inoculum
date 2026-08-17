package presentation

import "testing"

func TestDetectCapabilities(t *testing.T) {
	tests := []struct {
		name        string
		options     ModeOptions
		stdinTTY    bool
		stdoutTTY   bool
		terminal    string
		ci          string
		noColor     string
		unicode     bool
		interactive bool
		color       bool
		wantUnicode bool
	}{
		{name: "interactive", stdinTTY: true, stdoutTTY: true, terminal: "xterm-256color", unicode: true, interactive: true, color: true, wantUnicode: true},
		{name: "redirected", stdinTTY: true, stdoutTTY: false, terminal: "xterm", unicode: true, wantUnicode: true},
		{name: "plain flag", options: ModeOptions{Plain: true}, stdinTTY: true, stdoutTTY: true, terminal: "xterm", unicode: true, wantUnicode: true},
		{name: "CI", stdinTTY: true, stdoutTTY: true, terminal: "xterm", ci: "true", unicode: true, wantUnicode: true},
		{name: "dumb terminal", stdinTTY: true, stdoutTTY: true, terminal: "dumb", unicode: true, wantUnicode: true},
		{name: "no color env", stdinTTY: true, stdoutTTY: true, terminal: "xterm", noColor: "1", unicode: true, interactive: true, wantUnicode: true},
		{name: "ASCII flag", options: ModeOptions{ASCII: true}, stdinTTY: true, stdoutTTY: true, terminal: "xterm", unicode: true, interactive: true, color: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := detectCapabilities(test.options, test.stdinTTY, test.stdoutTTY, test.terminal, test.ci, test.noColor, test.unicode)
			if got.Interactive != test.interactive || got.Color != test.color || got.Unicode != test.wantUnicode {
				t.Fatalf("capabilities = %#v", got)
			}
		})
	}
}
