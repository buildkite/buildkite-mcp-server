package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ClusterQueuesClient interface {
	List(ctx context.Context, org, clusterID string, opts *buildkite.ClusterQueuesListOptions) ([]buildkite.ClusterQueue, *buildkite.Response, error)
	Get(ctx context.Context, org, clusterID, queueID string) (buildkite.ClusterQueue, *buildkite.Response, error)
	Insights(ctx context.Context, org, clusterID, queueID string, opts *buildkite.ClusterQueueInsightsOptions) (buildkite.ClusterQueueInsights, *buildkite.Response, error)
	Create(ctx context.Context, org, clusterID string, qc buildkite.ClusterQueueCreate) (buildkite.ClusterQueue, *buildkite.Response, error)
	Update(ctx context.Context, org, clusterID, queueID string, qu buildkite.ClusterQueueUpdate) (buildkite.ClusterQueue, *buildkite.Response, error)
	Pause(ctx context.Context, org, clusterID, queueID string, qp buildkite.ClusterQueuePause) (buildkite.ClusterQueue, *buildkite.Response, error)
	Resume(ctx context.Context, org, clusterID, queueID string) (*buildkite.Response, error)
}

type ListClusterQueuesArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

type GetClusterQueueArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	QueueID   string `json:"queue_id"`
}

type GetClusterQueueInsightsArgs struct {
	ToolInput
	OrgSlug    string `json:"org_slug"`
	ClusterID  string `json:"cluster_id" jsonschema:"Cluster UUID, available from list_clusters"`
	QueueID    string `json:"queue_id" jsonschema:"Queue UUID, not its key; available from list_cluster_queues"`
	Include    string `json:"include,omitempty" jsonschema:"Comma-separated: history, history_comparison, hourly_changes. Each works independently. history_comparison includes both history periods and changes; hourly_changes compares hourly averages with yesterday and last week. Omit for current and recent only."`
	TimePeriod string `json:"time_period,omitempty" jsonschema:"1h (default), 24h or 7d. Requires history or history_comparison; does not affect hourly_changes."`
}

type CreateClusterQueueArgs struct {
	ToolInput
	OrgSlug     string `json:"org_slug"`
	ClusterID   string `json:"cluster_id"`
	Key         string `json:"key"`
	Description string `json:"description,omitempty" jsonschema:"Description of the queue"`
}

type UpdateClusterQueueArgs struct {
	ToolInput
	OrgSlug            string  `json:"org_slug"`
	ClusterID          string  `json:"cluster_id"`
	QueueID            string  `json:"queue_id"`
	Description        *string `json:"description,omitempty" jsonschema:"New description for the queue"`
	RetryAgentAffinity *string `json:"retry_agent_affinity,omitempty" jsonschema:"Agent retry affinity: prefer-warmest or prefer-different"`
}

type PauseClusterQueueDispatchArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	ClusterID string `json:"cluster_id"`
	QueueID   string `json:"queue_id"`
	Note      string `json:"note,omitempty" jsonschema:"Reason for pausing dispatch"`
}

type ResumeClusterQueueDispatchArgs struct {
	ToolInput
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

func GetClusterQueueInsights() (mcp.Tool, mcp.ToolHandlerFor[GetClusterQueueInsightsArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_cluster_queue_insights",
		Description: "Get queue agent/job counts, recent completions and wait times, with optional history and comparisons. Current counts are snapshots; history counts are last values per bucket; hourly_changes counts are time-weighted averages, not totals. Null means unavailable, not zero. Check timestamps and coverage; attribute limitations to the section that reports them. Zero sampled values do not prove there was no backlog or waiting throughout a period. Treat capacity diagnoses and causal explanations as hypotheses unless supported by further evidence. Requires Cluster Insights entitlement and the Queue Insights REST API feature enabled.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Cluster Queue Insights",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetClusterQueueInsightsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetClusterQueueInsights")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("cluster_id", args.ClusterID),
			attribute.String("queue_id", args.QueueID),
			attribute.String("include", args.Include),
			attribute.String("time_period", args.TimePeriod),
		)

		deps := DepsFromContext(ctx)
		insights, _, err := deps.ClusterQueuesClient.Insights(ctx, args.OrgSlug, args.ClusterID, args.QueueID, &buildkite.ClusterQueueInsightsOptions{
			Include:    args.Include,
			TimePeriod: args.TimePeriod,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &insights)
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
			DestructiveHint: boolPtr(true),
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
		update := buildkite.ClusterQueueUpdate{}
		if args.Description != nil {
			update.Description = buildkite.Some(*args.Description)
		}
		if args.RetryAgentAffinity != nil {
			update.RetryAgentAffinity = buildkite.Some(buildkite.RetryAgentAffinity(*args.RetryAgentAffinity))
		}

		queue, _, err := deps.ClusterQueuesClient.Update(ctx, args.OrgSlug, args.ClusterID, args.QueueID, update)
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &queue)
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
			DestructiveHint: boolPtr(true),
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
