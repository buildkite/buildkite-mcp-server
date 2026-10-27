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

func TestConcurrentAgentsUsageThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		name, org, from, to, interval, agentType, scope string
		percentile, wantPercentile                      int
		maximum, percentileValue                        int64
		items                                           string
	}{
		{
			name: "server defaults and zero-filled daily inputs", org: "acme", from: "2026-08-29", to: "2026-09-03",
			scope: "organization_and_billing_children", wantPercentile: 95, maximum: 11, percentileValue: 11,
			items: `[{"date":"2026-08-29","max_concurrent_agents":0},{"date":"2026-08-30","max_concurrent_agents":0},
				{"date":"2026-08-31","max_concurrent_agents":1},{"date":"2026-09-01","max_concurrent_agents":3},
				{"date":"2026-09-02","max_concurrent_agents":7},{"date":"2026-09-03","max_concurrent_agents":11}]`,
		},
		{
			name: "hosted monthly bars retain percentile over daily inputs", org: "0199e940-7e01-7000-8000-000000000002",
			from: "2026-08-29", to: "2026-09-03", interval: "month", agentType: "hosted", scope: "organization",
			percentile: 80, wantPercentile: 80, maximum: 11, percentileValue: 7,
			items: `[{"date":"2026-08-01","max_concurrent_agents":1},{"date":"2026-09-01","max_concurrent_agents":11}]`,
		},
		{
			name: "minimum percentile and explicit zero counts", org: "acme", from: "2026-10-01", to: "2026-10-01",
			interval: "day", scope: "organization", percentile: 1, wantPercentile: 1,
			items: `[{"date":"2026-10-01","max_concurrent_agents":0}]`,
		},
		{
			name: "maximum percentile and explicit self-hosted filter", org: "acme", from: "2026-10-01", to: "2026-10-04",
			agentType: "self_hosted", scope: "organization_and_billing_children", percentile: 100, wantPercentile: 100,
			maximum: 11, percentileValue: 11,
			items: `[{"date":"2026-10-01","max_concurrent_agents":0},{"date":"2026-10-02","max_concurrent_agents":3},
				{"date":"2026-10-03","max_concurrent_agents":7},{"date":"2026-10-04","max_concurrent_agents":11}]`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			interval, agentType := tt.interval, tt.agentType
			if interval == "" {
				interval = "day"
			}
			if agentType == "" {
				agentType = "self_hosted"
			}
			report := fmt.Sprintf(`{"description":"Recorded agent concurrency","reporting_period":{"from":%q,"to":%q},
				"generated_at":"2026-10-27T11:00:00Z","scope":%q,"interval":%q,"unit":"agents",
				"definition":"Discrete percentile over zero-filled daily sums of organization peaks",
				"agent_type":%q,"percentile":%d,"summary":{"max_concurrent_agents":%d,"percentile_concurrent_agents":%d},
				"items":%s}`, tt.from, tt.to, tt.scope, interval, agentType, tt.wantPercentile, tt.maximum, tt.percentileValue, tt.items)
			var requests atomic.Int32
			client := organizationUsageSession(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/"+tt.org+"/usage/concurrent-agents", r.URL.Path)
				query := url.Values{"from": {tt.from}, "to": {tt.to}}
				if tt.interval != "" {
					query.Set("interval", tt.interval)
				}
				if tt.agentType != "" {
					query.Set("filter[agent_type]", tt.agentType)
				}
				if tt.percentile != 0 {
					query.Set("percentile", fmt.Sprint(tt.percentile))
				}
				assert.Equal(t, query, r.URL.Query(), "preserve exact bounds, omit server defaults, and use the SDK's nested agent filter")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, report)
			}, WithToolsets(toolsets.ToolsetUsage), WithReadOnly(true))
			args := map[string]any{"org_slug": tt.org, "from": tt.from, "to": tt.to}
			if tt.interval != "" {
				args["interval"] = tt.interval
			}
			if tt.agentType != "" {
				args["agent_type"] = tt.agentType
			}
			if tt.percentile != 0 {
				args["percentile"] = tt.percentile
			}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_concurrent_agents_usage", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, int32(1), requests.Load())
			require.Len(t, result.Content, 1)
			require.JSONEq(t, report, result.Content[0].(*mcp.TextContent).Text, "do not recompute the percentile from the monthly bars or change zero counts")
		})
	}
}

func TestHostedAgentsUsageThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		name, org, from, to, interval, scope, summary, items string
	}{
		{
			name: "daily resource buckets retain independently rounded period totals", org: "acme",
			from: "2026-10-01", to: "2026-10-02", scope: "organization_and_billing_children",
			summary: `{"vcpu_minutes":13,"vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":7,
				"hosted_agents_linux_arm64_vcpu_minutes":4,"hosted_agents_macos_m4_vcpu_minutes":2,"hosted_agents_windows_amd64_vcpu_minutes":0}}`,
			items: `[{"date":"2026-10-01","vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":3,
				"hosted_agents_linux_arm64_vcpu_minutes":2,"hosted_agents_macos_m4_vcpu_minutes":1,"hosted_agents_windows_amd64_vcpu_minutes":0}},
				{"date":"2026-10-02","vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":3,
				"hosted_agents_linux_arm64_vcpu_minutes":2,"hosted_agents_macos_m4_vcpu_minutes":1,"hosted_agents_windows_amd64_vcpu_minutes":0}}]`,
		},
		{
			name: "partial months preserve exact dates and all resource categories", org: "0199e940-7e01-7000-8000-000000000003",
			from: "2026-08-18", to: "2026-09-04", interval: "month", scope: "organization",
			summary: `{"vcpu_minutes":19,"vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":2,
				"hosted_agents_linux_arm64_vcpu_minutes":4,"hosted_agents_macos_m4_vcpu_minutes":5,"hosted_agents_windows_amd64_vcpu_minutes":8}}`,
			items: `[{"date":"2026-08-01","vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":1,
				"hosted_agents_linux_arm64_vcpu_minutes":2,"hosted_agents_macos_m4_vcpu_minutes":3,"hosted_agents_windows_amd64_vcpu_minutes":5}},
				{"date":"2026-09-01","vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":1,
				"hosted_agents_linux_arm64_vcpu_minutes":2,"hosted_agents_macos_m4_vcpu_minutes":2,"hosted_agents_windows_amd64_vcpu_minutes":3}}]`,
		},
		{
			name: "explicit day retains every zero resource", org: "acme", from: "2026-10-01", to: "2026-10-01",
			interval: "day", scope: "organization",
			summary: `{"vcpu_minutes":0,"vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":0,
				"hosted_agents_linux_arm64_vcpu_minutes":0,"hosted_agents_macos_m4_vcpu_minutes":0,"hosted_agents_windows_amd64_vcpu_minutes":0}}`,
			items: `[{"date":"2026-10-01","vcpu_minutes_by_resource":{"hosted_agents_linux_amd64_vcpu_minutes":0,
				"hosted_agents_linux_arm64_vcpu_minutes":0,"hosted_agents_macos_m4_vcpu_minutes":0,"hosted_agents_windows_amd64_vcpu_minutes":0}}]`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			interval := tt.interval
			if interval == "" {
				interval = "day"
			}
			report := fmt.Sprintf(`{"description":"Recorded hosted compute usage","reporting_period":{"from":%q,"to":%q},
				"generated_at":"2026-10-27T11:00:00Z","scope":%q,"interval":%q,"unit":"vCPU-minutes",
				"definition":"CPU-weighted shapes combined per resource before floor; period and buckets round independently; unknown shapes excluded",
				"summary":%s,"items":%s}`, tt.from, tt.to, tt.scope, interval, tt.summary, tt.items)
			var requests atomic.Int32
			client := organizationUsageSession(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/"+tt.org+"/usage/hosted-agents", r.URL.Path)
				query := url.Values{"from": {tt.from}, "to": {tt.to}}
				if tt.interval != "" {
					query.Set("interval", tt.interval)
				}
				assert.Equal(t, query, r.URL.Query(), "preserve bounds and omit server defaults and pagination")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, report)
			}, WithToolsets(toolsets.ToolsetUsage), WithReadOnly(true))
			args := map[string]any{"org_slug": tt.org, "from": tt.from, "to": tt.to}
			if tt.interval != "" {
				args["interval"] = tt.interval
			}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_hosted_agents_usage", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, int32(1), requests.Load())
			require.Len(t, result.Content, 1)
			require.JSONEq(t, report, result.Content[0].(*mcp.TextContent).Text, "preserve independent rounding, four resource keys, zeros, and metadata")
		})
	}
}

func TestTestExecutionsUsageThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		name, org, from, to, interval, scope, summary, items string
	}{
		{
			name: "daily executions include zero buckets in floored average", org: "acme",
			from: "2026-10-01", to: "2026-10-03", scope: "organization_and_billing_children",
			summary: `{"test_executions":7,"active_suites":2,"average_test_executions":2}`,
			items: `[{"date":"2026-10-01","test_executions":5,"cumulative_test_executions":5},
				{"date":"2026-10-02","test_executions":0,"cumulative_test_executions":5},
				{"date":"2026-10-03","test_executions":2,"cumulative_test_executions":7}]`,
		},
		{
			name: "partial months use requested month bucket count for average", org: "0199e940-7e01-7000-8000-000000000004",
			from: "2026-08-18", to: "2026-09-04", interval: "month", scope: "organization",
			summary: `{"test_executions":11,"active_suites":3,"average_test_executions":5}`,
			items: `[{"date":"2026-08-01","test_executions":4,"cumulative_test_executions":4},
				{"date":"2026-09-01","test_executions":7,"cumulative_test_executions":11}]`,
		},
		{
			name: "explicit day and zero execution counts", org: "acme", from: "2026-10-01", to: "2026-10-01",
			interval: "day", scope: "organization",
			summary: `{"test_executions":0,"active_suites":0,"average_test_executions":0}`,
			items:   `[{"date":"2026-10-01","test_executions":0,"cumulative_test_executions":0}]`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			interval := tt.interval
			if interval == "" {
				interval = "day"
			}
			report := fmt.Sprintf(`{"description":"Test execution usage","reporting_period":{"from":%q,"to":%q},
				"generated_at":"2026-10-27T11:00:00Z","scope":%q,"interval":%q,"unit":"executions",
				"definition":"Additive executions, not distinct managed tests; average is floor(total / requested bucket count)",
				"summary":%s,"items":%s}`, tt.from, tt.to, tt.scope, interval, tt.summary, tt.items)
			var requests atomic.Int32
			client := organizationUsageSession(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/"+tt.org+"/usage/test-executions", r.URL.Path)
				query := url.Values{"from": {tt.from}, "to": {tt.to}}
				if tt.interval != "" {
					query.Set("interval", tt.interval)
				}
				assert.Equal(t, query, r.URL.Query(), "preserve exact bounds and omit server defaults and pagination")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, report)
			}, WithToolsets(toolsets.ToolsetUsage), WithReadOnly(true))
			args := map[string]any{"org_slug": tt.org, "from": tt.from, "to": tt.to}
			if tt.interval != "" {
				args["interval"] = tt.interval
			}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_test_executions_usage", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, int32(1), requests.Load())
			require.Len(t, result.Content, 1)
			require.JSONEq(t, report, result.Content[0].(*mcp.TextContent).Text, "preserve summary averages, cumulative executions, zeros, and metadata")
		})
	}
}

