package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type PipelinesClient interface {
	Get(ctx context.Context, org, pipelineSlug string) (buildkite.Pipeline, *buildkite.Response, error)
	List(ctx context.Context, org string, options *buildkite.PipelineListOptions) ([]buildkite.Pipeline, *buildkite.Response, error)
	Create(ctx context.Context, org string, p buildkite.CreatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	Update(ctx context.Context, org, pipelineSlug string, p buildkite.UpdatePipeline) (buildkite.Pipeline, *buildkite.Response, error)
	AddWebhook(ctx context.Context, org, slug string) (*buildkite.Response, error)
	GetGitHubWebhookProcessing(ctx context.Context, org, slug string) (buildkite.GitHubWebhookProcessing, *buildkite.Response, error)
	EnableGitHubWebhookProcessing(ctx context.Context, org, slug string) (buildkite.GitHubWebhookProcessing, *buildkite.Response, error)
	DisableGitHubWebhookProcessing(ctx context.Context, org, slug string) (buildkite.GitHubWebhookProcessing, *buildkite.Response, error)
}

type ListPipelinesArgs struct {
	ToolInput
	OrgSlug     string `json:"org_slug"`
	Name        string `json:"name,omitempty" jsonschema:"Filter pipelines by name"`
	Repository  string `json:"repository,omitempty" jsonschema:"Filter pipelines by repository URL"`
	Page        int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage     int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
	DetailLevel string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary' (default), 'detailed', or 'full'"`
}

type CreatePipelineResult struct {
	Pipeline buildkite.Pipeline `json:"pipeline"`
	Webhook  *WebhookInfo       `json:"webhook,omitempty"`
}

type WebhookInfo struct {
	Created   bool     `json:"created"`
	Error     string   `json:"error,omitempty"`
	Note      string   `json:"note,omitempty"`
	SetupURL  string   `json:"setup_url,omitempty"`
	NextSteps []string `json:"next_steps,omitempty"`
}

var buildkiteSlugPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,99}$`)

func validateBuildkiteSlug(name, slug string) error {
	if !buildkiteSlugPattern.MatchString(slug) {
		return fmt.Errorf("%s must be 1-100 characters, start with a letter or number, and contain only letters, numbers, or hyphens", name)
	}
	return nil
}

func webhookErrorCode(err error) (string, bool) {
	var errResp *buildkite.ErrorResponse
	if !errors.As(err, &errResp) || errResp.Response == nil || errResp.Response.StatusCode != http.StatusUnprocessableEntity {
		return "", false
	}

	var webhookError struct {
		Code string `json:"code"`
	}
	if json.Unmarshal(errResp.RawBody, &webhookError) != nil || webhookError.Code == "" {
		return "", false
	}

	return webhookError.Code, true
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
			return handleBuildkiteError(err)
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
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	DetailLevel  string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary', 'detailed', or 'full' (default)"`
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
				return handleBuildkiteError(err)
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
	ToolInput
	OrgSlug                   string                        `json:"org_slug"`
	Name                      string                        `json:"name"`
	RepositoryURL             string                        `json:"repository_url" jsonschema:"The Git repository URL"`
	ClusterID                 string                        `json:"cluster_id" jsonschema:"The cluster ID to assign the pipeline to"`
	Description               string                        `json:"description,omitempty"`
	Configuration             string                        `json:"configuration,omitempty" jsonschema:"Nonblank YAML configuration; supply this or pipeline_template_uuid, not both"`
	PipelineTemplateUUID      string                        `json:"pipeline_template_uuid,omitempty" jsonschema:"Approved template UUID providing the pipeline's steps; supply this or configuration, not both"`
	DefaultBranch             string                        `json:"default_branch,omitempty" jsonschema:"The default branch for builds and metrics filtering"`
	SkipQueuedBranchBuilds    bool                          `json:"skip_queued_branch_builds,omitempty" jsonschema:"Skip intermediate builds when new builds are created on the same branch"`
	CancelRunningBranchBuilds bool                          `json:"cancel_running_branch_builds,omitempty" jsonschema:"Cancel running builds when new builds are created on the same branch"`
	Tags                      []string                      `json:"tags,omitempty" jsonschema:"Tags to apply to the pipeline for filtering and organization"`
	Teams                     map[string]string             `json:"teams,omitempty" jsonschema:"Team UUIDs mapped to their access level on the pipeline: read_only, build_and_read or manage_build_and_read. Organizations with teams enabled require at least one team, unless the user is an organization administrator"`
	BranchConfiguration       *string                       `json:"branch_configuration,omitempty" jsonschema:"Branch filter; omission or null uses the API's null default"`
	ProviderSettings          *PipelineProviderSettingsArgs `json:"provider_settings,omitempty" jsonschema:"GitHub provider settings; omitted keys use API defaults. Does not create a GitHub Actions trigger or webhook"`
	CreateWebhook             bool                          `json:"create_webhook" jsonschema:"Create a traditional GitHub webhook after creating a pipeline. Set true when GitHub push or pull-request events should trigger this pipeline; if the repository is not connected through a compatible Buildkite GitHub App, the pipeline is created and setup instructions are returned. Set false for GitHub Actions pipeline triggers, non-GitHub repositories, centralized or manually managed webhooks, or pipelines that must not receive events yet."`
}

