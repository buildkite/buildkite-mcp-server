package buildkite

import (
	"context"
	"errors"
	"fmt"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type PipelinesClient interface {
	Get(ctx context.Context, org, pipelineSlug string) (buildkite.Pipeline, *buildkite.Response, error)
	List(ctx context.Context, org string, options *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error)
	Create(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	Update(ctx context.Context, org, pipelineSlug string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	AddWebhook(ctx context.Context, org, slug string) (*buildkite.Response, error)
}

type ListPipelinesArgs struct {
	OrgSlug     string `json:"org_slug"`
	Name        string `json:"name,omitempty" jsonschema:"Filter pipelines by name"`
	Repository  string `json:"repository,omitempty" jsonschema:"Filter pipelines by repository URL"`
	Page        int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage     int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
	DetailLevel string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary' (default)\\, 'detailed'\\, or 'full'"`
}

type CreatePipelineResult struct {
	Pipeline buildkite.Pipeline `json:"pipeline"`
	Webhook  *WebhookInfo       `json:"webhook,omitempty"`
}

type WebhookInfo struct {
	Created bool   `json:"created"`
	Error   string `json:"error,omitempty"`
	Note    string `json:"note,omitempty"`
}

func ListPipelines() (mcp.Tool, mcp.ToolHandlerFor[ListPipelinesArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_pipelines",
			Description: "List all pipelines in an organization with their basic details, build counts, and current status",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Pipelines",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListPipelinesArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListPipelines")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
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
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("name_filter", args.Name),
				attribute.String("repository_filter", args.Repository),
				attribute.String("detail_level", args.DetailLevel),
				attribute.Int("page", args.Page),
				attribute.Int("per_page", args.PerPage),
			)

			deps := DepsFromContext(ctx)
			pipelines, resp, err := deps.PipelinesClient.List(ctx, args.OrgSlug, &buildkite.PipelineListOptions{
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
						return utils.NewToolResultError(string(errResp.RawBody)), nil, nil
					}
				}

				return utils.NewToolResultError(err.Error()), nil, nil
			}

			headers := map[string]string{"Link": resp.Header.Get("Link")}

			var result any
			switch args.DetailLevel {
			case "summary":
				result = createPaginatedResult(pipelines, summarizePipeline, headers)
			case "detailed":
				result = createPaginatedResult(pipelines, detailPipeline, headers)
			default: // "full"
				result = createPaginatedResult(pipelines, func(p buildkite.Pipeline) buildkite.Pipeline { return p }, headers)
			}

			span.SetAttributes(
				attribute.Int("item_count", len(pipelines)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_pipelines"}
}

type GetPipelineArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	DetailLevel  string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary'\\, 'detailed'\\, or 'full' (default)"`
}

func GetPipeline() (mcp.Tool, mcp.ToolHandlerFor[GetPipelineArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_pipeline",
			Description: "Get detailed information about a specific pipeline including its configuration, steps, environment variables, and build statistics",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Pipeline",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetPipelineArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetPipeline")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}
			if args.PipelineSlug == "" {
				return utils.NewToolResultError("pipeline_slug is required"), nil, nil
			}

			// Set default
			if args.DetailLevel == "" {
				args.DetailLevel = "full"
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("detail_level", args.DetailLevel),
			)

			deps := DepsFromContext(ctx)
			pipeline, _, err := deps.PipelinesClient.Get(ctx, args.OrgSlug, args.PipelineSlug)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return utils.NewToolResultError(string(errResp.RawBody)), nil, nil
					}
				}

				return utils.NewToolResultError(err.Error()), nil, nil
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

			return mcpTextResult(span, &result)
		}, []string{"read_pipelines"}
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
	RepositoryURL             string   `json:"repository_url" jsonschema:"The Git repository URL"`
	ClusterID                 string   `json:"cluster_id" jsonschema:"The cluster ID to assign the pipeline to"`
	Description               string   `json:"description,omitempty"`
	Configuration             string   `json:"configuration" jsonschema:"The pipeline configuration in YAML format"`
	DefaultBranch             string   `json:"default_branch,omitempty" jsonschema:"The default branch for builds and metrics filtering"`
	SkipQueuedBranchBuilds    bool     `json:"skip_queued_branch_builds,omitempty" jsonschema:"Skip intermediate builds when new builds are created on the same branch"`
	CancelRunningBranchBuilds bool     `json:"cancel_running_branch_builds,omitempty" jsonschema:"Cancel running builds when new builds are created on the same branch"`
	Tags                      []string `json:"tags,omitempty" jsonschema:"Tags to apply to the pipeline for filtering and organization"`
	CreateWebhook             bool     `json:"create_webhook,omitempty" jsonschema:"Create a GitHub webhook to trigger builds on pull-request and push events"`
}

