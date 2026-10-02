package buildkite

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestGetBuildFailureSummaryOmitsNeverRanJobsByDefault(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
				Number: 1, State: "failed",
				JobStateCounts: &buildkite.JobStateCounts{Total: 156, States: map[string]int{"failed": 2, "waiting_failed": 1, "broken": 153}},
			}, &buildkite.Response{}, nil
		},
	}
	var calls []string
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			calls = append(calls, options.State[0])
			switch options.State[0] {
			case "failed":
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "shard-48", State: "failed"},
					{ID: "shard-69", State: "failed"},
				}}, &buildkite.Response{}, nil
			case "canceled":
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "canceled", State: "canceled"}}}, &buildkite.Response{}, nil
			default:
				require.Failf(t, "unexpected job list", "never-ran states must not be fetched by default, got %v", options.State)
				return buildkite.JobsList{}, nil, nil
			}
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 10,
		IncludeLogs: &include, IncludeAnnotations: &include, IncludeFailedTests: &include,
	})
	require.NoError(t, err)

	require.Equal(t, []string{"failed", "canceled"}, calls)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Len(t, summary.Jobs, 3, "canceled jobs still appear; never-ran jobs do not")
	require.Equal(
		t,
		[]string{"shard-48", "shard-69", "canceled"},
		[]string{summary.Jobs[0].ID, summary.Jobs[1].ID, summary.Jobs[2].ID},
	)
	require.False(t, summary.JobsTruncated, "skipped never-ran tiers must not count as truncation")
	require.NotNil(t, summary.Build.JobStateCounts)
	require.Equal(t, 153, summary.Build.JobStateCounts.States["broken"], "the census still reports the skipped states")
}

// With include_never_ran_jobs true, the never-ran tiers fill whatever
// max_jobs budget the failed and canceled jobs left, dependency-failed first.
func TestGetBuildFailureSummaryIncludesNeverRanJobsWhenEnabled(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
				Number: 1, State: "failed",
				JobStateCounts: &buildkite.JobStateCounts{Total: 156, States: map[string]int{"failed": 2, "waiting_failed": 1, "broken": 153}},
			}, &buildkite.Response{}, nil
		},
	}
	var calls []string
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			calls = append(calls, options.State[0])
			switch options.State[0] {
			case "failed":
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "shard-48", State: "failed"},
					{ID: "shard-69", State: "failed"},
				}}, &buildkite.Response{}, nil
			case "canceled":
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "canceled", State: "canceled"}}}, &buildkite.Response{}, nil
			case "waiting_failed":
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "verify", State: "waiting_failed"}}}, &buildkite.Response{}, nil
			case "broken":
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "docs", State: "broken"}}}, &buildkite.Response{}, nil
			default:
				require.Failf(t, "unexpected job list", "unexpected job states %v", options.State)
				return buildkite.JobsList{}, nil, nil
			}
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 10,
		IncludeLogs: &include, IncludeAnnotations: &include, IncludeFailedTests: &include,
		IncludeNeverRanJobs: true,
	})
	require.NoError(t, err)

	require.Equal(t, []string{"failed", "canceled", "waiting_failed", "broken"}, calls)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(
		t,
		[]string{"shard-48", "shard-69", "canceled", "verify", "docs"},
		[]string{summary.Jobs[0].ID, summary.Jobs[1].ID, summary.Jobs[2].ID, summary.Jobs[3].ID, summary.Jobs[4].ID},
	)
	require.False(t, summary.JobsTruncated)
}

// A further page of primary failures must keep jobs_truncated true even when
// client-side filtering leaves the returned list shorter than max_jobs.
func TestGetBuildFailureSummaryPrimaryPageWithMoreOutranksShortJobList(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failing"}, &buildkite.Response{}, nil
		},
	}
	softPromise := 0
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			switch options.State[0] {
			case "failed":
				require.Equal(t, 4, options.PerPage)
				return buildkite.JobsList{
					Items: []buildkite.Job{
						{ID: "failed-1", State: "failed"},
						{ID: "running-soft-1", State: "running", PromisedExitStatus: &softPromise},
						{ID: "failed-2", State: "failed"},
						{ID: "running-soft-2", State: "running", PromisedExitStatus: &softPromise},
					},
					Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/v2/...?after=page-2"},
				}, &buildkite.Response{}, nil
			case "canceled":
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			default:
				require.Failf(t, "unexpected job list", "never-ran states must not be fetched by default, got %v", options.State)
				return buildkite.JobsList{}, nil, nil
			}
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 3,
		IncludeLogs: &include, IncludeAnnotations: &include, IncludeFailedTests: &include,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, []string{"failed-1", "failed-2"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID})
	require.Less(t, len(summary.Jobs), 3)
	require.True(t, summary.JobsTruncated, "a further page of primary failures outranks a short job list")
}
