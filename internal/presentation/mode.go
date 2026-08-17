// Package presentation contains terminal-independent layout and output mode
// selection. Terminal control lives in the tui subpackage.
package presentation

import (
	"os"
	"strings"

	"golang.org/x/term"
)

type ModeOptions struct {
	Plain   bool
	Verbose bool
	NoColor bool
	ASCII   bool
}

type Capabilities struct {
	Interactive bool
	Verbose     bool
	Color       bool
	Unicode     bool
}

func Detect(options ModeOptions, stdin, stdout *os.File) Capabilities {
	stdinTTY := stdin != nil && term.IsTerminal(int(stdin.Fd()))
	stdoutTTY := stdout != nil && term.IsTerminal(int(stdout.Fd()))
	return detectCapabilities(options, stdinTTY, stdoutTTY, os.Getenv("TERM"), os.Getenv("CI"), os.Getenv("NO_COLOR"), supportsUnicodeLocale())
}

func detectCapabilities(options ModeOptions, stdinTTY, stdoutTTY bool, terminal, ci, noColor string, unicodeLocale bool) Capabilities {
	interactive := !options.Plain && stdinTTY && stdoutTTY && terminal != "dumb" && ci == ""
	color := interactive && !options.NoColor && noColor == ""
	unicode := !options.ASCII && unicodeLocale
	return Capabilities{
		Interactive: interactive,
		Verbose:     options.Verbose,
		Color:       color,
		Unicode:     unicode,
	}
}

func supportsUnicodeLocale() bool {
	locale := os.Getenv("LC_ALL")
	if locale == "" {
		locale = os.Getenv("LC_CTYPE")
	}
	if locale == "" {
		locale = os.Getenv("LANG")
	}
	if locale == "" {
		// Modern macOS, Linux, and Windows terminals are normally UTF-8. Users
		// can force the conservative path with --ascii.
		return true
	}
	upper := strings.ToUpper(locale)
	return strings.Contains(upper, "UTF-8") || strings.Contains(upper, "UTF8")
}
