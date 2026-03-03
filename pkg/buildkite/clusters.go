package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

type ClustersClient interface {
	List(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error)
	Get(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error)
}

func ListClusters(client ClustersClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("list_clusters",
			mcp.WithDescription("List all clusters in an organization with their names, descriptions, default queues, and creation details"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Clusters",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListClusters")
			defer span.End()

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return handleAPIError(err), nil
			}
			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			clusters, resp, err := client.List(ctx, orgSlug, &buildkite.ClustersListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return handleAPIError(err), nil
			}

			if len(clusters) == 0 {
				return mcp.NewToolResultText("No clusters found"), nil
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

func GetCluster(client ClustersClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("get_cluster",
			mcp.WithDescription("Get detailed information about a specific cluster including its name, description, default queue, and configuration"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("cluster_id",
				mcp.Required(),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Cluster",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetCluster")
			defer span.End()

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			clusterID, err := request.RequireString("cluster_id")
			if err != nil {
				return handleAPIError(err), nil
			}
			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("cluster_id", clusterID),
			)

			cluster, _, err := client.Get(ctx, orgSlug, clusterID)
			if err != nil {
				return handleAPIError(err), nil
			}

			return mcpTextResult(span, &cluster)
		}, []string{"read_clusters"}
}