func TestConcurrentAgentsUsageByOrganizationThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		name, org, from, to, interval, agentType, scope, after string
		percentile, wantPercentile, perPage                    int
		items, next                                            string
	}{
		{
			name: "defaults preserve daily peaks and zero-count organization", org: "acme",
			from: "2026-10-01", to: "2026-10-01", scope: "organization_and_billing_children", wantPercentile: 95,
			items: `[{"organization_uuid":"00000000-0000-0000-0000-000000000001","organization_slug":"child-a","organization_name":"Child A",
				"summary":{"max_concurrent_agents":3,"percentile_concurrent_agents":3},
				"items":[{"date":"2026-10-01","max_concurrent_agents":3,"percentile_concurrent_agents":3}]},
				{"organization_uuid":"00000000-0000-0000-0000-000000000002","organization_slug":"child-b","organization_name":"Child B",
				"summary":{"max_concurrent_agents":0,"percentile_concurrent_agents":0},
				"items":[{"date":"2026-10-01","max_concurrent_agents":0,"percentile_concurrent_agents":0}]}]`,
			next: `"https://api.buildkite.com/v2/organizations/acme/usage/concurrent-agents/organizations?after=opaque%2Bnext%3D"`,
		},
		{
			name: "hosted month bucket percentiles differ from full-range summary", org: "0199e940-7e01-7000-8000-000000000005",
			from: "2026-08-29", to: "2026-09-03", interval: "month", agentType: "hosted", scope: "organization",
			percentile: 80, wantPercentile: 80, perPage: 1, after: "opaque+cursor=/&?",
			items: `[{"organization_uuid":"00000000-0000-0000-0000-000000000003","organization_slug":"acme","organization_name":"Acme",
				"summary":{"max_concurrent_agents":11,"percentile_concurrent_agents":7},
				"items":[{"date":"2026-08-01","max_concurrent_agents":1,"percentile_concurrent_agents":1},
				{"date":"2026-09-01","max_concurrent_agents":11,"percentile_concurrent_agents":11}]}]`,
			next: `null`,
		},
		{
			name: "empty terminal page retains links next null", org: "acme", from: "2026-10-01", to: "2026-10-01",
			interval: "day", agentType: "self_hosted", scope: "organization_and_billing_children",
			percentile: 100, wantPercentile: 100, perPage: 100, after: "opaque+next=", items: `[]`, next: `null`,
		},
	} {
		t.Run(tt.name, func(t *testing.T) {
			interval, agentType := tt.interval, tt.agentType
			if interval == "" {
				interval = "day"
			}
			if agentType == "" {
				agentType = "self_hosted"
			}
			report := fmt.Sprintf(`{"description":"Concurrency by organization","reporting_period":{"from":%q,"to":%q},
				"generated_at":"2026-10-27T11:00:00Z","scope":%q,"interval":%q,"unit":"agents",
				"definition":"Discrete percentiles of daily peaks within the requested range or clipped bucket; nonadditive",
				"agent_type":%q,"percentile":%d,"items":%s,
				"links":{"self":"https://api.buildkite.com/v2/organizations/acme/usage/concurrent-agents/organizations","next":%s}}`,
				tt.from, tt.to, tt.scope, interval, agentType, tt.wantPercentile, tt.items, tt.next)
			var requests atomic.Int32
			client := organizationUsageSession(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/"+tt.org+"/usage/concurrent-agents/organizations", r.URL.Path)
				query := url.Values{"from": {tt.from}, "to": {tt.to}}
				for key, value := range map[string]string{"interval": tt.interval, "filter[agent_type]": tt.agentType, "after": tt.after} {
					if value != "" {
						query.Set(key, value)
					}
				}
				if tt.percentile != 0 {
					query.Set("percentile", fmt.Sprint(tt.percentile))
				}
				if tt.perPage != 0 {
					query.Set("per_page", fmt.Sprint(tt.perPage))
				}
				assert.Equal(t, query, r.URL.Query(), "preserve opaque cursors and use the SDK's embedded filter options")
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, report)
			}, WithToolsets(toolsets.ToolsetUsage), WithReadOnly(true))
			args := map[string]any{"org_slug": tt.org, "from": tt.from, "to": tt.to}
			for key, value := range map[string]string{"interval": tt.interval, "agent_type": tt.agentType, "after": tt.after} {
				if value != "" {
					args[key] = value
				}
			}
			if tt.percentile != 0 {
				args["percentile"] = tt.percentile
			}
			if tt.perPage != 0 {
				args["per_page"] = tt.perPage
			}
			result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: "list_concurrent_agents_usage_by_organization", Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, int32(1), requests.Load(), "return one page without auto-following links")
			require.Len(t, result.Content, 1)
			require.JSONEq(t, report, result.Content[0].(*mcp.TextContent).Text, "preserve per-organization/bucket statistics, zero counts, ordering, and nullable links without inventing a top-level summary")
		})
	}
}

