package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type PipelinesClient interface {
	Get(ctx context.Context, org, pipelineSlug string) (buildkite.Pipeline, *buildkite.Response, error)
	List(ctx context.Context, org string, options *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error)
	Create(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	Update(ctx context.Context, org, pipelineSlug string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
}

type ListPipelinesArgs struct {
	Org         string `json:"org"`
	Name        string `json:"name"`
	Repository  string `json:"repository"`
	Page        int    `json:"page"`
	PerPage     int    `json:"per_page"`
	DetailLevel string `json:"detail_level"` // "summary", "detailed", "full"
}

func ListPipelines(client PipelinesClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[ListPipelinesArgs]) {
	return mcp.NewTool("list_pipelines",
			mcp.WithDescription("List all pipelines in an organization with their basic details, build counts, and current status"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("name",
				mcp.Description("Filter pipelines by name"),
			),
			mcp.WithString("repository",
				mcp.Description("Filter pipelines by repository URL"),
			),
			mcp.WithString("detail_level",
				mcp.Description("Response detail level: 'summary' (default), 'detailed', or 'full'"),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Pipelines",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest, args ListPipelinesArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListPipelines")
			defer span.End()

			if args.Org == "" {
				return mcp.NewToolResultError("org is required"), nil
			}

			// Set defaults
			if args.DetailLevel == "" {
				args.DetailLevel = "summary"
			}
			if args.Page == 0 {
				args.Page = 1
			}
			if args.PerPage == 0 {
				args.PerPage = 30
			}

			span.SetAttributes(
				attribute.String("org", args.Org),
				attribute.String("name_filter", args.Name),
				attribute.String("repository_filter", args.Repository),
				attribute.String("detail_level", args.DetailLevel),
				attribute.Int("page", args.Page),
				attribute.Int("per_page", args.PerPage),
			)

			pipelines, resp, err := client.List(ctx, args.Org, &buildkite.PipelineListOptions{
				ListOptions: buildkite.ListOptions{
					Page:    args.Page,
					PerPage: args.PerPage,
				},
				Name:       args.Name,
				Repository: args.Repository,
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

			headers := map[string]string{"Link": resp.Header.Get("Link")}

			var result interface{}
			switch args.DetailLevel {
			case "summary":
				result = createPaginatedResult(pipelines, summarizePipeline, headers)
			case "detailed":
				result = createPaginatedResult(pipelines, detailPipeline, headers)
			default: // "full"
				result = createPaginatedResult(pipelines, func(p buildkite.Pipeline) buildkite.Pipeline { return p }, headers)
			}

			r, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal pipelines: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

type GetPipelineArgs struct {
	Org          string `json:"org"`
	PipelineSlug string `json:"pipeline_slug"`
	DetailLevel  string `json:"detail_level"` // "summary", "detailed", "full"
}

func GetPipeline(client PipelinesClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[GetPipelineArgs]) {
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
			mcp.WithString("detail_level",
				mcp.Description("Response detail level: 'summary', 'detailed', or 'full' (default)"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Pipeline",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args GetPipelineArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetPipeline")
			defer span.End()

			if args.Org == "" {
				return mcp.NewToolResultError("org is required"), nil
			}
			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug is required"), nil
			}

			// Set default
			if args.DetailLevel == "" {
				args.DetailLevel = "full"
			}

			span.SetAttributes(
				attribute.String("org", args.Org),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("detail_level", args.DetailLevel),
			)

			pipeline, _, err := client.Get(ctx, args.Org, args.PipelineSlug)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			var result any
			switch args.DetailLevel {
			case "summary":
				result = summarizePipeline(pipeline)
			case "detailed":
				result = detailPipeline(pipeline)
			default: // "full"
				result = pipeline
			}

			r, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal pipeline: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// PipelineSummary contains essential pipeline fields for token-efficient responses
type PipelineSummary struct {
	ID            string               `json:"id"`
	Name          string               `json:"name"`
	Slug          string               `json:"slug"`
	Repository    string               `json:"repository"`
	DefaultBranch string               `json:"default_branch"`
	WebURL        string               `json:"web_url"`
	Visibility    string               `json:"visibility"`
	CreatedAt     *buildkite.Timestamp `json:"created_at"`
	ArchivedAt    *buildkite.Timestamp `json:"archived_at,omitempty"`
}

// PipelineDetail contains pipeline fields excluding heavy configuration data
type PipelineDetail struct {
	ID                        string               `json:"id"`
	Name                      string               `json:"name"`
	Slug                      string               `json:"slug"`
	Repository                string               `json:"repository"`
	WebURL                    string               `json:"web_url"`
	DefaultBranch             string               `json:"default_branch"`
	Description               string               `json:"description"`
	ClusterID                 string               `json:"cluster_id"`
	Visibility                string               `json:"visibility"`
	Tags                      []string             `json:"tags"`
	SkipQueuedBranchBuilds    bool                 `json:"skip_queued_branch_builds"`
	CancelRunningBranchBuilds bool                 `json:"cancel_running_branch_builds"`
	StepsCount                int                  `json:"steps_count"`
	CreatedAt                 *buildkite.Timestamp `json:"created_at"`
	ArchivedAt                *buildkite.Timestamp `json:"archived_at,omitempty"`
}

// summarizePipeline converts a full Pipeline to PipelineSummary
func summarizePipeline(p buildkite.Pipeline) PipelineSummary {
	return PipelineSummary{
		ID:            p.ID,
		Name:          p.Name,
		Slug:          p.Slug,
		Repository:    p.Repository,
		DefaultBranch: p.DefaultBranch,
		WebURL:        p.WebURL,
		Visibility:    p.Visibility,
		CreatedAt:     p.CreatedAt,
		ArchivedAt:    p.ArchivedAt,
	}
}

// detailPipeline converts a full Pipeline to PipelineDetail
func detailPipeline(p buildkite.Pipeline) PipelineDetail {
	stepsCount := 0
	if p.Steps != nil {
		stepsCount = len(p.Steps)
	}

	return PipelineDetail{
		ID:                        p.ID,
		Name:                      p.Name,
		Slug:                      p.Slug,
		Repository:                p.Repository,
		WebURL:                    p.WebURL,
		DefaultBranch:             p.DefaultBranch,
		Description:               p.Description,
		ClusterID:                 p.ClusterID,
		Visibility:                p.Visibility,
		Tags:                      p.Tags,
		SkipQueuedBranchBuilds:    p.SkipQueuedBranchBuilds,
		CancelRunningBranchBuilds: p.CancelRunningBranchBuilds,
		StepsCount:                stepsCount,
		CreatedAt:                 p.CreatedAt,
		ArchivedAt:                p.ArchivedAt,
	}
}

// createPaginatedResult is a generic helper to convert pipelines and wrap in paginated result
func createPaginatedResult[T any](pipelines []buildkite.Pipeline, converter func(buildkite.Pipeline) T, headers map[string]string) PaginatedResult[T] {
	items := make([]T, len(pipelines))
	for i, p := range pipelines {
		items[i] = converter(p)
	}
	return PaginatedResult[T]{
		Items:   items,
		Headers: headers,
	}
}

type CreatePipelineArgs struct {
	OrgSlug                   string   `json:"org_slug"`
	Name                      string   `json:"name"`
	RepositoryURL             string   `json:"repository_url"`
	ClusterID                 string   `json:"cluster_id"`
	Description               string   `json:"description"`
	Configuration             string   `json:"configuration"`
	DefaultBranch             string   `json:"default_branch"`
	SkipQueuedBranchBuilds    bool     `json:"skip_queued_branch_builds"`
	CancelRunningBranchBuilds bool     `json:"cancel_running_branch_builds"`
	Tags                      []string `json:"tags"`
}

func CreatePipeline(client PipelinesClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[CreatePipelineArgs]) {
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
	OrgSlug                   string   `json:"org_slug"`
	PipelineSlug              string   `json:"pipeline_slug"`
	Name                      string   `json:"name"`
	RepositoryURL             string   `json:"repository_url"`
	ClusterID                 string   `json:"cluster_id"`
	Description               string   `json:"description"`
	Configuration             string   `json:"configuration"`
	DefaultBranch             string   `json:"default_branch"`
	SkipQueuedBranchBuilds    bool     `json:"skip_queued_branch_builds"`
	CancelRunningBranchBuilds bool     `json:"cancel_running_branch_builds"`
	Tags                      []string `json:"tags"` // Optional, labels to apply to the pipeline
}

func UpdatePipeline(client PipelinesClient) (mcp.Tool, mcp.TypedToolHandlerFunc[UpdatePipelineArgs]) {
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
