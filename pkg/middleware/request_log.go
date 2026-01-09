package middleware

import (
	"net/http"
	"time"

	"github.com/rs/zerolog/log"
)

// responseWriter wraps http.ResponseWriter to capture the status code
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	written    bool
}

func newResponseWriter(w http.ResponseWriter) *responseWriter {
	return &responseWriter{
		ResponseWriter: w,
		statusCode:     http.StatusOK,
		written:        false,
	}
}

func (rw *responseWriter) WriteHeader(code int) {
	if !rw.written {
		rw.statusCode = code
		rw.written = true
		rw.ResponseWriter.WriteHeader(code)
	}
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if !rw.written {
		rw.WriteHeader(http.StatusOK)
	}
	return rw.ResponseWriter.Write(b)
}

// RequestLog creates an HTTP middleware that logs each request with method, path,
// status code, duration, and client IP. The ClientIP middleware should be placed
// before this middleware in the chain to ensure the client IP is available.
func RequestLog() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			wrapped := newResponseWriter(w)

			// Process the request
			next.ServeHTTP(wrapped, r)

			// Log after the request is complete
			duration := time.Since(start)
			clientIP := GetClientIPFromContext(r.Context())

			log.Info().
				Str("method", r.Method).
				Str("path", r.URL.Path).
				Int("status", wrapped.statusCode).
				Dur("duration_ms", duration).
				Str("client_ip", clientIP).
				Str("user_agent", r.UserAgent()).
				Msg("HTTP request")
		})
	}
}
