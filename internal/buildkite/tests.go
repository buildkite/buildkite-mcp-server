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

type TestsClient interface {
	Get(ctx context.Context, org, slug, testID string) (buildkite.Test, *buildkite.Response, error)
}

func GetTest(ctx context.Context, client TestsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_test",
			mcp.WithDescription("Get a specific test in Buildkite Test Engine. This provides additional metadata for failed test executions"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the test suite"),
			),
			mcp.WithString("test_suite_slug",
				mcp.Required(),
				mcp.Description("The slug of the test suite"),
			),
			mcp.WithString("test_id",
				mcp.Required(),
				mcp.Description("The ID of the test"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Test",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTest")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			testSuiteSlug, err := request.RequireString("test_suite_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			testID, err := request.RequireString("test_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("test_suite_slug", testSuiteSlug),
				attribute.String("test_id", testID),
			)

			test, resp, err := client.Get(ctx, org, testSuiteSlug, testID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get test: %s", string(body))), nil
			}

			r, err := json.Marshal(&test)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal test: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}
