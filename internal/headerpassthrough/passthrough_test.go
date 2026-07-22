package headerpassthrough

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	config, err := New(
		[]string{"x-identity", "X-Identity", "Authorization"},
		nil,
		"https://api.buildkite.com/api",
	)
	require.NoError(t, err)
	require.Equal(t, []string{"X-Identity", "Authorization"}, config.headerNames)
	require.True(t, config.UsesAuthorization())

	tests := []struct {
		name    string
		headers []string
		fixed   map[string]string
		baseURL string
	}{
		{name: "invalid header", headers: []string{"Bad Header"}, baseURL: "https://api.buildkite.com"},
		{name: "fixed overlap", headers: []string{"X-Identity"}, fixed: map[string]string{"x-identity": "fixed"}, baseURL: "https://api.buildkite.com"},
		{name: "relative base URL", headers: []string{"X-Identity"}, baseURL: "/api"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.headers, tt.fixed, tt.baseURL)
			require.Error(t, err)
		})
	}
}

func TestNewRejectsTransportManagedHeaders(t *testing.T) {
	for _, name := range []string{
		"Connection",
		"Content-Length",
		"Host",
		"Keep-Alive",
		"Proxy-Authenticate",
		"Proxy-Authorization",
		"Proxy-Connection",
		"TE",
		"Trailer",
		"Transfer-Encoding",
		"Upgrade",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := New([]string{strings.ToLower(name)}, nil, "https://api.buildkite.com")
			require.ErrorContains(t, err, "managed by the HTTP transport")
		})
	}
}

func TestForwardsAllowedHeadersThroughRequestContext(t *testing.T) {
	config, err := New([]string{"X-Identity", "X-Groups"}, nil, "https://api.buildkite.com/")
	require.NoError(t, err)

	transport := config.WrapTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		require.Equal(t, []string{"user-123"}, req.Header.Values("X-Identity"))
		require.Equal(t, []string{"admins", "developers"}, req.Header.Values("X-Groups"))
		require.Empty(t, req.Header.Get("X-Unlisted"))
		return response(req), nil
	}))

	handler := config.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, requestErr := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.buildkite.com/v2/builds", nil)
		if !assert.NoError(t, requestErr) {
			http.Error(w, requestErr.Error(), http.StatusInternalServerError)
			return
		}
		req.Header.Set("X-Identity", "stale")
		_, requestErr = transport.RoundTrip(req)
		if !assert.NoError(t, requestErr) {
			http.Error(w, requestErr.Error(), http.StatusBadGateway)
			return
		}
		assert.Equal(t, "stale", req.Header.Get("X-Identity"), "the original request must not be mutated")
		w.WriteHeader(http.StatusNoContent)
	}))

	inbound := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	inbound.Header.Set("X-Identity", "user-123")
	inbound.Header.Add("X-Groups", "admins")
	inbound.Header.Add("X-Groups", "developers")
	inbound.Header.Set("X-Unlisted", "private")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, inbound)

	require.Equal(t, http.StatusNoContent, recorder.Code)
}

func TestAuthorizationIsRequiredWhenAllowed(t *testing.T) {
	config, err := New([]string{"Authorization"}, nil, "https://api.buildkite.com/")
	require.NoError(t, err)

	for _, tt := range []struct {
		name   string
		values []string
		want   int
	}{
		{name: "missing", want: http.StatusUnauthorized},
		{name: "empty", values: []string{" "}, want: http.StatusUnauthorized},
		{name: "multiple", values: []string{"Bearer one", "Bearer two"}, want: http.StatusUnauthorized},
		{name: "one", values: []string{"Bearer user-token"}, want: http.StatusNoContent},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := config.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true
				w.WriteHeader(http.StatusNoContent)
			}))
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			for _, value := range tt.values {
				req.Header.Add("Authorization", value)
			}
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)

			require.Equal(t, tt.want, recorder.Code)
			require.Equal(t, tt.want == http.StatusNoContent, called)
		})
	}
}

func TestTransportStripsHeadersFromOtherOrigins(t *testing.T) {
	config, err := New([]string{"Authorization", "X-Identity"}, nil, "https://api.buildkite.com/")
	require.NoError(t, err)

	ctx := context.WithValue(context.Background(), contextKey{}, http.Header{
		"Authorization": {"Bearer secret"},
		"X-Identity":    {"user-123"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "https://uploads.buildkite.com/artifact", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer stale")
	req.Header.Set("X-Identity", "stale-user")

	transport := config.WrapTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		require.Empty(t, req.Header.Get("Authorization"))
		require.Empty(t, req.Header.Get("X-Identity"))
		return response(req), nil
	}))
	_, err = transport.RoundTrip(req)
	require.NoError(t, err)
	require.Equal(t, "Bearer stale", req.Header.Get("Authorization"))
}

func TestTransportStripsHeadersOnCrossOriginRedirect(t *testing.T) {
	targetHeaders := make(chan http.Header, 1)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targetHeaders <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(target.Close)

	sourceHeaders := make(chan http.Header, 1)
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sourceHeaders <- r.Header.Clone()
		http.Redirect(w, r, target.URL+"/artifact", http.StatusFound)
	}))
	t.Cleanup(source.Close)

	config, err := New([]string{"Authorization", "X-Identity"}, nil, source.URL)
	require.NoError(t, err)
	client := &http.Client{Transport: config.WrapTransport(http.DefaultTransport)}

	handler := config.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, requestErr := http.NewRequestWithContext(r.Context(), http.MethodGet, source.URL+"/artifact", nil)
		if requestErr != nil {
			http.Error(w, requestErr.Error(), http.StatusInternalServerError)
			return
		}
		resp, requestErr := client.Do(req)
		if requestErr != nil {
			http.Error(w, requestErr.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		w.WriteHeader(resp.StatusCode)
	}))

	inbound := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	inbound.Header.Set("Authorization", "Bearer secret")
	inbound.Header.Set("X-Identity", "user-123")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, inbound)
	require.Equal(t, http.StatusNoContent, recorder.Code)

	require.Equal(t, "Bearer secret", (<-sourceHeaders).Get("Authorization"))
	targetReceived := <-targetHeaders
	require.Empty(t, targetReceived.Get("Authorization"))
	require.Empty(t, targetReceived.Get("X-Identity"))
}

func TestConcurrentRequestHeadersRemainIsolated(t *testing.T) {
	config, err := New([]string{"X-Identity"}, nil, "https://api.buildkite.com/")
	require.NoError(t, err)

	transport := config.WrapTransport(roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		resp := response(req)
		resp.Body = io.NopCloser(strings.NewReader(req.Header.Get("X-Identity")))
		return resp, nil
	}))
	handler := config.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, _ := http.NewRequestWithContext(r.Context(), http.MethodGet, "https://api.buildkite.com/v2/user", nil)
		resp, requestErr := transport.RoundTrip(req)
		if requestErr != nil {
			http.Error(w, requestErr.Error(), http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		_, _ = io.Copy(w, resp.Body)
	}))

	const requestCount = 20
	errors := make(chan error, requestCount)
	var wg sync.WaitGroup
	for i := range requestCount {
		wg.Add(1)
		go func() {
			defer wg.Done()
			identity := fmt.Sprintf("user-%02d", i)
			req := httptest.NewRequest(http.MethodPost, "/mcp", nil)
			req.Header.Set("X-Identity", identity)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, req)
			if recorder.Body.String() != identity {
				errors <- fmt.Errorf("wanted %q, got %q", identity, recorder.Body.String())
			}
		}()
	}
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
}

func response(req *http.Request) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       http.NoBody,
		Request:    req,
	}
}
