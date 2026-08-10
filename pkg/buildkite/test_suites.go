package buildkite

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestSuitesClient interface {
	List(ctx context.Context, org string, opt *buildkite.TestSuiteListOptions) ([]buildkite.TestSuite, *buildkite.Response, error)
	Get(ctx context.Context, org, slug string) (buildkite.TestSuite, *buildkite.Response, error)
}

type ListTestSuitesArgs struct {
	OrgSlug string `json:"org_slug"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

type GetTestSuiteArgs struct {
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
}

func ListTestSuites() (mcp.Tool, mcp.ToolHandlerFor[ListTestSuitesArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_test_suites",
			Description: "List all test suites for an organization in Buildkite Test Engine",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Test Suites",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListTestSuitesArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListTestSuites")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)
			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.TestSuiteListOptions{ListOptions: paginationParams}
			deps := DepsFromContext(ctx)
			testSuites, resp, err := deps.TestSuitesClient.List(ctx, args.OrgSlug, options)
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.TestSuite]{
				Items: testSuites,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}
			span.SetAttributes(attribute.Int("item_count", len(testSuites)))

			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}

func GetTestSuite() (mcp.Tool, mcp.ToolHandlerFor[GetTestSuiteArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_test_suite",
			Description: "Get a specific test suite in Buildkite Test Engine",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Test Suite",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetTestSuiteArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTestSuite")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
			)

			deps := DepsFromContext(ctx)
			testSuite, resp, err := deps.TestSuitesClient.Get(ctx, args.OrgSlug, args.TestSuiteSlug)
			if err != nil {
				return handleBuildkiteError(err)
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return utils.NewToolResultError(fmt.Sprintf("failed to get test suite: %s", string(body))), nil, nil
			}

			return mcpTextResult(span, &testSuite)
		}, []string{"read_suites"}
}
