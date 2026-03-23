package server

import (
	"context"
	"net/http"
	"sync/atomic"
)

type unauthorizedContextKey struct{}

// signalUnauthorized sets the unauthorized flag in the context, if present.
// It is a no-op when not running in HTTP mode (i.e., when the context key is absent).
func signalUnauthorized(ctx context.Context) {
	if flag, ok := ctx.Value(unauthorizedContextKey{}).(*atomic.Bool); ok {
		flag.Store(true)
	}
}

// NewHTTPUnauthorizedHandler wraps an HTTP handler to return HTTP 401 when the
// Buildkite API returns a 401, instead of a 200 with a JSON-RPC error body.
// Works for both JSON and SSE transport modes in stateless operation.
//
// wwwAuthenticate is the value of the WWW-Authenticate header sent on 401
// responses (e.g. `Bearer realm="buildkite"`).
func NewHTTPUnauthorizedHandler(handler http.Handler, wwwAuthenticate string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flag := &atomic.Bool{}
		ctx := context.WithValue(r.Context(), unauthorizedContextKey{}, flag)

		iw := &interceptingWriter{ResponseWriter: w, unauthorized: flag}
		handler.ServeHTTP(iw, r.WithContext(ctx))

		if flag.Load() {
			// Clear any headers the inner handler set (e.g. Content-Type from the
			// discarded JSON-RPC response) so they don't appear on the 401 reply.
			h := w.Header()
			for k := range h {
				delete(h, k)
			}
			h.Set("WWW-Authenticate", wwwAuthenticate)
			w.WriteHeader(http.StatusUnauthorized)
		}
	})
}

// interceptingWriter discards header and body writes once the unauthorized flag
// is set, allowing the outer handler to return HTTP 401 instead.
type interceptingWriter struct {
	http.ResponseWriter
	unauthorized *atomic.Bool
}

func (w *interceptingWriter) WriteHeader(code int) {
	if w.unauthorized.Load() {
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *interceptingWriter) Write(b []byte) (int, error) {
	if w.unauthorized.Load() {
		return len(b), nil
	}
	return w.ResponseWriter.Write(b)
}
