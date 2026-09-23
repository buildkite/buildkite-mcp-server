package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
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

type SlowestExecutionsClient interface {
	ListSlowestByBuild(ctx context.Context, org, buildUUID string, opt *buildkite.SlowestExecutionsOptions) ([]buildkite.BuildExecution, *buildkite.Response, error)
}

type ReadExecutionTraceArgs struct {
	ToolInput
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
	ExecutionID   string `json:"execution_id"`
}

type SlowestExecutionsForBuildArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	BuildUUID string `json:"build_uuid" jsonschema:"Buildkite build UUID. This is the build ID, not the pipeline build number."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum number of executions to return. Defaults to 20 and is capped by the organization's slowest executions quota."`
}

func SlowestExecutionsForBuild() (mcp.Tool, mcp.ToolHandlerFor[SlowestExecutionsForBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "slowest_executions_for_build",
			Description: "List the slowest test executions for a Buildkite build across every visible Test Engine suite, slowest first. Returns the identifiers needed by read_execution_trace and whether each execution has a trace.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Slowest Executions for Build",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args SlowestExecutionsForBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.SlowestExecutionsForBuild")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("build_uuid", args.BuildUUID),
				attribute.Int("limit", args.Limit),
			)

			deps := DepsFromContext(ctx)
			executions, _, err := deps.SlowestExecutionsClient.ListSlowestByBuild(ctx, args.OrgSlug, args.BuildUUID, &buildkite.SlowestExecutionsOptions{
				Limit: args.Limit,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(attribute.Int("item_count", len(executions)))

			return mcpTextResult(span, &executions)
		}, []string{"read_suites"}
}

func ReadExecutionTrace() (mcp.Tool, mcp.ToolHandlerFor[ReadExecutionTraceArgs, any], []string) {
	return mcp.Tool{
			Name:        "read_execution_trace",
			Description: "Read an OpenTelemetry trace summary for a Buildkite Test Engine execution, including category rollups and slowest spans",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Read Execution Trace",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ReadExecutionTraceArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ReadExecutionTrace")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("execution_id", args.ExecutionID),
			)

			deps := DepsFromContext(ctx)
			executionTrace, _, err := deps.ExecutionTraceClient.GetTrace(ctx, args.OrgSlug, args.TestSuiteSlug, args.ExecutionID, &buildkite.ExecutionTraceOptions{
				View: buildkite.TraceViewSummary,
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
	IncludeFailureExpanded bool   `json:"include_failure_expanded,omitempty" jsonschema:"Include expanded failure details such as full error messages and stack traces. Useful for unit or model spec failures where the backtrace is the root cause. For feature or browser specs, use search_logs tool on the failed job IDs instead"`
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
