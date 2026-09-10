package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestExecutionsClient interface {
	GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

type ExecutionTraceClient interface {
	GetTrace(ctx context.Context, org, slug, executionID string, opt *buildkite.ExecutionTraceOptions) (buildkite.ExecutionTrace, *buildkite.Response, error)
}

type ReadExecutionTraceArgs struct {
	ToolInput
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
	ExecutionID   string `json:"execution_id"`
	View          string `json:"view,omitempty" jsonschema:"Trace view: 'summary' (default) or 'full'"`
}

func ReadExecutionTrace() (mcp.Tool, mcp.ToolHandlerFor[ReadExecutionTraceArgs, any], []string) {
	return mcp.Tool{
			Name:        "read_execution_trace",
			Description: "Read the OpenTelemetry trace for a Buildkite Test Engine execution. The summary view returns category rollups and slowest spans; the full view also returns the span list",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Read Execution Trace",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ReadExecutionTraceArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ReadExecutionTrace")
			defer span.End()

			if args.View == "" {
				args.View = string(buildkite.TraceViewSummary)
			}
			if args.View != string(buildkite.TraceViewSummary) && args.View != string(buildkite.TraceViewFull) {
				return utils.NewToolResultError("view must be 'summary' or 'full'"), nil, nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("execution_id", args.ExecutionID),
				attribute.String("view", args.View),
			)

			deps := DepsFromContext(ctx)
			executionTrace, _, err := deps.ExecutionTraceClient.GetTrace(ctx, args.OrgSlug, args.TestSuiteSlug, args.ExecutionID, &buildkite.ExecutionTraceOptions{
				View: buildkite.TraceView(args.View),
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &executionTrace)
		}, []string{"read_suites"}
}

type GetFailedTestExecutionsArgs struct {
	ToolInput
	OrgSlug                string `json:"org_slug"`
	TestSuiteSlug          string `json:"test_suite_slug"`
	RunID                  string `json:"run_id"`
	IncludeFailureExpanded bool   `json:"include_failure_expanded,omitempty" jsonschema:"Include expanded failure details such as full error messages and stack traces"`
	Page                   int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage                int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

func GetFailedTestExecutions() (mcp.Tool, mcp.ToolHandlerFor[GetFailedTestExecutionsArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_failed_executions",
			Description: "Get failed test executions for a specific test run in Buildkite Test Engine. Optionally get the expanded failure details such as full error messages and stack traces.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Failed Test Executions",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetFailedTestExecutionsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetFailedExecutions")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("run_id", args.RunID),
				attribute.Bool("include_failure_expanded", args.IncludeFailureExpanded),
				attribute.Int("page", args.Page),
				attribute.Int("per_page", args.PerPage),
			)

			options := &buildkite.FailedExecutionsOptions{
				IncludeFailureExpanded: args.IncludeFailureExpanded,
				Page:                   args.Page,
				PerPage:                args.PerPage,
			}

			deps := DepsFromContext(ctx)
			failedExecutions, resp, err := deps.TestExecutionsClient.GetFailedExecutions(ctx, args.OrgSlug, args.TestSuiteSlug, args.RunID, options)
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.FailedExecution]{
				Items: failedExecutions,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(failedExecutions)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}
