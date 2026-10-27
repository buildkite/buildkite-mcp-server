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

type OrganizationUsageClient interface {
	GetActiveUsersUsage(ctx context.Context, org string, opt *buildkite.ActiveUsersUsageOptions) (buildkite.ActiveUsersUsage, *buildkite.Response, error)
	GetConcurrentAgentsUsage(ctx context.Context, org string, opt *buildkite.ConcurrentAgentsUsageOptions) (buildkite.ConcurrentAgentsUsage, *buildkite.Response, error)
	GetHostedAgentsUsage(ctx context.Context, org string, opt *buildkite.HostedAgentsUsageOptions) (buildkite.HostedAgentsUsage, *buildkite.Response, error)
}

type GetActiveUsersUsageArgs struct {
	ToolInput
	OrgSlug  string `json:"org_slug" jsonschema:"Organization slug or UUID"`
	From     string `json:"from" jsonschema:"Inclusive UTC date in YYYY-MM-DD; earliest is today minus three calendar months"`
	To       string `json:"to" jsonschema:"Inclusive UTC date in YYYY-MM-DD; must be on or after from and no later than today"`
	Interval string `json:"interval,omitempty" jsonschema:"Defaults to day. Month buckets use the calendar first while source data still respects the exact from/to dates"`
}

func GetActiveUsersUsage() (mcp.Tool, mcp.ToolHandlerFor[GetActiveUsersUsageArgs, any], []string) {
	schema, err := jsonschema.For[GetActiveUsersUsageArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate active users usage schema: %v", err))
	}
	schema.Properties["interval"].Enum = []any{"day", "month"}

	return mcp.Tool{
		Name:        "get_active_users_usage",
		Description: "Report distinct active users for an organization and any billing children, matching the Usage page. Returns day or month buckets with zero counts and cumulative carry, plus whole-range distinct users and the Usage-page average monthly active users. Do not sum daily or child counts to calculate distinct users; the monthly average can differ from whole-range users. Returns the complete date range without pagination. Reports are cached for five minutes; generated_at retains UTC calculation time, not an ingestion watermark. Late events can revise results on refresh. Requires read_organizations. The default-off usage-breakdowns feature returns 404 when disabled; query failures return 503. Not invoice reconciliation or per-user cost attribution.",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Active Users Usage",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args GetActiveUsersUsageArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetActiveUsersUsage")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.String("interval", args.Interval),
		)

		report, _, err := DepsFromContext(ctx).OrganizationUsageClient.GetActiveUsersUsage(ctx, args.OrgSlug, &buildkite.ActiveUsersUsageOptions{
			From: args.From, To: args.To, Interval: args.Interval,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, report)
	}, []string{"read_organizations"}
}
