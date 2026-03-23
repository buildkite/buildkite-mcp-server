package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ClusterQueuesClient interface {
	List(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error)
	Get(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error)
}

type ListClusterQueuesArgs struct {
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

type GetClusterQueueArgs struct {
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	QueueID   string `json:"queue_id"`
}

func ListClusterQueues() (mcp.Tool, mcp.ToolHandlerFor[ListClusterQueuesArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_cluster_queues",
			Description: "List all queues in a cluster with their keys, descriptions, dispatch status, and agent configuration",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Cluster Queues",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListClusterQueuesArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListClusterQueues")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			queues, resp, err := deps.ClusterQueuesClient.List(ctx, args.OrgSlug, args.ClusterID, &buildkite.ClusterQueuesListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return handleBuildkiteError(err)
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

func GetClusterQueue() (mcp.Tool, mcp.ToolHandlerFor[GetClusterQueueArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_cluster_queue",
			Description: "Get detailed information about a specific queue including its key, description, dispatch status, and hosted agent configuration",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Cluster Queue",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args GetClusterQueueArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetClusterQueue")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.String("queue_id", args.QueueID),
			)

			deps := DepsFromContext(ctx)
			queue, _, err := deps.ClusterQueuesClient.Get(ctx, args.OrgSlug, args.ClusterID, args.QueueID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &queue)
		}, []string{"read_clusters"}
}
