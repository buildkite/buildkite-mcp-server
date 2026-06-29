package buildkite

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

const textArtifactInlineLimit int64 = 65536 // 64 KiB

// inlineLimitWriter buffers up to limit bytes of artifact content and discards
// the remainder, recording whether the source exceeded the limit. It bounds the
// memory used when inlining an artifact whose reported size under-reports the
// actual content. Write always reports success so the underlying download is
// drained to completion.
type inlineLimitWriter struct {
	buf      bytes.Buffer
	limit    int64
	overflow bool
}

func (w *inlineLimitWriter) Write(p []byte) (int, error) {
	if remaining := w.limit - int64(w.buf.Len()); remaining > 0 {
		if int64(len(p)) > remaining {
			w.buf.Write(p[:remaining])
			w.overflow = true
		} else {
			w.buf.Write(p)
		}
	} else if len(p) > 0 {
		w.overflow = true
	}
	return len(p), nil
}

type ArtifactsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	ListByJob(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error)
	GetByJob(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error)
	DownloadArtifact(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error)
	ResolveDownloadURL(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error)
}

// BuildkiteClientAdapter adapts the buildkite.Client to work with our interfaces
type BuildkiteClientAdapter struct {
	*buildkite.Client
	HTTPClient *http.Client
}

// ListByBuild implements ArtifactsClient
func (a *BuildkiteClientAdapter) ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
	return a.Artifacts.ListByBuild(ctx, org, pipelineSlug, buildNumber, opts)
}

// ListByJob implements ArtifactsClient
func (a *BuildkiteClientAdapter) ListByJob(ctx context.Context, org, pipelineSlug, buildNumber string, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
	return a.Artifacts.ListByJob(ctx, org, pipelineSlug, buildNumber, jobID, opts)
}

