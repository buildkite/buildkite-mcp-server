package buildkite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

type ClustersClient interface {
	List(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error)
	Get(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error)
}

func ListClusters(ctx context.Context, client ClustersClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_clusters",
			mcp.WithDescription("List all buildkite clusters in an organization"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Clusters",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListClusters")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			span.SetAttributes(
				attribute.String("org", org),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			clusters, resp, err := client.List(ctx, org, &buildkite.ClustersListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != 200 {
				return mcp.NewToolResultError("Failed to list clusters"), nil
			}
			if len(clusters) == 0 {
				return mcp.NewToolResultText("No clusters found"), nil
			}

			r, err := json.Marshal(clusters)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal clusters response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetCluster(ctx context.Context, client ClustersClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_cluster",
			mcp.WithDescription("Get details of a buildkite cluster in an organization"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("cluster_id",
				mcp.Required(),
				mcp.Description("The id of the cluster"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Cluster",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetCluster")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			clusterID, err := request.RequireString("cluster_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("cluster_id", clusterID),
			)

			cluster, resp, err := client.Get(ctx, org, clusterID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != 200 {
				return mcp.NewToolResultError("Failed to get cluster"), nil
			}

			r, err := json.Marshal(cluster)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal cluster response: %w", err)
			}
			return mcp.NewToolResultText(string(r)), nil
		}
}