func CreatePipeline() (mcp.Tool, mcp.ToolHandlerFor[CreatePipelineArgs, any], []string) {
	return mcp.Tool{
			Name:        "create_pipeline",
			Description: "Set up a new CI/CD pipeline in Buildkite with YAML configuration, repository connection, and cluster assignment",
			Annotations: &mcp.ToolAnnotations{
				Title: "Create Pipeline",
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args CreatePipelineArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreatePipeline")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}
			if args.Name == "" {
				return utils.NewToolResultError("name is required"), nil, nil
			}
			if args.RepositoryURL == "" {
				return utils.NewToolResultError("repository_url is required"), nil, nil
			}
			if args.ClusterID == "" {
				return utils.NewToolResultError("cluster_id is required"), nil, nil
			}
			if args.Configuration == "" {
				return utils.NewToolResultError("configuration is required"), nil, nil
			}

			// parse the URL to ensure it's valid
			if _, err := url.Parse(args.RepositoryURL); err != nil {
				return utils.NewToolResultError(fmt.Sprintf("invalid repository URL: %s", err.Error())), nil, nil
			}

			span.SetAttributes(
				attribute.String("name", args.Name),
				attribute.String("repository_url", args.RepositoryURL),
				attribute.Bool("create_webhook", args.CreateWebhook),
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

			deps := DepsFromContext(ctx)
			pipeline, _, err := deps.PipelinesClient.Create(ctx, args.OrgSlug, create)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return utils.NewToolResultError(string(errResp.RawBody)), nil, nil
					}
				}

				return utils.NewToolResultError(err.Error()), nil, nil
			}

			if args.CreateWebhook {
				_, err := deps.PipelinesClient.AddWebhook(ctx, args.OrgSlug, pipeline.Slug)
				result := CreatePipelineResult{
					Pipeline: pipeline,
					Webhook: &WebhookInfo{
						Created: err == nil,
						Note:    "Pipeline and webhook created successfully.",
					},
				}

				if err != nil {
					result.Webhook.Error = err.Error()
					result.Webhook.Note = "Pipeline created successfully, but webhook creation failed."
				}

				return mcpTextResult(span, &result)
			}

			result := CreatePipelineResult{
				Pipeline: pipeline,
			}
			return mcpTextResult(span, &result)
		}, []string{"write_pipelines"}
}

type UpdatePipelineArgs struct {
	OrgSlug                   string   `json:"org_slug"`
	PipelineSlug              string   `json:"pipeline_slug"`
	Name                      string   `json:"name,omitempty"`
	RepositoryURL             string   `json:"repository_url" jsonschema:"The Git repository URL"`
	ClusterID                 string   `json:"cluster_id,omitempty"`
	Description               string   `json:"description,omitempty"`
	Configuration             string   `json:"configuration" jsonschema:"The pipeline configuration in YAML format"`
	DefaultBranch             string   `json:"default_branch,omitempty" jsonschema:"The default branch for builds and metrics filtering"`
	SkipQueuedBranchBuilds    bool     `json:"skip_queued_branch_builds,omitempty" jsonschema:"Skip intermediate builds when new builds are created on the same branch"`
	CancelRunningBranchBuilds bool     `json:"cancel_running_branch_builds,omitempty" jsonschema:"Cancel running builds when new builds are created on the same branch"`
	Tags                      []string `json:"tags,omitempty" jsonschema:"Tags to apply to the pipeline for filtering and organization"`
}

func UpdatePipeline() (mcp.Tool, mcp.ToolHandlerFor[UpdatePipelineArgs, any], []string) {
	return mcp.Tool{
			Name:        "update_pipeline",
			Description: "Modify an existing Buildkite pipeline's configuration, repository, settings, or metadata",
			Annotations: &mcp.ToolAnnotations{
				Title: "Update Pipeline",
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdatePipelineArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.UpdatePipeline")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}
			if args.RepositoryURL == "" {
				return utils.NewToolResultError("repository_url is required"), nil, nil
			}

			if args.PipelineSlug == "" {
				return utils.NewToolResultError("pipeline_slug is required"), nil, nil
			}
			if args.Configuration == "" {
				return utils.NewToolResultError("configuration is required"), nil, nil
			}

			// parse the URL to ensure it's valid
			if _, err := url.Parse(args.RepositoryURL); err != nil {
				return utils.NewToolResultError(fmt.Sprintf("invalid repository URL: %s", err.Error())), nil, nil
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

			deps := DepsFromContext(ctx)
			pipeline, _, err := deps.PipelinesClient.Update(ctx, args.OrgSlug, args.PipelineSlug, update)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return utils.NewToolResultError(string(errResp.RawBody)), nil, nil
					}
				}

				return utils.NewToolResultError(err.Error()), nil, nil
			}

			return mcpTextResult(span, &pipeline)
		}, []string{"write_pipelines"}
}
