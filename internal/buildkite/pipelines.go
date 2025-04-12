package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

func ListPipeline(ctx context.Context, client *buildkite.Client) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_pipelines",
			mcp.WithDescription("List all pipelines in a buildkite organization"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			withPagination(),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			org, err := requiredParam[string](request, "org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			log.Ctx(ctx).Debug().Fields(map[string]any{
				"org":        org,
				"pagination": paginationParams,
			}).Msg("Listing pipelines")

			pipelines, resp, err := client.Pipelines.List(ctx, org, &buildkite.PipelineListOptions{
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

func GetPipeline(ctx context.Context, client *buildkite.Client) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			org, err := requiredParam[string](request, "org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := requiredParam[string](request, "pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipeline, resp, err := client.Pipelines.Get(ctx, org, pipelineSlug)
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
