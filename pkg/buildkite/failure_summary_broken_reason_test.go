package buildkite

import (
	"context"
	"encoding/json"
	"slices"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestGetBuildFailureSummaryIncludesBrokenReasonOnBrokenJobs(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			switch {
			case options.State[0] == "failed":
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "tests", State: "failed"}}}, &buildkite.Response{}, nil
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
		IncludeNonPrimaryJobs: true,
	})
	require.NoError(t, err)

	text := getTextResult(t, callResult).Text
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.Equal(t, []string{"tests", "deploy"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID})
	require.Empty(t, summary.Jobs[0].BrokenReason)
	require.Equal(t, "branch_mismatch", summary.Jobs[1].BrokenReason)
	require.Contains(t, text, `"broken_reason": "branch_mismatch"`)
}
