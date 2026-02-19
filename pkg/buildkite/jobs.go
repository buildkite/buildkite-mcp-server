package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type JobsClient interface {
	UnblockJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
}

// GetJobLogsArgs struct for typed parameters
type GetJobLogsArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobUUID      string `json:"job_uuid"`
}

// UnblockJobArgs struct for typed parameters
type UnblockJobArgs struct {
	OrgSlug      string            `json:"org_slug"`
	PipelineSlug string            `json:"pipeline_slug"`
	BuildNumber  string            `json:"build_number"`
	JobID        string            `json:"job_id"`
	Fields       map[string]string `json:"fields,omitempty"`
}

func UnblockJob(client JobsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[UnblockJobArgs], scopes []string) {
	return mcp.NewTool("unblock_job",
			mcp.WithDescription("Unblock a blocked job in a Buildkite build to allow it to continue execution"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
			),
			mcp.WithString("build_number",
				mcp.Required(),
			),
			mcp.WithString("job_id",
				mcp.Required(),
			),
			mcp.WithObject("fields",
				mcp.Description("JSON object containing string values for block step fields"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Unblock Job",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args UnblockJobArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.UnblockJob")
			defer span.End()

			// Validate required parameters
			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug parameter is required"), nil
			}
			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug parameter is required"), nil
			}
			if args.BuildNumber == "" {
				return mcp.NewToolResultError("build_number parameter is required"), nil
			}
			if args.JobID == "" {
				return mcp.NewToolResultError("job_id parameter is required"), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("job_id", args.JobID),
			)

			// Prepare unblock options
			unblockOptions := buildkite.JobUnblockOptions{}
			if len(args.Fields) > 0 {
				unblockOptions.Fields = args.Fields
			}

			// Unblock the job
			job, _, err := client.UnblockJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, &unblockOptions)
			if err != nil {
				return handleAPIError(err), nil
			}

			return mcpTextResult(span, &job)
		}, []string{"write_builds"}
}
