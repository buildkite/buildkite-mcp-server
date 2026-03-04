package buildkite

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/url"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ArtifactsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	ListByJob(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
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

// ListByJob implements ArtifactsClient
func (a *BuildkiteClientAdapter) ListByJob(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
	return a.Artifacts.ListByJob(ctx, org, pipelineSlug, buildNumber, jobID, opts)
}

// DownloadArtifactByURL implements ArtifactsClient with URL rewriting support
func (a *BuildkiteClientAdapter) DownloadArtifactByURL(ctx context.Context, url string, writer io.Writer) (*buildkite.Response, error) {
	// Rewrite URL if it's using the default Buildkite API URL and we have a custom base URL
	rewrittenURL := a.rewriteArtifactURL(url)
	return a.Artifacts.DownloadArtifactByURL(ctx, rewrittenURL, writer)
}

// rewriteArtifactURL rewrites artifact URLs to use the configured base URL
func (a *BuildkiteClientAdapter) rewriteArtifactURL(inputURL string) string {
	// Parse the input URL
	parsedURL, err := url.Parse(inputURL)
	if err != nil {
		// If we can't parse the URL, return it as-is
		return inputURL
	}

	// Get the configured base URL from the client
	baseURL := a.BaseURL
	if baseURL == nil || baseURL.String() == "" {
		return inputURL
	}

	// Only rewrite if the base URL is different from the input URL's host and scheme
	// and the base URL is non-empty
	if baseURL.Host != parsedURL.Host || baseURL.Scheme != parsedURL.Scheme {
		// Replace the host and scheme with the configured base URL
		parsedURL.Scheme = baseURL.Scheme
		parsedURL.Host = baseURL.Host

		// If the base URL has a path prefix, prepend it to the existing path
		if baseURL.Path != "" && baseURL.Path != "/" {
			// Remove trailing slash from base path if present
			basePath := strings.TrimSuffix(baseURL.Path, "/")
			parsedURL.Path = basePath + parsedURL.Path
		}
	}

	return parsedURL.String()
}

type ListArtifactsForBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	Page         int    `json:"page"`
	PerPage      int    `json:"per_page"`
}

type ListArtifactsForJobArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobID        string `json:"job_id"`
	Page         int    `json:"page"`
	PerPage      int    `json:"per_page"`
}

type GetArtifactArgs struct {
	URL string `json:"url"`
}

func ListArtifactsForBuild() (mcp.Tool, mcp.ToolHandlerFor[ListArtifactsForBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_artifacts_for_build",
			Description: "List all artifacts for a build across all jobs, including file details, paths, sizes, MIME types, and download URLs",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Build Artifact List",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListArtifactsForBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListArtifactsForBuild")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}
			if args.PipelineSlug == "" {
				return utils.NewToolResultError("pipeline_slug is required"), nil, nil
			}
			if args.BuildNumber == "" {
				return utils.NewToolResultError("build_number is required"), nil, nil
			}

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			artifacts, resp, err := deps.ArtifactsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.ArtifactListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			result := PaginatedResult[buildkite.Artifact]{
				Items: artifacts,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(artifacts)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_artifacts"}
}

func ListArtifactsForJob() (mcp.Tool, mcp.ToolHandlerFor[ListArtifactsForJobArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_artifacts_for_job",
			Description: "List all artifacts for an individual job, including file details, paths, sizes, MIME types, and download URLs",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Job Artifact List",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListArtifactsForJobArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListArtifactsForJob")
			defer span.End()

			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug is required"), nil, nil
			}
			if args.PipelineSlug == "" {
				return utils.NewToolResultError("pipeline_slug is required"), nil, nil
			}
			if args.BuildNumber == "" {
				return utils.NewToolResultError("build_number is required"), nil, nil
			}
			if args.JobID == "" {
				return utils.NewToolResultError("job_id is required"), nil, nil
			}

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("job_id", args.JobID),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)
			artifacts, resp, err := deps.ArtifactsClient.ListByJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, &buildkite.ArtifactListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return utils.NewToolResultError(err.Error()), nil, nil
			}

			result := PaginatedResult[buildkite.Artifact]{
				Items: artifacts,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(artifacts)),
				attribute.Int("estimated_tokens", tokens.EstimateTokens(fmt.Sprintf("%v", result))),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_artifacts"}
}

func GetArtifact() (mcp.Tool, mcp.ToolHandlerFor[GetArtifactArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_artifact",
			Description: "Get detailed information about a specific artifact including its metadata, file size, SHA-1 hash, and download URL",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Artifact",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetArtifactArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetArtifact")
			defer span.End()

			if args.URL == "" {
				return utils.NewToolResultError("url is required"), nil, nil
			}

			artifactURL := args.URL

			// Validate the URL format and scheme
			parsedURL, err := url.Parse(artifactURL)
			if err != nil || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
				return utils.NewToolResultError("invalid URL format: must be an http or https URL"), nil, nil
			}

			span.SetAttributes(attribute.String("url", artifactURL))

			// Use a buffer to capture the artifact data instead of writing directly to stdout
			var buffer bytes.Buffer
			deps := DepsFromContext(ctx)
			resp, err := deps.ArtifactsClient.DownloadArtifactByURL(ctx, artifactURL, &buffer)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("response failed with error %s", err.Error())), nil, nil
			}

			// Create a response with the artifact data encoded safely for JSON
			result := map[string]any{
				"status":     resp.Status,
				"statusCode": resp.StatusCode,
				"data":       base64.StdEncoding.EncodeToString(buffer.Bytes()),
				"encoding":   "base64",
			}

			return mcpTextResult(span, &result)
		}, []string{"read_artifacts"}
}
