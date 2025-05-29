package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

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
}

func ListPipelines(ctx context.Context, client PipelinesClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_pipelines",
			mcp.WithDescription("List all pipelines in a buildkite organization"),
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
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get issue: %s", string(body))), nil
			}

			r, err := json.Marshal(&pipelines)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal pipelines: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetPipeline(ctx context.Context, client PipelinesClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_pipeline",
			mcp.WithDescription("Get details of a specific pipeline in Buildkite"),
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

			pipeline, resp, err := client.Get(ctx, org, pipelineSlug)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get issue: %s", string(body))), nil
			}

			r, err := json.Marshal(&pipeline)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal issue: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func CreatePipeline(ctx context.Context, client PipelinesClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("create_pipeline",
			mcp.WithDescription("Create a new pipeline in Buildkite using the provided repository URL. The repository URL must be a valid Git repository URL that is accessible to Buildkite."),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("name",
				mcp.Required(),
				mcp.Description("The name of the pipeline"),
			),
			mcp.WithString("repository_url",
				mcp.Required(),
				mcp.Description("The Git repository URL to use for the pipeline"),
			),
			mcp.WithString("description",
				mcp.Description("The description of the pipeline"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Create Pipeline",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreatePipeline")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			name, err := request.RequireString("name")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			repositoryURL, err := request.RequireString("repository_url")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("name", name),
				attribute.String("repository_url", repositoryURL),
			)

			create := buildkite.CreatePipeline{
				Name:       name,
				Repository: repositoryURL,
			}

			// if the description is not empty, set it
			if description := request.GetString("description", ""); description != "" {
				create.Description = description
			}

			pipeline, resp, err := client.Create(ctx, org, create)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}
			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get issue: %s", string(body))), nil
			}
			r, err := json.Marshal(&pipeline)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal issue: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}
