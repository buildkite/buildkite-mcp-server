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

type ListConcurrentAgentsUsageByOrganizationArgs struct {
	GetConcurrentAgentsUsageArgs
	PerPage int    `json:"per_page,omitempty" jsonschema:"Defaults to 30"`
	After   string `json:"after,omitempty" jsonschema:"Opaque after value from a previous response's links.next URL; forward-only"`
}

func ListConcurrentAgentsUsageByOrganization() (mcp.Tool, mcp.ToolHandlerFor[ListConcurrentAgentsUsageByOrganizationArgs, any], []string) {
	schema, err := jsonschema.For[ListConcurrentAgentsUsageByOrganizationArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate concurrent agents usage by organization schema: %v", err))
	}
	schema.Properties["interval"].Enum = []any{"day", "month"}
	schema.Properties["agent_type"].Enum = []any{"self_hosted", "hosted"}
	schema.Properties["percentile"].Minimum = jsonschema.Ptr(float64(1))
	schema.Properties["percentile"].Maximum = jsonschema.Ptr(float64(100))
	schema.Properties["per_page"].Minimum = jsonschema.Ptr(float64(1))
	schema.Properties["per_page"].Maximum = jsonschema.Ptr(float64(100))

	return mcp.Tool{
		Name:        "list_concurrent_agents_usage_by_organization",
		Description: "List recorded concurrent-agent usage by organization, including billing children, in organization UUID order. Each organization has a full-range summary and zero-filled day or month buckets. Discrete percentiles use daily peaks: the summary uses the requested range, while each bucket uses daily samples within its clipped dates (day percentile equals the maximum). These are not intraday percentiles or plan-specific billable usage. Per-organization percentiles are nonadditive; use get_concurrent_agents_usage for the aggregate. Returns one page and links.self/links.next (null on the last page); pass the next URL's after value unchanged. Five-minute cached reports retain generated_at as UTC calculation time, not ingestion watermark. Late events can revise results; no cross-page snapshot is promised. Requires read_organizations; disabled usage-breakdowns returns 404 and query failures return 503.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Concurrent Agents Usage by Organization",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListConcurrentAgentsUsageByOrganizationArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListConcurrentAgentsUsageByOrganization")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.String("interval", args.Interval),
			attribute.Int("percentile", args.Percentile),
			attribute.String("agent_type", args.AgentType),
			attribute.Int("per_page", args.PerPage),
		)

		report, _, err := DepsFromContext(ctx).OrganizationUsageClient.GetConcurrentAgentsUsageByOrganization(ctx, args.OrgSlug, &buildkite.ConcurrentAgentsUsageByOrganizationOptions{
			ConcurrentAgentsUsageOptions: buildkite.ConcurrentAgentsUsageOptions{
				From: args.From, To: args.To, Interval: args.Interval,
				Percentile: args.Percentile, AgentType: args.AgentType,
			},
			PerPage: args.PerPage, After: args.After,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, report)
	}, []string{"read_organizations"}
}
