package buildkite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpanAggregatesThroughMCP(t *testing.T) {
	const scopesPath = "/v2/analytics/organizations/acme/suites/backend/span-aggregates/scopes"
	const buildScopesPath = "/v2/analytics/organizations/acme/builds/019d66fb-e8db-47eb-866c-94b85d42b9a1/span-aggregates/scopes"
	const suiteNamesPath = "/v2/analytics/organizations/acme/suites/backend/span-aggregates/span-names"
	const namesPath = "/v2/analytics/organizations/acme/builds/019d66fb-e8db-47eb-866c-94b85d42b9a1/span-aggregates/span-names"
	const scopesJSON = `[{"name":"db.client","span_count":3,"duration_ns":1000000001},{"name":"","span_count":1,"duration_ns":7}]`
	const namesJSON = `{"scope_name":"db.client","span_names":[{"name":"SELECT users","span_count":2,"duration_ns":15,"avg_duration_ns":7.5,"p50_duration_ns":6.25,"p95_duration_ns":8.75,"max_duration_ns":9}],"span_names_truncated":true}`

	for _, tt := range []struct {
		name     string
		tool     string
		args     map[string]any
		path     string
		query    url.Values
		response string
		status   int
	}{
		{
			name: "scopes with explicit period", tool: "list_span_aggregate_scopes",
			args: map[string]any{"test_suite_slug": "backend", "period": "4hours"},
			path: scopesPath, query: url.Values{"period": {"4hours"}}, response: scopesJSON,
		},
		{
			name: "scopes with API default period", tool: "list_span_aggregate_scopes",
			args: map[string]any{"test_suite_slug": "backend"},
			path: scopesPath, query: url.Values{}, response: `[]`,
		},
		{
			name: "build scopes", tool: "list_span_aggregate_scopes_for_build",
			args: map[string]any{},
			path: buildScopesPath, query: url.Values{}, response: scopesJSON,
		},
		{
			name: "suite names with period and sorting", tool: "list_span_aggregate_names",
			args: map[string]any{"test_suite_slug": "backend", "scope_name": "db.client", "period": "7days", "sort": "-p95", "limit": 7},
			path: suiteNamesPath, query: url.Values{"scope_name": {"db.client"}, "period": {"7days"}, "sort": {"-p95"}, "limit": {"7"}}, response: namesJSON,
		},
		{
			name: "suite unnamed scope with API defaults", tool: "list_span_aggregate_names",
			args: map[string]any{"test_suite_slug": "backend", "scope_name": ""},
			path: suiteNamesPath, query: url.Values{"scope_name": {""}},
			response: `{"scope_name":"","span_names":[],"span_names_truncated":false}`,
		},
		{
			name: "names with sorting and truncation", tool: "list_span_aggregate_names_for_build",
			args: map[string]any{"scope_name": "db.client", "sort": "-p95", "limit": 7},
			path: namesPath, query: url.Values{"scope_name": {"db.client"}, "sort": {"-p95"}, "limit": {"7"}}, response: namesJSON,
		},
		{
			name: "unnamed scope with API defaults", tool: "list_span_aggregate_names_for_build",
			args: map[string]any{"scope_name": ""},
			path: namesPath, query: url.Values{"scope_name": {""}},
			response: `{"scope_name":"","span_names":[],"span_names_truncated":false}`,
		},
		{
			name: "scope access denied", tool: "list_span_aggregate_scopes",
			args: map[string]any{"test_suite_slug": "backend"},
			path: scopesPath, query: url.Values{}, status: http.StatusForbidden, response: `{"message":"Access denied"}`,
		},
		{
			name: "invalid names sort", tool: "list_span_aggregate_names_for_build",
			args: map[string]any{"scope_name": "db.client", "sort": "invalid"},
			path: namesPath, query: url.Values{"scope_name": {"db.client"}, "sort": {"invalid"}},
			status: http.StatusUnprocessableEntity, response: `{"message":"Invalid sort"}`,
		},
		{
			name: "scopes authentication error", tool: "list_span_aggregate_scopes",
			args: map[string]any{"test_suite_slug": "backend"},
			path: scopesPath, query: url.Values{}, status: http.StatusUnauthorized, response: `{"message":"Unauthorized"}`,
		},
		{
			name: "names authentication error", tool: "list_span_aggregate_names_for_build",
			args: map[string]any{"scope_name": ""},
			path: namesPath, query: url.Values{"scope_name": {""}}, status: http.StatusUnauthorized, response: `{"message":"Unauthorized"}`,
		},
		{
			name: "build scopes access denied", tool: "list_span_aggregate_scopes_for_build",
			args: map[string]any{},
			path: buildScopesPath, query: url.Values{}, status: http.StatusForbidden, response: `{"message":"Access denied"}`,
		},
		{
			name: "suite names authentication error", tool: "list_span_aggregate_names",
			args: map[string]any{"test_suite_slug": "backend", "scope_name": ""},
			path: suiteNamesPath, query: url.Values{"scope_name": {""}}, status: http.StatusUnauthorized, response: `{"message":"Unauthorized"}`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			apiServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, tt.path, r.URL.Path)
				assert.Equal(t, tt.query, r.URL.Query())
				w.Header().Set("Content-Type", "application/json")
				if tt.status != 0 {
					w.WriteHeader(tt.status)
				}
				_, _ = w.Write([]byte(tt.response))
			}))
			defer apiServer.Close()
			api, err := buildkite.NewOpts(buildkite.WithBaseURL(apiServer.URL + "/"))
			require.NoError(t, err)
			server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "test"}, nil)
			server.AddReceivingMiddleware(InjectDepsMiddleware(ToolDependencies{SpanAggregatesClient: api.SpanAggregates}))
			scopesTool, scopesHandler, _ := ListSpanAggregateScopes()
			mcp.AddTool(server, &scopesTool, scopesHandler)
			buildScopesTool, buildScopesHandler, _ := ListSpanAggregateScopesForBuild()
			mcp.AddTool(server, &buildScopesTool, buildScopesHandler)
			suiteNamesTool, suiteNamesHandler, _ := ListSpanAggregateNames()
			mcp.AddTool(server, &suiteNamesTool, suiteNamesHandler)
			namesTool, namesHandler, _ := ListSpanAggregateNamesForBuild()
			mcp.AddTool(server, &namesTool, namesHandler)
			ctx := context.Background()
			serverTransport, clientTransport := mcp.NewInMemoryTransports()
			serverSession, err := server.Connect(ctx, serverTransport, nil)
			require.NoError(t, err)
			defer serverSession.Close()
			client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
			require.NoError(t, err)
			defer client.Close()

			tt.args["org_slug"] = "acme"
			if tt.tool == namesTool.Name || tt.tool == buildScopesTool.Name {
				tt.args["build_uuid"] = "019d66fb-e8db-47eb-866c-94b85d42b9a1"
			}
			tt.args["telemetry"] = map[string]any{"context": "Inspecting test span aggregates"}
			result, err := client.CallTool(ctx, &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			if tt.status == http.StatusUnauthorized {
				require.ErrorIs(t, err, ErrUnauthorized)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.status >= 400, result.IsError)
			require.JSONEq(t, tt.response, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}
