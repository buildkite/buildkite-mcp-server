package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ClustersClient interface {
	List(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error)
	Get(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error)
}

type ListClustersArgs struct {
	OrgSlug string `json:"org_slug"`
	Page    int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

type GetClusterArgs struct {
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
}

func ListClusters() (mcp.Tool, mcp.ToolHandlerFor[ListClustersArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_clusters",
			Description: "List all clusters in an organization with their names, descriptions, default queues, and creation details",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Clusters",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListClustersArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListClusters")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			clusters, resp, err := deps.ClustersClient.List(ctx, args.OrgSlug, &buildkite.ClustersListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			result := PaginatedResult[buildkite.Cluster]{
				Items: clusters,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(clusters)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_clusters"}
}

func GetCluster() (mcp.Tool, mcp.ToolHandlerFor[GetClusterArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_cluster",
			Description: "Get detailed information about a specific cluster including its name, description, default queue, and configuration",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Cluster",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args GetClusterArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetCluster")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
			)

			deps := DepsFromContext(ctx)
			cluster, _, err := deps.ClustersClient.Get(ctx, args.OrgSlug, args.ClusterID)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			return mcpTextResult(span, &cluster)
		}, []string{"read_clusters"}
}
