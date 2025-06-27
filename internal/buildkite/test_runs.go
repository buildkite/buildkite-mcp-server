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

type TestRunsClient interface {
	Get(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error)
	List(ctx context.Context, org, slug string, opt *buildkite.TestRunsListOptions) ([]buildkite.TestRun, *buildkite.Response, error)
	GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

func ListTestRuns(ctx context.Context, client TestRunsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_test_runs",
			mcp.WithDescription("List all test runs for a test suite in Buildkite Test Engine"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the test suite"),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
				mcp.Description("The slug of the test suite"),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Test Runs",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListTestRuns")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			testSuiteSlug, err := request.RequireString("test_suite_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.TestRunsListOptions{
				ListOptions: paginationParams,
			}

			testRuns, resp, err := client.List(ctx, org, testSuiteSlug, options)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get test runs: %s", string(body))), nil
			}

			result := PaginatedResult[buildkite.TestRun]{
				Items: testRuns,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			r, err := json.Marshal(&result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal test runs: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetTestRun(ctx context.Context, client TestRunsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_test_run",
			mcp.WithDescription("Get a specific test run in Buildkite Test Engine"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the test suite"),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
				mcp.Description("The slug of the test suite"),
			),
			mcp.WithString("run_id",
				mcp.Required(),
				mcp.Description("The ID of the test run"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Test Run",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTestRun")
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

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.String("run_id", runID),
			)

			testRun, resp, err := client.Get(ctx, org, testSuiteSlug, runID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get test run: %s", string(body))), nil
			}

			r, err := json.Marshal(&testRun)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal test run: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}


