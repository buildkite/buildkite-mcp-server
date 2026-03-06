package buildkite

import (
	"context"
	"errors"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type JobsClient interface {
	UnblockJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
}

// GetJobLogsArgs struct for typed parameters
type GetJobLogsArgs struct {
	OrgSlug      string `json:"org_slug" jsonschema:"The organization slug"`
	PipelineSlug string `json:"pipeline_slug" jsonschema:"The pipeline slug"`
	BuildNumber  string `json:"build_number" jsonschema:"The build number"`
	JobUUID      string `json:"job_uuid" jsonschema:"The job UUID"`
}

// UnblockJobArgs struct for typed parameters
type UnblockJobArgs struct {
	OrgSlug      string            `json:"org_slug" jsonschema:"The organization slug"`
	PipelineSlug string            `json:"pipeline_slug" jsonschema:"The pipeline slug"`
	BuildNumber  string            `json:"build_number" jsonschema:"The build number"`
	JobID        string            `json:"job_id" jsonschema:"The job ID"`
	Fields       map[string]string `json:"fields,omitempty" jsonschema:"JSON object containing string values for block step fields"`
}

func UnblockJob() (mcp.Tool, mcp.ToolHandlerFor[UnblockJobArgs, any], []string) {
	return mcp.Tool{
			Name:        "unblock_job",
			Description: "Unblock a blocked job in a Buildkite build to allow it to continue execution",
			Annotations: &mcp.ToolAnnotations{
				Title: "Unblock Job",
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args UnblockJobArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.UnblockJob")
			defer span.End()

			// Validate required parameters
			if args.OrgSlug == "" {
				return utils.NewToolResultError("org_slug parameter is required"), nil, nil
			}
			if args.PipelineSlug == "" {
				return utils.NewToolResultError("pipeline_slug parameter is required"), nil, nil
			}
			if args.BuildNumber == "" {
				return utils.NewToolResultError("build_number parameter is required"), nil, nil
			}
			if args.JobID == "" {
				return utils.NewToolResultError("job_id parameter is required"), nil, nil
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
			deps := DepsFromContext(ctx)
			job, _, err := deps.JobsClient.UnblockJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, &unblockOptions)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return utils.NewToolResultError(string(errResp.RawBody)), nil, nil
					}
				}

				return utils.NewToolResultError(err.Error()), nil, nil
			}

			return mcpTextResult(span, &job)
		}, []string{"write_builds"}
}
