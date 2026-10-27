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

type GetTestExecutionsUsageArgs struct {
	ToolInput
	OrgSlug  string `json:"org_slug" jsonschema:"Organization slug or UUID"`
	From     string `json:"from" jsonschema:"Inclusive UTC date in YYYY-MM-DD; earliest is today minus three calendar months"`
	To       string `json:"to" jsonschema:"Inclusive UTC date in YYYY-MM-DD; must be on or after from and no later than today"`
	Interval string `json:"interval,omitempty" jsonschema:"Defaults to day; month date labels use the calendar first"`
}

func GetTestExecutionsUsage() (mcp.Tool, mcp.ToolHandlerFor[GetTestExecutionsUsageArgs, any], []string) {
	schema, err := jsonschema.For[GetTestExecutionsUsageArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate test executions usage schema: %v", err))
	}
	schema.Properties["interval"].Enum = []any{"day", "month"}

	return mcp.Tool{
		Name:        "get_test_executions_usage",
		Description: "Report test execution usage for an organization and any billing children, matching the Usage panels. Returns zero-filled day or month execution counts, additive cumulative executions, and period totals, active suites and average executions. Dated items use date as a label; period.from/to give actual inclusive UTC bounds clipped to the request. The average is floor(total executions / requested day or month bucket count), including zero buckets. Execution counts are not the distinct managed-test billing metric. Returns the complete series without pagination or MCP recalculation. Five-minute server cache retains generated_at (UTC calculation time, not ingestion watermark); late events can revise results on refresh. Requires read_organizations. The default-off usage-breakdowns feature returns 404 when disabled; query failures return 503.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Test Executions Usage",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetTestExecutionsUsageArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetTestExecutionsUsage")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.String("interval", args.Interval),
		)

		report, _, err := DepsFromContext(ctx).OrganizationUsageClient.GetTestExecutionsUsage(ctx, args.OrgSlug, &buildkite.TestExecutionsUsageOptions{
			From: args.From, To: args.To, Interval: args.Interval,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, report)
	}, []string{"read_organizations"}
}
