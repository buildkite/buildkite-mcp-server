package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestsClient interface {
	Get(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error)
}

type GetTestArgs struct {
	OrgSlug       string `json:"org_slug" jsonschema:"The organization slug"`
	TestSuiteSlug string `json:"test_suite_slug" jsonschema:"The test suite slug"`
	TestID        string `json:"test_id" jsonschema:"The test ID"`
}

func GetTest() (mcp.Tool, mcp.ToolHandlerFor[GetTestArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_test",
			Description: "Get a specific test in Buildkite Test Engine. This provides additional metadata for failed test executions",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Test",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetTestArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTest")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}

			if args.TestSuiteSlug == "" {
				return utils.NewToolResultError("test_suite_slug is required"), nil, nil
			}

			if args.TestID == "" {
				return utils.NewToolResultError("test_id is required"), nil, nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("test_id", args.TestID),
			)

			deps := DepsFromContext(ctx)
			test, _, err := deps.TestsClient.Get(ctx, args.OrgSlug, args.TestSuiteSlug, args.TestID)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			return mcpTextResult(span, &test)
		}, []string{"read_suites"}
}
