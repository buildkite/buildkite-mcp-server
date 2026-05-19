package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

const (
	annotationScopeBuild = "build"
	annotationScopeJob   = "job"
)

// AnnotationsClient describes the subset of the Buildkite client we need for annotations.
type AnnotationsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error)
	Create(ctx context.Context, org, pipelineSlug, buildNumber string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error)
	ListByJob(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error)
	CreateForJob(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, ac buildkite.AnnotationCreate) (buildkite.Annotation, *buildkite.Response, error)
}

type ListAnnotationsArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	Scope        string `json:"scope,omitempty" jsonschema:"Annotation scope: build or job (defaults to build)"`
	JobID        string `json:"job_id,omitempty" jsonschema:"Job ID required when scope is job"`
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

type CreateAnnotationArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	Scope        string `json:"scope,omitempty" jsonschema:"Annotation scope: build or job (defaults to build)"`
	JobID        string `json:"job_id,omitempty" jsonschema:"Job ID required when scope is job"`
	Body         string `json:"body" jsonschema:"The annotation body as HTML or Markdown"`
	Style        string `json:"style,omitempty" jsonschema:"Optional annotation style: success, info, warning, or error"`
	Priority     int    `json:"priority,omitempty" jsonschema:"Optional annotation priority from 1 to 10"`
	Context      string `json:"context,omitempty" jsonschema:"Optional annotation context used to identify or append to an annotation"`
	Append       bool   `json:"append,omitempty" jsonschema:"Append the body to an existing annotation with the same context"`
}

func normalizeAnnotationScope(scope, jobID string) (string, string) {
	if scope == "" {
		scope = annotationScopeBuild
	}

	switch scope {
	case annotationScopeBuild:
		return scope, ""
	case annotationScopeJob:
		if jobID == "" {
			return "", "job_id is required when scope is 'job'"
		}
		return scope, ""
	default:
		return "", "scope must be 'build' or 'job'"
	}
}

// ListAnnotations returns an MCP tool + handler pair that lists annotations for a build or job.
func ListAnnotations() (mcp.Tool, mcp.ToolHandlerFor[ListAnnotationsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_annotations",
			Description: "List annotations for a build or a specific job. Use scope='build' (default) or scope='job' with job_id",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Annotations",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListAnnotationsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListAnnotations")
			defer span.End()

			scope, validationErr := normalizeAnnotationScope(args.Scope, args.JobID)
			if validationErr != "" {
				return utils.NewToolResultError(validationErr), nil, nil
			}

			paginationParams := paginationFromArgs(args.Page, args.PerPage)

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("scope", scope),
				attribute.String("job_id", args.JobID),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			deps := DepsFromContext(ctx)

			var (
				annotations []buildkite.Annotation
				resp        *buildkite.Response
				err         error
			)

			if scope == annotationScopeJob {
				annotations, resp, err = deps.AnnotationsClient.ListByJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, &buildkite.AnnotationListOptions{
					ListOptions: paginationParams,
				})
			} else {
				annotations, resp, err = deps.AnnotationsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.AnnotationListOptions{
					ListOptions: paginationParams,
				})
			}
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.Annotation]{
				Items: annotations,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(
				attribute.Int("item_count", len(annotations)),
			)

			return mcpTextResult(span, &result)
		}, []string{"read_builds"}
}

// CreateAnnotation returns an MCP tool + handler pair that creates an annotation on a build or job.
func CreateAnnotation() (mcp.Tool, mcp.ToolHandlerFor[CreateAnnotationArgs, any], []string) {
	return mcp.Tool{
			Name:        "create_annotation",
			Description: "Create an annotation on a build or specific job. Use scope='build' (default) or scope='job' with job_id",
			Annotations: &mcp.ToolAnnotations{
				Title: "Create Annotation",
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args CreateAnnotationArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreateAnnotation")
			defer span.End()

			scope, validationErr := normalizeAnnotationScope(args.Scope, args.JobID)
			if validationErr != "" {
				return utils.NewToolResultError(validationErr), nil, nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("scope", scope),
				attribute.String("job_id", args.JobID),
				attribute.String("context", args.Context),
				attribute.String("style", args.Style),
				attribute.Int("priority", args.Priority),
				attribute.Bool("append", args.Append),
			)

			create := buildkite.AnnotationCreate{
				Body:     args.Body,
				Context:  args.Context,
				Style:    args.Style,
				Priority: args.Priority,
				Append:   args.Append,
			}

			deps := DepsFromContext(ctx)

			var (
				annotation buildkite.Annotation
				err        error
			)

			if scope == annotationScopeJob {
				annotation, _, err = deps.AnnotationsClient.CreateForJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, create)
			} else {
				annotation, _, err = deps.AnnotationsClient.Create(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, create)
			}
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &annotation)
		}, []string{"write_builds"}
}
