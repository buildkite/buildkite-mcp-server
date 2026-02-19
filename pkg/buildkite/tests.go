package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

type TestsClient interface {
	Get(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error)
}

func GetTest(client TestsClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("get_test",
			mcp.WithDescription("Get a specific test in Buildkite Test Engine. This provides additional metadata for failed test executions"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
			),
			mcp.WithString("test_id",
				mcp.Required(),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Test",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTest")
			defer span.End()

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			testSuiteSlug, err := request.RequireString("test_suite_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			testID, err := request.RequireString("test_id")
			if err != nil {
				return handleAPIError(err), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.String("test_id", testID),
			)

			test, _, err := client.Get(ctx, orgSlug, testSuiteSlug, testID)
			if err != nil {
				return handleAPIError(err), nil
			}

			return mcpTextResult(span, &test)
		}, []string{"read_suites"}
}
