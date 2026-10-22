package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

type MockTestWorkflowsClient struct {
	ListFunc func(context.Context, string, string, *buildkite.TestWorkflowListOptions) ([]buildkite.TestWorkflow, *buildkite.Response, error)
	GetFunc  func(context.Context, string, string, string) (buildkite.TestWorkflow, *buildkite.Response, error)
}

func (m *MockTestWorkflowsClient) List(ctx context.Context, org, suite string, opt *buildkite.TestWorkflowListOptions) ([]buildkite.TestWorkflow, *buildkite.Response, error) {
	return m.ListFunc(ctx, org, suite, opt)
}

func (m *MockTestWorkflowsClient) Get(ctx context.Context, org, suite, workflowID string) (buildkite.TestWorkflow, *buildkite.Response, error) {
	return m.GetFunc(ctx, org, suite, workflowID)
}

var _ TestWorkflowsClient = (*MockTestWorkflowsClient)(nil)

// Webhook endpoints must be fully redacted in both action lists, while
// other configuration and an explicit zero recovery threshold survive.
const testWorkflowJSON = `{
	"id": "0199e940-7e01-7000-8000-000000000001",
	"url": "https://api.buildkite.com/v2/analytics/organizations/acme/suites/backend/workflows/0199e940-7e01-7000-8000-000000000001",
	"web_url": "https://buildkite.com/organizations/acme/analytics/suites/backend/workflows",
	"monitor": {"type": "transition_count", "window_size": 30, "alarm_threshold": 5, "recover_threshold": 0},
	"filters": [{"key": "name", "operator": "starts_with", "value": "Checkout"}],
	"alarm_actions": [
		{"type": "add_label", "label": "intermittent"},
		{"type": "send_webhook_notification", "endpoint": "[REDACTED]"}
	],
	"recover_actions": [
		{"type": "remove_label", "label": "intermittent"},
		{"type": "send_webhook_notification", "endpoint": "[REDACTED]"},
		{"type": "send_webhook_notification"}
	],
	"created_at": null, "updated_at": null, "created_by": null
}`

