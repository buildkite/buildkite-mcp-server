package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestGetBuildFailureSummaryFetchesDependencyFailedJobsBeforeBroken(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	var calls []string
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			calls = append(calls, options.State[0])
			switch options.State[0] {
			case "failed":
				require.Equal(t, 3, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "failed", State: "failed"}}}, &buildkite.Response{}, nil
			case "canceled":
				require.Equal(t, 2, options.PerPage)
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			case "waiting_failed":
				require.Equal(t, []string{"waiting_failed", "blocked_failed", "unblocked_failed"}, options.State)
				require.Equal(t, 2, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "verify", State: "waiting_failed"}}}, &buildkite.Response{}, nil
			case "broken":
				require.Equal(t, []string{"broken"}, options.State)
				require.Equal(t, 1, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "deploy", State: "broken"}}}, &buildkite.Response{}, nil
			default:
				return buildkite.JobsList{}, nil, fmt.Errorf("unexpected job states: %v", options.State)
			}
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 2,
		IncludeLogs: &include, IncludeAnnotations: &include, IncludeFailedTests: &include,
		IncludeNonPrimaryJobs: true,
	})
	require.NoError(t, err)

	require.Equal(t, []string{"failed", "canceled", "waiting_failed", "broken"}, calls)

	text := getTextResult(t, callResult).Text
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.Equal(t, []string{"failed", "verify"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID})
	require.True(t, summary.JobsTruncated)

	require.Nil(t, summary.Jobs[0].LogTotalRows, "logs were not read, so the row count is unknown")
	require.NotNil(t, summary.Jobs[1].LogTotalRows)
	require.Zero(t, *summary.Jobs[1].LogTotalRows, "a job that never ran has an empty log")
	require.Contains(t, text, `"log_total_rows": 0`)
}

func TestFailureSummaryNotRunJobClassification(t *testing.T) {
	for _, state := range []string{"waiting_failed", "blocked_failed", "unblocked_failed"} {
		job := buildkite.Job{State: state}
		require.True(t, isDependencyFailedFailureSummaryJob(job), state)
		require.False(t, isBrokenFailureSummaryJob(job), state)
		require.True(t, isDownstreamFailureSummaryJob(job), state)
	}

	broken := buildkite.Job{State: "broken"}
	require.False(t, isDependencyFailedFailureSummaryJob(broken))
	require.True(t, isBrokenFailureSummaryJob(broken))
	require.True(t, isDownstreamFailureSummaryJob(broken))

	failed := buildkite.Job{State: "failed"}
	require.False(t, isDependencyFailedFailureSummaryJob(failed))
	require.False(t, isBrokenFailureSummaryJob(failed))
	require.False(t, isDownstreamFailureSummaryJob(failed))
}
