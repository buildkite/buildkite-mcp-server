package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

type PipelinesClient interface {
	Get(ctx context.Context, org, pipelineSlug string) (buildkite.Pipeline, *buildkite.Response, error)
	List(ctx context.Context, org string, options *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error)
	Create(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	Update(ctx context.Context, org, pipelineSlug string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
}

func ListPipelines(ctx context.Context, client PipelinesClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_pipelines",
			mcp.WithDescription("List all pipelines in an organization with their basic details, build counts, and current status"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Pipelines",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListPipelines")
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

			pipelines, resp, err := client.List(ctx, org, &buildkite.PipelineListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			result := PaginatedResult[buildkite.Pipeline]{
				Items: pipelines,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			r, err := json.Marshal(&result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal pipelines: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetPipeline(ctx context.Context, client PipelinesClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_pipeline",
			mcp.WithDescription("Get detailed information about a specific pipeline including its configuration, steps, environment variables, and build statistics"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
				mcp.Description("The slug of the pipeline"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Pipeline",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetPipeline")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := request.RequireString("pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
			)

			pipeline, _, err := client.Get(ctx, org, pipelineSlug)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			r, err := json.Marshal(&pipeline)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal issue: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

type CreatePipelineArgs struct {
	OrgSlug                   string
	Name                      string
	RepositoryURL             string
	ClusterID                 string
	Description               string
	Configuration             string
	DefaultBranch             string
	SkipQueuedBranchBuilds    bool
	CancelRunningBranchBuilds bool
	Tags                      []string
}

func CreatePipeline(ctx context.Context, client PipelinesClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[CreatePipelineArgs]) {
	return mcp.NewTool("create_pipeline",
			mcp.WithDescription("Set up a new CI/CD pipeline in Buildkite with YAML configuration, repository connection, and cluster assignment"),
			mcp.WithString("org_slug",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline. This is used to determine where to create the pipeline"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("The name of the pipeline"),
			),
			mcp.WithString("repository_url",
				mcp.Required(),
				mcp.Description("The Git repository URL to use for the pipeline"),
			),
			mcp.WithString("cluster_id",
				mcp.Required(),
				mcp.Description("The ID value of the cluster the pipeline will be associated with"),
			),
			mcp.WithString("configuration",
				mcp.Required(),
				mcp.Description("The pipeline configuration in YAML format. Contains the build steps and pipeline settings. If not provided, a basic configuration will be used"),
			),
			mcp.WithString("description",
				mcp.Description("The description of the pipeline"),
			),
			mcp.WithString("default_branch",
				mcp.Description("The default branch for builds and metrics filtering"),
			),
			mcp.WithBoolean("skip_queued_branch_builds",
				mcp.Description("Skip intermediate builds when new builds are created on the same branch"),
			),
			mcp.WithBoolean("cancel_running_branch_builds",
				mcp.Description("Cancel running builds when new builds are created on the same branch"),
			),
			mcp.WithArray("tags",
				mcp.Description("Tags to apply to the pipeline. These can be used for filtering and organization"),
				mcp.Items(map[string]any{
					"type":        "string",
					"description": "A tag to apply to the pipeline",
				}),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Create Pipeline",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args CreatePipelineArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreatePipeline")
			defer span.End()

			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug is required"), nil
			}
			if args.Name == "" {
				return mcp.NewToolResultError("name is required"), nil
			}
			if args.RepositoryURL == "" {
				return mcp.NewToolResultError("repository_url is required"), nil
			}
			if args.ClusterID == "" {
				return mcp.NewToolResultError("cluster_id is required"), nil
			}
			if args.Configuration == "" {
				return mcp.NewToolResultError("configuration is required"), nil
			}

			// parse the URL to ensure it's valid
			if _, err := url.Parse(args.RepositoryURL); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid repository URL: %s", err.Error())), nil
			}

			span.SetAttributes(
				attribute.String("name", args.Name),
				attribute.String("repository_url", args.RepositoryURL),
			)

			create := buildkite.CreatePipeline{
				Name:                      args.Name,
				Repository:                args.RepositoryURL,
				ClusterID:                 args.ClusterID,
				Description:               args.Description,
				CancelRunningBranchBuilds: args.CancelRunningBranchBuilds,
				SkipQueuedBranchBuilds:    args.SkipQueuedBranchBuilds,
				Configuration:             args.Configuration,
				Tags:                      args.Tags,
			}

			if args.DefaultBranch != "" {
				create.DefaultBranch = args.DefaultBranch
			}

			pipeline, _, err := client.Create(ctx, args.OrgSlug, create)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			r, err := json.Marshal(&pipeline)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal issue: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

type UpdatePipelineArgs struct {
	OrgSlug                   string
	PipelineSlug              string
	Name                      string
	RepositoryURL             string
	ClusterID                 string
	Description               string
	Configuration             string
	DefaultBranch             string
	SkipQueuedBranchBuilds    bool
	CancelRunningBranchBuilds bool
	Tags                      []string `json:"tags"` // Optional, labels to apply to the pipeline
}

func UpdatePipeline(ctx context.Context, client PipelinesClient) (mcp.Tool, mcp.TypedToolHandlerFunc[UpdatePipelineArgs]) {
	return mcp.NewTool("update_pipeline",
			mcp.WithDescription("Modify an existing Buildkite pipeline's configuration, repository, settings, or metadata"),
			mcp.WithString("org_slug",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline. This is used to determine where to update the pipeline"),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
				mcp.Description("The slug of the pipeline to update"),
			),
			mcp.WithString("name",
				mcp.Description("The name of the pipeline"),
			),
			mcp.WithString("repository_url",
				mcp.Description("The Git repository URL to use for the pipeline"),
			),
			mcp.WithString("cluster_id",
				mcp.Description("The ID value of the cluster the pipeline will be associated with"),
			),
			mcp.WithString("configuration",
				mcp.Description("The pipeline configuration in YAML format. Contains the build steps and pipeline settings. If not provided, the existing configuration will be used"),
			),
			mcp.WithString("description",
				mcp.Description("The description of the pipeline"),
			),
			mcp.WithString("default_branch",
				mcp.Description("The default branch for builds and metrics filtering"),
			),
			mcp.WithBoolean("skip_queued_branch_builds",
				mcp.Description("Skip intermediate builds when new builds are created on the same branch"),
			),
			mcp.WithBoolean("cancel_running_branch_builds",
				mcp.Description("Cancel running builds when new builds are created on the same branch"),
			),
			mcp.WithArray("tags",
				mcp.Description("Tags to apply to the pipeline. These can be used for filtering and organization"),
				mcp.Items(map[string]any{
					"type":        "string",
					"description": "A tag to apply to the pipeline",
				}),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Update Pipeline",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest, args UpdatePipelineArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.UpdatePipeline")
			defer span.End()

			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug is required"), nil
			}
			if args.RepositoryURL == "" {
				return mcp.NewToolResultError("repository_url is required"), nil
			}

			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug is required"), nil
			}
			if args.Configuration == "" {
				return mcp.NewToolResultError("configuration is required"), nil
			}

			// parse the URL to ensure it's valid
			if _, err := url.Parse(args.RepositoryURL); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid repository URL: %s", err.Error())), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("repository_url", args.RepositoryURL),
			)

			update := buildkite.UpdatePipeline{
				Name:                      args.Name,
				Repository:                args.RepositoryURL,
				ClusterID:                 args.ClusterID,
				Description:               args.Description,
				CancelRunningBranchBuilds: args.CancelRunningBranchBuilds,
				SkipQueuedBranchBuilds:    args.SkipQueuedBranchBuilds,
				Configuration:             args.Configuration,
				Tags:                      args.Tags,
			}
			if args.DefaultBranch != "" {
				update.DefaultBranch = args.DefaultBranch
			}

			pipeline, _, err := client.Update(ctx, args.OrgSlug, args.PipelineSlug, update)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			r, err := json.Marshal(&pipeline)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal pipeline: %w", err)
			}
			return mcp.NewToolResultText(string(r)), nil
		}
}
