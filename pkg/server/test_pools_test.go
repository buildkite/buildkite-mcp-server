package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	sdk "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Exercise the registered tools through MCP and the real SDK, not another
// implementation of the SDK interface. Detailed API decoding lives in the SDK.
func TestSchedulerToolsThroughMCP(t *testing.T) {
	const pipelineID = "11111111-1111-4111-8111-111111111111"
	const buildID = "22222222-2222-4222-8222-222222222222"
	const poolID = "33333333-3333-4333-8333-333333333333"
	const basePath = "/v2/organizations/acme/test-scheduler/pools"
	const next = "https://api.buildkite.com" + basePath + "?after=opaque%2Bcursor&pipeline_id=" + pipelineID + "&build_id=" + buildID + "&key=Ruby&per_page=30"

	var calls atomic.Int64
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "Bearer fixture-token", r.Header.Get("Authorization"))
		assert.Equal(t, buildID, r.URL.Query().Get("build_id"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("build_id") != buildID {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		switch r.URL.Path {
		case basePath:
			q := r.URL.Query()
			assert.Equal(t, pipelineID, q.Get("pipeline_id"))
			assert.Equal(t, "Ruby", q.Get("key"))
			assert.Equal(t, "30", q.Get("per_page"))
			assert.Empty(t, q.Get("page"))
			if q.Get("after") == "" {
				_, _ = fmt.Fprintf(w, `{"items":[{"id":%q,"key":"Ruby","state":"errored","parallelism":0,"error":{"message":"planning failed"},"muted_tests":[{"name":"large snapshot omitted"}]}],"links":{"next":%q}}`, poolID, next)
			} else {
				assert.Equal(t, "opaque+cursor", q.Get("after"))
				_, _ = w.Write([]byte(`{"items":[],"links":{}}`))
			}
		case basePath + "/" + poolID:
			_, _ = fmt.Fprintf(w, `{"id":%q,"state":"errored","error":{"message":"planning failed"},"muted_tests":[{"name":"muted example"}],"attempt_policy":{"max_attempts":3},"lease":{"max_attempts":2}}`, poolID)
		case basePath + "/" + poolID + "/metrics":
			_, _ = fmt.Fprintf(w, `{"observed_at":"2026-11-16T12:00:00Z","pool":{"id":%q,"state":"errored","drained":true},"attempts":{"total":7,"states":{"waiting":{"count":7}}},"leases":{"active":{"count":0}}}`, poolID)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(api.Close)
	client, err := sdk.NewClient(sdk.WithBaseURL(api.URL+"/"), sdk.WithTokenAuth("fixture-token"))
	require.NoError(t, err)
	srv := NewMCPServer("test", buildkite.ToolDependencies{TestPoolsClient: client.TestPools}, WithToolsets("tests"), WithReadOnly(true))
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	call := func(name string, args map[string]any) map[string]any {
		t.Helper()
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		require.NoError(t, err)
		require.False(t, result.IsError, "%v", result.Content)
		var body map[string]any
		decoder := json.NewDecoder(strings.NewReader(result.Content[0].(*mcp.TextContent).Text))
		decoder.UseNumber()
		require.NoError(t, decoder.Decode(&body))
		return body
	}
	args := map[string]any{"org_slug": "acme", "pipeline_uuid": pipelineID, "build_uuid": buildID, "key": "Ruby"}
	page := call("list_test_pools", args)
	require.Equal(t, int64(1), calls.Load(), "list must not follow continuation links automatically")
	item := page["items"].([]any)[0].(map[string]any)
	require.Equal(t, "planning failed", item["error"].(map[string]any)["message"])
	require.Equal(t, json.Number("0"), item["parallelism"], "zero is a useful planning result, not an absent value")
	require.NotContains(t, item, "muted_tests", "discovery pages must stay compact")
	require.Equal(t, next, page["links"].(map[string]any)["next"])
	u, err := url.Parse(next)
	require.NoError(t, err)
	args["after"] = u.Query().Get("after")
	require.Empty(t, call("list_test_pools", args)["items"])
	require.Equal(t, int64(2), calls.Load())

	detailArgs := map[string]any{"org_slug": "acme", "pool_uuid": poolID, "build_uuid": buildID}
	detail := call("get_test_pool", detailArgs)
	require.Equal(t, json.Number("3"), detail["attempt_policy"].(map[string]any)["max_attempts"])
	require.Len(t, detail["muted_tests"], 1, "explicit detail preserves the planning snapshot")
	metrics := call("get_test_pool_metrics", detailArgs)
	require.Equal(t, "2026-11-16T12:00:00Z", metrics["observed_at"])
	require.True(t, metrics["pool"].(map[string]any)["drained"].(bool))
	require.Equal(t, json.Number("7"), metrics["attempts"].(map[string]any)["states"].(map[string]any)["waiting"].(map[string]any)["count"])
	require.NotContains(t, metrics, "success", "tools return evidence, not a completion verdict")

	for _, invalid := range []map[string]any{
		{"after": "a", "before": "b"}, {"per_page": -1}, {"per_page": 101},
	} {
		input := map[string]any{"org_slug": "acme", "pipeline_uuid": pipelineID, "build_uuid": buildID}
		for k, v := range invalid {
			input[k] = v
		}
		result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "list_test_pools", Arguments: input})
		require.NoError(t, err)
		require.True(t, result.IsError)
	}
	require.Equal(t, int64(4), calls.Load(), "invalid cursors and page sizes must not reach the API")
}

func TestSchedulerToolsPreserveAPIErrors(t *testing.T) {
	for _, tc := range []struct {
		tool   string
		status int
		body   string
	}{
		{"list_test_pools", http.StatusForbidden, `{"message":"missing read_test_pool"}`},
		{"list_test_pools", http.StatusNotFound, `{"message":"Test Scheduler is not enabled for your organization","code":"test_scheduler_not_enabled"}`},
		{"get_test_pool_metrics", http.StatusNotFound, `{"message":"No Test Scheduler usage was found for this build","code":"test_scheduler_build_usage_not_found"}`},
		{"get_test_pool", http.StatusNotFound, `{"message":"Not Found"}`},
		{"get_test_pool_metrics", http.StatusServiceUnavailable, `{"message":"scheduler temporarily unavailable"}`},
	} {
		t.Run(tc.tool, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, _ = w.Write([]byte(tc.body))
			}))
			t.Cleanup(api.Close)
			client, err := sdk.NewClient(sdk.WithBaseURL(api.URL+"/"), sdk.WithMaxRetries(0))
			require.NoError(t, err)
			srv := NewMCPServer("test", buildkite.ToolDependencies{TestPoolsClient: client.TestPools}, WithToolsets("tests"))
			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			ss, err := srv.Connect(ctx, st, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = ss.Close() })
			session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, ct, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close() })
			args := map[string]any{"org_slug": "acme", "build_uuid": "build"}
			if tc.tool == "list_test_pools" {
				args["pipeline_uuid"] = "pipeline"
			} else {
				args["pool_uuid"] = "pool"
			}
			result, err := session.CallTool(ctx, &mcp.CallToolParams{Name: tc.tool, Arguments: args})
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.JSONEq(t, tc.body, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
