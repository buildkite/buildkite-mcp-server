package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/alecthomas/kong"
	"github.com/buildkite/buildkite-mcp-server/internal/commands"
	"github.com/buildkite/buildkite-mcp-server/internal/headerpassthrough"
	buildkitetools "github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	gobuildkite "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinimumAccessCLI(t *testing.T) {
	for _, command := range []string{"stdio", "http"} {
		for _, tt := range []struct {
			name     string
			env      string
			flags    []string
			minimum  bool
			readOnly bool
		}{
			{name: "default unchanged"},
			{name: "flag", flags: []string{"--minimum-access"}, minimum: true},
			{name: "environment", env: "true", minimum: true},
			{name: "explicit false overrides environment", env: "true", flags: []string{"--minimum-access=false"}},
			{name: "both restrictions", flags: []string{"--minimum-access", "--read-only"}, minimum: true, readOnly: true},
		} {
			t.Run(command+"/"+tt.name, func(t *testing.T) {
				t.Setenv("BUILDKITE_MINIMUM_ACCESS", tt.env)
				if tt.env == "" {
					require.NoError(t, os.Unsetenv("BUILDKITE_MINIMUM_ACCESS"))
				}
				t.Setenv("BUILDKITE_READ_ONLY", "false")
				model := cli
				parser, err := kong.New(&model)
				require.NoError(t, err)
				_, err = parser.Parse(append([]string{command}, tt.flags...))
				require.NoError(t, err)
				if command == "stdio" {
					require.Equal(t, tt.minimum, model.Stdio.MinimumAccess)
					require.Equal(t, tt.readOnly, model.Stdio.ReadOnly)
				} else {
					require.Equal(t, tt.minimum, model.HTTP.MinimumAccess)
					require.Equal(t, tt.readOnly, model.HTTP.ReadOnly)
				}
			})
		}
	}
}

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

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "current_user",
		Arguments: map[string]any{
			"telemetry": map[string]any{
				"context": "Fetching the current Buildkite user to verify request header passthrough behavior during the authenticated integration test flow.",
			},
		},
	})
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

func TestAPIUserAgentIncludesMCPClient(t *testing.T) {
	prefix := commands.UserAgent("1.22.0")
	maxName := strings.Repeat("x", 1024-len(prefix)-1)
	for _, mode := range []string{"stdio", "http"} {
		t.Run(mode, func(t *testing.T) {
			userAgents := make(chan string, 1)
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				userAgents <- r.UserAgent()
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(`{"id":"user-id","name":"Test User"}`))
			}))
			t.Cleanup(api.Close)
			inner, err := newAPITransport(nil, "", "", "1.22.0")
			require.NoError(t, err)
			httpClient := trace.NewHTTPClientWithHeadersAndTransport(nil, inner)
			client, err := gobuildkite.NewOpts(
				gobuildkite.WithBaseURL(api.URL+"/"),
				gobuildkite.WithUserAgent(prefix),
				gobuildkite.WithHTTPClient(httpClient),
			)
			require.NoError(t, err)
			httpClient.Transport = &userAgentTransport{wrapped: httpClient.Transport, userAgent: prefix, apiURL: client.BaseURL}
			deps := buildkitetools.ToolDependencies{UserClient: client.User}
			factory := server.NewPerRequestServerFactory("test", deps, []string{toolsets.ToolsetUser}, true)
			var endpoint string
			if mode == "http" {
				httpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{Stateless: true}))
				t.Cleanup(httpServer.Close)
				endpoint = httpServer.URL
			}
			for _, tt := range []struct {
				name, clientName, version, want string
			}{
				{name: "client", clientName: "Cursor", version: "1.2.3", want: prefix + " Cursor/1.2.3"},
				{name: "different client", clientName: "Codex", version: "0.1.0", want: prefix + " Codex/0.1.0"},
				{name: "no client", want: prefix},
				{name: "no version", clientName: "Cursor", want: prefix + " Cursor"},
				{name: "invalid header", clientName: "Cursor\r\nX-Extra: bad", version: "1.2.3", want: prefix},
				{name: "at limit", clientName: maxName, want: prefix + " " + maxName},
				{name: "over limit", clientName: maxName + "overflow", want: prefix + " " + maxName},
			} {
				t.Run(tt.name, func(t *testing.T) {
					ctx := t.Context()
					var transport mcp.Transport
					options := &mcp.ClientSessionOptions{}
					if mode == "http" {
						transport = &mcp.StreamableClientTransport{Endpoint: endpoint, DisableStandaloneSSE: true}
					} else {
						serverTransport, clientTransport := mcp.NewInMemoryTransports()
						s := server.NewMCPServer("test", deps, server.WithToolsets(toolsets.ToolsetUser))
						serverSession, err := s.Connect(ctx, serverTransport, nil)
						require.NoError(t, err)
						t.Cleanup(func() { _ = serverSession.Close() })
						transport = clientTransport
						options.ProtocolVersion = "2025-06-18"
					}
					mcpClient := mcp.NewClient(&mcp.Implementation{Name: tt.clientName, Version: tt.version}, nil)
					session, err := mcpClient.Connect(ctx, transport, options)
					require.NoError(t, err)
					t.Cleanup(func() { _ = session.Close() })
					for range 2 {
						result, err := session.CallTool(ctx, &mcp.CallToolParams{
							Name: "current_user",
							Arguments: map[string]any{"telemetry": map[string]any{
								"context": "Check the current user's identity for this MCP client.",
							}},
						})
						require.NoError(t, err)
						require.False(t, result.IsError)
						require.Equal(t, tt.want, <-userAgents)
					}
				})
			}
		})
	}
}