func CreatePipeline() (mcp.Tool, mcp.ToolHandlerFor[CreatePipelineArgs, any], []string) {
	return mcp.Tool{
			Name:        "create_pipeline",
			Description: "Set up a new CI/CD pipeline in Buildkite with YAML configuration or an approved pipeline template, repository connection, and cluster assignment",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Create Pipeline",
				DestructiveHint: boolPtr(false),
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args CreatePipelineArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreatePipeline")
			defer span.End()

			span.SetAttributes(
				attribute.String("name", args.Name),
				attribute.String("repository_url", args.RepositoryURL),
				attribute.Bool("create_webhook", args.CreateWebhook),
			)

			raw, err := toolRawFields(request)
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			_, configurationPresent := raw["configuration"]
			_, templatePresent := raw["pipeline_template_uuid"]
			if (configurationPresent || args.Configuration != "") && (templatePresent || args.PipelineTemplateUUID != "") {
				return utils.NewToolResultError("supply configuration or pipeline_template_uuid, not both"), nil, nil
			}
			if strings.TrimSpace(args.Configuration) == "" && strings.TrimSpace(args.PipelineTemplateUUID) == "" {
				return utils.NewToolResultError("supply nonblank configuration or an approved pipeline_template_uuid"), nil, nil
			}
			provider, err := pipelineProviderSettings(args.ProviderSettings, raw["provider_settings"])
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}
			create := buildkite.CreatePipeline{
				Name:                      args.Name,
				Repository:                args.RepositoryURL,
				ClusterID:                 args.ClusterID,
				Description:               args.Description,
				CancelRunningBranchBuilds: args.CancelRunningBranchBuilds,
				SkipQueuedBranchBuilds:    args.SkipQueuedBranchBuilds,
				Configuration:             args.Configuration,
				Tags:                      args.Tags,
				Teams:                     args.Teams,
				ProviderSettings:          provider,
				PipelineTemplateUUID:      args.PipelineTemplateUUID,
			}

			if args.BranchConfiguration != nil {
				create.BranchConfiguration = *args.BranchConfiguration
			}
			if args.DefaultBranch != "" {
				create.DefaultBranch = args.DefaultBranch
			}

			deps := DepsFromContext(ctx)
			pipeline, _, err := deps.PipelinesClient.Create(ctx, args.OrgSlug, create)
			if err != nil {
				return handleBuildkiteError(err)
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

					if code, ok := webhookErrorCode(err); ok {
						switch code {
						case "github_app_required":
							result.Webhook.Note = "Pipeline created successfully, but its GitHub webhook could not be created automatically. Do not recreate the pipeline."
							if pipelineURL, parseErr := url.Parse(pipeline.WebURL); parseErr == nil && pipelineURL.IsAbs() && pipelineURL.Host != "" {
								result.Webhook.SetupURL = fmt.Sprintf("%s://%s/organizations/%s/repository-providers", pipelineURL.Scheme, pipelineURL.Host, url.PathEscape(args.OrgSlug))
								result.Webhook.NextSteps = []string{
									"Open setup_url and connect a compatible Buildkite GitHub App, or grant the existing app access to this repository.",
									fmt.Sprintf("Open the existing pipeline's repository settings at %s.", pipelineURL.JoinPath("settings", "repository")),
									"Follow the webhook setup instructions shown there to finish connecting the existing pipeline.",
									"Do not create another pipeline; the pipeline in this result was created successfully.",
								}
							}
						case "unsupported_repository_provider":
							result.Webhook.Note = "Pipeline created successfully, but automatic webhook creation is unsupported for this repository provider. Do not recreate the pipeline."
						}
					}
				}

				return mcpTextResult(span, &result)
			}

			result := CreatePipelineResult{
				Pipeline: pipeline,
			}
			return mcpTextResult(span, &result)
		}, []string{"write_pipelines"}
}

type CreatePipelineWebhookArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
}

func CreatePipelineWebhook() (mcp.Tool, mcp.ToolHandlerFor[CreatePipelineWebhookArgs, any], []string) {
	return mcp.Tool{
		Name:        "create_pipeline_webhook",
		Description: "Create a GitHub webhook for an existing pipeline configured with a compatible Buildkite GitHub App",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Create Pipeline Webhook",
			DestructiveHint: boolPtr(false),
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args CreatePipelineWebhookArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.CreatePipelineWebhook")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
		)

		if err := validateBuildkiteSlug("org_slug", args.OrgSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		if err := validateBuildkiteSlug("pipeline_slug", args.PipelineSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}

		deps := DepsFromContext(ctx)
		_, err := deps.PipelinesClient.AddWebhook(ctx, args.OrgSlug, args.PipelineSlug)
		if err == nil {
			result := WebhookInfo{
				Created: true,
				Note:    "Webhook created successfully.",
			}
			return mcpTextResult(span, &result)
		}

		code, ok := webhookErrorCode(err)
		if !ok {
			return handleBuildkiteError(err)
		}

		result := WebhookInfo{
			Created: false,
			Error:   err.Error(),
		}
		switch code {
		case "github_app_required":
			result.Note = "The webhook could not be created automatically because the pipeline is not connected through a compatible Buildkite GitHub App."
			result.SetupURL = fmt.Sprintf("https://buildkite.com/organizations/%s/repository-providers", args.OrgSlug)
			result.NextSteps = []string{
				"Open setup_url and connect a compatible Buildkite GitHub App, or grant the existing app access to this repository.",
				fmt.Sprintf("Open https://buildkite.com/%s/%s, then open Settings > GitHub > Setup Instructions.", args.OrgSlug, args.PipelineSlug),
			}
		case "unsupported_repository_provider":
			result.Note = "The webhook could not be created automatically because automatic webhook creation is unsupported for this repository provider."
		default:
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &result)
	}, []string{"write_pipelines"}
}

type PipelineGitHubWebhookProcessingArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
}

func GetPipelineGitHubWebhookProcessing() (mcp.Tool, mcp.ToolHandlerFor[PipelineGitHubWebhookProcessingArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_pipeline_github_webhook_processing",
		Description: "Get traditional GitHub webhook processing state. Requires Full Access to a GitHub or GitHub Enterprise pipeline and expanded-webhook enrollment; unavailable controls return 404. Does not describe dedicated GitHub Actions triggers",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Pipeline GitHub Webhook Processing",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args PipelineGitHubWebhookProcessingArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetPipelineGitHubWebhookProcessing")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("pipeline_slug", args.PipelineSlug))

		if err := validateBuildkiteSlug("org_slug", args.OrgSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		if err := validateBuildkiteSlug("pipeline_slug", args.PipelineSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		state, _, err := DepsFromContext(ctx).PipelinesClient.GetGitHubWebhookProcessing(ctx, args.OrgSlug, args.PipelineSlug)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &state)
	}, []string{"read_pipelines"}
}

