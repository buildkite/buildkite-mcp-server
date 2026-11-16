package buildkite

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestGetBuildFailureSummaryIncludesJobFailureReasons(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			switch {
			case options.State[0] == "failed":
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "tests", State: "failed"},
					{ID: "trigger", Type: "trigger", State: "failed", TriggerFailedReason: `Pipeline "deploy" does not exist`},
				}}, &buildkite.Response{}, nil
			case slices.Contains(options.State, "broken"):
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "deploy", State: "broken", BrokenReason: "branch_mismatch"}}}, &buildkite.Response{}, nil
			default:
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			}
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
		IncludeLogs: &include, IncludeAnnotations: &include, IncludeFailedTests: &include,
		IncludeNeverRanJobs: boolPtr(true),
	})
	require.NoError(t, err)

	text := getTextResult(t, callResult).Text
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.Len(t, summary.Jobs, 3)
	require.Equal(t, []string{"tests", "trigger", "deploy"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID, summary.Jobs[2].ID})
	require.Empty(t, summary.Jobs[0].BrokenReason)
	require.Empty(t, summary.Jobs[0].TriggerFailedReason)
	require.Equal(t, `Pipeline "deploy" does not exist`, summary.Jobs[1].TriggerFailedReason)
	requireJSONPathEqual(t, text, `Pipeline "deploy" does not exist`, "jobs", 1, "trigger_failed_reason")
	require.Equal(t, "branch_mismatch", summary.Jobs[2].BrokenReason)
	require.Contains(t, text, `"broken_reason": "branch_mismatch"`)
}
