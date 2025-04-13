package buildkite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

type ArtifactsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	DownloadArtifactByURL(ctx context.Context, url string, writer io.Writer) (*buildkite.Response, error)
}

// BuildkiteClientAdapter adapts the buildkite.Client to work with our interfaces
type BuildkiteClientAdapter struct {
	*buildkite.Client
}

// ListByBuild implements ArtifactsClient
func (a *BuildkiteClientAdapter) ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
	return a.Artifacts.ListByBuild(ctx, org, pipelineSlug, buildNumber, opts)
}

// DownloadArtifactByURL implements ArtifactsClient
func (a *BuildkiteClientAdapter) DownloadArtifactByURL(ctx context.Context, url string, writer io.Writer) (*buildkite.Response, error) {
	return a.Artifacts.DownloadArtifactByURL(ctx, url, writer)
}

func ListArtifacts(ctx context.Context, client ArtifactsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_artifacts",
			mcp.WithDescription("List the artifacts for a Buildkite build"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
				mcp.Description("The slug of the pipeline"),
			),
			mcp.WithString("build_number",
				mcp.Required(),
				mcp.Description("The build number"),
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

			buildNumber, err := requiredParam[string](request, "build_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			log.Ctx(ctx).Debug().Fields(map[string]any{
				"org":           org,
				"pipeline_slug": pipelineSlug,
				"build_number":  buildNumber,
				"pagination":    paginationParams,
			}).Msg("Listing artifacts")

			artifacts, resp, err := client.ListByBuild(ctx, org, pipelineSlug, buildNumber, &buildkite.ArtifactListOptions{
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

			r, err := json.Marshal(artifacts)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal artifacts: %w", err)
			}
			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetArtifact(ctx context.Context, client ArtifactsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_artifact",
			mcp.WithDescription("Get an artifact from a Buildkite build"),
			mcp.WithString("url",
				mcp.Required(),
				mcp.Description("The URL of the artifact to get"),
			),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			url, err := requiredParam[string](request, "url")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Use a buffer to capture the artifact data instead of writing directly to stdout
			var buffer bytes.Buffer
			resp, err := client.DownloadArtifactByURL(ctx, url, &buffer)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get artifact: %s", string(body))), nil
			}

			// Create a response with the artifact data encoded safely for JSON
			result := map[string]interface{}{
				"status":     resp.Status,
				"statusCode": resp.StatusCode,
				"data":       base64.StdEncoding.EncodeToString(buffer.Bytes()),
				"encoding":   "base64",
			}

			r, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal artifact response: %w", err)
			}
			return mcp.NewToolResultText(string(r)), nil
		}
}
