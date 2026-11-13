package buildkite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type RateLimitClient interface {
	GetRateLimit(context.Context, string) (json.RawMessage, *buildkite.Response, error)
}

func (a *BuildkiteClientAdapter) GetRateLimit(ctx context.Context, org string) (json.RawMessage, *buildkite.Response, error) {
	// Resolve against the configured base here: the SDK's relative URL resolver
	// drops escaped path separators.
	endpoint := a.BaseURL.JoinPath("v2/organizations", url.PathEscape(org), "rate_limit")
	req, err := a.NewRequest(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return nil, nil, err
	}

	var status json.RawMessage
	resp, err := a.Do(req, &status)
	return status, resp, err
}

type GetRateLimitArgs struct {
	ToolInput
	OrgSlug string `json:"org_slug"`
}

func GetRateLimit() (mcp.Tool, mcp.ToolHandlerFor[GetRateLimitArgs, any], []string) {
	return mcp.Tool{
		Name: "get_rate_limit",
		Description: "Inspect Buildkite API quotas without consuming quota, even when exhausted. " +
			"Ordinary REST requests use rest and rest_user; hosted remote MCP uses mcp instead. " +
			"rest_user is organization-scoped; mcp is user-wide. " +
			"current is consumed quota, reset is seconds until reset, and enforced indicates enforcement. " +
			"Caller buckets are null for compute tokens. Endpoint-specific limits are not included. " +
			"Requires read_organizations",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Rate Limit",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetRateLimitArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetRateLimit")
		defer span.End()

		span.SetAttributes(attribute.String("org_slug", args.OrgSlug))

		deps := DepsFromContext(ctx)
		status, _, err := deps.RateLimitClient.GetRateLimit(ctx, args.OrgSlug)
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, status)
	}, []string{"read_organizations"}
}