func EnablePipelineGitHubWebhookProcessing() (mcp.Tool, mcp.ToolHandlerFor[PipelineGitHubWebhookProcessingArgs, any], []string) {
	return mcp.Tool{
		Name:        "enable_pipeline_github_webhook_processing",
		Description: "Enable traditional GitHub webhook processing and return its state. Requires Full Access to a GitHub or GitHub Enterprise pipeline and expanded-webhook enrollment; unavailable controls return 404. Does not create repository webhooks or change dedicated GitHub Actions triggers",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Enable Pipeline GitHub Webhook Processing",
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args PipelineGitHubWebhookProcessingArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.EnablePipelineGitHubWebhookProcessing")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("pipeline_slug", args.PipelineSlug))

		if err := validateBuildkiteSlug("org_slug", args.OrgSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		if err := validateBuildkiteSlug("pipeline_slug", args.PipelineSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		state, _, err := DepsFromContext(ctx).PipelinesClient.EnableGitHubWebhookProcessing(ctx, args.OrgSlug, args.PipelineSlug)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &state)
	}, []string{"write_pipelines"}
}

func DisablePipelineGitHubWebhookProcessing() (mcp.Tool, mcp.ToolHandlerFor[PipelineGitHubWebhookProcessingArgs, any], []string) {
	return mcp.Tool{
		Name:        "disable_pipeline_github_webhook_processing",
		Description: "Disable traditional GitHub webhook processing and return its state to avoid duplicate builds during cutover. Requires Full Access to a GitHub or GitHub Enterprise pipeline and expanded-webhook enrollment; unavailable controls return 404. Does not delete repository webhooks or disable dedicated GitHub Actions triggers",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Disable Pipeline GitHub Webhook Processing",
			DestructiveHint: boolPtr(true),
			IdempotentHint:  true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args PipelineGitHubWebhookProcessingArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.DisablePipelineGitHubWebhookProcessing")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("pipeline_slug", args.PipelineSlug))

		if err := validateBuildkiteSlug("org_slug", args.OrgSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		if err := validateBuildkiteSlug("pipeline_slug", args.PipelineSlug); err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		state, _, err := DepsFromContext(ctx).PipelinesClient.DisableGitHubWebhookProcessing(ctx, args.OrgSlug, args.PipelineSlug)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &state)
	}, []string{"write_pipelines"}
}

type UpdatePipelineArgs struct {
	ToolInput
	OrgSlug                   string                        `json:"org_slug"`
	PipelineSlug              string                        `json:"pipeline_slug"`
	Name                      *string                       `json:"name,omitempty"`
	RepositoryURL             *string                       `json:"repository_url,omitempty" jsonschema:"The Git repository URL"`
	ClusterID                 *string                       `json:"cluster_id,omitempty"`
	Description               *string                       `json:"description,omitempty"`
	Configuration             *string                       `json:"configuration,omitempty" jsonschema:"Replaces the entire YAML configuration; omit when assigning a template. May accompany pipeline_template_uuid: null to replace template steps"`
	PipelineTemplateUUID      *string                       `json:"pipeline_template_uuid,omitempty" jsonschema:"Approved template UUID; omit to retain assignment, null to remove it. Do not combine assignment with configuration; removal may require replacement configuration and remains subject to organization policy"`
	DefaultBranch             *string                       `json:"default_branch,omitempty" jsonschema:"The default branch for builds and metrics filtering"`
	SkipQueuedBranchBuilds    *bool                         `json:"skip_queued_branch_builds,omitempty" jsonschema:"Skip intermediate builds when new builds are created on the same branch"`
	CancelRunningBranchBuilds *bool                         `json:"cancel_running_branch_builds,omitempty" jsonschema:"Cancel running builds when new builds are created on the same branch"`
	Tags                      []string                      `json:"tags,omitempty" jsonschema:"Tags to apply to the pipeline for filtering and organization"`
	BranchConfiguration       *string                       `json:"branch_configuration,omitempty" jsonschema:"Branch filter; null clears it, omission leaves it unchanged"`
	ProviderSettings          *PipelineProviderSettingsArgs `json:"provider_settings,omitempty" jsonschema:"GitHub provider settings; omitted keys remain unchanged. Does not create a GitHub Actions trigger or webhook"`
}

func UpdatePipeline() (mcp.Tool, mcp.ToolHandlerFor[UpdatePipelineArgs, any], []string) {
	return mcp.Tool{
		Name:        "update_pipeline",
		Description: "Modify an existing Buildkite pipeline's configuration, repository, settings, or metadata",
		Annotations: &mcp.ToolAnnotations{
			Title:           "Update Pipeline",
			DestructiveHint: boolPtr(true),
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args UpdatePipelineArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.UpdatePipeline")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
		)

		raw, err := toolRawFields(request)
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		if args.PipelineTemplateUUID != nil {
			if strings.TrimSpace(*args.PipelineTemplateUUID) == "" {
				return utils.NewToolResultError("pipeline_template_uuid must be nonblank; use null to remove the template"), nil, nil
			}
			_, configurationPresent := raw["configuration"]
			if args.Configuration != nil || configurationPresent {
				return utils.NewToolResultError("do not combine pipeline_template_uuid assignment with configuration"), nil, nil
			}
		}
		provider, err := pipelineProviderSettings(args.ProviderSettings, raw["provider_settings"])
		if err != nil {
			return utils.NewToolResultError(err.Error()), nil, nil
		}
		update := buildkite.UpdatePipeline{}
		if args.PipelineTemplateUUID != nil {
			update.PipelineTemplateUUID = buildkite.Some(*args.PipelineTemplateUUID)
		} else if isJSONNull(raw["pipeline_template_uuid"]) {
			update.PipelineTemplateUUID = buildkite.Null[string]()
		}
		if provider != nil {
			update.ProviderSettings = buildkite.Some(provider)
		}
		if args.BranchConfiguration != nil {
			update.BranchConfiguration = buildkite.Some(*args.BranchConfiguration)
		} else if isJSONNull(raw["branch_configuration"]) {
			update.BranchConfiguration = buildkite.Null[string]()
		}
		if args.Name != nil {
			update.Name = buildkite.Some(*args.Name)
		}
		if args.RepositoryURL != nil {
			span.SetAttributes(attribute.String("repository_url", *args.RepositoryURL))
			update.Repository = buildkite.Some(*args.RepositoryURL)
		}
		if args.ClusterID != nil {
			update.ClusterID = buildkite.Some(*args.ClusterID)
		}
		if args.Description != nil {
			update.Description = buildkite.Some(*args.Description)
		}
		if args.Configuration != nil {
			update.Configuration = buildkite.Some(*args.Configuration)
		}
		if args.DefaultBranch != nil {
			update.DefaultBranch = buildkite.Some(*args.DefaultBranch)
		}
		if args.SkipQueuedBranchBuilds != nil {
			update.SkipQueuedBranchBuilds = buildkite.Some(*args.SkipQueuedBranchBuilds)
		}
		if args.CancelRunningBranchBuilds != nil {
			update.CancelRunningBranchBuilds = buildkite.Some(*args.CancelRunningBranchBuilds)
		}
		if args.Tags != nil {
			update.Tags = buildkite.Some(args.Tags)
		}

		deps := DepsFromContext(ctx)
		pipeline, _, err := deps.PipelinesClient.Update(ctx, args.OrgSlug, args.PipelineSlug, update)
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, &pipeline)
	}, []string{"write_pipelines"}
}

