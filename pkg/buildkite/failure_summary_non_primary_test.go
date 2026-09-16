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
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 10,
		IncludeLogs: &include, IncludeAnnotations: &include, IncludeFailedTests: &include,
	})
	require.NoError(t, err)

	require.Equal(t, []string{"failed", "canceled"}, calls)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, []string{"shard-48", "shard-69"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID})
	require.False(t, summary.JobsTruncated, "skipped never-ran tiers must not count as truncation")
	require.NotNil(t, summary.Build.JobStateCounts)
	require.Equal(t, 153, summary.Build.JobStateCounts.States["broken"], "the census still reports the skipped states")
}