// GetByJob implements ArtifactsClient. The request path is built locally so
// artifact identifiers are path-escaped consistently with downloads.
func (a *BuildkiteClientAdapter) GetByJob(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (buildkite.Artifact, *buildkite.Response, error) {
	req, err := a.NewRequest(ctx, http.MethodGet, artifactMetadataPath(org, pipelineSlug, buildNumber, jobID, artifactID), nil)
	if err != nil {
		return buildkite.Artifact{}, nil, err
	}

	var artifact buildkite.Artifact
	resp, err := a.Do(req, &artifact)
	if err != nil {
		return buildkite.Artifact{}, resp, err
	}

	return artifact, resp, nil
}

// DownloadArtifact implements ArtifactsClient. The artifact endpoint is built
// from the supplied identifiers and resolved relative to the client's configured
// BaseURL, so proxied installations (with a base-path prefix) are handled by the
// client and the caller never supplies a raw URL.
func (a *BuildkiteClientAdapter) DownloadArtifact(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string, writer io.Writer) (*buildkite.Response, error) {
	return a.Artifacts.DownloadArtifactByURL(ctx, artifactDownloadPath(org, pipelineSlug, buildNumber, jobID, artifactID), writer)
}

// ResolveDownloadURL resolves Buildkite's authenticated artifact download
// endpoint to the short-lived redirected URL without following the redirect.
func (a *BuildkiteClientAdapter) ResolveDownloadURL(ctx context.Context, org, pipelineSlug, buildNumber, jobID, artifactID string) (string, error) {
	req, err := a.NewRequest(ctx, http.MethodGet, artifactDownloadPath(org, pipelineSlug, buildNumber, jobID, artifactID), nil)
	if err != nil {
		return "", err
	}

	client := http.Client{}
	if a.HTTPClient != nil {
		client = *a.HTTPClient
	}
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < http.StatusMultipleChoices || resp.StatusCode >= http.StatusBadRequest {
		return "", fmt.Errorf("expected artifact download redirect, got status %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", errors.New("artifact download redirect did not include Location header")
	}

	return location, nil
}

func artifactMetadataPath(org, pipelineSlug, buildNumber, jobID, artifactID string) string {
	return fmt.Sprintf("v2/organizations/%s/pipelines/%s/builds/%s/jobs/%s/artifacts/%s",
		url.PathEscape(org),
		url.PathEscape(pipelineSlug),
		url.PathEscape(buildNumber),
		url.PathEscape(jobID),
		url.PathEscape(artifactID),
	)
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

func isTextMIMEType(mimeType string) bool {
	if i := strings.IndexByte(mimeType, ';'); i >= 0 {
		mimeType = mimeType[:i]
	}
	mimeType = strings.ToLower(strings.TrimSpace(mimeType))

	if mimeType == "text/html" ||
		mimeType == "text/xml" ||
		mimeType == "application/xml" ||
		mimeType == "application/xhtml+xml" ||
		strings.HasSuffix(mimeType, "+xml") {
		return false
	}

	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	if strings.HasSuffix(mimeType, "+json") {
		return true
	}

	switch mimeType {
	case "application/json",
		"application/yaml",
		"application/x-yaml",
		"application/javascript",
		"application/x-javascript",
		"application/x-sh",
		"application/x-ndjson":
		return true
	default:
		return false
	}
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
			Description: "Get a specific artifact by organization, pipeline, build, job, and artifact identifiers. Small text artifacts are returned inline; large or binary artifacts return metadata plus a download URL",
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

			deps := DepsFromContext(ctx)
			artifact, _, err := deps.ArtifactsClient.GetByJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, args.ArtifactID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(
				attribute.String("mime_type", artifact.MimeType),
				attribute.Int64("file_size", artifact.FileSize),
			)

			downloadURL, downloadURLAuth, expiresInSeconds := artifactDownloadURL(ctx, deps.ArtifactsClient, args, artifact)

			// A reported size of zero is an empty file, which is cheap and safe to
			// inline. The download below is capped regardless, so an artifact whose
			// reported size under-reports its real content cannot exhaust memory.
			isInlineText := isTextMIMEType(artifact.MimeType) &&
				artifact.FileSize <= textArtifactInlineLimit

			if isInlineText {
				writer := &inlineLimitWriter{limit: textArtifactInlineLimit}
				_, err := deps.ArtifactsClient.DownloadArtifact(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, args.ArtifactID, writer)
				if err != nil {
					return handleBuildkiteError(err)
				}

				switch {
				case writer.overflow:
					result := urlArtifactResult(" because it was larger than expected", artifact, downloadURL, downloadURLAuth, expiresInSeconds)
					return mcpTextResult(span, &result)
				case !utf8.Valid(writer.buf.Bytes()):
					result := urlArtifactResult(" because it was not valid UTF-8", artifact, downloadURL, downloadURLAuth, expiresInSeconds)
					return mcpTextResult(span, &result)
				}

				result := artifactResult("text", artifact, downloadURL, downloadURLAuth, expiresInSeconds)
				result["content"] = writer.buf.String()
				return mcpTextResult(span, &result)
			}

			result := urlArtifactResult("", artifact, downloadURL, downloadURLAuth, expiresInSeconds)
			return mcpTextResult(span, &result)
		}, []string{"read_artifacts"}
}

func artifactDownloadURL(ctx context.Context, client ArtifactsClient, args GetArtifactArgs, artifact buildkite.Artifact) (string, string, int) {
	downloadURL, err := client.ResolveDownloadURL(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, args.ArtifactID)
	if err == nil && downloadURL != "" {
		return downloadURL, "none", downloadURLExpiresInSeconds(downloadURL)
	}
	if artifact.DownloadURL != "" {
		return artifact.DownloadURL, "requires Buildkite API authentication", 0
	}
	return "", "", 0
}

func downloadURLExpiresInSeconds(downloadURL string) int {
	u, err := url.Parse(downloadURL)
	if err != nil {
		return 60
	}

	expires := u.Query().Get("X-Amz-Expires")
	if expires == "" {
		return 60
	}

	seconds, err := strconv.Atoi(expires)
	if err != nil || seconds <= 0 {
		return 60
	}

	return seconds
}

// urlArtifactResult builds a non-inline result that points the caller at the
// download URL. reason is appended after "not returned inline" to explain why
// (e.g. " because it was larger than expected"); pass "" for no specific reason.
func urlArtifactResult(reason string, artifact buildkite.Artifact, downloadURL, downloadURLAuth string, expiresInSeconds int) map[string]any {
	result := artifactResult("url", artifact, downloadURL, downloadURLAuth, expiresInSeconds)
	if downloadURL == "" {
		result["note"] = fmt.Sprintf("Artifact content was not returned inline%s, and no download URL was available.", reason)
	} else {
		result["note"] = fmt.Sprintf("Artifact content was not returned inline%s. Use download_url to fetch it directly.", reason)
	}
	return result
}

func artifactResult(encoding string, artifact buildkite.Artifact, downloadURL, downloadURLAuth string, expiresInSeconds int) map[string]any {
	result := map[string]any{
		"encoding":  encoding,
		"mime_type": artifact.MimeType,
		"file_size": artifact.FileSize,
		"filename":  artifact.Filename,
	}
	if downloadURL != "" {
		result["download_url"] = downloadURL
	}
	if downloadURLAuth != "" {
		result["download_url_auth"] = downloadURLAuth
		result["download_url_expires_in_seconds"] = expiresInSeconds
	}
	return result
}
