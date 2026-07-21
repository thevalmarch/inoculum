package auth

import (
	"log"
	"net/http"
	"strconv"

	"github.com/inoculum/internal/audit"
)

// WithTokenAuth wraps an http.HandlerFunc to require a valid token,
// as well as a valid timestamp and nonce to prevent replay attacks.
func WithTokenAuth(expectedToken string, cache *NonceCache, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		presentedToken := r.Header.Get("X-Inoculum-Token")
		if presentedToken == "" || presentedToken != expectedToken {
			audit.LogEvent("auth_failure", r.RemoteAddr, "401", "Invalid or missing token", map[string]any{"path": r.URL.Path})
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		timestampStr := r.Header.Get("X-Inoculum-Timestamp")
		nonce := r.Header.Get("X-Inoculum-Nonce")

		if timestampStr == "" || nonce == "" {
			audit.LogEvent("auth_failure", r.RemoteAddr, "401", "Missing timestamp or nonce", map[string]any{"path": r.URL.Path})
			log.Printf("[auth] Missing timestamp or nonce from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		timestamp, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			audit.LogEvent("auth_failure", r.RemoteAddr, "401", "Invalid timestamp format", map[string]any{"path": r.URL.Path})
			log.Printf("[auth] Invalid timestamp format from %s", r.RemoteAddr)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		if !cache.CheckAndAdd(nonce, timestamp) {
			audit.LogEvent("auth_failure", r.RemoteAddr, "401", "Replay attack detected", map[string]any{"path": r.URL.Path, "nonce": nonce})
			log.Printf("🚨 [MITM ALERT] Replay attack detected from %s! Nonce: %s", r.RemoteAddr, nonce)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}

		next(w, r)
	}
}
