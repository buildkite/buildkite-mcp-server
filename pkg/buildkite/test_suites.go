package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestSuitesClient interface {
	ListByPipeline(ctx context.Context, org, pipelineSlug string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error)
}

type ListTestSuitesForPipelineArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

func ListTestSuitesForPipeline() (mcp.Tool, mcp.ToolHandlerFor[ListTestSuitesForPipelineArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_test_suites_for_pipeline",
			Description: "List the Buildkite Test Engine suites that have recorded at least one run attributed to a pipeline, ordered by creation time. Use this to find the test_suite_slug needed by the other Test Engine tools.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Test Suites for Pipeline",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListTestSuitesForPipelineArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListTestSuitesForPipeline")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.TestSuiteListOptions{
				ListOptions: paginationParams,
			}

			deps := DepsFromContext(ctx)
			testSuites, resp, err := deps.TestSuitesClient.ListByPipeline(ctx, args.OrgSlug, args.PipelineSlug, options)
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.TestSuite]{
				Items: testSuites,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(testSuites)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}
