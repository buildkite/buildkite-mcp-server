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
	Create(ctx context.Context, org, clusterID string, qc buildkite.ClusterQueueCreate) (buildkite.ClusterQueue, *buildkite.Response, error)
	Update(ctx context.Context, org, clusterID, queueID string, qu buildkite.ClusterQueueUpdate) (buildkite.ClusterQueue, *buildkite.Response, error)
	Delete(ctx context.Context, org, clusterID, queueID string) (*buildkite.Response, error)
	Pause(ctx context.Context, org, clusterID, queueID string, qp buildkite.ClusterQueuePause) (buildkite.ClusterQueue, *buildkite.Response, error)
	Resume(ctx context.Context, org, clusterID, queueID string) (*buildkite.Response, error)
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

type CreateClusterQueueArgs struct {
	OrgSlug     string `json:"org_slug"`
	ClusterID   string `json:"cluster_id"`
	Key         string `json:"key"`
	Description string `json:"description,omitempty" jsonschema:"Description of the queue"`
}

type UpdateClusterQueueArgs struct {
	OrgSlug            string `json:"org_slug"`
	ClusterID          string `json:"cluster_id"`
	QueueID            string `json:"queue_id"`
	Description        string `json:"description,omitempty" jsonschema:"New description for the queue"`
	RetryAgentAffinity string `json:"retry_agent_affinity,omitempty" jsonschema:"Agent retry affinity: prefer-warmest or prefer-different"`
}

type DeleteClusterQueueArgs struct {
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	QueueID   string `json:"queue_id"`
}

type PauseClusterQueueDispatchArgs struct {
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	QueueID   string `json:"queue_id"`
	Note      string `json:"note,omitempty" jsonschema:"Reason for pausing dispatch"`
}

type ResumeClusterQueueDispatchArgs struct {
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

func CreateClusterQueue() (mcp.Tool, mcp.ToolHandlerFor[CreateClusterQueueArgs, any], []string) {
	return mcp.Tool{
			Name:        "create_cluster_queue",
			Description: "Create a new queue in a cluster",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Create Cluster Queue",
				DestructiveHint: boolPtr(false),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args CreateClusterQueueArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreateClusterQueue")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.String("key", args.Key),
			)

			deps := DepsFromContext(ctx)
			queue, _, err := deps.ClusterQueuesClient.Create(ctx, args.OrgSlug, args.ClusterID, buildkite.ClusterQueueCreate{
				Key:         args.Key,
				Description: args.Description,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &queue)
		}, []string{"write_clusters"}
}

func UpdateClusterQueue() (mcp.Tool, mcp.ToolHandlerFor[UpdateClusterQueueArgs, any], []string) {
	return mcp.Tool{
			Name:        "update_cluster_queue",
			Description: "Update an existing cluster queue's description or retry agent affinity",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Update Cluster Queue",
				DestructiveHint: boolPtr(false),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdateClusterQueueArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.UpdateClusterQueue")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.String("queue_id", args.QueueID),
			)

			deps := DepsFromContext(ctx)
			queue, _, err := deps.ClusterQueuesClient.Update(ctx, args.OrgSlug, args.ClusterID, args.QueueID, buildkite.ClusterQueueUpdate{
				Description:        args.Description,
				RetryAgentAffinity: buildkite.RetryAgentAffinity(args.RetryAgentAffinity),
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &queue)
		}, []string{"write_clusters"}
}

func DeleteClusterQueue() (mcp.Tool, mcp.ToolHandlerFor[DeleteClusterQueueArgs, any], []string) {
	return mcp.Tool{
			Name:        "delete_cluster_queue",
			Description: "Delete a queue from a cluster. This is a destructive operation that cannot be undone.",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Delete Cluster Queue",
				DestructiveHint: boolPtr(true),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args DeleteClusterQueueArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.DeleteClusterQueue")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.String("queue_id", args.QueueID),
			)

			deps := DepsFromContext(ctx)
			_, err := deps.ClusterQueuesClient.Delete(ctx, args.OrgSlug, args.ClusterID, args.QueueID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, "Cluster queue deleted successfully")
		}, []string{"write_clusters"}
}

func PauseClusterQueueDispatch() (mcp.Tool, mcp.ToolHandlerFor[PauseClusterQueueDispatchArgs, any], []string) {
	return mcp.Tool{
			Name:        "pause_cluster_queue_dispatch",
			Description: "Pause dispatch on a cluster queue, preventing new jobs from being dispatched to agents",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Pause Cluster Queue Dispatch",
				DestructiveHint: boolPtr(true),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args PauseClusterQueueDispatchArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.PauseClusterQueueDispatch")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.String("queue_id", args.QueueID),
			)

			deps := DepsFromContext(ctx)
			queue, _, err := deps.ClusterQueuesClient.Pause(ctx, args.OrgSlug, args.ClusterID, args.QueueID, buildkite.ClusterQueuePause{
				Note: args.Note,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &queue)
		}, []string{"write_clusters"}
}

func ResumeClusterQueueDispatch() (mcp.Tool, mcp.ToolHandlerFor[ResumeClusterQueueDispatchArgs, any], []string) {
	return mcp.Tool{
			Name:        "resume_cluster_queue_dispatch",
			Description: "Resume dispatch on a paused cluster queue, allowing jobs to be dispatched to agents again",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Resume Cluster Queue Dispatch",
				DestructiveHint: boolPtr(false),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ResumeClusterQueueDispatchArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ResumeClusterQueueDispatch")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
				attribute.String("queue_id", args.QueueID),
			)

			deps := DepsFromContext(ctx)
			_, err := deps.ClusterQueuesClient.Resume(ctx, args.OrgSlug, args.ClusterID, args.QueueID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, "Cluster queue dispatch resumed successfully")
		}, []string{"write_clusters"}
}
