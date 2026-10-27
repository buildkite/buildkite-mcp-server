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

type GetHostedAgentsUsageArgs struct {
	ToolInput
	OrgSlug  string `json:"org_slug" jsonschema:"Organization slug or UUID"`
	From     string `json:"from" jsonschema:"Inclusive UTC date in YYYY-MM-DD; earliest is today minus three calendar months"`
	To       string `json:"to" jsonschema:"Inclusive UTC date in YYYY-MM-DD; must be on or after from and no later than today"`
	Interval string `json:"interval,omitempty" jsonschema:"Defaults to day; month date labels use the calendar first"`
}

func GetHostedAgentsUsage() (mcp.Tool, mcp.ToolHandlerFor[GetHostedAgentsUsageArgs, any], []string) {
	schema, err := jsonschema.For[GetHostedAgentsUsageArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate hosted agents usage schema: %v", err))
	}
	schema.Properties["interval"].Enum = []any{"day", "month"}

	return mcp.Tool{
		Name:        "get_hosted_agents_usage",
		Description: "Report hosted compute vCPU-minutes for an organization and any billing children, matching the Usage table and chart. Returns period totals and day or month resource buckets for Linux amd64, Linux arm64, macOS M4 and Windows amd64; all four resource keys include zeros. Dated items use date as a label; period.from/to give actual inclusive UTC bounds clipped to the request. CPU-weighted shapes combine per resource before flooring; unknown shapes are excluded. Period totals and interval buckets round independently, so summing rounded buckets can differ from the summary. Returns the complete series without pagination or MCP recalculation. Five-minute server cache retains generated_at (UTC calculation time, not ingestion watermark); late events can revise results on refresh. Requires read_organizations. The default-off usage-breakdowns feature returns 404 when disabled; query failures return 503. Not invoice reconciliation or per-job cost attribution.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Hosted Agents Usage",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetHostedAgentsUsageArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetHostedAgentsUsage")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.String("interval", args.Interval),
		)

		report, _, err := DepsFromContext(ctx).OrganizationUsageClient.GetHostedAgentsUsage(ctx, args.OrgSlug, &buildkite.HostedAgentsUsageOptions{
			From: args.From, To: args.To, Interval: args.Interval,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, report)
	}, []string{"read_organizations"}
}
