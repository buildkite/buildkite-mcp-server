package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ClusterSecretsClient interface {
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
