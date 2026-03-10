package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestRunsClient interface {
	Get(ctx context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error)
	List(ctx context.Context, org, slug string, opt *buildkite.TestRunsListOptions) ([]buildkite.TestRun, *buildkite.Response, error)
	GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

type ListTestRunsArgs struct {
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
	Page          int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage       int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

type GetTestRunArgs struct {
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
	RunID         string `json:"run_id"`
}

func ListTestRuns() (mcp.Tool, mcp.ToolHandlerFor[ListTestRunsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_test_runs",
			Description: "List all test runs for a test suite in Buildkite Test Engine",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Test Runs",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListTestRunsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListTestRuns")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}

			if args.TestSuiteSlug == "" {
				return utils.NewToolResultError("test_suite_slug is required"), nil, nil
			}

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.TestRunsListOptions{
				ListOptions: paginationParams,
			}

			deps := DepsFromContext(ctx)
			testRuns, resp, err := deps.TestRunsClient.List(ctx, args.OrgSlug, args.TestSuiteSlug, options)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			result := PaginatedResult[buildkite.TestRun]{
				Items: testRuns,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			r, err := json.Marshal(&result)
			if err != nil {
				return nil, nil, fmt.Errorf("failed to marshal test runs: %w", err)
			}

			span.SetAttributes(
				attribute.Int("item_count", len(testRuns)),
				attribute.Int("estimated_tokens", tokens.EstimateTokens(string(r))),
			)

			return utils.NewToolResultText(string(r)), nil, nil
		}, []string{"read_suites"}
}

func GetTestRun() (mcp.Tool, mcp.ToolHandlerFor[GetTestRunArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_test_run",
			Description: "Get a specific test run in Buildkite Test Engine",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Test Run",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetTestRunArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTestRun")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}

			if args.TestSuiteSlug == "" {
				return utils.NewToolResultError("test_suite_slug is required"), nil, nil
			}

			if args.RunID == "" {
				return utils.NewToolResultError("run_id is required"), nil, nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("run_id", args.RunID),
			)

			deps := DepsFromContext(ctx)
			testRun, resp, err := deps.TestRunsClient.Get(ctx, args.OrgSlug, args.TestSuiteSlug, args.RunID)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return utils.NewToolResultError(fmt.Sprintf("failed to get test run: %s", string(body))), nil, nil
			}

			return mcpTextResult(span, &testRun)
		}, []string{"read_suites"}
}