func TestAPIUserAgentPreservesExplicitHeaders(t *testing.T) {
	prefix := commands.UserAgent("1.22.0")
	for _, mode := range []string{"fixed", "passthrough"} {
		for _, value := range []string{"custom-client/9.8.7", prefix, "", strings.Repeat("x", 1100)} {
			t.Run(mode+"/"+value[:min(len(value), 50)], func(t *testing.T) {
				userAgents := make(chan string, 1)
				api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					userAgents <- r.UserAgent()
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"user-id","name":"Test User"}`))
				}))
				t.Cleanup(api.Close)
				var config *headerpassthrough.Config
				var headers map[string]string
				if mode == "fixed" {
					headers = map[string]string{"user-agent": value}
				} else {
					var err error
					config, err = headerpassthrough.New([]string{"User-Agent"}, nil, api.URL)
					require.NoError(t, err)
				}
				inner, err := newAPITransport(config, "", "", "1.22.0")
				require.NoError(t, err)
				httpClient := trace.NewHTTPClientWithHeadersAndTransport(headers, inner)
				client, err := gobuildkite.NewOpts(
					gobuildkite.WithBaseURL(api.URL+"/"),
					gobuildkite.WithUserAgent(prefix),
					gobuildkite.WithHTTPClient(httpClient),
				)
				require.NoError(t, err)
				httpClient.Transport = &userAgentTransport{wrapped: httpClient.Transport, userAgent: prefix, apiURL: client.BaseURL}
				deps := buildkitetools.ToolDependencies{UserClient: client.User}
				factory := server.NewPerRequestServerFactory("test", deps, []string{toolsets.ToolsetUser}, true)
				var handler http.Handler = mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{Stateless: true})
				if config != nil {
					handler = config.WrapHandler(handler)
				}
				httpServer := httptest.NewServer(handler)
				t.Cleanup(httpServer.Close)
				mcpHTTPClient := httpServer.Client()
				mcpHTTPClient.Transport = requestHeaderTransport{next: mcpHTTPClient.Transport, name: "User-Agent", value: value}
				mcpClient := mcp.NewClient(&mcp.Implementation{Name: "Cursor", Version: "1.2.3"}, nil)
				session, err := mcpClient.Connect(t.Context(), &mcp.StreamableClientTransport{
					Endpoint: httpServer.URL, HTTPClient: mcpHTTPClient, DisableStandaloneSSE: true,
				}, nil)
				require.NoError(t, err)
				t.Cleanup(func() { _ = session.Close() })
				result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
					Name: "current_user",
					Arguments: map[string]any{"telemetry": map[string]any{
						"context": "Verify the explicitly configured User-Agent stays unchanged.",
					}},
				})
				require.NoError(t, err)
				require.False(t, result.IsError)
				require.Equal(t, value, <-userAgents)
			})
		}
	}
}

func TestAPIUserAgentOmitsClientOnArtifactRedirect(t *testing.T) {
	prefix := commands.UserAgent("1.22.0")
	storage := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, prefix, r.UserAgent())
		_, _ = w.Write([]byte("hello\n"))
	}))
	t.Cleanup(storage.Close)
	api := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, prefix+" Cursor/1.2.3", r.UserAgent())
		if strings.HasSuffix(r.URL.Path, "/download") {
			http.Redirect(w, r, storage.URL+"/artifact.txt", http.StatusFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"artifact-id","mime_type":"text/plain","file_size":6}`))
	}))
	t.Cleanup(api.Close)
	httpClient := trace.NewHTTPClientWithHeadersAndTransport(nil, api.Client().Transport)
	client, err := gobuildkite.NewOpts(
		gobuildkite.WithBaseURL(api.URL+"/"),
		gobuildkite.WithUserAgent(prefix),
		gobuildkite.WithHTTPClient(httpClient),
	)
	require.NoError(t, err)
	httpClient.Transport = &userAgentTransport{wrapped: httpClient.Transport, userAgent: prefix, apiURL: client.BaseURL}
	deps := buildkitetools.ToolDependencies{
		ArtifactsClient: &buildkitetools.BuildkiteClientAdapter{Client: client, HTTPClient: httpClient},
	}
	s := server.NewMCPServer("test", deps, server.WithToolsets(toolsets.ToolsetArtifacts))
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := s.Connect(t.Context(), serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	mcpClient := mcp.NewClient(&mcp.Implementation{Name: "Cursor", Version: "1.2.3"}, nil)
	session, err := mcpClient.Connect(t.Context(), clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "get_artifact",
		Arguments: map[string]any{
			"org_slug": "org", "pipeline_slug": "pipeline", "build_number": "42",
			"job_id": "job-id", "artifact_id": "artifact-id",
			"telemetry": map[string]any{"context": "Read the small text artifact returned by the API."},
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "hello")
}
