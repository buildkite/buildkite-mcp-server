package toolsets

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	buildkiteapi "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func diagnosticsMCPClient(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	api, err := buildkiteapi.NewOpts(buildkiteapi.WithBaseURL(baseURL + "/"))
	require.NoError(t, err)
	registry := NewToolsetRegistry()
	registry.RegisterToolsets(CreateBuiltinToolsets())
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	server.AddReceivingMiddleware(buildkite.InjectDepsMiddleware(buildkite.ToolDependencies{JobsClient: api.Jobs}))
	for _, tool := range registry.GetEnabledTools([]string{ToolsetBuilds}, true) {
		tool.Register(server)
	}
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestJobDiagnosticsThroughMCP(t *testing.T) {
	for _, include := range []string{"", "events", "dependencies", "insights", "events,dependencies", "events,insights", "dependencies,insights", "events,dependencies,insights"} {
		t.Run("include="+include, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/acme/jobs/job-id/diagnostics", r.URL.Path)
				query := url.Values{}
				if include != "" {
					query.Set("include", include)
				}
				assert.Equal(t, query, r.URL.Query(), "do not add includes or forward telemetry")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, jobDiagnosticsResponse)
			}))
			t.Cleanup(srv.Close)
			client := diagnosticsMCPClient(t, srv.URL)
			args := map[string]any{"org_slug": "acme", "job_id": "job-id"}
			if include != "" {
				args["include"] = include
			}
			args["telemetry"] = map[string]any{"context": "The user is investigating a waiting job using its lifecycle, recorded events, dependency context, concurrency requirements and available queue evidence."}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_job_diagnostics", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, int32(1), requests.Load())
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			require.JSONEq(t, jobDiagnosticsResponse, text.Text, "preserve evidence, references, nulls, empty collections and limitations")
			require.Contains(t, text.Text, "9007199254740993", "dynamic event integers must not round through float64")
		})
	}
}

func TestJobDiagnosticsAPIErrorsThroughMCP(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			const body = `{"message":"Diagnostics unavailable"}`
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = io.WriteString(w, body)
			}))
			t.Cleanup(srv.Close)
			client := diagnosticsMCPClient(t, srv.URL)
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "get_job_diagnostics", Arguments: map[string]any{"org_slug": "acme", "job_id": "job-id"},
			})
			require.Equal(t, int32(1), requests.Load())
			if status == http.StatusUnauthorized {
				require.ErrorIs(t, err, buildkite.ErrUnauthorized)
			} else {
				require.NoError(t, err)
				require.True(t, result.IsError)
				require.Len(t, result.Content, 1)
				text, ok := result.Content[0].(*mcp.TextContent)
				require.True(t, ok)
				require.JSONEq(t, body, text.Text)
			}
		})
	}
}

const jobDiagnosticsResponse = `{
  "resource":{"type":"job","uuid":"job-id","url":"https://api.buildkite.com/v2/organizations/acme/jobs/job-id"},
  "observed_at":"2026-11-19T02:34:56.123456Z",
  "details":{"name":"Deploy","type":"script","priority":0,"hosted":false,"lifecycle":{"state":"running","created_at":"2026-11-19T02:00:00Z","scheduled_at":"2026-11-19T02:00:00Z","runnable_at":null,"started_at":"2026-11-19T02:01:01.25Z","ended_at":null},"timings":{"runnable_to_start_seconds":1.25,"execution_seconds":null},"step":{"type":"step","uuid":"step-id"},"agent":null,"queue":null,"instance_shape":null,"routing":{"type":"unclustered"},"exit_status":null,"soft_failed":false,"broken_reason":null,"signal":null,"signal_reason":null,"retries_count":null,"retry_source":null,"retried_in_job":null,"unblockable":null,"unblocked_at":null,"links":[]},
  "events":{"selection":{"order":"newest_first","max_items":100},"window":{"from":"2026-11-12T02:34:56.123456Z","to":"2026-11-19T02:34:56.123456Z"},"items":[{"uuid":"event-id","type":"future_event","occurred_at":"2026-11-19T02:01:01.123456Z","actor":{"type":"system","uuid":null},"details":{"large_integer":9007199254740993,"nested":{"null":null,"empty":[],"object":{},"values":[false,0,"text"]}}},{"uuid":"empty-event","type":"started","occurred_at":"2026-11-19T02:00:01Z","actor":{"type":"agent","uuid":"agent-id"},"details":{}}],"limitations":{"state_transition_history_incomplete":true,"future_limitation":true}},
  "dependencies":{"selection":{"order":"breadth_first","max_steps":50,"max_edges":100},"items":null,"limitations":{"disabled":true},"links":[]},
  "concurrency":{"items":[],"limitations":{}},
  "retries":{"requirements":{"automatic":[],"manual":{"allowed":true,"permit_on_passed":false,"reason":null}},"evidence":{"observed_at":"2026-11-19T02:34:56.123456Z","automatic_retries_before_attempt":[]},"limitations":{"retry_decision_not_evaluated":true},"links":[]},
  "capacity":{"requirements":{"agent_query_rules":["queue=deploy"]},"evidence":null,"limitations":{"capacity_evidence_not_supported":true},"links":[]},
  "errors":{"items":null,"limitations":{"not_supported":true}},
  "history":{"recent_runs":{"items":null,"limitations":{"not_supported":true}}},
  "insights":[{"resource":{"type":"queue","uuid":"queue-id"},"current":null,"recent":null,"limitations":{"not_requested":true},"links":[]}],
  "context":{"scope":{"organization":{"resource":{"type":"organization","uuid":"org-id","slug":"acme","url":"https://api.buildkite.com/organizations/acme"},"observed_at":"2026-11-19T02:34:56.123456Z","details":{"dispatch_paused":false}},"pipeline":{"resource":{"type":"pipeline","uuid":"pipeline-id","slug":"deploy","url":"https://api.buildkite.com/pipelines/deploy"}},"build":{"resource":{"type":"build","uuid":"build-id","number":42,"url":"https://api.buildkite.com/builds/42"},"observed_at":"2026-11-19T02:34:56.123456Z","details":{"state":"running","blocked":false,"cancel_reason":null,"branch":"main","commit":"abcdef"}},"step":null,"cluster":null,"queue":null},"steps":[],"agents":[],"queues":[],"jobs":[],"builds":[],"limitations":{}}
}`
