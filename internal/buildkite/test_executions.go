package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

type TestExecutionsClient interface {
	GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

func GetFailedTestExecutions(ctx context.Context, client TestExecutionsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_failed_executions",
			mcp.WithDescription("Get failed test executions for a specific test run in Buildkite Test Engine. Optionally get the expanded failure details such as full error messages and stack traces."),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the test suite"),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
				mcp.Description("The slug of the test suite the run belongs to"),
			),
			mcp.WithString("run_id",
				mcp.Required(),
				mcp.Description("The ID of the test run"),
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

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			testSuiteSlug, err := request.RequireString("test_suite_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			runID, err := request.RequireString("run_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			includeFailureExpanded := request.GetBool("include_failure_expanded", false)

			// Get client-side pagination parameters (always enabled)
			paginationParams := getClientSidePaginationParams(request)

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.String("run_id", runID),
				attribute.Bool("include_failure_expanded", includeFailureExpanded),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.FailedExecutionsOptions{
				IncludeFailureExpanded: includeFailureExpanded,
			}

			failedExecutions, resp, err := client.GetFailedExecutions(ctx, org, testSuiteSlug, runID, options)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get failed executions: %s", string(body))), nil
			}

			// Always apply client-side pagination
			result := applyClientSidePagination(failedExecutions, paginationParams)
			r, err := json.Marshal(&result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal failed executions: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}
