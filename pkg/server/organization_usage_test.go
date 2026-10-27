package server

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
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	buildkiteapi "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func organizationUsageSession(t *testing.T, handler http.HandlerFunc, opts ...ToolsetOption) *mcp.ClientSession {
	t.Helper()
	apiServer := httptest.NewServer(handler)
	t.Cleanup(apiServer.Close)
	api, err := buildkiteapi.NewOpts(buildkiteapi.WithBaseURL(apiServer.URL + "/"))
	require.NoError(t, err)
	srv := NewMCPServer("test", buildkite.ToolDependencies{OrganizationUsageClient: api.Organizations}, opts...)
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })
	return client
}

func TestActiveUsersUsageThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		name, from, to, interval, org, scope, summary, items string
	}{
		{
			name: "daily distinct users and cumulative carry", from: "2026-08-28", to: "2026-08-30",
			org: "acme", scope: "organization_and_billing_children",
			summary: `{"active_users":3,"average_monthly_active_users":3}`,
			items: `[{"date":"2026-08-28","active_users":2,"cumulative_active_users":2},
				{"date":"2026-08-29","active_users":2,"cumulative_active_users":3},
				{"date":"2026-08-30","active_users":0,"cumulative_active_users":3}]`,
		},
		{
			name: "partial calendar months and non-additive summary", from: "2026-08-18", to: "2026-09-04", interval: "month",
			org: "acme", scope: "organization_and_billing_children",
			summary: `{"active_users":4,"average_monthly_active_users":3}`,
			items: `[{"date":"2026-08-01","active_users":2,"cumulative_active_users":2},
				{"date":"2026-09-01","active_users":4,"cumulative_active_users":4}]`,
		},
		{
			name: "explicit day and zero usage", from: "2026-10-01", to: "2026-10-01", interval: "day",
			org: "0199e940-7e01-7000-8000-000000000001", scope: "organization",
			summary: `{"active_users":0,"average_monthly_active_users":0}`,
			items:   `[{"date":"2026-10-01","active_users":0,"cumulative_active_users":0}]`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			interval := tt.interval
			if interval == "" {
				interval = "day"
			}
			report := fmt.Sprintf(`{"description":"Active users","reporting_period":{"from":%q,"to":%q},
				"generated_at":"2026-10-27T11:00:00Z","scope":%q,
				"interval":%q,"unit":"users","definition":"Distinct users; late events may revise results",
				"summary":%s,"items":%s}`, tt.from, tt.to, tt.scope, interval, tt.summary, tt.items)
			var requests atomic.Int32
			client := organizationUsageSession(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/"+tt.org+"/usage/active-users", r.URL.Path)
				query := url.Values{"from": {tt.from}, "to": {tt.to}}
				if tt.interval != "" {
					query.Set("interval", tt.interval)
				}
				assert.Equal(t, query, r.URL.Query(), "forward exact bounds without pagination, telemetry, or date normalization")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, report)
			}, WithToolsets(toolsets.ToolsetUsage), WithReadOnly(true))
			args := map[string]any{"org_slug": tt.org, "from": tt.from, "to": tt.to,
				"telemetry": map[string]any{"context": "The user is checking organization active user usage over a specific reporting period before comparing the results with the Usage page."}}
			if tt.interval != "" {
				args["interval"] = tt.interval
			}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_active_users_usage", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, int32(1), requests.Load())
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			require.JSONEq(t, report, text.Text, "preserve server calculations, bounds, metadata, and explicit zero counts")
		})
	}
}

func TestActiveUsersUsageErrorsThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		status  int
		message string
	}{
		{http.StatusUnauthorized, "Token expired"},
		{http.StatusForbidden, "Missing read_organizations or organization access"},
		{http.StatusNotFound, "Usage breakdowns are not enabled"},
		{http.StatusBadRequest, "from must be on or before to"},
		{http.StatusServiceUnavailable, "Usage query unavailable"},
	} {
		t.Run(fmt.Sprint(tt.status), func(t *testing.T) {
			body := fmt.Sprintf(`{"message":%q,"errors":["Synthetic usage failure"]}`, tt.message)
			client := organizationUsageSession(t, func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "/v2/organizations/acme/usage/active-users", r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, body)
			}, WithToolsets(toolsets.ToolsetUsage))
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{
				Name: "get_active_users_usage", Arguments: map[string]any{"org_slug": "acme", "from": "2026-10-03", "to": "2026-10-01"},
			})
			if tt.status == http.StatusUnauthorized {
				require.ErrorIs(t, err, buildkite.ErrUnauthorized)
			} else {
				require.NoError(t, err)
				require.True(t, result.IsError)
				require.JSONEq(t, body, result.Content[0].(*mcp.TextContent).Text, "API validation and availability errors must not look like zero usage")
			}
		})
	}
}

func TestActiveUsersUsageDiscoveryAndSchema(t *testing.T) {
	registry := toolsets.NewToolsetRegistry()
	registry.RegisterToolsets(toolsets.CreateBuiltinToolsets())
	require.NoError(t, toolsets.ValidateToolsets([]string{toolsets.ToolsetUsage}))
	require.Equal(t, []string{"read_organizations"}, registry.GetRequiredScopes([]string{toolsets.ToolsetUsage}, true))
	for _, tt := range []struct {
		name string
		opts []ToolsetOption
		want bool
	}{
		{name: "all", want: true},
		{name: "read-only usage", opts: []ToolsetOption{WithToolsets(toolsets.ToolsetUsage), WithReadOnly(true)}, want: true},
		{name: "other toolset", opts: []ToolsetOption{WithToolsets(toolsets.ToolsetUser)}},
		{name: "usage disabled", opts: []ToolsetOption{WithDisabledToolsets(toolsets.ToolsetUsage)}},
		{name: "minimum access", opts: []ToolsetOption{WithToolsets(toolsets.ToolsetUsage), WithMinimumAccess(true)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var requests atomic.Int32
			client := organizationUsageSession(t, func(w http.ResponseWriter, _ *http.Request) {
				requests.Add(1)
				w.WriteHeader(http.StatusInternalServerError)
			}, tt.opts...)
			listed, err := client.ListTools(context.Background(), nil)
			require.NoError(t, err)
			var found *mcp.Tool
			for _, tool := range listed.Tools {
				if tool.Name == "get_active_users_usage" {
					found = tool
				}
			}
			if !tt.want {
				require.Nil(t, found)
				result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_active_users_usage",
					Arguments: map[string]any{"org_slug": "acme", "from": "2026-10-01", "to": "2026-10-03"}})
				require.True(t, err != nil || result.IsError)
			} else {
				require.NotNil(t, found)
				require.True(t, found.Annotations.ReadOnlyHint)
				schema := found.InputSchema.(map[string]any)
				require.ElementsMatch(t, []any{"org_slug", "from", "to"}, schema["required"])
				properties := schema["properties"].(map[string]any)
				require.ElementsMatch(t, []any{"day", "month"}, properties["interval"].(map[string]any)["enum"])
				require.NotContains(t, properties, "page")
				require.NotContains(t, properties, "per_page")
				for _, missing := range []string{"org_slug", "from", "to"} {
					args := map[string]any{"org_slug": "acme", "from": "2026-10-01", "to": "2026-10-03"}
					delete(args, missing)
					result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: found.Name, Arguments: args})
					require.NoError(t, err)
					require.True(t, result.IsError, "missing %s must be rejected by the published schema", missing)
					require.Contains(t, result.Content[0].(*mcp.TextContent).Text, missing)
				}
				result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: found.Name,
					Arguments: map[string]any{"org_slug": "acme", "from": "2026-10-01", "to": "2026-10-03", "interval": "week"}})
				require.NoError(t, err)
				require.True(t, result.IsError)
				require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "interval")
			}
			require.Zero(t, requests.Load(), "discovery, exclusions, and schema rejections must not query usage")
		})
	}
}
