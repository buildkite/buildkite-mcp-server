package middleware

import (
	"crypto/hmac"
	"net/http"
	"strings"

	"github.com/rs/zerolog/log"
)

// Auth creates an HTTP middleware that validates Bearer token authentication.
// It uses constant-time comparison to prevent timing attacks.
// The client IP for logging is read from the request context (set by ClientIP middleware).
func Auth(token string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// extract the token from the Authorization header
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" || !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				log.Warn().Str("client_ip", GetClientIPFromContext(r.Context())).Msg("Unauthorized access attempt to MCP HTTP server")
				return
			}

			// extract the bearer token
			bearerToken := strings.TrimPrefix(authHeader, "Bearer ")

			// constant time comparison to prevent timing attacks
			if !hmac.Equal([]byte(bearerToken), []byte(token)) {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				log.Warn().Str("client_ip", GetClientIPFromContext(r.Context())).Msg("Unauthorized access attempt to MCP HTTP server")
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
