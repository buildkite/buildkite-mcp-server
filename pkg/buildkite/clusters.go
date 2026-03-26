package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ClustersClient interface {
	List(ctx context.Context, org string, opts *buildkite.ClustersListOptions) ([]buildkite.Cluster, *buildkite.Response, error)
	Get(ctx context.Context, org, id string) (buildkite.Cluster, *buildkite.Response, error)
	Create(ctx context.Context, org string, cc buildkite.ClusterCreate) (buildkite.Cluster, *buildkite.Response, error)
	Update(ctx context.Context, org, id string, cu buildkite.ClusterUpdate) (buildkite.Cluster, *buildkite.Response, error)
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

type CreateClusterArgs struct {
	OrgSlug     string `json:"org_slug"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty" jsonschema:"Description of the cluster"`
	Emoji       string `json:"emoji,omitempty" jsonschema:"Emoji for the cluster (e.g. :toolbox:)"`
	Color       string `json:"color,omitempty" jsonschema:"Hex color code for the cluster (e.g. #A9CCE3)"`
}

type UpdateClusterArgs struct {
	OrgSlug        string `json:"org_slug"`
	ClusterID      string `json:"cluster_id"`
	Name           string `json:"name,omitempty" jsonschema:"New name for the cluster"`
	Description    string `json:"description,omitempty" jsonschema:"New description for the cluster"`
	Emoji          string `json:"emoji,omitempty" jsonschema:"New emoji for the cluster"`
	Color          string `json:"color,omitempty" jsonschema:"New hex color code for the cluster"`
	DefaultQueueID string `json:"default_queue_id,omitempty" jsonschema:"ID of the default queue for the cluster"`
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
				return handleBuildkiteError(err)
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
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &cluster)
		}, []string{"read_clusters"}
}

func CreateCluster() (mcp.Tool, mcp.ToolHandlerFor[CreateClusterArgs, any], []string) {
	return mcp.Tool{
			Name:        "create_cluster",
			Description: "Create a new cluster in an organization",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Create Cluster",
				DestructiveHint: boolPtr(false),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args CreateClusterArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreateCluster")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("name", args.Name),
			)

			deps := DepsFromContext(ctx)
			cluster, _, err := deps.ClustersClient.Create(ctx, args.OrgSlug, buildkite.ClusterCreate{
				Name:        args.Name,
				Description: args.Description,
				Emoji:       args.Emoji,
				Color:       args.Color,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &cluster)
		}, []string{"write_clusters"}
}

func UpdateCluster() (mcp.Tool, mcp.ToolHandlerFor[UpdateClusterArgs, any], []string) {
	return mcp.Tool{
			Name:        "update_cluster",
			Description: "Update an existing cluster's name, description, emoji, color, or default queue",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Update Cluster",
				DestructiveHint: boolPtr(false),
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdateClusterArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.UpdateCluster")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("cluster_id", args.ClusterID),
			)

			deps := DepsFromContext(ctx)
			cluster, _, err := deps.ClustersClient.Update(ctx, args.OrgSlug, args.ClusterID, buildkite.ClusterUpdate{
				Name:           args.Name,
				Description:    args.Description,
				Emoji:          args.Emoji,
				Color:          args.Color,
				DefaultQueueID: args.DefaultQueueID,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &cluster)
		}, []string{"write_clusters"}
}
