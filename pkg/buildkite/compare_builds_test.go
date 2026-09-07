package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestCompareBuildsDefinition(t *testing.T) {
	tool, handler, scopes := CompareBuilds()
	require.Equal(t, "compare_builds", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.NotNil(t, handler)
	require.Equal(t, []string{"read_builds", "read_build_logs"}, scopes)
	require.Equal(t, []string{"build_number", "org_slug", "pipeline_slug"}, sortedRequired[CompareBuildsArgs](t))
}

func TestCompareJobOutcomes(t *testing.T) {
	for _, tc := range []struct{ target, baseline, want string }{
		{"failed", "passed", "newly_failing"}, {"timed_out", "passed", "newly_failing"},
		{"expired", "passed", "newly_failing"}, {"passed", "failed", "recovered"},
		{"failed", "failed", "still_failing"}, {"broken", "passed", "state_changed"},
		{"running", "failed", "state_changed"}, {"failed", "skipped", "state_changed"},
		{"passed", "passed", "unchanged"},
	} {
		t.Run(tc.target+"_"+tc.baseline, func(t *testing.T) {
			require.Equal(t, tc.want, compareJobOutcomes(&ComparisonJob{State: tc.target}, &ComparisonJob{State: tc.baseline}))
		})
	}
	require.Equal(t, "retries_changed", compareJobOutcomes(&ComparisonJob{State: "passed", RetriesCount: 1}, &ComparisonJob{State: "passed"}))
	require.Equal(t, "state_changed", compareJobOutcomes(&ComparisonJob{State: "passed", SoftFailed: true}, &ComparisonJob{State: "passed"}))
}

func TestCompareBuildJobsIdentityAndTimings(t *testing.T) {
	stamp := func(seconds int) *buildkite.Timestamp { return buildkite.NewTimestamp(time.Unix(int64(seconds), 0)) }
	base := []buildkite.Job{
		{ID: "base-linux", StepKey: "test", Matrix: map[string]any{"os": "linux"}, State: "passed", StartedAt: stamp(10), FinishedAt: stamp(20)},
		{ID: "base-mac", StepKey: "test", Matrix: map[string]any{"os": "mac"}, State: "failed"},
		{ID: "base-shard0", StepKey: "parallel", ParallelGroupIndex: testPtr(0), ParallelGroupTotal: testPtr(2), State: "passed"},
		{ID: "base-shard1", StepKey: "parallel", ParallelGroupIndex: testPtr(1), ParallelGroupTotal: testPtr(2), State: "passed"},
		{ID: "base-no-key", Name: "same name", State: "passed"},
		{ID: "base-duplicate", StepKey: "duplicate", State: "passed"},
		{ID: "base-removed", StepKey: "removed", State: "passed"},
	}
	target := []buildkite.Job{
		{ID: "target-mac", StepKey: "test", Matrix: map[string]any{"os": "mac"}, State: "passed", RetriesCount: 1},
		{ID: "target-linux", StepKey: "test", Matrix: map[string]any{"os": "linux"}, State: "failed", ScheduledAt: stamp(90), StartedAt: stamp(100), FinishedAt: stamp(130)},
		{ID: "target-shard1", StepKey: "parallel", ParallelGroupIndex: testPtr(1), ParallelGroupTotal: testPtr(2), State: "failed"},
		{ID: "target-shard0", StepKey: "parallel", ParallelGroupIndex: testPtr(0), ParallelGroupTotal: testPtr(2), State: "passed"},
		{ID: "target-no-key", Name: "same name", State: "failed"},
		{ID: "target-duplicate1", StepKey: "duplicate", State: "passed"},
		{ID: "target-duplicate2", StepKey: "duplicate", State: "passed"},
		{ID: "target-added", StepKey: "added", State: "passed"},
	}
	result := BuildComparison{Baseline: &BuildSummary{}, ChangeCounts: map[string]int{}}
	compareBuildJobs(target, base, &result)
	require.Equal(t, map[string]int{"newly_failing": 3, "recovered": 1, "unchanged": 1, "added": 1, "removed": 1, "unmatched": 3}, result.ChangeCounts)
	require.Equal(t, "step_key", result.Steps[0].MatchMethod)
	require.Equal(t, "name_fallback", result.Steps[2].MatchMethod)
	require.Equal(t, "target-linux", result.Steps[0].Target.ID)
	require.Equal(t, "base-linux", result.Steps[0].Baseline.ID)
	require.InDelta(t, 20.0, *result.Steps[0].ExecutionDeltaSeconds, 0.001)
	require.InDelta(t, 10.0, *result.Steps[0].Target.SchedulingSeconds, 0.001)
	require.Nil(t, result.Steps[0].Baseline.SchedulingSeconds)
	require.Equal(t, "base-shard1", result.Steps[1].Baseline.ID)
	require.Nil(t, comparisonDuration(stamp(20), stamp(10)))
	require.Nil(t, comparisonDuration(nil, stamp(10)))
	// Different parallel group sizes must not produce misleading timing deltas.
	require.NotEqual(t, comparisonIdentity(base[2]), comparisonIdentity(buildkite.Job{StepKey: "parallel", ParallelGroupIndex: testPtr(0), ParallelGroupTotal: testPtr(3)}))
}

func TestCompareBuildJobsNameFallback(t *testing.T) {
	job := buildkite.Job{Name: ":go: test", Type: "script", State: "passed"}
	for _, tc := range []struct {
		name string
		edit func(*buildkite.Job)
	}{
		{"exact match", func(*buildkite.Job) {}},
		{"renamed", func(j *buildkite.Job) { j.Name = "renamed" }},
		{"different type", func(j *buildkite.Job) { j.Type = "trigger" }},
		{"different group", func(j *buildkite.Job) { j.GroupKey = "other" }},
		{"different matrix", func(j *buildkite.Job) { j.Matrix = map[string]any{"os": "linux"} }},
		{"different shard", func(j *buildkite.Job) { j.ParallelGroupIndex = testPtr(1) }},
		{"different parallelism", func(j *buildkite.Job) { j.ParallelGroupTotal = testPtr(3) }},
		{"key cannot match name", func(j *buildkite.Job) { j.StepKey = job.Name }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			target := job
			tc.edit(&target)
			result := BuildComparison{Baseline: &BuildSummary{}, ChangeCounts: map[string]int{}}
			compareBuildJobs([]buildkite.Job{target}, []buildkite.Job{job}, &result)
			if tc.name == "exact match" {
				require.Len(t, result.Steps, 1)
				require.Equal(t, "name_fallback", result.Steps[0].MatchMethod)
				require.Contains(t, result.Warnings[0], "heuristic")
			} else {
				require.Equal(t, map[string]int{"added": 1, "removed": 1}, result.ChangeCounts)
				for _, step := range result.Steps {
					require.Empty(t, step.MatchMethod)
				}
			}
		})
	}
	for _, targetDuplicates := range []bool{false, true} {
		target, baseline := []buildkite.Job{job}, []buildkite.Job{job}
		if targetDuplicates {
			target = append(target, job)
		} else {
			baseline = append(baseline, job)
		}
		result := BuildComparison{Baseline: &BuildSummary{}, ChangeCounts: map[string]int{}}
		compareBuildJobs(target, baseline, &result)
		require.Equal(t, map[string]int{"unmatched": 3}, result.ChangeCounts)
		for _, step := range result.Steps {
			require.Equal(t, "ambiguous step identity", step.UnmatchedReason)
			require.Empty(t, step.MatchMethod)
		}
	}
	for _, name := range []string{"", "  "} {
		job.Name = name
		result := BuildComparison{Baseline: &BuildSummary{}, ChangeCounts: map[string]int{}}
		compareBuildJobs([]buildkite.Job{job}, []buildkite.Job{job}, &result)
		require.Equal(t, map[string]int{"unmatched": 2}, result.ChangeCounts)
	}
}

