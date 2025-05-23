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

type ClusterQueuesClient interface {
	List(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error)
	Get(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error)
}

func ListClusterQueues(ctx context.Context, client ClusterQueuesClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_cluster_queues",
			mcp.WithDescription("List all buildkite queues in a cluster"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("cluster_id",
				mcp.Required(),
				mcp.Description("The id of the cluster"),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Cluster Queues",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListClusterQueues")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			clusterID, err := request.RequireString("cluster_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("cluster_id", clusterID),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			queues, resp, err := client.List(ctx, org, clusterID, &buildkite.ClusterQueuesListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != 200 {
				return mcp.NewToolResultError("Failed to list clusters"), nil
			}
			if len(queues) == 0 {
				return mcp.NewToolResultText("No clusters found"), nil
			}

			r, err := json.Marshal(queues)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal cluster queues response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetClusterQueue(ctx context.Context, client ClusterQueuesClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_cluster_queue",
			mcp.WithDescription("Get details of a buildkite cluster queue in an organization"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("cluster_id",
				mcp.Required(),
				mcp.Description("The id of the cluster"),
			),
			mcp.WithString("queue_id",
				mcp.Required(),
				mcp.Description("The id of the queue"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Cluster Queue",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetClusterQueue")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			clusterID, err := request.RequireString("cluster_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			queueID, err := request.RequireString("queue_id")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("cluster_id", clusterID),
				attribute.String("queue_id", queueID),
			)

			queue, resp, err := client.Get(ctx, org, clusterID, queueID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != 200 {
				return mcp.NewToolResultError("Failed to list clusters"), nil
			}

			r, err := json.Marshal(queue)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal cluster queue response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}
