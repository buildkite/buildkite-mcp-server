package buildkite

import (
	"context"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

const retryFailedJobsPageLimit = 10

type RetryFailedJobsArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	DryRun       bool   `json:"dry_run,omitempty" jsonschema:"Classify jobs without retrying; 'retried' lists the jobs that would be retried"`
}

type RetryFailedJobsItem struct {
	ID           string `json:"id"`
	Name         string `json:"name,omitempty"`
	StepKey      string `json:"step_key,omitempty"`
	State        string `json:"state"`
	ExitStatus   *int   `json:"exit_status,omitempty"`
	SignalReason string `json:"signal_reason,omitempty"`
	Reason       string `json:"reason"`
	RetryJobID   string `json:"retry_job_id,omitempty"`
	Error        string `json:"error,omitempty"`
}

type RetryFailedJobsResult struct {
	DryRun  bool                  `json:"dry_run"`
	Retried []RetryFailedJobsItem `json:"retried"`
	Skipped []RetryFailedJobsItem `json:"skipped"`
	Errors  []RetryFailedJobsItem `json:"errors,omitempty"`
}

// classifyRetry decides whether a failed job is safe to retry. Only failures
// with an infrastructure cause are retried; anything that may be a real
// failure or a deliberate stop is skipped.
func classifyRetry(job buildkite.Job) (bool, string) {
	if job.Type != "script" {
		return false, "not_command_job"
	}
	switch job.State {
	case "failed", "timed_out", "expired", "canceled":
	default:
		// The failed filter also returns running jobs that promised a failure.
		return false, "not_finished"
	}
	if job.SoftFailed {
		return false, "soft_failed"
	}
	if job.State == "canceled" {
		return false, "canceled"
	}
	if job.State == "expired" {
		return true, "expired"
	}
	if job.ExitStatus != nil && *job.ExitStatus == -1 {
		return true, "agent_lost"
	}
	switch job.SignalReason {
	case "agent_stop", "agent_refused", "stack_error":
		return true, job.SignalReason
	}
	if job.State == "timed_out" {
		return false, "timed_out"
	}
	if job.SignalReason != "" {
		return false, job.SignalReason
	}
	return false, "command_failed"
}

func retryFailedJobsItem(job buildkite.Job, reason string) RetryFailedJobsItem {
	return RetryFailedJobsItem{
		ID:           job.ID,
		Name:         job.Name,
		StepKey:      job.StepKey,
		State:        job.State,
		ExitStatus:   job.ExitStatus,
		SignalReason: job.SignalReason,
		Reason:       reason,
	}
}

func listFailedJobs(ctx context.Context, client JobsClient, args RetryFailedJobsArgs) ([]buildkite.Job, error) {
	includeRetried := false
	newOptions := func(options *buildkite.JobsListOptions) *buildkite.JobsListOptions {
		options.State = []string{"failed", "timed_out", "expired", "canceled"}
		options.IncludeRetriedJobs = &includeRetried
		options.PerPage = 100
		return options
	}
	options := newOptions(&buildkite.JobsListOptions{})
	var jobs []buildkite.Job
	for range retryFailedJobsPageLimit {
		list, _, err := client.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, list.Items...)
		if list.Links.Next == "" {
			return jobs, nil
		}
		next, err := list.Links.Next.ToOptions()
		if err != nil {
			return nil, err
		}
		options = newOptions(next)
	}
	return nil, fmt.Errorf("build %s has more than %d failed jobs; no jobs were retried. Use list_jobs and retry_job instead", args.BuildNumber, retryFailedJobsPageLimit*100)
}

func RetryFailedJobs() (mcp.Tool, mcp.ToolHandlerFor[RetryFailedJobsArgs, any], []string) {
	return mcp.Tool{
			Name:        "retry_failed_jobs",
			Description: "Retry the failed jobs in a build that are safe to retry, and report every job retried or skipped with a reason. Only infrastructure failures are retried: expired, agent_lost (exit status -1), agent_stop, agent_refused and stack_error. Soft-failed, canceled and timed-out jobs, command failures and other signal reasons are skipped because a retry would likely fail again or undo a deliberate stop. Use retry_job to retry a skipped job after inspecting it",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Retry Failed Jobs",
				DestructiveHint: boolPtr(true),
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args RetryFailedJobsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.RetryFailedJobs")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.Bool("dry_run", args.DryRun),
			)

			deps := DepsFromContext(ctx)
			jobs, err := listFailedJobs(ctx, deps.JobsClient, args)
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := RetryFailedJobsResult{
				DryRun:  args.DryRun,
				Retried: []RetryFailedJobsItem{},
				Skipped: []RetryFailedJobsItem{},
			}
			for _, job := range jobs {
				retry, reason := classifyRetry(job)
				item := retryFailedJobsItem(job, reason)
				switch {
				case !retry:
					result.Skipped = append(result.Skipped, item)
				case args.DryRun:
					result.Retried = append(result.Retried, item)
				default:
					retryJob, _, err := deps.JobsClient.RetryJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, job.ID)
					if isBuildkiteUnauthorized(err) {
						// Later retries would fail with the same token; surface the 401 so
						// clients can reauthenticate. Rerunning is safe because retried jobs
						// are excluded from the failed job list.
						return nil, nil, ErrUnauthorized
					}
					if err != nil {
						item.Error = err.Error()
						result.Errors = append(result.Errors, item)
						continue
					}
					item.RetryJobID = retryJob.ID
					result.Retried = append(result.Retried, item)
				}
			}

			span.SetAttributes(
				attribute.Int("retried_count", len(result.Retried)),
				attribute.Int("skipped_count", len(result.Skipped)),
				attribute.Int("error_count", len(result.Errors)),
			)

			return mcpTextResult(span, result)
		}, []string{"write_builds"}
}
