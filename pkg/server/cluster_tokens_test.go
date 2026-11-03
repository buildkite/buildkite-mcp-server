package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	buildkiteapi "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// Exercise JSON argument decoding, registration, middleware, and the real SDK's
// HTTP encoding rather than asserting values passed to a mock client.
func clusterTokenSession(t *testing.T, handler http.HandlerFunc, opts ...ToolsetOption) *mcp.ClientSession {
	t.Helper()
	apiServer := httptest.NewServer(handler)
	t.Cleanup(apiServer.Close)
	api, err := buildkiteapi.NewOpts(buildkiteapi.WithBaseURL(apiServer.URL + "/"))
	require.NoError(t, err)
	factory := NewPerRequestServerFactory("test", buildkite.ToolDependencies{ClusterTokensClient: api.ClusterTokens}, []string{"clusters"}, false, opts...)
	mcpServer := httptest.NewServer(mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{Stateless: true}))
	t.Cleanup(mcpServer.Close)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: mcpServer.URL}, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}

func TestClusterTokenWritesThroughHTTP(t *testing.T) {
	for _, tool := range []string{"create_cluster_token", "update_cluster_token"} {
		for _, tt := range []struct{ name, fields string }{
			{"omitted", `{}`},
			{"restricted", `{"permissions":["run_jobs","read_metrics"],"allow_all_queues":false,"allowed_queue_ids":["queue-uuid-a","queue-uuid-b"]}`},
			{"no actions or queues", `{"permissions":[],"allow_all_queues":false,"allowed_queue_ids":[]}`},
			{"all queues", `{"allow_all_queues":true,"allowed_queue_ids":[]}`},
			{"clear bindings only", `{"allowed_queue_ids":[]}`},
			{"permissions only", `{"permissions":["read_secrets"]}`},
			{"description only", `{"description":"test token"}`},
		} {
			t.Run(tool+"/"+tt.name, func(t *testing.T) {
				var expected map[string]any
				require.NoError(t, json.Unmarshal([]byte(tt.fields), &expected))
				if tool == "create_cluster_token" {
					expected["description"] = "test token"
				}
				expectedJSON, err := json.Marshal(expected)
				require.NoError(t, err)
				var calls atomic.Int32
				session := clusterTokenSession(t, func(w http.ResponseWriter, r *http.Request) {
					calls.Add(1)
					path, method := "/v2/organizations/acme/clusters/cluster-uuid/tokens", http.MethodPost
					if tool == "update_cluster_token" {
						path, method = path+"/token-uuid", http.MethodPatch
					}
					assert.Equal(t, method, r.Method)
					assert.Equal(t, path, r.URL.Path)
					body, err := io.ReadAll(r.Body)
					assert.NoError(t, err)
					assert.JSONEq(t, string(expectedJSON), string(body))
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, `{"id":"token-uuid","permissions":[],"allow_all_queues":false,"allowed_queue_ids":[]}`)
				})
				args := map[string]any{"org_slug": "acme", "cluster_id": "cluster-uuid"}
				if tool == "create_cluster_token" {
					args["description"] = "test token"
				}
				require.NoError(t, json.Unmarshal([]byte(tt.fields), &args))
				if tool == "update_cluster_token" {
					args["token_id"] = "token-uuid"
				}
				result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
				require.NoError(t, err)
				require.False(t, result.IsError)
				require.EqualValues(t, 1, calls.Load())
				require.Contains(t, result.Content[0].(*mcp.TextContent).Text, `"allow_all_queues": false`)
			})
		}
	}
}

