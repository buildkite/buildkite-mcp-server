package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestWorkflowsClient interface {
	List(ctx context.Context, org, suite string, opt *buildkite.TestWorkflowListOptions) ([]buildkite.TestWorkflow, *buildkite.Response, error)
	Get(ctx context.Context, org, suite, workflowID string) (buildkite.TestWorkflow, *buildkite.Response, error)
}

type ListTestWorkflowsArgs struct {
	ToolInput
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug" jsonschema:"Suite slug or UUID"`
	Page          int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1, default 1)"`
	PerPage       int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100, default 100)"`
}

type GetTestWorkflowArgs struct {
	ToolInput
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug" jsonschema:"Suite slug or UUID"`
	WorkflowID    string `json:"workflow_id" jsonschema:"Workflow UUID returned by list_test_workflows"`
}

func ListTestWorkflows() (mcp.Tool, mcp.ToolHandlerFor[ListTestWorkflowsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_test_workflows",
			Description: "List configured workflows for a Buildkite Test Engine suite, newest first. Returns monitors, filters, alarm/recovery actions, and links. Use this to discover automation and labels used to mark flaky tests before querying list_tests. Follow pagination links for more workflows; use get_test_workflow to inspect one. Configuration does not prove an action ran or rank tests by flakiness.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Test Workflows",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListTestWorkflowsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListTestWorkflows")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)
			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			workflows, resp, err := deps.TestWorkflowsClient.List(ctx, args.OrgSlug, args.TestSuiteSlug, &buildkite.TestWorkflowListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.TestWorkflow]{
				Items: workflows,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}
			span.SetAttributes(attribute.Int("item_count", len(workflows)))
			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}

func GetTestWorkflow() (mcp.Tool, mcp.ToolHandlerFor[GetTestWorkflowArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_test_workflow",
			Description: "Inspect a configured Buildkite Test Engine workflow by UUID, including its monitor conditions, filters, alarm/recovery actions, and links. Webhook endpoints may be redacted according to the caller's suite permissions. Describes configured behavior, not event history or proof that an action ran.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Test Workflow",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetTestWorkflowArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTestWorkflow")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("workflow_id", args.WorkflowID),
			)

			deps := DepsFromContext(ctx)
			workflow, _, err := deps.TestWorkflowsClient.Get(ctx, args.OrgSlug, args.TestSuiteSlug, args.WorkflowID)
			if err != nil {
				return handleBuildkiteError(err)
			}
			return mcpTextResult(span, &workflow)
		}, []string{"read_suites"}
}