func TestCompareBuildJobsUnkeyedPipeline(t *testing.T) {
	// Shape of the live MCP server pipeline: seven named jobs and an unnamed
	// broken job on each side. Matrix values still distinguish the build jobs.
	jobs := []buildkite.Job{{State: "broken"}}
	for _, name := range []string{":pipeline:", ":golangci-lint: lint", ":go: test", ":docker: build image"} {
		jobs = append(jobs, buildkite.Job{Name: name, Type: "script", State: "passed"})
	}
	for _, os := range []string{"darwin", "linux", "windows"} {
		jobs = append(jobs, buildkite.Job{Name: ":terminal: build", Type: "script", State: "passed", Matrix: map[string]any{"": os}})
	}
	result := BuildComparison{Baseline: &BuildSummary{}, ChangeCounts: map[string]int{}}
	compareBuildJobs(jobs, jobs, &result)
	require.Equal(t, map[string]int{"unchanged": 7, "unmatched": 2}, result.ChangeCounts)
	for _, step := range result.Steps {
		if step.Change == "unchanged" {
			require.Equal(t, "name_fallback", step.MatchMethod)
		}
	}
}

func TestCompareBuildJobsOutputLimit(t *testing.T) {
	var jobs []buildkite.Job
	for i := 0; i < 110; i++ {
		jobs = append(jobs, buildkite.Job{StepKey: fmt.Sprint(i), State: "passed"})
	}
	result := BuildComparison{Baseline: &BuildSummary{}, ChangeCounts: map[string]int{}}
	compareBuildJobs(jobs, jobs, &result)
	require.Len(t, result.Steps, 100)
	require.Equal(t, 10, result.StepsOmitted)
	require.Equal(t, 110, result.ChangeCounts["unchanged"])
}

