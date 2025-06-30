package buildkite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
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
			mcp.WithDescription("List all artifacts for a build across all jobs, including file details, paths, sizes, MIME types, and download URLs"),
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
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Artifact List",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Artifacts",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListArtifacts")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := request.RequireString("pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			buildNumber, err := request.RequireString("build_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("build_number", buildNumber),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

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

			result := PaginatedResult[buildkite.Artifact]{
				Items: artifacts,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			r, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal artifacts: %w", err)
			}
			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetArtifact(ctx context.Context, client ArtifactsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_artifact",
			mcp.WithDescription("Get detailed information about a specific artifact including its metadata, file size, SHA-1 hash, and download URL"),
			mcp.WithString("url",
				mcp.Required(),
				mcp.Description("The URL of the artifact to get"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Artifact",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetArtifact")
			defer span.End()

			artifactURL, err := request.RequireString("url")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			// Validate the URL format
			if _, err := url.Parse(artifactURL); err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("invalid URL format: %s", err.Error())), nil
			}

			span.SetAttributes(attribute.String("url", artifactURL))

			// Use a buffer to capture the artifact data instead of writing directly to stdout
			var buffer bytes.Buffer
			resp, err := client.DownloadArtifactByURL(ctx, artifactURL, &buffer)
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
			result := map[string]any{
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
