package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

type TestExecutionsClient interface {
	GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

func GetFailedTestExecutions(client TestExecutionsClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("get_failed_executions",
			mcp.WithDescription("Get failed test executions for a specific test run in Buildkite Test Engine. Optionally get the expanded failure details such as full error messages and stack traces."),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
			),
			mcp.WithString("run_id",
				mcp.Required(),
			),
			mcp.WithBoolean("include_failure_expanded",
				mcp.Description("Include the expanded failure details such as full error messages and stack traces. This can be used to explain and diganose the cause of test failures."),
			),
			withClientSidePagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Failed Test Executions",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetFailedExecutions")
			defer span.End()

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			testSuiteSlug, err := request.RequireString("test_suite_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			runID, err := request.RequireString("run_id")
			if err != nil {
				return handleAPIError(err), nil
			}

			includeFailureExpanded := request.GetBool("include_failure_expanded", false)

			// Get client-side pagination parameters (always enabled)
			paginationParams := getClientSidePaginationParams(request)

			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.String("run_id", runID),
				attribute.Bool("include_failure_expanded", includeFailureExpanded),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.FailedExecutionsOptions{
				IncludeFailureExpanded: includeFailureExpanded,
			}

			failedExecutions, _, err := client.GetFailedExecutions(ctx, orgSlug, testSuiteSlug, runID, options)
			if err != nil {
				return handleAPIError(err), nil
			}

			// Always apply client-side pagination
			result := applyClientSidePagination(failedExecutions, paginationParams)

			span.SetAttributes(
				attribute.Int("item_count", len(failedExecutions)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}
