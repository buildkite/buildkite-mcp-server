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

type ListHostedAgentsUsageByPipelineArgs struct {
	GetHostedAgentsUsageArgs
	PerPage int    `json:"per_page,omitempty" jsonschema:"Grouped rows per page, not pipelines; defaults to 30"`
	After   string `json:"after,omitempty" jsonschema:"Opaque after value from a previous response's links.next URL; forward-only"`
}

func ListHostedAgentsUsageByPipeline() (mcp.Tool, mcp.ToolHandlerFor[ListHostedAgentsUsageByPipelineArgs, any], []string) {
	schema, err := jsonschema.For[ListHostedAgentsUsageByPipelineArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate hosted agents usage by pipeline schema: %v", err))
	}
	schema.Properties["interval"].Enum = []any{"day", "month"}
	schema.Properties["per_page"].Minimum = jsonschema.Ptr(float64(1))
	schema.Properties["per_page"].Maximum = jsonschema.Ptr(float64(100))

	return mcp.Tool{
		Name:        "list_hosted_agents_usage_by_pipeline",
		Description: "List hosted compute CSV-detail rows for an organization and any billing children, grouped by interval, organization, pipeline and instance shape. Sparse rows include duration_microseconds, job_count and per-row CPU-weighted floored vcpu_minutes; their sum need not equal get_hosted_agents_usage totals. Unknown nonempty shapes retain raw duration/job counts with zero weighted minutes; empty self-hosted shapes are excluded. Pipeline UUID/slug are null when unavailable, deleted or not caller-visible; rows remain. Ordered by date, legacy organization ID, pipeline ID and shape. Returns one page with links.self/links.next (null on the last page), no summary or MCP accumulation. Five-minute cache retains generated_at as UTC calculation time, not ingestion watermark. Late events can revise results; no cross-page snapshot is promised. Requires read_organizations; disabled usage-breakdowns returns 404 and query failures return 503.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Hosted Agents Usage by Pipeline",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args ListHostedAgentsUsageByPipelineArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListHostedAgentsUsageByPipeline")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.String("interval", args.Interval),
			attribute.Int("per_page", args.PerPage),
		)

		report, _, err := DepsFromContext(ctx).OrganizationUsageClient.GetHostedAgentsUsageByPipeline(ctx, args.OrgSlug, &buildkite.HostedAgentsUsageByPipelineOptions{
			HostedAgentsUsageOptions: buildkite.HostedAgentsUsageOptions{From: args.From, To: args.To, Interval: args.Interval},
			PerPage:                  args.PerPage, After: args.After,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, report)
	}, []string{"read_organizations"}
}
