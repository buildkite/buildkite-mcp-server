package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

type ClusterQueuesClient interface {
	List(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error)
	Get(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error)
}

func ListClusterQueues(client ClusterQueuesClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("list_cluster_queues",
			mcp.WithDescription("List all queues in a cluster with their keys, descriptions, dispatch status, and agent configuration"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("cluster_id",
				mcp.Required(),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Cluster Queues",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListClusterQueues")
			defer span.End()

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			clusterID, err := request.RequireString("cluster_id")
			if err != nil {
				return handleAPIError(err), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return handleAPIError(err), nil
			}
			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("cluster_id", clusterID),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			queues, resp, err := client.List(ctx, orgSlug, clusterID, &buildkite.ClusterQueuesListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return handleAPIError(err), nil
			}

			if len(queues) == 0 {
				return mcp.NewToolResultText("No clusters found"), nil
			}

			result := PaginatedResult[buildkite.ClusterQueue]{
				Items: queues,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(queues)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_clusters"}
}

func GetClusterQueue(client ClusterQueuesClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("get_cluster_queue",
			mcp.WithDescription("Get detailed information about a specific queue including its key, description, dispatch status, and hosted agent configuration"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("cluster_id",
				mcp.Required(),
			),
			mcp.WithString("queue_id",
				mcp.Required(),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Cluster Queue",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetClusterQueue")
			defer span.End()

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			clusterID, err := request.RequireString("cluster_id")
			if err != nil {
				return handleAPIError(err), nil
			}

			queueID, err := request.RequireString("queue_id")
			if err != nil {
				return handleAPIError(err), nil
			}
			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("cluster_id", clusterID),
				attribute.String("queue_id", queueID),
			)

			queue, _, err := client.Get(ctx, orgSlug, clusterID, queueID)
			if err != nil {
				return handleAPIError(err), nil
			}

			return mcpTextResult(span, &queue)
		}, []string{"read_clusters"}
}
