package middleware

import (
	"context"
	"net/http"
	"strings"
)

// contextKey is a private type for context keys to avoid collisions
type contextKey string

const (
	// clientIPKey is the context key for storing the client IP address
	clientIPKey contextKey = "client_ip"
)

// ClientIP creates an HTTP middleware that extracts the real client IP address
// and injects it into the request context. This should be the first middleware
// in the chain to ensure all subsequent middlewares and handlers can access it.
//
// When trustProxy is false, it uses r.RemoteAddr directly.
// When trustProxy is true, it checks proxy headers in priority order.
func ClientIP(trustProxy bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			clientIP := getClientIP(r, trustProxy)
			ctx := context.WithValue(r.Context(), clientIPKey, clientIP)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// GetClientIPFromContext extracts the client IP from the request context.
// Returns an empty string if the IP is not found in the context.
// This should be used after the ClientIP middleware has run.
func GetClientIPFromContext(ctx context.Context) string {
	if ip, ok := ctx.Value(clientIPKey).(string); ok {
		return ip
	}
	return ""
}

// getClientIP extracts the real client IP from the request, checking multiple proxy headers.
// This is an internal helper function. Use ClientIP middleware and GetClientIPFromContext instead.
//
// When trustProxy is false, it returns r.RemoteAddr directly without checking proxy headers.
// When trustProxy is true, it checks headers in priority order:
//   - CF-Connecting-IP: Cloudflare
//   - True-Client-IP: Akamai and Cloudflare Enterprise
//   - X-Real-IP: Nginx proxy/FastCGI
//   - X-Forwarded-For: Standard proxy header (takes first IP from comma-separated list)
//   - X-Client-IP: Apache and others
//
// Security Warning: Only enable trustProxy when behind a trusted reverse proxy that
// properly sets these headers. Proxy headers can be spoofed if the application is
// directly exposed to the internet.
func getClientIP(r *http.Request, trustProxy bool) string {
	if !trustProxy {
		return r.RemoteAddr
	}

	// Priority order of headers to check
	headers := []string{
		"CF-Connecting-IP", // Cloudflare
		"True-Client-IP",   // Akamai and Cloudflare Enterprise
		"X-Real-IP",        // Nginx proxy/FastCGI
		"X-Forwarded-For",  // Standard proxy header
		"X-Client-IP",      // Apache, others
	}

	for _, header := range headers {
		if ip := r.Header.Get(header); ip != "" {
			// For X-Forwarded-For, take the first IP (original client)
			// Format: X-Forwarded-For: client, proxy1, proxy2
			if header == "X-Forwarded-For" {
				ips := strings.Split(ip, ",")
				if len(ips) > 0 {
					firstIP := strings.TrimSpace(ips[0])
					if firstIP != "" {
						return firstIP
					}
				}
				// Empty or malformed X-Forwarded-For, continue checking other headers
				continue
			}
			return ip
		}
	}

	// Fall back to RemoteAddr
	return r.RemoteAddr
}