func TestListTestWorkflows(t *testing.T) {
	for _, tt := range []struct {
		name          string
		suite         string
		page, perPage int
		wantPage      int
		wantPerPage   int
		empty         bool
	}{
		{name: "default pagination", suite: "backend", wantPage: 1, wantPerPage: 100},
		{name: "UUID and explicit pagination", suite: "0199e940-7e01-7000-8000-000000000002", page: 3, perPage: 17, wantPage: 3, wantPerPage: 17},
		{name: "empty suite", suite: "empty", wantPage: 1, wantPerPage: 100, empty: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var workflow buildkite.TestWorkflow
			apiJSON := strings.ReplaceAll(testWorkflowJSON, "[REDACTED]", "https://hooks.example.com/private-token?key=secret")
			require.NoError(t, json.Unmarshal([]byte(apiJSON), &workflow))
			link := `<https://api.buildkite.com/v2/analytics/organizations/acme/suites/backend/workflows?page=4&per_page=17>; rel="next"`
			itemsJSON := "[" + testWorkflowJSON + "]"
			if tt.empty {
				link, itemsJSON = "", "[]"
			}
			client := &MockTestWorkflowsClient{
				ListFunc: func(ctx context.Context, org, suite string, opt *buildkite.TestWorkflowListOptions) ([]buildkite.TestWorkflow, *buildkite.Response, error) {
					require.Equal(t, "acme", org)
					require.Equal(t, tt.suite, suite)
					require.Equal(t, tt.wantPage, opt.Page)
					require.Equal(t, tt.wantPerPage, opt.PerPage)
					items := []buildkite.TestWorkflow{}
					if !tt.empty {
						items = append(items, workflow)
					}
					return items, &buildkite.Response{Response: &http.Response{Header: http.Header{"Link": {link}}}}, nil
				},
			}
			ctx := ContextWithDeps(context.Background(), ToolDependencies{TestWorkflowsClient: client})
			_, handler, _ := ListTestWorkflows()
			result, _, err := handler(ctx, createMCPRequest(t, nil), ListTestWorkflowsArgs{
				OrgSlug: "acme", TestSuiteSlug: tt.suite, Page: tt.page, PerPage: tt.perPage,
			})
			require.NoError(t, err)
			require.False(t, result.IsError)
			linkJSON, err := json.Marshal(link)
			require.NoError(t, err)
			require.JSONEq(t, fmt.Sprintf(`{"items": %s, "headers": {"Link": %s}}`, itemsJSON, linkJSON), result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

func TestGetTestWorkflow(t *testing.T) {
	for _, suite := range []string{"backend", "0199e940-7e01-7000-8000-000000000002"} {
		t.Run(suite, func(t *testing.T) {
			client := &MockTestWorkflowsClient{
				GetFunc: func(ctx context.Context, org, gotSuite, workflowID string) (buildkite.TestWorkflow, *buildkite.Response, error) {
					require.Equal(t, "acme", org)
					require.Equal(t, suite, gotSuite)
					require.Equal(t, "0199e940-7e01-7000-8000-000000000001", workflowID)
					var workflow buildkite.TestWorkflow
					apiJSON := strings.ReplaceAll(testWorkflowJSON, "[REDACTED]", "https://hooks.example.com/private-token?key=secret")
					require.NoError(t, json.Unmarshal([]byte(apiJSON), &workflow))
					return workflow, &buildkite.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
				},
			}
			ctx := ContextWithDeps(context.Background(), ToolDependencies{TestWorkflowsClient: client})
			_, handler, _ := GetTestWorkflow()
			result, _, err := handler(ctx, createMCPRequest(t, nil), GetTestWorkflowArgs{
				OrgSlug: "acme", TestSuiteSlug: suite, WorkflowID: "0199e940-7e01-7000-8000-000000000001",
			})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.JSONEq(t, testWorkflowJSON, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

func TestTestWorkflowErrors(t *testing.T) {
	for _, tt := range []struct {
		name    string
		code    int
		message string
	}{
		{name: "unauthorized", code: http.StatusUnauthorized, message: "Unauthorized"},
		{name: "forbidden", code: http.StatusForbidden, message: "You do not have access to this suite"},
		{name: "feature unavailable", code: http.StatusForbidden, message: "Workflows are not available for this organization"},
		{name: "not found", code: http.StatusNotFound, message: "Workflow not found"},
		{name: "transport error", message: "connection failed"},
	} {
		for _, tool := range []string{"list", "get"} {
			t.Run(tt.name+"/"+tool, func(t *testing.T) {
				var apiErr = fmt.Errorf("%s", tt.message)
				if tt.code != 0 {
					apiErr = &buildkite.ErrorResponse{Response: &http.Response{StatusCode: tt.code}, RawBody: []byte(fmt.Sprintf(`{"message": %q}`, tt.message))}
				}
				client := &MockTestWorkflowsClient{
					ListFunc: func(context.Context, string, string, *buildkite.TestWorkflowListOptions) ([]buildkite.TestWorkflow, *buildkite.Response, error) {
						return nil, nil, apiErr
					},
					GetFunc: func(context.Context, string, string, string) (buildkite.TestWorkflow, *buildkite.Response, error) {
						return buildkite.TestWorkflow{}, nil, apiErr
					},
				}
				ctx := ContextWithDeps(context.Background(), ToolDependencies{TestWorkflowsClient: client})
				var result *mcp.CallToolResult
				var err error
				if tool == "list" {
					_, handler, _ := ListTestWorkflows()
					result, _, err = handler(ctx, createMCPRequest(t, nil), ListTestWorkflowsArgs{OrgSlug: "acme", TestSuiteSlug: "backend"})
				} else {
					_, handler, _ := GetTestWorkflow()
					result, _, err = handler(ctx, createMCPRequest(t, nil), GetTestWorkflowArgs{OrgSlug: "acme", TestSuiteSlug: "backend", WorkflowID: "missing"})
				}
				if tt.code == http.StatusUnauthorized {
					require.ErrorIs(t, err, ErrUnauthorized)
					require.Nil(t, result)
				} else {
					require.NoError(t, err)
					require.True(t, result.IsError)
					require.Contains(t, result.Content[0].(*mcp.TextContent).Text, tt.message)
				}
			})
		}
	}
}
