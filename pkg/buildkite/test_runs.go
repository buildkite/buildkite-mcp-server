package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
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

func ListTestRuns(client TestRunsClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("list_test_runs",
			mcp.WithDescription("List all test runs for a test suite in Buildkite Test Engine"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
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

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			testSuiteSlug, err := request.RequireString("test_suite_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return handleAPIError(err), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.TestRunsListOptions{
				ListOptions: paginationParams,
			}

			testRuns, resp, err := client.List(ctx, orgSlug, testSuiteSlug, options)
			if err != nil {
				return handleAPIError(err), nil
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

			span.SetAttributes(
				attribute.Int("item_count", len(testRuns)),
				attribute.Int("estimated_tokens", tokens.EstimateTokens(string(r))),
			)

			return mcp.NewToolResultText(string(r)), nil
		}, []string{"read_suites"}
}

func GetTestRun(client TestRunsClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("get_test_run",
			mcp.WithDescription("Get a specific test run in Buildkite Test Engine"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
			),
			mcp.WithString("run_id",
				mcp.Required(),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Test Run",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTestRun")
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

			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.String("run_id", runID),
			)

			testRun, resp, err := client.Get(ctx, orgSlug, testSuiteSlug, runID)
			if err != nil {
				return handleAPIError(err), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get test run: %s", string(body))), nil
			}

			return mcpTextResult(span, &testRun)
		}, []string{"read_suites"}
}
