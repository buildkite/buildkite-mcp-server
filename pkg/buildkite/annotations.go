package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// AnnotationsClient describes the subset of the Buildkite client we need for annotations.
type AnnotationsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error)
}

type ListAnnotationsArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

// ListAnnotations returns an MCP tool + handler pair that lists annotations for a build.
func ListAnnotations() (mcp.Tool, mcp.ToolHandlerFor[ListAnnotationsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_annotations",
			Description: "List all annotations for a build, including their context, style (success/info/warning/error), rendered HTML content, and creation timestamps",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Annotations",
				ReadOnlyHint: true,
			},
		}, func(ctx context.Context, request *mcp.CallToolRequest, args ListAnnotationsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListAnnotations")
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
			annotations, resp, err := deps.AnnotationsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.AnnotationListOptions{
				ListOptions: paginationParams,
			})
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
