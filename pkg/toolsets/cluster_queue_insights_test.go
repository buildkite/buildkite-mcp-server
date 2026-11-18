package toolsets

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	buildkiteapi "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func queueInsightsMCPClient(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	api, err := buildkiteapi.NewOpts(buildkiteapi.WithBaseURL(baseURL + "/"))
	require.NoError(t, err)
	registry := NewToolsetRegistry()
	registry.RegisterToolsets(CreateBuiltinToolsets())
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	server.AddReceivingMiddleware(buildkite.InjectDepsMiddleware(buildkite.ToolDependencies{ClusterQueuesClient: api.ClusterQueues}))
	for _, tool := range registry.GetEnabledTools([]string{ToolsetClusters}, true) {
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

func TestClusterQueueInsightsThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		name       string
		include    string
		timePeriod string
	}{
		{name: "defaults"},
		{name: "history", include: "history", timePeriod: "1h"},
		{name: "comparison without history", include: "history_comparison", timePeriod: "24h"},
		{name: "hourly changes without history", include: "hourly_changes"},
		{name: "history and comparison", include: "history,history_comparison", timePeriod: "7d"},
		{name: "history and hourly changes", include: "history,hourly_changes"},
		{name: "comparison and hourly changes", include: "history_comparison,hourly_changes"},
		{name: "all includes", include: "history,history_comparison,hourly_changes", timePeriod: "7d"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var response map[string]json.RawMessage
			require.NoError(t, json.Unmarshal([]byte(queueInsightsResponse), &response))
			if !strings.Contains(tt.include, "history") {
				delete(response, "history")
			} else if !strings.Contains(tt.include, "history_comparison") {
				var history map[string]json.RawMessage
				require.NoError(t, json.Unmarshal(response["history"], &history))
				delete(history, "previous_period")
				delete(history, "changes")
				encoded, err := json.Marshal(history)
				require.NoError(t, err)
				response["history"] = encoded
			}
			if !strings.Contains(tt.include, "hourly_changes") {
				delete(response, "hourly_changes")
			}
			body, err := json.Marshal(response)
			require.NoError(t, err)
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/acme/clusters/cluster-id/queues/queue-id/insights", r.URL.Path)
				query := url.Values{}
				if tt.include != "" {
					query.Set("include", tt.include)
				}
				if tt.timePeriod != "" {
					query.Set("time_period", tt.timePeriod)
				}
				assert.Equal(t, query, r.URL.Query(), "do not add defaults or forward telemetry to the API")
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write(body)
			}))
			t.Cleanup(srv.Close)
			client := queueInsightsMCPClient(t, srv.URL)
			args := map[string]any{"org_slug": "acme", "cluster_id": "cluster-id", "queue_id": "queue-id"}
			if tt.include != "" {
				args["include"] = tt.include
			}
			if tt.timePeriod != "" {
				args["time_period"] = tt.timePeriod
			}
			args["telemetry"] = map[string]any{"context": "The user is investigating queue capacity and waiting jobs by comparing current observations with historical metrics for the same queue."}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_cluster_queue_insights", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, int32(1), requests.Load())
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			require.JSONEq(t, string(body), text.Text, "preserve nulls, zeroes, fractional averages, timestamps, coverage and limitations")
		})
	}
}

func TestClusterQueueInsightsAPIErrorsThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		status int
		body   string
	}{
		{http.StatusUnauthorized, `{"message":"Token expired"}`},
		{http.StatusForbidden, `{"message":"Your plan does not include Cluster Insights."}`},
		{http.StatusNotFound, `{"message":"Not Found"}`},
		{http.StatusUnprocessableEntity, `{"message":"Invalid time_period","errors":["time_period requires history or history_comparison"]}`},
	} {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.body)
			}))
			t.Cleanup(srv.Close)
			client := queueInsightsMCPClient(t, srv.URL)
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{
				Name:      "get_cluster_queue_insights",
				Arguments: map[string]any{"org_slug": "acme", "cluster_id": "cluster-id", "queue_id": "queue-id", "time_period": "24h"},
			})
			require.Equal(t, int32(1), requests.Load(), "the API validates access and option combinations")
			if tt.status == http.StatusUnauthorized {
				require.ErrorIs(t, err, buildkite.ErrUnauthorized)
			} else {
				require.NoError(t, err)
				require.True(t, result.IsError)
				require.Len(t, result.Content, 1)
				text, ok := result.Content[0].(*mcp.TextContent)
				require.True(t, ok)
				require.JSONEq(t, tt.body, text.Text)
			}
		})
	}
}

