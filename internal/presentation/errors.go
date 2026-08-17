package presentation

import (
	"fmt"
	"strings"
)

func FriendlyError(err error, verbose bool) string {
	if err == nil {
		return ""
	}
	message := err.Error()
	lower := strings.ToLower(message)
	var summary, advice string
	switch {
	case strings.Contains(lower, "no coordinator identity is trusted"):
		summary = "No coordinator identity is trusted yet."
		advice = "Copy the fingerprint from the coordinator and reconnect with:\n--coordinator-fingerprint <fingerprint>"
	case strings.Contains(lower, "http 401") || strings.Contains(lower, "authentication rejected"):
		summary = "Authentication failed."
		advice = "The coordinator rejected the token. Check INOCULUM_TOKEN or -token."
	case strings.Contains(lower, "fingerprint mismatch") || strings.Contains(lower, "certificate changed") || strings.Contains(lower, "coordinator identity mismatch"):
		summary = "The coordinator identity does not match the trusted fingerprint."
		advice = "Connection refused. The stored trust was not bypassed."
	case strings.Contains(lower, "coordinator identity could not be loaded safely"):
		summary = "Coordinator identity could not be loaded safely."
		advice = "Check the certificate and key in the reported configuration path. Inoculum did not replace them."
	case strings.Contains(lower, "connection refused") || strings.Contains(lower, "no such host"):
		summary = "Coordinator unavailable."
		advice = "Check the coordinator address and confirm the coordinator is running."
	default:
		return message
	}
	if verbose {
		return fmt.Sprintf("%s\n\n%s\n\nDetail: %s", summary, advice, message)
	}
	return summary + "\n\n" + advice
}
