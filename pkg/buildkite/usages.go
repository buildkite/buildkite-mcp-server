package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type UsagesClient interface {
	ListUsage(ctx context.Context, org, resource string, opts *UsageListOptions) ([]json.RawMessage, *buildkite.Response, error)
}

type UsageListOptions struct {
	From string
	To   string
	buildkite.ListOptions
}

// ListUsage implements UsagesClient. Rows stay raw JSON so large counters keep
// their exact values.
func (a *BuildkiteClientAdapter) ListUsage(ctx context.Context, org, resource string, opts *UsageListOptions) ([]json.RawMessage, *buildkite.Response, error) {
	query := url.Values{}
	if opts.From != "" {
		query.Set("from", opts.From)
	}
	if opts.To != "" {
		query.Set("to", opts.To)
	}
	query.Set("page", strconv.Itoa(opts.Page))
	query.Set("per_page", strconv.Itoa(opts.PerPage))

	path := fmt.Sprintf("v2/organizations/%s/usages/%s?%s", url.PathEscape(org), url.PathEscape(resource), query.Encode())
	req, err := a.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}

	var rows []json.RawMessage
	resp, err := a.Do(req, &rows)
	if err != nil {
		return nil, resp, err
	}

	return rows, resp, nil
}

type ListOrganizationUsageArgs struct {
	ToolInput
	OrgSlug  string `json:"org_slug"`
	Resource string `json:"resource" jsonschema:"Usage resource to read"`
	From     string `json:"from,omitempty" jsonschema:"First UTC day to include, as YYYY-MM-DD; requires to"`
	To       string `json:"to,omitempty" jsonschema:"Last UTC day to include, as YYYY-MM-DD; requires from"`
	Page     int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage  int    `json:"per_page,omitempty" jsonschema:"Rows per page (1-2000; omitted or 0 uses 100)"`
}

func ListOrganizationUsage() (mcp.Tool, mcp.ToolHandlerFor[ListOrganizationUsageArgs, any], []string) {
	schema, err := jsonschema.For[ListOrganizationUsageArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate organization usage input schema: %v", err))
	}
	schema.Properties["resource"].Enum = []any{
		"active_users",
		"self_hosted_jobs",
		"hosted_jobs",
		"self_hosted_concurrent_agents",
		"hosted_concurrent_agents",
		"managed_tests",
		"test_executions",
		"otel_ingestion",
		"otel_storage",
		"artifact_storage",
		"artifact_uploads",
		"artifact_downloads",
		"package_storage",
		"package_transfer",
		"cache",
		"anthropic_tokens",
		"openai_tokens",
	}

	return mcp.Tool{
		Name: "list_organization_usage",
		Description: "List an organization's raw daily usage for one resource. Each row has a UTC date and the resource's counters for that day, split by pipeline, suite, registry, cluster, or model where the resource records them. " +
			"Values are raw usage, not billed amounts, plan limits, or percentiles. Without from and to, returns the organization's current billing period; " +
			"from and to are inclusive, must be given together, span at most 2 years, and start within the last 2 years. Rows are ordered by date. " +
			"Returns one page; when headers.Link has rel=\"next\", request that page for more rows. " +
			"Requires the read_usages scope and permission to view the organization's usage. Returns not found when the usage API isn't enabled for the organization; cache also needs hosted cache usage enabled",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Organization Usage",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListOrganizationUsageArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListOrganizationUsage")
		defer span.End()

		paginationParams := paginationFromArgs(args.Page, args.PerPage)

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("resource", args.Resource),
			attribute.String("from", args.From),
			attribute.String("to", args.To),
			attribute.Int("page", paginationParams.Page),
			attribute.Int("per_page", paginationParams.PerPage),
		)

		deps := DepsFromContext(ctx)
		rows, resp, err := deps.UsagesClient.ListUsage(ctx, args.OrgSlug, args.Resource, &UsageListOptions{
			From:        args.From,
			To:          args.To,
			ListOptions: paginationParams,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		if rows == nil {
			rows = []json.RawMessage{}
		}

		result := PaginatedResult[json.RawMessage]{
			Items: rows,
			Headers: map[string]string{
				"Link": resp.Header.Get("Link"),
			},
		}

		span.SetAttributes(attribute.Int("item_count", len(rows)))

		return mcpTextResult(span, &result)
	}, []string{"read_usages"}
}
