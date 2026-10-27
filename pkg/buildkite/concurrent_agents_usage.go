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

type GetConcurrentAgentsUsageArgs struct {
	ToolInput
	OrgSlug    string `json:"org_slug" jsonschema:"Organization slug or UUID"`
	From       string `json:"from" jsonschema:"Inclusive UTC date in YYYY-MM-DD; earliest is today minus three calendar months"`
	To         string `json:"to" jsonschema:"Inclusive UTC date in YYYY-MM-DD; must be on or after from and no later than today"`
	Interval   string `json:"interval,omitempty" jsonschema:"Defaults to day. Month buckets use the calendar first while source data still respects the exact from/to dates"`
	Percentile int    `json:"percentile,omitempty" jsonschema:"Defaults to 95; discrete percentile across daily values even when interval is month"`
	AgentType  string `json:"agent_type,omitempty" jsonschema:"Defaults to self_hosted"`
}

func GetConcurrentAgentsUsage() (mcp.Tool, mcp.ToolHandlerFor[GetConcurrentAgentsUsageArgs, any], []string) {
	schema, err := jsonschema.For[GetConcurrentAgentsUsageArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate concurrent agents usage schema: %v", err))
	}
	schema.Properties["interval"].Enum = []any{"day", "month"}
	schema.Properties["agent_type"].Enum = []any{"self_hosted", "hosted"}
	schema.Properties["percentile"].Minimum = jsonschema.Ptr(float64(1))
	schema.Properties["percentile"].Maximum = jsonschema.Ptr(float64(100))

	return mcp.Tool{
		Name:        "get_concurrent_agents_usage",
		Description: "Report recorded concurrent-agent usage for an organization and any billing children. Percentiles are discrete across zero-filled DAILY sums of each organization's peak, not intraday P80, simultaneous cross-organization concurrency, or plan-specific billable usage. Monthly items are maximum daily sums within each month; the percentile still uses underlying daily values, not monthly bars. Use interval=day to retrieve the statistic's inputs. Preserves Usage-page scope, aggregation and rounding; complete series without pagination. Five-minute server cache retains generated_at (UTC calculation time, not ingestion watermark); late events can revise results on refresh. Requires read_organizations. The default-off usage-breakdowns feature returns 404 when disabled; query failures return 503.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Concurrent Agents Usage",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetConcurrentAgentsUsageArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetConcurrentAgentsUsage")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.String("interval", args.Interval),
			attribute.Int("percentile", args.Percentile),
			attribute.String("agent_type", args.AgentType),
		)

		report, _, err := DepsFromContext(ctx).OrganizationUsageClient.GetConcurrentAgentsUsage(ctx, args.OrgSlug, &buildkite.ConcurrentAgentsUsageOptions{
			From: args.From, To: args.To, Interval: args.Interval,
			Percentile: args.Percentile, AgentType: args.AgentType,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, report)
	}, []string{"read_organizations"}
}