func TestLoadComparisonJobsPaginationAndLimit(t *testing.T) {
	calls := 0
	client := &MockJobsClient{ListByBuildFunc: func(_ context.Context, _, _, _ string, opts *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
		calls++
		require.False(t, *opts.IncludeRetriedJobs)
		require.Equal(t, 100, opts.PerPage)
		if calls == 1 {
			return buildkite.JobsList{Items: []buildkite.Job{{ID: "old", Retried: true}, {ID: "final", RetriesCount: 1}}, Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/jobs?after=cursor"}}, nil, nil
		}
		require.Equal(t, "cursor", opts.After)
		return buildkite.JobsList{Items: []buildkite.Job{{ID: "second"}}}, nil, nil
	}}
	jobs, err := loadComparisonJobs(context.Background(), client, CompareBuildsArgs{}, "42")
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	require.Equal(t, "final", jobs[0].ID)
	require.Equal(t, 1, jobs[0].RetriesCount)
	calls = 0
	client.ListByBuildFunc = func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
		calls++
		return buildkite.JobsList{Links: buildkite.JobsListLinks{Next: "https://api.buildkite.com/jobs?after=next"}}, nil, nil
	}
	jobs, err = loadComparisonJobs(context.Background(), client, CompareBuildsArgs{}, "42")
	require.ErrorContains(t, err, "No partial added/removed")
	require.Nil(t, jobs)
	require.Equal(t, 10, calls)
}

func TestCompareBuildsBaselineSelection(t *testing.T) {
	now := time.Now()
	for _, explicit := range []bool{false, true} {
		t.Run(fmt.Sprint(explicit), func(t *testing.T) {
			gets, lists := []string{}, 0
			builds := &MockBuildsClient{
				GetFunc: func(_ context.Context, org, pipeline, number string, opts *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
					require.Equal(t, "org", org)
					require.Equal(t, "pipeline", pipeline)
					require.True(t, opts.ExcludeJobs)
					require.True(t, opts.ExcludePipeline)
					gets = append(gets, number)
					if number == "42" {
						return buildkite.Build{Number: 42, Branch: "main", State: "failed", CreatedAt: buildkite.NewTimestamp(now)}, nil, nil
					}
					return buildkite.Build{Number: 40, Branch: "main", State: "passed"}, nil, nil
				},
				ListByPipelineFunc: func(_ context.Context, _, _ string, opts *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
					lists++
					require.False(t, explicit)
					require.Equal(t, []string{"main"}, opts.Branch)
					require.Equal(t, []string{"passed"}, opts.State)
					require.Equal(t, now, opts.CreatedTo)
					if opts.Page == 1 {
						return []buildkite.Build{{Number: 43, Branch: "main", State: "passed", CreatedAt: buildkite.NewTimestamp(now.Add(time.Hour))}}, &buildkite.Response{NextPage: 2}, nil
					}
					require.Equal(t, 2, opts.Page)
					return []buildkite.Build{
						{Number: 41, Branch: "other", State: "passed", CreatedAt: buildkite.NewTimestamp(now.Add(-time.Hour))},
						{Number: 40, Branch: "main", State: "passed", CreatedAt: buildkite.NewTimestamp(now.Add(-2 * time.Hour))},
						{Number: 39, Branch: "main", State: "passed", CreatedAt: buildkite.NewTimestamp(now.Add(-3 * time.Hour))},
					}, nil, nil
				},
			}
			jobs := &MockJobsClient{ListByBuildFunc: func(_ context.Context, _, _, number string, _ *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				state := "passed"
				if number == "42" {
					state = "failed"
				}
				return buildkite.JobsList{Items: []buildkite.Job{{ID: number + "-job", StepKey: "test", State: state}}}, nil, nil
			}}
			args := CompareBuildsArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42", IncludeLogs: testPtr(false)}
			if explicit {
				args.BaselineBuildNumber = "40"
			}
			_, handler, _ := CompareBuilds()
			res, _, err := handler(ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: builds, JobsClient: jobs}), nil, args)
			require.NoError(t, err)
			require.False(t, res.IsError)
			var result BuildComparison
			require.NoError(t, json.Unmarshal([]byte(getTextResult(t, res).Text), &result))
			require.Equal(t, []string{"42", "40"}, gets)
			require.Equal(t, 40, result.Baseline.Number)
			require.Equal(t, 1, result.ChangeCounts["newly_failing"])
			if explicit {
				require.Zero(t, lists)
			} else {
				require.Equal(t, 2, lists)
				require.Equal(t, 3, result.CandidatesScanned)
			}
		})
	}
}

