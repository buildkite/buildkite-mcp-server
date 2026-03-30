package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type JobsClient interface {
	UnblockJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
	RetryJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error)
	GetJobEnvironmentVariables(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.JobEnvs, *buildkite.Response, error)
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
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &job)
		}, []string{"write_builds"}
}

// RetryJobArgs struct for typed parameters
type RetryJobArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobID        string `json:"job_id"`
}

func RetryJob() (mcp.Tool, mcp.ToolHandlerFor[RetryJobArgs, any], []string) {
	return mcp.Tool{
			Name:        "retry_job",
			Description: "Retry a specific failed or timed out job in a Buildkite build",
			Annotations: &mcp.ToolAnnotations{
				Title: "Retry Job",
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args RetryJobArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.RetryJob")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("job_id", args.JobID),
			)

			deps := DepsFromContext(ctx)
			job, _, err := deps.JobsClient.RetryJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &job)
		}, []string{"write_builds"}
}

// GetJobEnvironmentVariablesArgs struct for typed parameters
type GetJobEnvironmentVariablesArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobID        string `json:"job_id"`
}

func GetJobEnvironmentVariables() (mcp.Tool, mcp.ToolHandlerFor[GetJobEnvironmentVariablesArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_job_env",
			Description: "Get the environment variables for a specific job in a Buildkite build",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Job Environment Variables",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetJobEnvironmentVariablesArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetJobEnvironmentVariables")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("job_id", args.JobID),
			)

			deps := DepsFromContext(ctx)
			jobEnvs, _, err := deps.JobsClient.GetJobEnvironmentVariables(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &jobEnvs)
		}, []string{"read_job_env"}
}
