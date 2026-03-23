package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNewHTTPUnauthorizedHandler_NormalRequest(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"result":"ok"}`))
	})

	handler := NewHTTPUnauthorizedHandler(inner, `Bearer realm="buildkite"`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	require.JSONEq(t, `{"result":"ok"}`, rec.Body.String())
}

func TestNewHTTPUnauthorizedHandler_UnauthorizedSignal(t *testing.T) {
	// An inner handler that signals unauthorized via the context flag.
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Simulate the MCP middleware detecting ErrUnauthorized.
		SignalUnauthorized(r.Context())
		// The interceptingWriter will discard these writes.
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	})

	handler := NewHTTPUnauthorizedHandler(inner, `Bearer realm="buildkite"`)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/mcp", nil)

	handler.ServeHTTP(rec, req)

	require.Equal(t, http.StatusUnauthorized, rec.Code)
	require.Equal(t, `Bearer realm="buildkite"`, rec.Header().Get("WWW-Authenticate"))
	require.Empty(t, rec.Header().Get("Content-Type"), "stale inner-handler headers must not appear on 401")
	require.Empty(t, rec.Body.String())
}

func TestSignalUnauthorized_NoopWithoutContext(t *testing.T) {
	// Should not panic when the context key is absent.
	require.NotPanics(t, func() {
		SignalUnauthorized(context.Background())
	})
}

func TestSignalUnauthorized_SetsFlag(t *testing.T) {
	flag := &atomic.Bool{}
	ctx := context.WithValue(context.Background(), unauthorizedContextKey{}, flag)

	require.False(t, flag.Load())
	SignalUnauthorized(ctx)
	require.True(t, flag.Load())
}

func TestInterceptingWriter_PassthroughWhenFlagFalse(t *testing.T) {
	rec := httptest.NewRecorder()
	flag := &atomic.Bool{} // false by default

	iw := &interceptingWriter{ResponseWriter: rec, unauthorized: flag}

	iw.WriteHeader(http.StatusCreated)
	_, _ = iw.Write([]byte("hello"))

	require.Equal(t, http.StatusCreated, rec.Code)
	require.Equal(t, "hello", rec.Body.String())
}

func TestInterceptingWriter_DiscardsWhenFlagTrue(t *testing.T) {
	rec := httptest.NewRecorder()
	flag := &atomic.Bool{}
	flag.Store(true)

	iw := &interceptingWriter{ResponseWriter: rec, unauthorized: flag}

	iw.WriteHeader(http.StatusOK)
	n, err := iw.Write([]byte("discarded"))

	require.NoError(t, err)
	require.Equal(t, len("discarded"), n)
	// Default recorder code is 200 but WriteHeader was never forwarded.
	require.Equal(t, http.StatusOK, rec.Code) // recorder default, not explicitly set
	require.Empty(t, rec.Body.String())
}