type PipelineProviderSettingsArgs struct {
	GitHubActionsMode                       *bool   `json:"github_actions_mode,omitempty" jsonschema:"Enables GitHub Actions workflow selection; requires server-side availability. Leave branch-wide cancel/skip disabled for sibling workflow builds"`
	GitHubWorkflowAccessTokensEnabled       *bool   `json:"github_workflow_access_tokens_enabled,omitempty" jsonschema:"Requires GitHub.com, server-side availability and workflow token permissions"`
	IgnoreDefaultBranchPullRequests         *bool   `json:"ignore_default_branch_pull_requests,omitempty"`
	SkipBuildsForClosedPullRequests         *bool   `json:"skip_builds_for_closed_pull_requests,omitempty"`
	SkipBuildsForExistingCommits            *bool   `json:"skip_builds_for_existing_commits,omitempty"`
	TriggerMode                             *string `json:"trigger_mode,omitempty" jsonschema:"code, deployment, fork or none; none disables traditional triggering, not GitHub Actions pipeline triggers"`
	BuildPullRequests                       *bool   `json:"build_pull_requests,omitempty"`
	BuildBranches                           *bool   `json:"build_branches,omitempty"`
	PullRequestBranchFilterEnabled          *bool   `json:"pull_request_branch_filter_enabled,omitempty"`
	PullRequestBranchFilterConfiguration    *string `json:"pull_request_branch_filter_configuration,omitempty" jsonschema:"null clears it; omission leaves the API default on create or the existing value on update"`
	SkipPullRequestBuildsForExistingCommits *bool   `json:"skip_pull_request_builds_for_existing_commits,omitempty"`
	BuildPullRequestForks                   *bool   `json:"build_pull_request_forks,omitempty"`
	PrefixPullRequestForkBranchNames        *bool   `json:"prefix_pull_request_fork_branch_names,omitempty"`
	BuildTags                               *bool   `json:"build_tags,omitempty"`
	PublishCommitStatus                     *bool   `json:"publish_commit_status,omitempty"`
	PublishCommitStatusPerStep              *bool   `json:"publish_commit_status_per_step,omitempty"`
	FilterEnabled                           *bool   `json:"filter_enabled,omitempty"`
	FilterCondition                         *string `json:"filter_condition,omitempty" jsonschema:"Build condition; null clears it, omission leaves the API default on create or the existing value on update"`
	SeparatePullRequestStatuses             *bool   `json:"separate_pull_request_statuses,omitempty"`
	PublishBlockedAsPending                 *bool   `json:"publish_blocked_as_pending,omitempty"`
}

