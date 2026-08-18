package auth

import (
	"crypto/sha256"
	"crypto/subtle"
	"log"
	"net/http"
	"strings"

	"github.com/thevalmarch/inoculum/internal/audit"
)

// WithBearerAuth requires the standard Authorization: Bearer header. TLS
// protects the token in transit; lease and task identifiers provide the
// domain-level handling for stale or duplicate results.
func WithBearerAuth(expectedToken string, next http.HandlerFunc) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		authorizationHeaders := request.Header.Values("Authorization")
		presentedToken, ok := "", false
		if len(authorizationHeaders) == 1 {
			presentedToken, ok = bearerToken(authorizationHeaders[0])
		}
		if !ok || !tokensEqual(presentedToken, expectedToken) {
			log.Printf("[auth] Authentication failed from %s for %s", request.RemoteAddr, request.URL.Path)
			audit.LogEvent("auth_failure", request.RemoteAddr, "401", "Authentication failed", map[string]any{"path": request.URL.Path})
			writer.Header().Set("WWW-Authenticate", `Bearer realm="inoculum"`)
			http.Error(writer, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(writer, request)
	}
}

func bearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] == "" {
		return "", false
	}
	return parts[1], true
}

func tokensEqual(presented, expected string) bool {
	presentedHash := sha256.Sum256([]byte(presented))
	expectedHash := sha256.Sum256([]byte(expected))
	return subtle.ConstantTimeCompare(presentedHash[:], expectedHash[:]) == 1
}