func TestCompareBuildsNoBaselineAndErrors(t *testing.T) {
	for _, scenario := range []string{"no baseline", "history limit", "bad target", "bad baseline", "same build", "missing branch", "api error", "job error"} {
		t.Run(scenario, func(t *testing.T) {
			calls := 0
			builds := &MockBuildsClient{
				GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
					if scenario == "api error" {
						return buildkite.Build{}, nil, errors.New("api unavailable")
					}
					b := buildkite.Build{Number: 42, Branch: "main", CreatedAt: buildkite.NewTimestamp(time.Now())}
					if scenario == "missing branch" {
						b.Branch = ""
					}
					return b, nil, nil
				},
				ListByPipelineFunc: func(context.Context, string, string, *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error) {
					calls++
					if scenario == "history limit" {
						return nil, &buildkite.Response{NextPage: calls + 1}, nil
					}
					return nil, nil, nil
				},
			}
			args := CompareBuildsArgs{BuildNumber: "42"}
			switch scenario {
			case "bad target":
				args.BuildNumber = "uuid"
			case "bad baseline":
				args.BaselineBuildNumber = "-1"
			case "same build":
				args.BaselineBuildNumber = "042"
			case "job error":
				args.BaselineBuildNumber = "40"
			}
			jobs := &MockJobsClient{ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{}, nil, errors.New("jobs unavailable")
			}}
			_, handler, _ := CompareBuilds()
			res, _, err := handler(ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: builds, JobsClient: jobs}), nil, args)
			require.NoError(t, err)
			if scenario == "no baseline" || scenario == "history limit" {
				require.False(t, res.IsError)
				var result BuildComparison
				require.NoError(t, json.Unmarshal([]byte(getTextResult(t, res).Text), &result))
				require.Nil(t, result.Baseline)
				require.Empty(t, result.Steps)
				require.Contains(t, result.Warnings[0], "No earlier successful build")
				if scenario == "history limit" {
					require.Equal(t, 5, calls)
				}
			} else {
				require.True(t, res.IsError)
			}
		})
	}
}

func TestCompareBuildsLogEvidence(t *testing.T) {
	path := t.TempDir() + "/logs.parquet"
	writeTestParquetFile(t, path, []string{strings.Repeat("x", 10000), "failure evidence"})
	builds := &MockBuildsClient{GetFunc: func(_ context.Context, _, _, number string, _ *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
		if number == "42" {
			return buildkite.Build{Number: 42, Branch: "main"}, nil, nil
		}
		return buildkite.Build{Number: 40, Branch: "other"}, nil, nil
	}}
	jobs := &MockJobsClient{ListByBuildFunc: func(_ context.Context, _, _, number string, _ *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
		var list buildkite.JobsList
		for i := 0; i < 4; i++ {
			state := "passed"
			if number == "42" {
				state = "failed"
			}
			list.Items = append(list.Items, buildkite.Job{ID: fmt.Sprint(i), StepKey: fmt.Sprint(i), State: state})
		}
		return list, nil, nil
	}}
	calls := 0
	logs := &MockBuildkiteLogsClient{NewReaderFunc: func(ctx context.Context, _, _, number, job string, _ time.Duration, _ bool) (*buildkitelogs.ParquetReader, error) {
		calls++
		require.Equal(t, "42", number)
		if job == "1" {
			return nil, errors.New("logs unavailable")
		}
		return buildkitelogs.NewParquetReader(path), nil
	}}
	_, handler, _ := CompareBuilds()
	res, _, err := handler(ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: builds, JobsClient: jobs, BuildkiteLogsClient: logs}), nil, CompareBuildsArgs{BuildNumber: "42", BaselineBuildNumber: "40"})
	require.NoError(t, err)
	require.False(t, res.IsError)
	var result BuildComparison
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, res).Text), &result))
	require.Equal(t, 3, calls)
	require.Equal(t, "failure evidence", result.Steps[0].Target.LogTail[1].C)
	require.True(t, result.Steps[0].Target.LogTruncated)
	require.Contains(t, result.Steps[1].Target.LogError, "logs unavailable")
	require.Empty(t, result.Steps[3].Target.LogTail)
	require.Contains(t, strings.Join(result.Warnings, " "), "different branch")
	require.Contains(t, strings.Join(result.Warnings, " "), "limited to three")
}