func pipelineSetting[T any](value *T) buildkite.Optional[T] {
	if value == nil {
		return buildkite.Optional[T]{}
	}
	return buildkite.Some(*value)
}

func pipelineProviderSettings(args *PipelineProviderSettingsArgs, raw json.RawMessage) (buildkite.ProviderSettings, error) {
	if isJSONNull(raw) {
		return nil, errors.New("provider_settings must be an object; clear individual nullable settings instead")
	}
	if args == nil {
		return nil, nil
	}
	var fields map[string]json.RawMessage
	if len(raw) != 0 {
		if err := json.Unmarshal(raw, &fields); err != nil {
			return nil, err
		}
	}
	for key, value := range fields {
		if isJSONNull(value) && key != "filter_condition" && key != "pull_request_branch_filter_configuration" {
			return nil, fmt.Errorf("provider_settings.%s does not accept null; omit it to leave it unchanged", key)
		}
	}
	settings := &buildkite.GitHubSettingsInput{
		GitHubActionsMode:                       pipelineSetting(args.GitHubActionsMode),
		GitHubWorkflowAccessTokensEnabled:       pipelineSetting(args.GitHubWorkflowAccessTokensEnabled),
		IgnoreDefaultBranchPullRequests:         pipelineSetting(args.IgnoreDefaultBranchPullRequests),
		SkipBuildsForClosedPullRequests:         pipelineSetting(args.SkipBuildsForClosedPullRequests),
		SkipBuildsForExistingCommits:            pipelineSetting(args.SkipBuildsForExistingCommits),
		TriggerMode:                             pipelineSetting(args.TriggerMode),
		BuildPullRequests:                       pipelineSetting(args.BuildPullRequests),
		BuildBranches:                           pipelineSetting(args.BuildBranches),
		PullRequestBranchFilterEnabled:          pipelineSetting(args.PullRequestBranchFilterEnabled),
		PullRequestBranchFilterConfiguration:    pipelineSetting(args.PullRequestBranchFilterConfiguration),
		SkipPullRequestBuildsForExistingCommits: pipelineSetting(args.SkipPullRequestBuildsForExistingCommits),
		BuildPullRequestForks:                   pipelineSetting(args.BuildPullRequestForks),
		PrefixPullRequestForkBranchNames:        pipelineSetting(args.PrefixPullRequestForkBranchNames),
		BuildTags:                               pipelineSetting(args.BuildTags),
		PublishCommitStatus:                     pipelineSetting(args.PublishCommitStatus),
		PublishCommitStatusPerStep:              pipelineSetting(args.PublishCommitStatusPerStep),
		FilterEnabled:                           pipelineSetting(args.FilterEnabled),
		FilterCondition:                         pipelineSetting(args.FilterCondition),
		SeparatePullRequestStatuses:             pipelineSetting(args.SeparatePullRequestStatuses),
		PublishBlockedAsPending:                 pipelineSetting(args.PublishBlockedAsPending),
	}
	if isJSONNull(fields["filter_condition"]) {
		settings.FilterCondition = buildkite.Null[string]()
	}
	if isJSONNull(fields["pull_request_branch_filter_configuration"]) {
		settings.PullRequestBranchFilterConfiguration = buildkite.Null[string]()
	}
	return settings, nil
}
