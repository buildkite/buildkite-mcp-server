package buildkite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type ArtifactsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	ListByJob(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	DownloadArtifact(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error)
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

// DownloadArtifact implements ArtifactsClient. The artifact endpoint is built
// from the supplied identifiers and resolved relative to the client's configured
// BaseURL, so proxied installations (with a base-path prefix) are handled by the
// client and the caller never supplies a raw URL.
func (a *BuildkiteClientAdapter) DownloadArtifact(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
	return a.Artifacts.DownloadArtifactByURL(ctx, artifactDownloadPath(org, pipelineSlug, buildNumber, jobID, artifactID), writer)
}

// artifactDownloadPath builds the relative Buildkite REST path for an artifact
// download. Each identifier is path-escaped so a value cannot inject extra path
// segments, and the fixed endpoint structure is hard-coded, so the request can
// only ever address an artifact download resource.
func artifactDownloadPath(org, pipelineSlug, buildNumber, jobID, artifactID string) string {
	return fmt.Sprintf("v2/organizations/%s/pipelines/%s/builds/%s/jobs/%s/artifacts/%s/download",
		url.PathEscape(org),
		url.PathEscape(pipelineSlug),
		url.PathEscape(buildNumber),
		url.PathEscape(jobID),
		url.PathEscape(artifactID),
	)
}

type ListArtifactsForBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

type ListArtifactsForJobArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobID        string `json:"job_id"`
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

type GetArtifactArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobID        string `json:"job_id" jsonschema:"The UUID of the job that produced the artifact"`
	ArtifactID   string `json:"artifact_id" jsonschema:"The UUID of the artifact to download"`
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
				return handleBuildkiteError(err)
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
				return handleBuildkiteError(err)
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
			Description: "Download a specific artifact's content, identified by its organization, pipeline, build, job, and artifact identifiers. JSON artifacts are returned inline; other content is returned base64-encoded",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Artifact",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetArtifactArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetArtifact")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("job_id", args.JobID),
				attribute.String("artifact_id", args.ArtifactID),
			)

			// Use a buffer to capture the artifact data instead of writing directly to stdout
			var buffer bytes.Buffer
			deps := DepsFromContext(ctx)
			resp, err := deps.ArtifactsClient.DownloadArtifact(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, args.ArtifactID, &buffer)
			if err != nil {
				return utils.NewToolResultError(fmt.Sprintf("response failed with error %s", err.Error())), nil, nil
			}

			content := buffer.Bytes()
			result := map[string]any{
				"status":     resp.Status,
				"statusCode": resp.StatusCode,
			}
			if json.Valid(content) {
				result["data"] = json.RawMessage(content)
				result["encoding"] = "json"
			} else {
				// Non-JSON artifacts may be arbitrary bytes, so encode safely for JSON.
				result["data"] = base64.StdEncoding.EncodeToString(content)
				result["encoding"] = "base64"
			}

			return mcpTextResult(span, &result)
		}, []string{"read_artifacts"}
}
