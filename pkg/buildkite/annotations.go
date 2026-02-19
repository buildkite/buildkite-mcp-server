package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

// AnnotationsClient describes the subset of the Buildkite client we need for annotations.
type AnnotationsClient interface {
	ListByBuild(ctx context.Context, org, pipelineSlug, buildNumber string, opts *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error)
}

// ListAnnotations returns an MCP tool + handler pair that lists annotations for a build.
func ListAnnotations(client AnnotationsClient) (tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) {
	return mcp.NewTool("list_annotations",
			mcp.WithDescription("List all annotations for a build, including their context, style (success/info/warning/error), rendered HTML content, and creation timestamps"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
			),
			mcp.WithString("build_number",
				mcp.Required(),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Annotations",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		), func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListAnnotations")
			defer span.End()

			orgSlug, err := request.RequireString("org_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			pipelineSlug, err := request.RequireString("pipeline_slug")
			if err != nil {
				return handleAPIError(err), nil
			}

			buildNumber, err := request.RequireString("build_number")
			if err != nil {
				return handleAPIError(err), nil
			}

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return handleAPIError(err), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", orgSlug),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("build_number", buildNumber),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			annotations, resp, err := client.ListByBuild(ctx, orgSlug, pipelineSlug, buildNumber, &buildkite.AnnotationListOptions{
				ListOptions: paginationParams,
			})
			if err != nil {
				return handleAPIError(err), nil
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
