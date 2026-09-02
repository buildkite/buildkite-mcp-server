package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ClusterSecretsClient interface {
	List(ctx context.Context, org, clusterID string, opt *buildkite.ClusterSecretsListOptions) ([]buildkite.ClusterSecret, *buildkite.Response, error)
	Get(ctx context.Context, org, clusterID, secretID string) (buildkite.ClusterSecret, *buildkite.Response, error)
}

type GetClusterSecretArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	SecretID  string `json:"secret_id"`
}

func GetClusterSecret() (mcp.Tool, mcp.ToolHandlerFor[GetClusterSecretArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_cluster_secret",
			Description: "Get cluster secret information",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Cluster Secret",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args GetClusterSecretArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetClusterSecret")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.String("secret_id", args.SecretID),
			)

			deps := DepsFromContext(ctx)
			secret, _, err := deps.ClusterSecretsClient.Get(ctx, args.OrgSlug, args.ClusterID, args.SecretID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &secret)
		}, []string{"read_secrets_details"}
}

type ListClusterSecretsArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

func ListClusterSecrets() (mcp.Tool, mcp.ToolHandlerFor[ListClusterSecretsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_cluster_secrets",
			Description: "List cluster secrets",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Cluster Secrets",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListClusterSecretsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListClusterSecrets")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			secrets, resp, err := deps.ClusterSecretsClient.List(
				ctx,
				args.OrgSlug,
				args.ClusterID,
				&buildkite.ClusterSecretsListOptions{
					ListOptions: paginationParams,
				},
			)
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.ClusterSecret]{
				Items: secrets,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(secrets)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_secrets_details"}
}
