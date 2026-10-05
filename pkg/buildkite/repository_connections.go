package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type RepositoryConnectionsClient interface {
	List(ctx context.Context, org string) ([]buildkite.RepositoryConnection, *buildkite.Response, error)
	Get(ctx context.Context, org, id string) (buildkite.RepositoryConnection, *buildkite.Response, error)
}

type ListRepositoryConnectionsArgs struct {
	ToolInput
	OrgSlug string `json:"org_slug"`
}

func ListRepositoryConnections() (mcp.Tool, mcp.ToolHandlerFor[ListRepositoryConnectionsArgs, any], []string) {
	return mcp.Tool{
		Name:        "list_repository_connections",
		Description: "List an organization's source control repository connections, such as GitHub apps, Bitbucket Server, and GitLab Self-Managed. Returns all connections unpaginated with id, type, display_name, and url only; use get_repository_connection for host details and GitHub provider rate limits. Requires organization administrator access",
		Annotations: &mcp.ToolAnnotations{
			Title:        "List Repository Connections",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListRepositoryConnectionsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListRepositoryConnections")
		defer span.End()

		span.SetAttributes(attribute.String("org_slug", args.OrgSlug))

		deps := DepsFromContext(ctx)
		connections, _, err := deps.RepositoryConnectionsClient.List(ctx, args.OrgSlug)
		if err != nil {
			return handleBuildkiteError(err)
		}

		if connections == nil {
			connections = []buildkite.RepositoryConnection{}
		}

		span.SetAttributes(attribute.Int("item_count", len(connections)))
		return mcpTextResult(span, connections)
	}, []string{"read_organization_repository_connections"}
}

type GetRepositoryConnectionArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	ConnectionID string `json:"connection_id" jsonschema:"Repository connection UUID from list_repository_connections"`
}

// repositoryConnectionResult always emits rate_limit so an unavailable or
// non-applicable provider quota is an explicit null rather than an omitted key.
type repositoryConnectionResult struct {
	buildkite.RepositoryConnection
	RateLimit *buildkite.RepositoryConnectionRateLimit `json:"rate_limit"`
}

func GetRepositoryConnection() (mcp.Tool, mcp.ToolHandlerFor[GetRepositoryConnectionArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_repository_connection",
		Description: "Get a repository connection's service account, host, and provider rate limit. rate_limit is the cached GitHub installation core API quota for this connection (limit, used, remaining, reset_at), not the Buildkite API rate limit; it is null when the quota is unavailable or does not apply to the connection type. If reset_at is in the past, the quota has reset since it was cached, so treat used and remaining as stale. Requires organization administrator access",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Repository Connection",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetRepositoryConnectionArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetRepositoryConnection")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("connection_id", args.ConnectionID),
		)

		deps := DepsFromContext(ctx)
		connection, _, err := deps.RepositoryConnectionsClient.Get(ctx, args.OrgSlug, args.ConnectionID)
		if err != nil {
			return handleBuildkiteError(err)
		}

		span.SetAttributes(attribute.Bool("rate_limit_available", connection.RateLimit != nil))
		return mcpTextResult(span, repositoryConnectionResult{
			RepositoryConnection: connection,
			RateLimit:            connection.RateLimit,
		})
	}, []string{"read_organization_repository_connections"}
}
