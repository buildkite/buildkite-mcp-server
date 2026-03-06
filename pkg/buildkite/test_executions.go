package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestExecutionsClient interface {
	GetFailedExecutions(ctx context.Context, org, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error)
}

type GetFailedTestExecutionsArgs struct {
	OrgSlug                string `json:"org_slug" jsonschema:"The organization slug"`
	TestSuiteSlug          string `json:"test_suite_slug" jsonschema:"The test suite slug"`
	RunID                  string `json:"run_id" jsonschema:"The test run ID"`
	IncludeFailureExpanded bool   `json:"include_failure_expanded,omitempty" jsonschema:"Include expanded failure details such as full error messages and stack traces"`
	Page                   int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage                int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
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

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}

			if args.TestSuiteSlug == "" {
				return utils.NewToolResultError("test_suite_slug is required"), nil, nil
			}

			if args.RunID == "" {
				return utils.NewToolResultError("run_id is required"), nil, nil
			}

			// Get client-side pagination parameters (always enabled)
			paginationParams := clientSidePaginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("run_id", args.RunID),
				attribute.Bool("include_failure_expanded", args.IncludeFailureExpanded),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.FailedExecutionsOptions{
				IncludeFailureExpanded: args.IncludeFailureExpanded,
			}

			deps := DepsFromContext(ctx)
			failedExecutions, _, err := deps.TestExecutionsClient.GetFailedExecutions(ctx, args.OrgSlug, args.TestSuiteSlug, args.RunID, options)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			// Always apply client-side pagination
			result := applyClientSidePagination(failedExecutions, paginationParams)

			span.SetAttributes(
				attribute.Int("item_count", len(failedExecutions)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}
