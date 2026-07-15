package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/internal/headerpassthrough"
	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
	"github.com/stretchr/testify/require"
)

func TestResolveAPITokenForModePreservesExistingAuthentication(t *testing.T) {
	t.Run("static token", func(t *testing.T) {
		token, err := resolveAPITokenForMode(nil, "", "shared-token", "")
		require.NoError(t, err)
		require.Equal(t, "shared-token", token)
	})

	t.Run("missing static token", func(t *testing.T) {
		_, err := resolveAPITokenForMode(nil, "", "", "")
		require.ErrorContains(t, err, "must specify either --api-token or --api-token-from-1password")
	})

	t.Run("replay does not require token", func(t *testing.T) {
		token, err := resolveAPITokenForMode(nil, "session.har", "", "")
		require.NoError(t, err)
		require.Empty(t, token)
	})
}

func TestResolveAPITokenForModeUsesPerRequestAuthorization(t *testing.T) {
	config, err := headerpassthrough.New([]string{"Authorization"}, nil, "https://api.buildkite.com/")
	require.NoError(t, err)

	token, err := resolveAPITokenForMode(config, "", "", "")
	require.NoError(t, err)
	require.Empty(t, token)

	_, err = resolveAPITokenForMode(config, "", "shared-token", "")
	require.ErrorContains(t, err, "cannot configure a fixed Buildkite API token")

	_, err = resolveAPITokenForMode(config, "", "", "op://vault/item/token")
	require.ErrorContains(t, err, "cannot configure a fixed Buildkite API token")
}

func TestRecordingDoesNotCapturePassthroughHeaders(t *testing.T) {
	receivedHeaders := make(chan http.Header, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(api.Close)

	config, err := headerpassthrough.New([]string{"X-Identity", "Cookie"}, nil, api.URL)
	require.NoError(t, err)
	harPath := filepath.Join(t.TempDir(), "session.har")
	transport, err := newAPITransport(config, harPath, "", "test")
	require.NoError(t, err)
	client := &http.Client{Transport: transport}

	handler := config.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, requestErr := http.NewRequestWithContext(r.Context(), http.MethodGet, api.URL+"/v2/user", nil)
		require.NoError(t, requestErr)
		resp, requestErr := client.Do(req)
		require.NoError(t, requestErr)
		resp.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	inbound := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	inbound.Header.Set("X-Identity", "user-123")
	inbound.Header.Set("Cookie", "session-secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, inbound)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	received := <-receivedHeaders
	require.Equal(t, "user-123", received.Get("X-Identity"))
	require.Equal(t, "session-secret", received.Get("Cookie"))

	har, err := recording.LoadHAR(harPath)
	require.NoError(t, err)
	require.Len(t, har.Log.Entries, 1)
	for _, header := range har.Log.Entries[0].Request.Headers {
		require.NotEqual(t, "X-Identity", header.Name)
		require.NotEqual(t, "Cookie", header.Name)
	}
}