const queueInsightsResponse = `{
  "resource":{"type":"queue","uuid":"queue-id"},
  "current":{
    "agents":{"observed_at":null,"source_high_watermark_at":null,"values":{"connected":0,"busy":null},"limitations":{"missing_publication":true}},
    "jobs":{"observed_at":"2026-11-12T08:30:00.123456Z","source_high_watermark_at":"2026-11-12T08:29:55Z","values":{"scheduled":3,"running":0},"computed":{"awaiting_execution":3,"in_progress":0},"limitations":{}}
  },
  "recent":null,
  "history":{
    "selected_period":{
      "window":{"from":"2026-11-12T07:30:00Z","to":"2026-11-12T08:30:00Z","interval_seconds":1800},
      "counts":{"aggregation":"last","agents":{"values":{"connected":[0,null]},"limitations":{"missing_publication":[1]}},"jobs":{"values":{"awaiting_execution":[3,null],"in_progress":[0,null]},"limitations":{"missing_publication":[1]}}},
      "job_completions":{"values":{"total":[4,0],"passed":[3,0],"failed":[1,0]},"limitations":{}},
      "wait_times":{"unit":"seconds","values":{"min":[1.25,null],"p50":[2.5,null],"p95":[4.75,null],"max":[8.5,null]},"samples":{"valid_count":[4,0],"incomplete_count":[1,0]},"limitations":{"no_samples":[1]}}
    },
    "previous_period":{
      "window":{"from":"2026-11-12T06:30:00Z","to":"2026-11-12T07:30:00Z","interval_seconds":1800},
      "counts":{"aggregation":"last","agents":{"values":{"connected":[1,0]},"limitations":{}},"jobs":{"values":{"awaiting_execution":[1,1],"in_progress":[0,0]},"limitations":{}}},
      "job_completions":{"values":{"total":[2,0],"passed":[1,0],"failed":[1,0]},"limitations":{}},
      "wait_times":{"unit":"seconds","values":{"min":[1,1],"p50":[2,2],"p95":[4,4],"max":[6,6]},"samples":{"valid_count":[2,1],"incomplete_count":[0,0]},"limitations":{}}
    },
    "changes":{
      "counts":{"basis":"last_bucket","agents":{"connected":{"selected":null,"previous":0,"difference":null,"relative_change_percent":null,"limitations":{"selected_value_unavailable":true}}},"jobs":{"awaiting_execution":{"selected":null,"previous":1,"difference":null,"relative_change_percent":null,"limitations":{"selected_value_unavailable":true}},"in_progress":{"selected":null,"previous":0,"difference":null,"relative_change_percent":null,"limitations":{"selected_value_unavailable":true}}}},
      "job_completions":{"basis":"period_total","total":{"selected":4,"previous":2,"difference":2,"relative_change_percent":100,"limitations":{}},"pass_rate":{"unit":"percent","selected":75,"previous":50,"percentage_point_change":25,"limitations":{}}},
      "wait_times":{"basis":"last_bucket","unit":"seconds","min":{"selected":null,"previous":1,"difference":null,"relative_change_percent":null,"limitations":{"selected_value_unavailable":true}},"p50":{"selected":null,"previous":2,"difference":null,"relative_change_percent":null,"limitations":{"selected_value_unavailable":true}},"p95":{"selected":null,"previous":4,"difference":null,"relative_change_percent":null,"limitations":{"selected_value_unavailable":true}},"max":{"selected":null,"previous":6,"difference":null,"relative_change_percent":null,"limitations":{"selected_value_unavailable":true}}}
    }
  },
  "hourly_changes":{
    "aggregation":"time_weighted_average","interval_seconds":3600,
    "current":{"reference_at":"2026-11-12T08:00:00Z","window":{"from":"2026-11-12T07:00:00Z","to":"2026-11-12T08:30:00Z"},"agents":{"values":{"connected":[1.25,0]},"covered_seconds":[3600,1799.25],"limitations":{"partial_bucket":[1]}},"jobs":{"values":{"awaiting_execution":[2.5,null],"in_progress":[0.75,null]},"covered_seconds":[3600,null],"limitations":{"missing_publication":[1]}}},
    "yesterday":{"reference_at":"2026-11-11T08:00:00Z","window":{"from":"2026-11-11T07:00:00Z","to":"2026-11-11T10:00:00Z"},"agents":{"values":{"connected":[1,0,2]},"covered_seconds":[3600,3600,3600],"limitations":{}},"jobs":{"values":{"awaiting_execution":[5,0,1],"in_progress":[0,1,2]},"covered_seconds":[3600,3600,3600],"limitations":{}}},
    "last_week":{"reference_at":"2026-11-05T08:00:00Z","window":{"from":"2026-11-05T07:00:00Z","to":"2026-11-05T10:00:00Z"},"agents":{"values":{"connected":[null,null,null]},"covered_seconds":[null,null,null],"limitations":{"missing_publication":[0,1,2]}},"jobs":{"values":{"awaiting_execution":[null,null,null],"in_progress":[null,null,null]},"covered_seconds":[null,null,null],"limitations":{"missing_publication":[0,1,2]}}},
    "changes":{"basis":"completed_hour","yesterday":{"agents":{"connected":{"selected":1.25,"previous":1,"difference":0.25,"relative_change_percent":25,"limitations":{}}},"jobs":{"awaiting_execution":{"selected":2.5,"previous":5,"difference":-2.5,"relative_change_percent":-50,"limitations":{}},"in_progress":{"selected":0.75,"previous":0,"difference":0.75,"relative_change_percent":null,"limitations":{}}}},"last_week":{"agents":{"connected":{"selected":1.25,"previous":null,"difference":null,"relative_change_percent":null,"limitations":{"previous_value_unavailable":true}}},"jobs":{"awaiting_execution":{"selected":2.5,"previous":null,"difference":null,"relative_change_percent":null,"limitations":{"previous_value_unavailable":true}},"in_progress":{"selected":0.75,"previous":null,"difference":null,"relative_change_percent":null,"limitations":{"previous_value_unavailable":true}}}}}
  }
}`
