package buildkite

import (
	"context"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type JobsClient interface {
	ListByBuild(ctx context.Context, org string, pipeline string, buildNumber string, opt *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error)
	GetJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error)
	GetJobByOrg(ctx context.Context, org string, jobID string) (buildkite.Job, *buildkite.Response, error)
	UnblockJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
	RetryJob(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.Job, *buildkite.Response, error)
	GetJobEnvironmentVariables(ctx context.Context, org string, pipeline string, buildNumber string, jobID string) (buildkite.JobEnvs, *buildkite.Response, error)
}

func redactUnusedJobFields(job *buildkite.Job) {
	job.WebURL = ""       // not useful in MCP
	job.RawLogsURL = ""   // provided by another tool
	job.ArtifactsURL = "" // provided by another tool
	job.LogsURL = ""      // deprecated
	job.GraphQLID = ""    // random id not useful in the MCP
}

// ListJobsArgs struct for typed parameters
type ListJobsArgs struct {
	OrgSlug            string `json:"org_slug"`
	PipelineSlug       string `json:"pipeline_slug"`
	BuildNumber        string `json:"build_number"`
	State              string `json:"state,omitempty" jsonschema:"Filter jobs by state. Comma-separated for multiple states (e.g.\\, 'passed\\,failed\\,running')"`
	IncludeRetriedJobs *bool  `json:"include_retried_jobs,omitempty" jsonschema:"Include retried jobs in the response. Defaults to true on the server when omitted"`
	PerPage            int    `json:"per_page,omitempty" jsonschema:"Results per page for cursor pagination (min 1\\, max 100\\, default 30)"`
	After              string `json:"after,omitempty" jsonschema:"Cursor for the next page. Take this from the 'links.next' URL of a previous response. Mutually exclusive with 'before'"`
	Before             string `json:"before,omitempty" jsonschema:"Cursor for the previous page. Take this from a previous response. Mutually exclusive with 'after'"`
	IncludeAgent       bool   `json:"include_agent,omitempty" jsonschema:"Include full agent details in job objects. When false (default)\\, only agent.id is included"`
}

func ListJobs() (mcp.Tool, mcp.ToolHandlerFor[ListJobsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_jobs",
			Description: "List jobs for a Buildkite build, with optional state filtering and cursor-based pagination. Returns an object with 'items' (the jobs) and 'links' containing 'next'/'self' URLs; pass the cursor from 'links.next' back as 'after' to fetch the next page",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Jobs",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListJobsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListJobs")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("state", args.State),
				attribute.Int("per_page", args.PerPage),
			)

			if args.After != "" && args.Before != "" {
				return utils.NewToolResultError("'after' and 'before' are mutually exclusive; provide at most one"), nil, nil
			}

			options := &buildkite.JobsListOptions{
				IncludeRetriedJobs: args.IncludeRetriedJobs,
				PerPage:            args.PerPage,
				After:              args.After,
				Before:             args.Before,
			}

			if args.State != "" {
				states := strings.Split(args.State, ",")
				jobStates := make([]string, len(states))
				for i, state := range states {
					jobStates[i] = strings.TrimSpace(state)
				}
				options.State = jobStates
			}

			deps := DepsFromContext(ctx)
			jobs, _, err := deps.JobsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
			if err != nil {
				return handleBuildkiteError(err)
			}

			for i := range jobs.Items {
				redactUnusedJobFields(&jobs.Items[i])
			}

			if !args.IncludeAgent && len(jobs.Items) > 0 {
				for i := range jobs.Items {
					jobs.Items[i].Agent = buildkite.Agent{
						ID: jobs.Items[i].Agent.ID,
					}
				}
			}

			return mcpTextResult(span, &jobs)
		}, []string{"read_builds"}
}

// GetJobArgs struct for typed parameters
type GetJobArgs struct {
	OrgSlug      string `json:"org_slug"`
	JobID        string `json:"job_id"`
	PipelineSlug string `json:"pipeline_slug,omitempty" jsonschema:"Pipeline slug. Provide together with 'build_number' for a build-scoped lookup. Omit both to look up the job by organization and job ID alone"`
	BuildNumber  string `json:"build_number,omitempty" jsonschema:"Build number. Provide together with 'pipeline_slug' for a build-scoped lookup. Omit both to look up the job by organization and job ID alone"`
	IncludeAgent bool   `json:"include_agent,omitempty" jsonschema:"Include full agent details in job objects. When false (default)\\, only agent.id is included"`
}

func GetJob() (mcp.Tool, mcp.ToolHandlerFor[GetJobArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_job",
			Description: "Get a single job by its UUID. Provide 'pipeline_slug' and 'build_number' for a build-scoped lookup, or omit both to look the job up by organization and job ID alone",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Job",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetJobArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetJob")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("job_id", args.JobID),
			)

			// Require both build-scoping fields together, or neither.
			if (args.PipelineSlug == "") != (args.BuildNumber == "") {
				return utils.NewToolResultError("provide both 'pipeline_slug' and 'build_number' for a build-scoped lookup, or omit both"), nil, nil
			}

			deps := DepsFromContext(ctx)
			var job buildkite.Job
			var err error
			if args.PipelineSlug != "" {
				job, _, err = deps.JobsClient.GetJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID)
			} else {
				job, _, err = deps.JobsClient.GetJobByOrg(ctx, args.OrgSlug, args.JobID)
			}
			if err != nil {
				return handleBuildkiteError(err)
			}

			redactUnusedJobFields(&job)

			if !args.IncludeAgent {
				job.Agent = buildkite.Agent{
					ID: job.Agent.ID,
				}
			}

			return mcpTextResult(span, &job)
		}, []string{"read_builds"}
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
				Title:           "Unblock Job",
				DestructiveHint: boolPtr(true),
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
				Title:           "Retry Job",
				DestructiveHint: boolPtr(true),
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
