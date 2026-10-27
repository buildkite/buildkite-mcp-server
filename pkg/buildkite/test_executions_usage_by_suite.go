package buildkite

import (
	"context"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ListTestExecutionsUsageBySuiteArgs struct {
	GetTestExecutionsUsageArgs
	PerPage int    `json:"per_page,omitempty" jsonschema:"Grouped rows per page, not suites; defaults to 30"`
	After   string `json:"after,omitempty" jsonschema:"Opaque after value from a previous response's links.next URL; forward-only"`
}

func ListTestExecutionsUsageBySuite() (mcp.Tool, mcp.ToolHandlerFor[ListTestExecutionsUsageBySuiteArgs, any], []string) {
	schema, err := jsonschema.For[ListTestExecutionsUsageBySuiteArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate test executions usage by suite schema: %v", err))
	}
	schema.Properties["interval"].Enum = []any{"day", "month"}
	schema.Properties["per_page"].Minimum = jsonschema.Ptr(float64(1))
	schema.Properties["per_page"].Maximum = jsonschema.Ptr(float64(100))

	return mcp.Tool{
		Name:        "list_test_executions_usage_by_suite",
		Description: "List recorded test executions by suite for an organization and any billing children. Sparse rows group by clipped day or month, organization UUID and suite UUID, ordered by date then organization UUID then suite UUID. Counts are additive corrected execution sums, not distinct managed tests; fully corrected zero groups are omitted rather than zero-filled. Suite slug/name are null when unavailable or not caller-visible; rows remain. Returns one page with links.self/links.next (null on the last page), no summary or MCP accumulation. Use get_test_executions_usage for the main report. Five-minute cache retains generated_at as UTC calculation time, not ingestion watermark. Late events can revise results; no cross-page snapshot is promised. Requires read_organizations; disabled usage-breakdowns returns 404 and query failures return 503.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Test Executions Usage by Suite",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListTestExecutionsUsageBySuiteArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListTestExecutionsUsageBySuite")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.String("interval", args.Interval),
			attribute.Int("per_page", args.PerPage),
		)

		report, _, err := DepsFromContext(ctx).OrganizationUsageClient.GetTestExecutionsUsageBySuite(ctx, args.OrgSlug, &buildkite.TestExecutionsUsageBySuiteOptions{
			TestExecutionsUsageOptions: buildkite.TestExecutionsUsageOptions{From: args.From, To: args.To, Interval: args.Interval},
			PerPage:                    args.PerPage, After: args.After,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, report)
	}, []string{"read_organizations"}
}