func TestClusterTokenMetadataWritesThroughHTTP(t *testing.T) {
	for _, tt := range []struct{ tool, fields string }{
		{"create_cluster_token", `{"description":"ephemeral","allowed_ip_addresses":"192.0.2.0/24 198.51.100.1","expires_at":"2030-01-02T03:04:05Z"}`},
		{"update_cluster_token", `{"description":"","allowed_ip_addresses":""}`},
	} {
		t.Run(tt.tool, func(t *testing.T) {
			session := clusterTokenSession(t, func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.JSONEq(t, tt.fields, string(body))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"id":"token-uuid"}`)
			})
			args := map[string]any{"org_slug": "acme", "cluster_id": "cluster-uuid"}
			require.NoError(t, json.Unmarshal([]byte(tt.fields), &args))
			if tt.tool == "update_cluster_token" {
				args["token_id"] = "token-uuid"
			}
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
		})
	}
}

func TestClusterTokenNullsDoNotBecomeOmittedAccess(t *testing.T) {
	var calls atomic.Int32
	session := clusterTokenSession(t, func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_, _ = io.WriteString(w, `{}`)
	})
	for _, tool := range []string{"create_cluster_token", "update_cluster_token"} {
		fields := []string{"permissions", "allow_all_queues", "allowed_queue_ids", "description", "allowed_ip_addresses"}
		if tool == "create_cluster_token" {
			fields = append(fields, "expires_at")
		}
		for _, field := range fields {
			t.Run(tool+"/"+field, func(t *testing.T) {
				args := map[string]any{"org_slug": "acme", "cluster_id": "cluster-uuid"}
				if tool == "create_cluster_token" {
					args["description"] = "test token"
				}
				args[field] = nil
				if tool == "update_cluster_token" {
					args["token_id"] = "token-uuid"
				}
				result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
				require.NoError(t, err)
				require.True(t, result.IsError)
				require.Contains(t, result.Content[0].(*mcp.TextContent).Text, field)
			})
		}
	}
	require.Zero(t, calls.Load())
}

func TestClusterTokenReadbackThroughHTTP(t *testing.T) {
	for _, tool := range []string{"list_cluster_tokens", "get_cluster_token"} {
		for _, response := range []string{
			`{"id":"token-uuid","description":"flags disabled"}`,
			`{"id":"token-uuid","permissions":[],"allow_all_queues":false,"allowed_queue_ids":[]}`,
			`{"id":"token-uuid","permissions":["read_secrets"],"allow_all_queues":false,"allowed_queue_ids":["queue-uuid-b"]}`,
			`{"id":"token-uuid","permissions":["run_jobs"],"allow_all_queues":true,"allowed_queue_ids":[]}`,
		} {
			t.Run(tool+"/"+response, func(t *testing.T) {
				session := clusterTokenSession(t, func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, http.MethodGet, r.Method)
					path := "/v2/organizations/acme/clusters/cluster-uuid/tokens"
					body := response
					if tool == "list_cluster_tokens" {
						assert.Equal(t, "3", r.URL.Query().Get("page"))
						assert.Equal(t, "7", r.URL.Query().Get("per_page"))
						w.Header().Set("Link", "<next-page>; rel=\"next\"")
						body = "[" + body + "]"
					} else {
						path += "/token-uuid"
					}
					assert.Equal(t, path, r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					_, _ = io.WriteString(w, body)
				})
				args := map[string]any{"org_slug": "acme", "cluster_id": "cluster-uuid"}
				if tool == "list_cluster_tokens" {
					args["page"], args["per_page"] = 3, 7
				} else {
					args["token_id"] = "token-uuid"
				}
				result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
				require.NoError(t, err)
				require.False(t, result.IsError)
				var output map[string]any
				require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &output))
				if tool == "list_cluster_tokens" {
					require.Equal(t, `<next-page>; rel="next"`, output["headers"].(map[string]any)["Link"])
					output = output["items"].([]any)[0].(map[string]any)
				}
				var expected map[string]any
				require.NoError(t, json.Unmarshal([]byte(response), &expected))
				for _, key := range []string{"id", "permissions", "allow_all_queues", "allowed_queue_ids", "token"} {
					expectedValue, present := expected[key]
					if present {
						require.Equal(t, expectedValue, output[key], key)
					} else {
						require.NotContains(t, output, key)
					}
				}
			})
		}
	}
}

func TestClusterTokenCreateReturnsSecretWithoutLoggingOrTracingIt(t *testing.T) {
	const secret = "synthetic-agent-token-not-a-real-credential"
	var logs bytes.Buffer
	previousLogger := log.Logger
	log.Logger = zerolog.New(&logs).Level(zerolog.DebugLevel)
	t.Cleanup(func() { log.Logger = previousLogger })
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	previousProvider := otel.GetTracerProvider()
	otel.SetTracerProvider(provider)
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		_ = provider.Shutdown(context.Background())
	})
	session := clusterTokenSession(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprintf(w, `{"id":"token-uuid","token":%q}`, secret)
	})
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "create_cluster_token", Arguments: map[string]any{"org_slug": "acme", "cluster_id": "cluster-uuid", "description": "test token"},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	var output map[string]any
	require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &output))
	require.Equal(t, secret, output["token"])
	require.NotEmpty(t, logs.String())
	require.NotContains(t, logs.String(), secret)
	require.NotEmpty(t, recorder.Ended())
	for _, span := range recorder.Ended() {
		require.NotContains(t, fmt.Sprint(span.Attributes(), span.Events(), span.Status()), secret)
	}
}

func TestClusterTokenDiscoveryAndFiltering(t *testing.T) {
	reads := []string{"list_cluster_tokens", "get_cluster_token"}
	writes := []string{"create_cluster_token", "update_cluster_token"}
	for _, tt := range []struct {
		name        string
		opts        []ToolsetOption
		read, write bool
	}{
		{name: "clusters", read: true, write: true},
		{name: "read only", opts: []ToolsetOption{WithReadOnly(true)}, read: true},
		{name: "minimum access", opts: []ToolsetOption{WithMinimumAccess(true)}},
		{name: "excluded", opts: []ToolsetOption{WithDisabledToolsets("clusters")}},
		{name: "other toolset", opts: []ToolsetOption{WithToolsets("pipelines")}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			session := clusterTokenSession(t, func(w http.ResponseWriter, r *http.Request) { calls.Add(1) }, tt.opts...)
			result, err := session.ListTools(context.Background(), nil)
			require.NoError(t, err)
			found := map[string]*mcp.Tool{}
			for _, tool := range result.Tools {
				found[tool.Name] = tool
			}
			for _, names := range [][]string{reads, writes} {
				for _, name := range names {
					want := tt.read
					if strings.HasPrefix(name, "create_") || strings.HasPrefix(name, "update_") {
						want = tt.write
					}
					if !want {
						require.NotContains(t, found, name)
						_, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: map[string]any{"org_slug": "acme", "cluster_id": "cluster-uuid", "token_id": "token-uuid"}})
						require.Error(t, err)
						continue
					}
					require.Contains(t, found, name)
					require.Equal(t, !strings.HasPrefix(name, "create_") && !strings.HasPrefix(name, "update_"), found[name].Annotations.ReadOnlyHint)
					if names[0] == writes[0] {
						schemaBytes, err := json.Marshal(found[name].InputSchema)
						require.NoError(t, err)
						var schema struct {
							Properties map[string]any
							Required   []string
						}
						require.NoError(t, json.Unmarshal(schemaBytes, &schema))
						for _, field := range []string{"permissions", "allow_all_queues", "allowed_queue_ids"} {
							require.Contains(t, schema.Properties, field)
							require.NotContains(t, schema.Required, field)
						}
					}
				}
			}
			require.Zero(t, calls.Load())
		})
	}
	for _, tool := range toolsets.CreateBuiltinToolsets()["clusters"].Tools {
		if strings.Contains(tool.Tool.Name, "cluster_token") {
			want := "write_clusters"
			if tool.IsReadOnly() {
				want = "read_clusters"
			}
			require.Equal(t, []string{want}, tool.RequiredScopes)
		}
	}
}

func TestClusterTokenAPIErrorsThroughHTTP(t *testing.T) {
	for _, tool := range []string{"list_cluster_tokens", "get_cluster_token", "create_cluster_token", "update_cluster_token"} {
		for _, status := range []int{401, 403, 422} {
			t.Run(fmt.Sprintf("%s/%d", tool, status), func(t *testing.T) {
				var unauthorized atomic.Bool
				session := clusterTokenSession(t, func(w http.ResponseWriter, r *http.Request) {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(status)
					_, _ = io.WriteString(w, `{"message":"API rejected scoped token request"}`)
				}, WithOnUnauthorized(func() { unauthorized.Store(true) }))
				args := map[string]any{"org_slug": "acme", "cluster_id": "cluster-uuid"}
				if tool == "create_cluster_token" {
					args["description"] = "test token"
				}
				if tool == "get_cluster_token" || tool == "update_cluster_token" {
					args["token_id"] = "token-uuid"
				}
				if tool == "create_cluster_token" || tool == "update_cluster_token" {
					// Invalid combinations must reach the authoritative API unchanged.
					args["allow_all_queues"], args["allowed_queue_ids"] = true, []string{"queue-uuid"}
				}
				result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
				if status == 401 {
					require.ErrorContains(t, err, "unauthorized")
					require.True(t, unauthorized.Load())
				} else {
					require.NoError(t, err)
					require.True(t, result.IsError)
					require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "API rejected scoped token request")
					require.False(t, unauthorized.Load())
				}
			})
		}
	}
}