func TestOrganizationUsageErrorsThroughMCP(t *testing.T) {
	for _, resource := range []struct{ tool, path string }{
		{"get_active_users_usage", "active-users"},
		{"get_concurrent_agents_usage", "concurrent-agents"},
		{"list_concurrent_agents_usage_by_organization", "concurrent-agents/organizations"},
		{"get_hosted_agents_usage", "hosted-agents"},
		{"get_test_executions_usage", "test-executions"},
	} {
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
			t.Run(resource.tool+"/"+fmt.Sprint(tt.status), func(t *testing.T) {
				body := fmt.Sprintf(`{"message":%q,"errors":["Synthetic usage failure"]}`, tt.message)
				client := organizationUsageSession(t, func(w http.ResponseWriter, r *http.Request) {
					assert.Equal(t, "/v2/organizations/acme/usage/"+resource.path, r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(tt.status)
					_, _ = io.WriteString(w, body)
				}, WithToolsets(toolsets.ToolsetUsage))
				result, err := client.CallTool(context.Background(), &mcp.CallToolParams{
					Name: resource.tool, Arguments: map[string]any{"org_slug": "acme", "from": "2026-10-03", "to": "2026-10-01"},
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
}

func TestOrganizationUsageDiscoveryAndSchema(t *testing.T) {
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
			for _, name := range []string{
				"get_active_users_usage", "get_concurrent_agents_usage", "list_concurrent_agents_usage_by_organization",
				"get_hosted_agents_usage", "get_test_executions_usage",
			} {
				var found *mcp.Tool
				for _, tool := range listed.Tools {
					if tool.Name == name {
						found = tool
					}
				}
				if !tt.want {
					require.Nil(t, found)
					result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name,
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
					require.NotContains(t, properties, "before")
					if name == "list_concurrent_agents_usage_by_organization" {
						require.Contains(t, properties, "after")
						require.InDelta(t, 1, properties["per_page"].(map[string]any)["minimum"], 0)
						require.InDelta(t, 100, properties["per_page"].(map[string]any)["maximum"], 0)
						for _, invalid := range []any{0, 101, 1.5} {
							result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name,
								Arguments: map[string]any{"org_slug": "acme", "from": "2026-10-01", "to": "2026-10-03", "per_page": invalid}})
							require.NoError(t, err)
							require.True(t, result.IsError)
							require.Contains(t, result.Content[0].(*mcp.TextContent).Text, "per_page")
						}
					} else {
						require.NotContains(t, properties, "per_page")
						require.NotContains(t, properties, "after")
					}
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
					if name == "get_concurrent_agents_usage" || name == "list_concurrent_agents_usage_by_organization" {
						require.InDelta(t, 1, properties["percentile"].(map[string]any)["minimum"], 0)
						require.InDelta(t, 100, properties["percentile"].(map[string]any)["maximum"], 0)
						require.ElementsMatch(t, []any{"self_hosted", "hosted"}, properties["agent_type"].(map[string]any)["enum"])
						for _, invalid := range []struct {
							field string
							value any
						}{
							{"percentile", 0}, {"percentile", 101}, {"percentile", 80.5}, {"agent_type", "all"},
						} {
							args := map[string]any{"org_slug": "acme", "from": "2026-10-01", "to": "2026-10-03", invalid.field: invalid.value}
							result, err := client.CallTool(context.Background(), &mcp.CallToolParams{Name: name, Arguments: args})
							require.NoError(t, err)
							require.True(t, result.IsError)
							require.Contains(t, result.Content[0].(*mcp.TextContent).Text, invalid.field)
						}
					}
				}
			}
			require.Zero(t, requests.Load(), "discovery, exclusions, and schema rejections must not query usage")
		})
	}
}
