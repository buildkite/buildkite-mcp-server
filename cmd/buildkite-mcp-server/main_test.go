package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/internal/headerpassthrough"
	buildkitetools "github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	gobuildkite "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
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
		if !assert.NoError(t, requestErr) {
			http.Error(w, requestErr.Error(), http.StatusInternalServerError)
			return
		}
		resp, requestErr := client.Do(req)
		if !assert.NoError(t, requestErr) {
			http.Error(w, requestErr.Error(), http.StatusBadGateway)
			return
		}
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

func TestMCPToolCallPassesRequestHeaderToBuildkiteAPI(t *testing.T) {
	type receivedRequest struct {
		path    string
		headers http.Header
	}
	receivedRequests := make(chan receivedRequest, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRequests <- receivedRequest{path: r.URL.Path, headers: r.Header.Clone()}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"user-id","name":"Test User"}`))
	}))
	t.Cleanup(api.Close)

	config, err := headerpassthrough.New([]string{"X-Identity"}, nil, api.URL)
	require.NoError(t, err)
	apiTransport, err := newAPITransport(config, "", "", "test")
	require.NoError(t, err)
	apiClient := &http.Client{Transport: apiTransport}
	client, err := gobuildkite.NewOpts(
		gobuildkite.WithHTTPClient(apiClient),
		gobuildkite.WithBaseURL(api.URL+"/"),
	)
	require.NoError(t, err)

	deps := buildkitetools.ToolDependencies{UserClient: client.User}
	factory := server.NewPerRequestServerFactory("test", deps, []string{toolsets.ToolsetUser}, true)
	handler := config.WrapHandler(server.NewHTTPUnauthorizedHandler(
		mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{Stateless: true}),
		`Bearer realm="buildkite"`,
	))
	mcpServer := httptest.NewServer(handler)
	t.Cleanup(mcpServer.Close)

	mcpHTTPClient := mcpServer.Client()
	mcpHTTPClient.Transport = requestHeaderTransport{
		next:  mcpHTTPClient.Transport,
		name:  "X-Identity",
		value: "user-123",
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             mcpServer.URL,
		HTTPClient:           mcpHTTPClient,
		DisableStandaloneSSE: true,
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(context.Background(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "current_user"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	received := <-receivedRequests
	require.Equal(t, "/v2/user", received.path)
	require.Equal(t, "user-123", received.headers.Get("X-Identity"))
}

type requestHeaderTransport struct {
	next        http.RoundTripper
	name, value string
}

func (t requestHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Set(t.name, t.value)
	return t.next.RoundTrip(cloned)
}
