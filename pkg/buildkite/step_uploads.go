package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// StepUploadsClient describes the subset of the Buildkite client we need for
// step uploads.
type StepUploadsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.StepUploadsListOptions) (buildkite.StepUploadsList, *buildkite.Response, error)
	Get(ctx context.Context, org, pipelineSlug, buildNumber, uploadUUID string) (buildkite.StepUpload, *buildkite.Response, error)
}

type ListStepUploadsArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	SourceJobID  string `json:"source_job_id,omitempty" jsonschema:"Only return uploads performed by this job (a job UUID)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
	After        string `json:"after,omitempty" jsonschema:"Cursor from a previous response's links.next to fetch the next page"`
	Before       string `json:"before,omitempty" jsonschema:"Cursor from a previous response's links.prev to fetch the previous page"`
}

type GetStepUploadArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	UploadUUID   string `json:"upload_uuid" jsonschema:"UUID of the step upload, from list_step_uploads"`
}

// ListStepUploads returns an MCP tool + handler pair that lists a build's
// dynamic pipeline uploads.
func ListStepUploads() (mcp.Tool, mcp.ToolHandlerFor[ListStepUploadsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_step_uploads",
			Description: "List the dynamic pipeline uploads (`buildkite-agent pipeline upload`) a build received, newest first, without their definitions. Each item includes state, the uploading job (source_job_id), created_jobs_count (omitted until applied, 0 when applied but no jobs were created), and rejection details. Use get_step_upload to read an upload's pipeline definition. Only available while the build is within its maximum lifetime (~30 days); older builds return 410 Gone",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Step Uploads",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListStepUploadsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListStepUploads")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("source_job_id", args.SourceJobID),
			)

			deps := DepsFromContext(ctx)

			uploads, _, err := deps.StepUploadsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.StepUploadsListOptions{
				SourceJobID: args.SourceJobID,
				PerPage:     args.PerPage,
				After:       args.After,
				Before:      args.Before,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(
				attribute.Int("item_count", len(uploads.Items)),
			)

			return mcpTextResult(span, &uploads)
		}, []string{"read_builds"}
}

// GetStepUpload returns an MCP tool + handler pair that fetches one step
// upload including its pipeline definition rendered as YAML.
func GetStepUpload() (mcp.Tool, mcp.ToolHandlerFor[GetStepUploadArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_step_upload",
			Description: "Get a single dynamic pipeline upload including its pipeline definition rendered as YAML (definition_yaml). Definitions over the API's render limit are omitted: definition_yaml is omitted, definition_yaml_omitted is true and definition_bytes reports the size. Only available while the build is within its maximum lifetime (~30 days); older builds return 410 Gone",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Step Upload",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args GetStepUploadArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetStepUpload")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("upload_uuid", args.UploadUUID),
			)

			deps := DepsFromContext(ctx)

			upload, _, err := deps.StepUploadsClient.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.UploadUUID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &upload)
		}, []string{"read_builds"}
}
