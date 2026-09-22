package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestGetBuildFailureSummaryToolDefinition(t *testing.T) {
	tool, handler, scopes := GetBuildFailureSummary()

	require.Equal(t, "get_build_failure_summary", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Contains(t, tool.Description, "one call")
	require.Equal(t, []string{"read_builds", "read_build_logs", "read_suites"}, scopes)
	require.NotNil(t, handler)
}

func TestGetBuildFailureSummaryAggregatesDiagnostics(t *testing.T) {
	failedLog := t.TempDir() + "/failed.parquet"
	promisedLog := t.TempDir() + "/promised.parquet"
	writeTestParquetFile(t, failedLog, []string{"setup", "compile error", "build failed"})
	writeTestParquetFile(t, promisedLog, []string{"tests running", "test failure promised"})

	buildsClient := &MockBuildsClient{
		GetFunc: func(_ context.Context, org, pipeline, number string, options *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "pipeline", pipeline)
			require.Equal(t, "42", number)
			require.True(t, options.ExcludeJobs)
			require.True(t, options.ExcludePipeline)
			return buildkite.Build{
				ID:      "build-id",
				Number:  42,
				State:   "failing",
				Branch:  "main",
				Commit:  "abc123",
				Message: "Fix tests",
				JobStateCounts: &buildkite.JobStateCounts{
					Total:  5,
					States: map[string]int{"passed": 2, "failed": 1, "running": 1, "broken": 1},
				},
			}, &buildkite.Response{Response: &http.Response{StatusCode: http.StatusOK}}, nil
		},
	}

	promisedExitStatus := 1
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, org, pipeline, number string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			require.NotNil(t, options.IncludeRetriedJobs)
			require.False(t, *options.IncludeRetriedJobs)
			switch options.State[0] {
			case "failed":
				require.Equal(t, []string{"failed", "timed_out", "expired"}, options.State)
				require.Equal(t, defaultFailureSummaryJobs+1, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "job-failed", Name: "compile", State: "failed", Command: "make build", ExitStatus: testPtr(1)},
					{ID: "job-promised", Name: "tests", State: "running", PromisedExitStatus: &promisedExitStatus},
					{ID: "job-running", Name: "unrelated", State: "running"},
				}}, &buildkite.Response{}, nil
			case "canceled":
				require.Equal(t, []string{"canceled"}, options.State)
				require.Equal(t, defaultFailureSummaryJobs-1, options.PerPage)
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			case "waiting_failed":
				require.Equal(t, []string{"waiting_failed", "blocked_failed", "unblocked_failed"}, options.State)
				require.Equal(t, defaultFailureSummaryJobs-1, options.PerPage)
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			case "broken":
				require.Equal(t, []string{"broken"}, options.State)
				require.Equal(t, defaultFailureSummaryJobs-1, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "job-broken", Name: "deploy", State: "broken"},
				}}, &buildkite.Response{}, nil
			default:
				return buildkite.JobsList{}, nil, fmt.Errorf("unexpected job states: %v", options.State)
			}
		},
	}

	annotationsClient := &MockAnnotationsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
			require.Equal(t, "all", options.Scope)
			require.Equal(t, failureSummaryAnnotationPageSize, options.PerPage)
			switch options.Page {
			case 1:
				return []buildkite.Annotation{{ID: "annotation-success", Context: "coverage", Style: "success", BodyHTML: "coverage passed"}}, &buildkite.Response{NextPage: 2}, nil
			case 2:
				return []buildkite.Annotation{
					{ID: "annotation-error", Context: "tests", Style: "error", BodyHTML: "<p>2 tests failed</p>"},
					{ID: "annotation-warning", Context: "lint", Style: "warning", JobID: "job-failed", BodyHTML: "lint warning"},
				}, &buildkite.Response{}, nil
			default:
				return nil, nil, errors.New("unexpected annotation page")
			}
		},
	}

	type logCall struct {
		ttl          time.Duration
		forceRefresh bool
	}
	var logCallsMu sync.Mutex
	logCalls := map[string][]logCall{}
	logsClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(_ context.Context, _, _, _, job string, ttl time.Duration, forceRefresh bool) (*buildkitelogs.ParquetReader, error) {
			logCallsMu.Lock()
			logCalls[job] = append(logCalls[job], logCall{ttl: ttl, forceRefresh: forceRefresh})
			logCallsMu.Unlock()
			switch job {
			case "job-failed":
				return buildkitelogs.NewParquetReader(failedLog), nil
			case "job-promised":
				return buildkitelogs.NewParquetReader(promisedLog), nil
			default:
				return nil, errors.New("unexpected log request")
			}
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:        buildsClient,
		JobsClient:          jobsClient,
		AnnotationsClient:   annotationsClient,
		BuildkiteLogsClient: logsClient,
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "42",
		LogTail:      2,
	})
	require.NoError(t, err)
	require.False(t, callResult.IsError)

	text := getTextResult(t, callResult).Text
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.LessOrEqual(t, len(text), failureSummaryContentByteLimit)
	require.Equal(t, len(text), summary.ContentBytes)
	require.Equal(t, "failing", summary.Build.State)
	require.Equal(t, 42, summary.Build.Number)
	require.NotNil(t, summary.Build.JobStateCounts)
	require.Equal(t, 5, summary.Build.JobStateCounts.Total)
	require.Equal(t, map[string]int{"passed": 2, "failed": 1, "running": 1, "broken": 1}, summary.Build.JobStateCounts.States)
	require.Contains(t, text, `"truncated": false`)
	require.Len(t, summary.Jobs, 3)
	require.False(t, summary.JobsTruncated)

	require.Equal(t, "job-failed", summary.Jobs[0].ID)
	require.NotNil(t, summary.Jobs[0].LogTotalRows)
	require.Equal(t, int64(3), *summary.Jobs[0].LogTotalRows)
	require.True(t, summary.Jobs[0].LogTruncated)
	require.Equal(t, []string{"compile error", "build failed"}, []string{summary.Jobs[0].LogTail[0].C, summary.Jobs[0].LogTail[1].C})

	require.Equal(t, "job-promised", summary.Jobs[1].ID)
	require.Equal(t, 1, *summary.Jobs[1].PromisedExitStatus)
	require.Len(t, summary.Jobs[1].LogTail, 2)

	require.Equal(t, "job-broken", summary.Jobs[2].ID)
	require.Empty(t, summary.Jobs[2].LogTail)
	require.Empty(t, summary.Jobs[2].LogError)

	require.Len(t, summary.Annotations, 2)
	require.False(t, summary.AnnotationsTruncated)
	require.Equal(t, "annotation-error", summary.Annotations[0].ID)
	require.NotContains(t, getTextResult(t, callResult).Text, "coverage passed")

	logCallsMu.Lock()
	require.Equal(t, map[string][]logCall{
		"job-failed":   {{ttl: 30 * time.Second}},
		"job-promised": {{ttl: 30 * time.Second}},
	}, logCalls)
	logCallsMu.Unlock()
}

func TestGetBuildFailureSummaryOmitsJobStateCountsWhenAbsent(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{ID: "build-id", Number: 42, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			return buildkite.JobsList{}, &buildkite.Response{}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})
	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "42",
	})
	require.NoError(t, err)
	require.False(t, callResult.IsError)
	require.NotContains(t, getTextResult(t, callResult).Text, "job_state_counts")
}

func TestGetBuildFailureSummaryPrioritizesFailuresAndCanceledJobsBeforeDownstreamJobs(t *testing.T) {
	promisedExitStatus := 1
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failing"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			switch options.State[0] {
			case "failed":
				require.Equal(t, []string{"failed", "timed_out", "expired"}, options.State)
				require.Equal(t, 4, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "failed", State: "failed"},
					{ID: "promised", State: "running", PromisedExitStatus: &promisedExitStatus},
				}}, &buildkite.Response{}, nil
			case "canceled":
				require.Equal(t, []string{"canceled"}, options.State)
				require.Equal(t, 2, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "canceled", State: "canceled"}}}, &buildkite.Response{}, nil
			case "waiting_failed":
				require.Equal(t, []string{"waiting_failed", "blocked_failed", "unblocked_failed"}, options.State)
				require.Equal(t, 1, options.PerPage)
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			case "broken":
				require.Equal(t, []string{"broken"}, options.State)
				require.Equal(t, 1, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "broken-1", State: "broken"},
					{ID: "broken-2", State: "broken"},
				}}, &buildkite.Response{}, nil
			default:
				return buildkite.JobsList{}, nil, fmt.Errorf("unexpected job states: %v", options.State)
			}
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 3,
		IncludeLogs: &include, IncludeAnnotations: &include,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, []string{"failed", "promised", "canceled"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID, summary.Jobs[2].ID})
	require.True(t, summary.JobsTruncated)
}

func TestGetBuildFailureSummaryEnforcesServerJobLimit(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
		},
	}

	jobs := make([]buildkite.Job, 6)
	for i := range jobs {
		jobs[i] = buildkite.Job{ID: fmt.Sprintf("job-%d", i+1), State: "failed"}
	}
	jobListCalls := 0
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			jobListCalls++
			require.Equal(t, []string{"failed", "timed_out", "expired"}, options.State)
			require.Equal(t, 6, options.PerPage)
			return buildkite.JobsList{Items: jobs}, &buildkite.Response{}, nil
		},
	}

	var logCallsMu sync.Mutex
	logCalls := 0
	logsClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
			logCallsMu.Lock()
			logCalls++
			logCallsMu.Unlock()
			return nil, errors.New("log unavailable")
		},
	}

	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:        buildsClient,
		JobsClient:          jobsClient,
		BuildkiteLogsClient: logsClient,
		FailureSummary:      FailureSummaryConfig{MaxJobs: 5},
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 50,
		IncludeAnnotations: &include,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, 5, summary.JobLimit)
	require.Len(t, summary.Jobs, 5)
	require.True(t, summary.JobsTruncated)
	require.Equal(t, 1, jobListCalls)
	logCallsMu.Lock()
	require.Equal(t, 5, logCalls)
	logCallsMu.Unlock()
}

func TestGetBuildFailureSummaryIncludesTimedOutJobAndLog(t *testing.T) {
	logPath := t.TempDir() + "/timed-out.parquet"
	writeTestParquetFile(t, logPath, []string{"running", "job timed out"})
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			switch options.State[0] {
			case "failed":
				require.Equal(t, []string{"failed", "timed_out", "expired"}, options.State)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "timed-out", State: "timed_out"}}}, &buildkite.Response{}, nil
			case "canceled", "waiting_failed", "broken":
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			default:
				return buildkite.JobsList{}, nil, fmt.Errorf("unexpected job states: %v", options.State)
			}
		},
	}
	logsClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(_ context.Context, _, _, _, job string, _ time.Duration, _ bool) (*buildkitelogs.ParquetReader, error) {
			require.Equal(t, "timed-out", job)
			return buildkitelogs.NewParquetReader(logPath), nil
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient: buildsClient, JobsClient: jobsClient, BuildkiteLogsClient: logsClient,
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
		IncludeAnnotations: &include,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Len(t, summary.Jobs, 1)
	require.Equal(t, "timed_out", summary.Jobs[0].State)
	require.Equal(t, []string{"running", "job timed out"}, []string{summary.Jobs[0].LogTail[0].C, summary.Jobs[0].LogTail[1].C})
}

func TestGetBuildFailureSummaryIncludesExpiredAndDownstreamFailedJobsWithoutLogs(t *testing.T) {
	expiredAt := buildkite.NewTimestamp(time.Date(2026, time.July, 24, 1, 2, 3, 0, time.UTC))
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			switch options.State[0] {
			case "failed":
				require.Equal(t, []string{"failed", "timed_out", "expired"}, options.State)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "expired", State: "expired", ExpiredAt: expiredAt}}}, &buildkite.Response{}, nil
			case "canceled":
				require.Equal(t, []string{"canceled"}, options.State)
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			case "waiting_failed":
				require.Equal(t, []string{"waiting_failed", "blocked_failed", "unblocked_failed"}, options.State)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "waiting", State: "waiting_failed"}}}, &buildkite.Response{}, nil
			case "broken":
				require.Equal(t, []string{"broken"}, options.State)
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			default:
				return buildkite.JobsList{}, nil, fmt.Errorf("unexpected job states: %v", options.State)
			}
		},
	}
	logCalls := 0
	logsClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
			logCalls++
			return nil, errors.New("unexpected log request")
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient: buildsClient, JobsClient: jobsClient, BuildkiteLogsClient: logsClient,
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
		IncludeAnnotations: &include,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, []string{"expired", "waiting"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID})
	require.NotNil(t, summary.Jobs[0].ExpiredAt)
	require.Empty(t, summary.Jobs[0].LogTail)
	require.Empty(t, summary.Jobs[1].LogTail)
	require.Zero(t, logCalls)
}

func TestFailureSummaryJobClassification(t *testing.T) {
	promisedExitStatus := 1
	tests := []struct {
		name       string
		job        buildkite.Job
		primary    bool
		canceled   bool
		downstream bool
		readLog    bool
	}{
		{name: "failed", job: buildkite.Job{State: "failed"}, primary: true, readLog: true},
		{name: "timed out", job: buildkite.Job{State: "timed_out"}, primary: true, readLog: true},
		{name: "expired", job: buildkite.Job{State: "expired"}, primary: true},
		{name: "canceled", job: buildkite.Job{State: "canceled"}, canceled: true, readLog: true},
		{name: "promised failure", job: buildkite.Job{State: "running", PromisedExitStatus: &promisedExitStatus}, primary: true, readLog: true},
		{name: "broken", job: buildkite.Job{State: "broken"}, downstream: true},
		{name: "waiting failed", job: buildkite.Job{State: "waiting_failed"}, downstream: true},
		{name: "blocked failed", job: buildkite.Job{State: "blocked_failed"}, downstream: true},
		{name: "unblocked failed", job: buildkite.Job{State: "unblocked_failed"}, downstream: true},
		{name: "passed", job: buildkite.Job{State: "passed"}},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.primary, isPrimaryFailureSummaryJob(test.job))
			require.Equal(t, test.canceled, isCanceledFailureSummaryJob(test.job))
			require.Equal(t, test.downstream, isDownstreamFailureSummaryJob(test.job))
			require.Equal(t, test.readLog, shouldReadFailureLog(test.job))
		})
	}
}

func TestGetBuildFailureSummaryCanDisableOptionalSections(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "passed"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			return buildkite.JobsList{}, &buildkite.Response{}, nil
		},
	}
	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: buildsClient, JobsClient: jobsClient})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:            "org",
		PipelineSlug:       "pipeline",
		BuildNumber:        "1",
		IncludeLogs:        &include,
		IncludeAnnotations: &include,
	})

	require.NoError(t, err)
	require.False(t, callResult.IsError)
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, "passed", summary.Build.State)
	require.Empty(t, summary.Jobs)
	require.Empty(t, summary.Annotations)
}

func TestGetBuildFailureSummaryLimitsFinalEscapedJSONPayload(t *testing.T) {
	escapeHeavy := strings.Repeat("\"\\\n", failureSummaryContentByteLimit)
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
				ID:      "build-id",
				Number:  1,
				State:   "failed",
				Message: escapeHeavy,
			}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			return buildkite.JobsList{Items: []buildkite.Job{{
				ID:      "job-id",
				Name:    "escaped command",
				State:   "failed",
				Command: escapeHeavy,
			}}}, &buildkite.Response{}, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient: buildsClient,
		JobsClient:   jobsClient,
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})

	require.NoError(t, err)
	require.False(t, callResult.IsError)
	text := getTextResult(t, callResult).Text
	require.LessOrEqual(t, len(text), failureSummaryContentByteLimit)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.Equal(t, len(text), summary.ContentBytes)
	require.Equal(t, failureSummaryContentByteLimit, summary.ContentLimitBytes)
	require.True(t, summary.ContentTruncated)
	require.Less(t, len(summary.Build.Message), len(escapeHeavy))
	require.Less(t, len(summary.Jobs[0].Command), len(escapeHeavy))
}

func TestGetBuildFailureSummaryHonorsContentLimitBytesArg(t *testing.T) {
	large := strings.Repeat("x", failureSummaryContentByteLimit)
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{
				ID:      "build-id",
				Number:  1,
				State:   "failed",
				Message: large,
			}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			return buildkite.JobsList{Items: []buildkite.Job{{
				ID:      "job-id",
				Name:    "large command",
				State:   "failed",
				Command: large,
			}}}, &buildkite.Response{}, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient: buildsClient,
		JobsClient:   jobsClient,
	})
	_, handler, _ := GetBuildFailureSummary()

	t.Run("lowers the payload cap", func(t *testing.T) {
		requested := 8 * 1024
		callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
			OrgSlug:           "org",
			PipelineSlug:      "pipeline",
			BuildNumber:       "1",
			ContentLimitBytes: requested,
		})

		require.NoError(t, err)
		require.False(t, callResult.IsError)
		text := getTextResult(t, callResult).Text
		require.LessOrEqual(t, len(text), requested)

		var summary BuildFailureSummary
		require.NoError(t, json.Unmarshal([]byte(text), &summary))
		require.Equal(t, requested, summary.ContentLimitBytes)
		require.True(t, summary.ContentTruncated)
	})

	t.Run("trims log tails semantically before the generic limiter", func(t *testing.T) {
		lines := make([]string, 60)
		for i := range lines {
			lines[i] = fmt.Sprintf("line-%02d %s", i, strings.Repeat("x", 200))
		}
		logPath := t.TempDir() + "/failed.parquet"
		writeTestParquetFile(t, logPath, lines)
		logsClient := &MockBuildkiteLogsClient{
			NewReaderFunc: func(_ context.Context, _, _, _, _ string, _ time.Duration, _ bool) (*buildkitelogs.ParquetReader, error) {
				return buildkitelogs.NewParquetReader(logPath), nil
			},
		}
		logCtx := ContextWithDeps(context.Background(), ToolDependencies{
			BuildsClient: &MockBuildsClient{
				GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
					return buildkite.Build{ID: "build-id", Number: 1, State: "failed"}, &buildkite.Response{}, nil
				},
			},
			JobsClient: &MockJobsClient{
				ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
					if options.State[0] == "failed" {
						return buildkite.JobsList{Items: []buildkite.Job{{ID: "job-id", State: "failed"}}}, &buildkite.Response{}, nil
					}
					return buildkite.JobsList{}, &buildkite.Response{}, nil
				},
			},
			BuildkiteLogsClient: logsClient,
		})

		requested := 6 * 1024
		include := false
		callResult, _, err := handler(logCtx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
			OrgSlug:            "org",
			PipelineSlug:       "pipeline",
			BuildNumber:        "1",
			ContentLimitBytes:  requested,
			IncludeAnnotations: &include,
		})

		require.NoError(t, err)
		require.False(t, callResult.IsError)
		text := getTextResult(t, callResult).Text
		require.LessOrEqual(t, len(text), requested)

		var summary BuildFailureSummary
		require.NoError(t, json.Unmarshal([]byte(text), &summary))
		require.Len(t, summary.Jobs, 1)
		job := summary.Jobs[0]
		require.NotEmpty(t, job.LogTail)
		require.Positive(t, job.LogEntriesOmitted)
		require.True(t, job.LogTruncated)
		// The semantic trim keeps the newest lines, so the final fetched line
		// must survive.
		require.Contains(t, job.LogTail[len(job.LogTail)-1].C, "line-59")
	})

	t.Run("clamps values above the server maximum", func(t *testing.T) {
		callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
			OrgSlug:           "org",
			PipelineSlug:      "pipeline",
			BuildNumber:       "1",
			ContentLimitBytes: failureSummaryContentByteLimit * 2,
		})

		require.NoError(t, err)
		require.False(t, callResult.IsError)
		text := getTextResult(t, callResult).Text
		require.LessOrEqual(t, len(text), failureSummaryContentByteLimit)

		var summary BuildFailureSummary
		require.NoError(t, json.Unmarshal([]byte(text), &summary))
		require.Equal(t, failureSummaryContentByteLimit, summary.ContentLimitBytes)
	})
}

func TestGetBuildFailureSummaryDefaultLimitPreservesCollectionsForStringOverage(t *testing.T) {
	// An escape-heavy build message pushes the serialized payload past the
	// default limit while the strings-emptied structure stays tiny. The
	// generic limiter can fix that by shortening strings alone, so the
	// semantic pass must not sacrifice annotation items for it.
	escapeHeavy := strings.Repeat("\"\\\n", failureSummaryContentByteLimit)
	annotationsClient := &MockAnnotationsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
			if options.Page == 1 {
				return []buildkite.Annotation{{ID: "ann-1", Style: "error", BodyHTML: "test failed"}}, &buildkite.Response{}, nil
			}
			return nil, &buildkite.Response{}, nil
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient: &MockBuildsClient{
			GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
				return buildkite.Build{
					Number:  1,
					State:   "failed",
					Message: escapeHeavy,
				}, &buildkite.Response{}, nil
			},
		},
		JobsClient: &MockJobsClient{
			ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			},
		},
		AnnotationsClient: annotationsClient,
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:      "org",
		PipelineSlug: "pipeline",
		BuildNumber:  "1",
	})

	require.NoError(t, err)
	require.False(t, callResult.IsError)
	text := getTextResult(t, callResult).Text
	require.LessOrEqual(t, len(text), failureSummaryContentByteLimit)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.True(t, summary.ContentTruncated)
	require.Less(t, len(summary.Build.Message), len(escapeHeavy))
	require.Len(t, summary.Annotations, 1)
}

func TestGetBuildFailureSummaryPreservesPartialResultForForbiddenOptionalSections(t *testing.T) {
	forbidden := &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusForbidden,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "api.buildkite.com"},
			},
		},
		Message: "Your access token doesn't have the required scope",
	}
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			if options.State[0] == "failed" {
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "job", State: "failed"}}}, &buildkite.Response{}, nil
			}
			return buildkite.JobsList{}, &buildkite.Response{}, nil
		},
	}
	logsClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
			return nil, forbidden
		},
	}
	annotationsClient := &MockAnnotationsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
			return nil, nil, forbidden
		},
	}
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:        buildsClient,
		JobsClient:          jobsClient,
		BuildkiteLogsClient: logsClient,
		AnnotationsClient:   annotationsClient,
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
	})

	require.NoError(t, err)
	require.False(t, callResult.IsError)
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Len(t, summary.Jobs, 1)
	require.Contains(t, summary.Jobs[0].LogError, forbidden.Message)
	require.Len(t, summary.Warnings, 1)
	require.Contains(t, summary.Warnings[0], forbidden.Message)
}

func TestFailureSummaryOptionalLoadersPropagateUnauthorized(t *testing.T) {
	unauthorized := fmt.Errorf("wrapped API failure: %w", &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusUnauthorized,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "api.buildkite.com"},
			},
		},
	})
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}

	t.Run("annotations", func(t *testing.T) {
		client := &MockAnnotationsClient{
			ListByBuildFunc: func(context.Context, string, string, string, *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
				return nil, nil, unauthorized
			},
		}

		_, _, err := loadFailureAnnotations(context.Background(), client, args, 1)
		require.ErrorIs(t, err, ErrUnauthorized)
	})

	t.Run("logs", func(t *testing.T) {
		client := &MockBuildkiteLogsClient{
			NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
				return nil, unauthorized
			},
		}
		jobs := []FailureSummaryJob{{}}

		err := loadFailureLogs(context.Background(), client, args, []buildkite.Job{{ID: "job", State: "failed"}}, jobs, 1)
		require.ErrorIs(t, err, ErrUnauthorized)
		require.Empty(t, jobs[0].LogError)
	})

}

func TestFailureSummaryOptionalLoadersPreserveOrdinaryErrors(t *testing.T) {
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}

	logsClient := &MockBuildkiteLogsClient{
		NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
			return nil, errors.New("logs unavailable")
		},
	}
	jobs := []FailureSummaryJob{{}}
	require.NoError(t, loadFailureLogs(context.Background(), logsClient, args, []buildkite.Job{{ID: "job", State: "failed"}}, jobs, 1))
	require.Contains(t, jobs[0].LogError, "logs unavailable")
}

func TestReadFailureLogWindowBoundsEntryContent(t *testing.T) {
	logPath := t.TempDir() + "/large.parquet"
	writeTestParquetFile(t, logPath, []string{strings.Repeat("é", failureSummaryEntryContentByteLimit)})
	client := &MockBuildkiteLogsClient{
		NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
			return buildkitelogs.NewParquetReader(logPath), nil
		},
	}

	window, err := readFailureLogWindow(context.Background(), client, GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
	}, buildkite.Job{ID: "job"}, 1)

	require.NoError(t, err)
	require.Len(t, window.Entries, 1)
	require.LessOrEqual(t, len(window.Entries[0].C), failureSummaryEntryContentByteLimit)
	require.True(t, window.Entries[0].ContentTruncated)
	require.True(t, window.ContentTruncated)
	require.True(t, utf8.ValidString(window.Entries[0].C))
	require.Equal(t, failureSummaryLogSelectionTail, window.Selection)
}

func TestBoundFailureLogEntriesReportsPartialEntryTruncation(t *testing.T) {
	entries, omitted, truncated := boundFailureLogEntries([]FailureSummaryLogEntry{{
		TerseLogEntry: TerseLogEntry{C: "123456"},
	}}, 5)

	require.Len(t, entries, 1)
	require.Zero(t, omitted)
	require.True(t, truncated)
	require.True(t, entries[0].ContentTruncated)
	require.LessOrEqual(t, len(entries[0].C), 5)
}

func TestApplyFailureSummaryContentLimitsBoundsAggregateContent(t *testing.T) {
	const jobs = 10
	const entriesPerJob = 50
	content := strings.Repeat("x", failureSummaryEntryContentByteLimit)
	result := BuildFailureSummary{
		Jobs:        make([]FailureSummaryJob, jobs),
		Annotations: make([]FailureSummaryAnnotation, 20),
	}
	for i := range result.Jobs {
		result.Jobs[i].LogTail = make([]FailureSummaryLogEntry, entriesPerJob)
		for j := range result.Jobs[i].LogTail {
			result.Jobs[i].LogTail[j].C = content
		}
	}
	for i := range result.Annotations {
		result.Annotations[i].BodyHTML = content
	}

	applyFailureSummaryContentLimits(&result)

	require.Equal(t, failureSummaryContentByteLimit, result.ContentLimitBytes)
	require.True(t, result.ContentTruncated)
	for _, job := range result.Jobs {
		require.True(t, job.LogContentTruncated)
		require.Positive(t, job.LogEntriesOmitted)
		require.True(t, job.LogTruncated)
		for _, entry := range job.LogTail {
			require.LessOrEqual(t, len(entry.C), failureSummaryEntryContentByteLimit)
		}
	}
	require.True(t, result.Annotations[len(result.Annotations)-1].BodyTruncated)
}

func TestLimitFailureSummaryLogCollectionsRetainsNewestRowsAndUpdatesMetadata(t *testing.T) {
	const jobCount = 50
	const entriesPerJob = 200
	result := BuildFailureSummary{Jobs: make([]FailureSummaryJob, jobCount)}
	for i := range result.Jobs {
		result.Jobs[i].LogTail = make([]FailureSummaryLogEntry, entriesPerJob)
		for row := range result.Jobs[i].LogTail {
			result.Jobs[i].LogTail[row] = FailureSummaryLogEntry{
				TerseLogEntry: TerseLogEntry{C: "0123456789", RN: int64(row)},
			}
		}
	}
	applyFailureSummaryContentLimits(&result)
	for _, job := range result.Jobs {
		require.Len(t, job.LogTail, entriesPerJob)
	}

	require.NoError(t, limitFailureSummaryCollections(&result, failureSummaryContentByteLimit))
	payload, err := marshalFailureSummaryWithContentBytes(&result)
	require.NoError(t, err)
	require.LessOrEqual(t, len(payload), failureSummaryContentByteLimit)
	require.Equal(t, len(payload), result.ContentBytes)
	require.True(t, result.ContentTruncated)

	for _, job := range result.Jobs {
		require.NotEmpty(t, job.LogTail)
		require.Less(t, len(job.LogTail), entriesPerJob)
		require.Equal(t, int64(entriesPerJob-1), job.LogTail[len(job.LogTail)-1].RN)
		require.Equal(t, int64(job.LogEntriesOmitted), job.LogTail[0].RN)
		require.Equal(t, entriesPerJob-len(job.LogTail), job.LogEntriesOmitted)
		require.True(t, job.LogTruncated)
		require.True(t, job.LogContentTruncated)
	}

	genericLimited, err := limitSanitizedJSONPayload(payload, failureSummaryContentByteLimit)
	require.NoError(t, err)
	var genericResult BuildFailureSummary
	require.NoError(t, json.Unmarshal(genericLimited, &genericResult))
	for i := range result.Jobs {
		require.Len(t, genericResult.Jobs[i].LogTail, len(result.Jobs[i].LogTail))
		require.Equal(t, result.Jobs[i].LogTail[0].RN, genericResult.Jobs[i].LogTail[0].RN)
		require.Equal(t, int64(entriesPerJob-1), genericResult.Jobs[i].LogTail[len(genericResult.Jobs[i].LogTail)-1].RN)
	}
}

func TestApplyFailureSummaryContentLimitsPreservesWarnings(t *testing.T) {
	content := strings.Repeat("x", failureSummaryEntryContentByteLimit)
	result := BuildFailureSummary{
		Annotations: make([]FailureSummaryAnnotation, failureSummaryAnnotationContentLimit/failureSummaryEntryContentByteLimit),
		Warnings:    []string{"annotations unavailable after partial scan: request failed"},
	}
	for i := range result.Annotations {
		result.Annotations[i].BodyHTML = content
	}

	applyFailureSummaryContentLimits(&result)

	require.Equal(t, "annotations unavailable after partial scan: request failed", result.Warnings[0])
	require.True(t, result.ContentTruncated)
	require.True(t, result.Annotations[len(result.Annotations)-1].BodyTruncated)
}

func TestFailureSummaryAnnotationsBoundsLargeBodies(t *testing.T) {
	body := strings.Repeat("annotation line\n", 100_000)

	annotations, truncated := failureSummaryAnnotations([]buildkite.Annotation{{
		Style:    "error",
		BodyHTML: body,
	}}, 1)

	require.False(t, truncated)
	require.Len(t, annotations, 1)
	require.LessOrEqual(t, len(annotations[0].BodyHTML), failureSummaryEntryContentByteLimit)
	require.True(t, annotations[0].BodyTruncated)
	require.NotEqual(t, body, annotations[0].BodyHTML)
}

func TestLoadFailureAnnotationsStopsAtScanLimit(t *testing.T) {
	pages := 0
	client := &MockAnnotationsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.AnnotationListOptions) ([]buildkite.Annotation, *buildkite.Response, error) {
			pages++
			return []buildkite.Annotation{{Style: "info", BodyHTML: "not relevant"}}, &buildkite.Response{NextPage: options.Page + 1}, nil
		},
	}

	annotations, truncated, err := loadFailureAnnotations(context.Background(), client, GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
	}, defaultFailureSummaryAnnotations)

	require.NoError(t, err)
	require.Empty(t, annotations)
	require.True(t, truncated)
	require.Equal(t, failureSummaryAnnotationScanPages, pages)
}

func failureSummaryTestBuild(finished bool) buildkite.Build {
	build := buildkite.Build{
		ID:     "build-uuid",
		Number: 42,
		State:  "failed",
		TestEngine: &buildkite.TestEngineProperty{Runs: []buildkite.TestEngineRun{
			{ID: "run-1", Suite: buildkite.TestEngineSuite{Slug: "suite-1"}},
		}},
	}
	if finished {
		build.FinishedAt = buildkite.NewTimestamp(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC))
	}
	return build
}

func failureSummaryTestJobsClient(anchorJobID string) *MockJobsClient {
	return &MockJobsClient{
		ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			switch options.State[0] {
			case "failed":
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: anchorJobID, Name: "rspec", State: "failed"},
				}}, &buildkite.Response{}, nil
			case "broken":
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "job-broken", Name: "deploy", State: "broken"},
				}}, &buildkite.Response{}, nil
			default:
				return buildkite.JobsList{}, &buildkite.Response{}, nil
			}
		},
	}
}

func finishedTestRunsClient(t *testing.T) *MockTestRunsClient {
	return &MockTestRunsClient{
		GetFunc: func(_ context.Context, org, slug, runID string) (buildkite.TestRun, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			return buildkite.TestRun{ID: runID, State: "finished"}, &buildkite.Response{}, nil
		},
	}
}

func TestGetBuildFailureSummaryIncludesJobAnchoredFailedTests(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(_ context.Context, _, _, _ string, options *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			require.True(t, options.IncludeTestEngine)
			return failureSummaryTestBuild(true), &buildkite.Response{}, nil
		},
	}

	buildTestsCalls := 0
	buildTestsClient := &MockBuildTestsClient{
		ListFunc: func(_ context.Context, org, buildUUID string, opt *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			buildTestsCalls++
			require.Equal(t, "org", org)
			require.Equal(t, "build-uuid", buildUUID)
			require.Equal(t, "build.job_id:job-failed,result:^failed", opt.Tags)
			require.Equal(t, "enabled", opt.State, "muted and skipped tests are never failure causes")
			require.Equal(t, defaultFailureSummaryFailedTests, opt.PerPage)
			return []buildkite.TestWithMetrics{
				{Test: buildkite.Test{
					ID:       "test-a",
					Name:     "a always fails",
					Scope:    "Feature A",
					Location: "./spec/a_spec.rb:1",
					URL:      "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-1/tests/test-a",
					WebURL:   "https://buildkite.com/organizations/org/analytics/suites/suite-1/tests/test-a",
				}},
				{Test: buildkite.Test{
					ID:       "test-b",
					Name:     "b always fails",
					Location: "./spec/b_spec.rb:2",
					URL:      "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-1/tests/test-b",
				}},
			}, &buildkite.Response{}, nil
		},
	}

	older := buildkite.NewTimestamp(time.Date(2026, 9, 10, 10, 0, 0, 0, time.UTC))
	newer := buildkite.NewTimestamp(time.Date(2026, 9, 10, 11, 0, 0, 0, time.UTC))
	testExecutionsClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(_ context.Context, _, slug, runID string, opt *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			require.Equal(t, "suite-1", slug)
			require.Equal(t, "run-1", runID)
			require.Equal(t, failureSummaryRunExecutionsPageSize, opt.PerPage)
			return []buildkite.FailedExecution{
				{TestID: "test-a", FailureReason: "older failure", CreatedAt: older},
				{TestID: "test-a", FailureReason: "newest failure", CreatedAt: newer},
				{TestID: "test-rescued", FailureReason: "passed on retry", CreatedAt: older},
				{TestID: "test-b", FailureReason: "b failed", CreatedAt: older},
			}, &buildkite.Response{}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:         buildsClient,
		JobsClient:           failureSummaryTestJobsClient("job-failed"),
		BuildTestsClient:     buildTestsClient,
		TestRunsClient:       finishedTestRunsClient(t),
		TestExecutionsClient: testExecutionsClient,
	})
	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42",
	})
	require.NoError(t, err)
	require.False(t, callResult.IsError)

	text := getTextResult(t, callResult).Text
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.Equal(t, testEngineStatusActive, summary.TestEngine)
	require.Equal(t, 1, buildTestsCalls, "only the terminal failed job should be queried")
	require.Empty(t, summary.Warnings)

	require.Len(t, summary.Jobs, 2)
	anchor := summary.Jobs[0]
	require.Equal(t, "job-failed", anchor.ID)
	require.Equal(t, failedTestsStatusFound, anchor.FailedTestsStatus)
	require.Empty(t, anchor.FailedTestsHint, "a settled found list carries no hint")
	require.False(t, anchor.FailedTestsTruncated)
	require.Len(t, anchor.FailedTests, 2)

	require.Equal(t, "test-a", anchor.FailedTests[0].TestID)
	require.Equal(t, "newest failure", anchor.FailedTests[0].FailureReason)
	require.Equal(t, "suite-1", anchor.FailedTests[0].TestSuiteSlug)
	require.Equal(t, "run-1", anchor.FailedTests[0].RunID)
	require.Equal(t, "b failed", anchor.FailedTests[1].FailureReason)

	broken := summary.Jobs[1]
	require.Equal(t, "job-broken", broken.ID)
	require.Empty(t, broken.FailedTestsStatus, "non-anchor jobs carry no test fields")

	require.NotContains(t, text, "test-rescued")
	require.NotContains(t, text, "executions_count")
	require.NotContains(t, text, "duration_avg")
}

func TestGetBuildFailureSummaryMarksTestEngineNoData(t *testing.T) {
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{ID: "build-uuid", Number: 42, State: "failed"}, &buildkite.Response{}, nil
		},
	}
	buildTestsClient := &MockBuildTestsClient{
		ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			require.Fail(t, "build tests must not be listed for builds without Test Engine data")
			return nil, nil, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:     buildsClient,
		JobsClient:       failureSummaryTestJobsClient("job-failed"),
		BuildTestsClient: buildTestsClient,
	})
	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42",
	})
	require.NoError(t, err)
	require.False(t, callResult.IsError)

	text := getTextResult(t, callResult).Text
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.Equal(t, testEngineStatusNoData, summary.TestEngine)
	require.NotContains(t, text, "failed_tests_status")
}

func TestGetBuildFailureSummaryFailedTestsStatuses(t *testing.T) {
	cases := []struct {
		name       string
		runState   string
		finished   bool
		tests      []buildkite.TestWithMetrics
		wantStatus string
		wantHint   string
	}{
		{
			name:       "settled and empty is none_recorded",
			runState:   "finished",
			finished:   true,
			wantStatus: failedTestsStatusNoneRecorded,
			wantHint:   failedTestsHintNoneRecorded,
		},
		{
			name:       "unsettled run and empty is ingestion_pending",
			runState:   "running",
			finished:   true,
			wantStatus: failedTestsStatusIngestionPending,
			wantHint:   failedTestsHintIngestionPending,
		},
		{
			name:     "unsettled build with results is found with partial hint",
			runState: "finished",
			finished: false,
			tests: []buildkite.TestWithMetrics{
				{Test: buildkite.Test{ID: "test-a", Name: "a", URL: "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-1/tests/test-a"}},
			},
			wantStatus: failedTestsStatusFound,
			wantHint:   failedTestsHintIngestionPartial,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			buildsClient := &MockBuildsClient{
				GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
					return failureSummaryTestBuild(testCase.finished), &buildkite.Response{}, nil
				},
			}
			buildTestsClient := &MockBuildTestsClient{
				ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
					return testCase.tests, &buildkite.Response{}, nil
				},
			}
			testRunsClient := &MockTestRunsClient{
				GetFunc: func(context.Context, string, string, string) (buildkite.TestRun, *buildkite.Response, error) {
					return buildkite.TestRun{State: testCase.runState}, &buildkite.Response{}, nil
				},
			}

			ctx := ContextWithDeps(context.Background(), ToolDependencies{
				BuildsClient:     buildsClient,
				JobsClient:       failureSummaryTestJobsClient("job-failed"),
				BuildTestsClient: buildTestsClient,
				TestRunsClient:   testRunsClient,
			})
			_, handler, _ := GetBuildFailureSummary()
			callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
				OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42",
			})
			require.NoError(t, err)
			require.False(t, callResult.IsError)

			var summary BuildFailureSummary
			require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
			require.Equal(t, testCase.wantStatus, summary.Jobs[0].FailedTestsStatus)
			require.Equal(t, testCase.wantHint, summary.Jobs[0].FailedTestsHint)
			require.Len(t, summary.Jobs[0].FailedTests, len(testCase.tests))
		})
	}
}

func TestGetBuildFailureSummaryFailedTestsForbiddenBecomesUnavailable(t *testing.T) {
	forbidden := &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusForbidden,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "api.buildkite.com"},
			},
		},
		Message: "Your access token is missing the read_suites scope",
	}
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return failureSummaryTestBuild(true), &buildkite.Response{}, nil
		},
	}
	buildTestsClient := &MockBuildTestsClient{
		ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			return nil, nil, forbidden
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:     buildsClient,
		JobsClient:       failureSummaryTestJobsClient("job-failed"),
		BuildTestsClient: buildTestsClient,
		TestRunsClient:   finishedTestRunsClient(t),
	})
	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42",
	})
	require.NoError(t, err)
	require.False(t, callResult.IsError)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, failedTestsStatusUnavailable, summary.Jobs[0].FailedTestsStatus)
	require.Equal(t, failedTestsHintUnavailable, summary.Jobs[0].FailedTestsHint)
	require.Empty(t, summary.Jobs[0].FailedTests)
	require.Len(t, summary.Warnings, 1)
	require.Contains(t, summary.Warnings[0], forbidden.Message)
}

func TestLoadFailureJobTestsPropagatesUnauthorized(t *testing.T) {
	unauthorized := fmt.Errorf("wrapped API failure: %w", &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusUnauthorized,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "api.buildkite.com"},
			},
		},
	})
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}
	build := failureSummaryTestBuild(true)
	sourceJobs := []buildkite.Job{{ID: "job-failed", State: "failed"}}

	t.Run("build tests list", func(t *testing.T) {
		deps := ToolDependencies{
			BuildTestsClient: &MockBuildTestsClient{
				ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
					return nil, nil, unauthorized
				},
			},
		}
		jobs := make([]FailureSummaryJob, 1)
		_, err := loadFailureJobTests(context.Background(), deps, args, build, sourceJobs, jobs, defaultFailureSummaryFailedTests, defaultFailureSummaryTestRuns, true)
		require.ErrorIs(t, err, ErrUnauthorized)
	})

	t.Run("failed executions", func(t *testing.T) {
		deps := ToolDependencies{
			BuildTestsClient: &MockBuildTestsClient{
				ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
					return []buildkite.TestWithMetrics{{Test: buildkite.Test{ID: "test-a"}}}, &buildkite.Response{}, nil
				},
			},
			TestExecutionsClient: &MockTestExecutionsClient{
				GetFailedExecutionsFunc: func(context.Context, string, string, string, *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
					return nil, nil, unauthorized
				},
			},
		}
		jobs := make([]FailureSummaryJob, 1)
		_, err := loadFailureJobTests(context.Background(), deps, args, build, sourceJobs, jobs, defaultFailureSummaryFailedTests, defaultFailureSummaryTestRuns, true)
		require.ErrorIs(t, err, ErrUnauthorized)
	})
}

func TestLoadFailureJobTestsBoundsRunsAndReportsWarnings(t *testing.T) {
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}
	build := buildkite.Build{
		ID:         "build-uuid",
		FinishedAt: buildkite.NewTimestamp(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)),
		TestEngine: &buildkite.TestEngineProperty{Runs: []buildkite.TestEngineRun{
			{ID: "run-1", Suite: buildkite.TestEngineSuite{Slug: "suite-1"}},
			{ID: "run-2", Suite: buildkite.TestEngineSuite{Slug: "suite-2"}},
			{ID: "run-3", Suite: buildkite.TestEngineSuite{Slug: "suite-3"}},
		}},
	}
	sourceJobs := []buildkite.Job{{ID: "job-failed", State: "failed"}}
	deps := ToolDependencies{
		BuildTestsClient: &MockBuildTestsClient{
			ListFunc: func(_ context.Context, _, _ string, opt *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
				require.Equal(t, 2, opt.PerPage)
				return []buildkite.TestWithMetrics{
					{Test: buildkite.Test{ID: "test-a", URL: "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-1/tests/test-a"}},
					{Test: buildkite.Test{ID: "test-b", URL: "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-2/tests/test-b"}},
				}, &buildkite.Response{NextPage: 2}, nil
			},
		},
		TestExecutionsClient: &MockTestExecutionsClient{
			GetFailedExecutionsFunc: func(_ context.Context, _, _, runID string, _ *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
				switch runID {
				case "run-1":
					return []buildkite.FailedExecution{{TestID: "test-a", FailureReason: "boom"}}, &buildkite.Response{}, nil
				case "run-2":
					return nil, nil, errors.New("executions endpoint unavailable")
				default:
					return nil, nil, fmt.Errorf("unexpected run scanned: %s", runID)
				}
			},
		},
	}

	jobs := make([]FailureSummaryJob, 1)
	warnings, err := loadFailureJobTests(context.Background(), deps, args, build, sourceJobs, jobs, 2, 2, true)
	require.NoError(t, err)
	require.Equal(t, failedTestsStatusFound, jobs[0].FailedTestsStatus)
	require.True(t, jobs[0].FailedTestsTruncated)
	require.Len(t, jobs[0].FailedTests, 2)
	require.Equal(t, "boom", jobs[0].FailedTests[0].FailureReason)
	require.Equal(t, "run-1", jobs[0].FailedTests[0].RunID)
	require.Empty(t, jobs[0].FailedTests[0].FailureDetailStatus)
	require.Empty(t, jobs[0].FailedTests[1].FailureReason)
	require.Equal(t, failureDetailStatusNotRetrieved, jobs[0].FailedTests[1].FailureDetailStatus, "a run lookup error leaves its tests marked not_retrieved")
	require.Equal(t, "suite-2", jobs[0].FailedTests[1].TestSuiteSlug, "suite slug falls back to the test URL when detail is missing")
	require.Equal(t, "run-2", jobs[0].FailedTests[1].RunID, "run id falls back to the suite's run")
	require.Len(t, warnings, 1, "run-3 holds no returned test, so leaving it unscanned is not a cap warning")
	require.Contains(t, warnings[0], "run-2")
	require.Contains(t, warnings[0], "executions endpoint unavailable")
}

func TestLoadFailureJobTestsScansRunsHoldingTheMostFailedTests(t *testing.T) {
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}
	build := buildkite.Build{
		ID:         "build-uuid",
		FinishedAt: buildkite.NewTimestamp(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)),
		TestEngine: &buildkite.TestEngineProperty{Runs: []buildkite.TestEngineRun{
			{ID: "run-1", Suite: buildkite.TestEngineSuite{Slug: "suite-1"}},
			{ID: "run-2", Suite: buildkite.TestEngineSuite{Slug: "suite-2"}},
			{ID: "run-3", Suite: buildkite.TestEngineSuite{Slug: "suite-3"}},
			{ID: "run-4", Suite: buildkite.TestEngineSuite{Slug: "suite-4"}},
		}},
	}
	testURL := func(suite, id string) string {
		return fmt.Sprintf("https://api.buildkite.com/v2/analytics/organizations/org/suites/%s/tests/%s", suite, id)
	}
	sourceJobs := []buildkite.Job{{ID: "job-failed", State: "failed"}}

	var scannedMu sync.Mutex
	var scanned []string
	deps := ToolDependencies{
		BuildTestsClient: &MockBuildTestsClient{
			ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
				return []buildkite.TestWithMetrics{
					{Test: buildkite.Test{ID: "s4-a", URL: testURL("suite-4", "s4-a")}},
					{Test: buildkite.Test{ID: "s3-a", URL: testURL("suite-3", "s3-a")}},
					{Test: buildkite.Test{ID: "s1-a", URL: testURL("suite-1", "s1-a")}},
					{Test: buildkite.Test{ID: "s3-b", URL: testURL("suite-3", "s3-b")}},
				}, &buildkite.Response{}, nil
			},
		},
		TestExecutionsClient: &MockTestExecutionsClient{
			GetFailedExecutionsFunc: func(_ context.Context, _, _, runID string, _ *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
				scannedMu.Lock()
				scanned = append(scanned, runID)
				scannedMu.Unlock()
				switch runID {
				case "run-3":
					return []buildkite.FailedExecution{{TestID: "s3-a", FailureReason: "s3-a boom"}, {TestID: "s3-b", FailureReason: "s3-b boom"}}, &buildkite.Response{}, nil
				case "run-1":
					return []buildkite.FailedExecution{{TestID: "s1-a", FailureReason: "s1-a boom"}}, &buildkite.Response{}, nil
				default:
					return nil, nil, fmt.Errorf("run %s holds returned tests only when ranked in", runID)
				}
			},
		},
	}

	jobs := make([]FailureSummaryJob, 1)
	warnings, err := loadFailureJobTests(context.Background(), deps, args, build, sourceJobs, jobs, 10, 2, true)
	require.NoError(t, err)

	slices.Sort(scanned)
	require.Equal(t, []string{"run-1", "run-3"}, scanned, "suite-3 holds two returned tests, then suite-1 wins the tie with suite-4 by slug; suite-2 holds none")

	byID := map[string]FailureSummaryFailedTest{}
	for _, entry := range jobs[0].FailedTests {
		byID[entry.TestID] = entry
	}
	require.Equal(t, "s3-a boom", byID["s3-a"].FailureReason)
	require.Equal(t, "s3-b boom", byID["s3-b"].FailureReason)
	require.Equal(t, "s1-a boom", byID["s1-a"].FailureReason)
	require.Empty(t, byID["s3-a"].FailureDetailStatus)
	require.Empty(t, byID["s4-a"].FailureReason)
	require.Equal(t, failureDetailStatusNotRetrieved, byID["s4-a"].FailureDetailStatus, "the run past the cap leaves its test marked, with handles for drill-down")
	require.Equal(t, "suite-4", byID["s4-a"].TestSuiteSlug)
	require.Equal(t, "run-4", byID["s4-a"].RunID)

	require.Len(t, warnings, 1)
	require.Contains(t, warnings[0], "2 most affected of 3 Test Engine runs")
	require.Contains(t, warnings[0], "max_test_runs")
}

func TestLoadFailureJobTestsFallsBackToBuildOrderForUnrankedTests(t *testing.T) {
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}
	build := buildkite.Build{
		ID:         "build-uuid",
		FinishedAt: buildkite.NewTimestamp(time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)),
		TestEngine: &buildkite.TestEngineProperty{Runs: []buildkite.TestEngineRun{
			{ID: "run-1", Suite: buildkite.TestEngineSuite{Slug: "suite-1"}},
			{ID: "run-2", Suite: buildkite.TestEngineSuite{Slug: "suite-2"}},
		}},
	}
	sourceJobs := []buildkite.Job{{ID: "job-failed", State: "failed"}}
	var scannedMu sync.Mutex
	var scanned []string
	deps := ToolDependencies{
		BuildTestsClient: &MockBuildTestsClient{
			ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
				return []buildkite.TestWithMetrics{
					{Test: buildkite.Test{ID: "no-suite", URL: "https://api.buildkite.com/v2/analytics/organizations/org/tests/no-suite"}},
				}, &buildkite.Response{}, nil
			},
		},
		TestExecutionsClient: &MockTestExecutionsClient{
			GetFailedExecutionsFunc: func(_ context.Context, _, _, runID string, _ *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
				scannedMu.Lock()
				scanned = append(scanned, runID)
				scannedMu.Unlock()
				if runID == "run-2" {
					return []buildkite.FailedExecution{{TestID: "no-suite", FailureReason: "found in build order"}}, &buildkite.Response{}, nil
				}
				return nil, &buildkite.Response{}, nil
			},
		},
	}

	jobs := make([]FailureSummaryJob, 1)
	warnings, err := loadFailureJobTests(context.Background(), deps, args, build, sourceJobs, jobs, 10, 5, true)
	require.NoError(t, err)
	require.Empty(t, warnings)
	slices.Sort(scanned)
	require.Equal(t, []string{"run-1", "run-2"}, scanned, "a test with no readable suite falls back to scanning runs in build order")
	require.Equal(t, "found in build order", jobs[0].FailedTests[0].FailureReason)
	require.Equal(t, "suite-2", jobs[0].FailedTests[0].TestSuiteSlug, "the matching run fills in the suite the URL could not")
	require.Empty(t, jobs[0].FailedTests[0].FailureDetailStatus)
}

func TestLoadFailureJobTestsMarksNotRetrievedWithoutExecutionsClient(t *testing.T) {
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}
	sourceJobs := []buildkite.Job{{ID: "job-failed", State: "failed"}}
	deps := ToolDependencies{
		BuildTestsClient: &MockBuildTestsClient{
			ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
				return []buildkite.TestWithMetrics{
					{Test: buildkite.Test{ID: "test-a", URL: "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-1/tests/test-a"}},
				}, &buildkite.Response{}, nil
			},
		},
	}

	jobs := make([]FailureSummaryJob, 1)
	warnings, err := loadFailureJobTests(context.Background(), deps, args, failureSummaryTestBuild(true), sourceJobs, jobs, 10, 5, true)
	require.NoError(t, err)
	require.Empty(t, warnings)
	require.Equal(t, failureDetailStatusNotRetrieved, jobs[0].FailedTests[0].FailureDetailStatus, "no scan ran, so the entry must say its detail was not retrieved")
}

func TestFailureSummaryTestsIngestionSettled(t *testing.T) {
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}

	t.Run("unfinished build is unsettled", func(t *testing.T) {
		settled, err := failureSummaryTestsIngestionSettled(context.Background(), finishedTestRunsClient(t), args, failureSummaryTestBuild(false))
		require.NoError(t, err)
		require.False(t, settled)
	})

	t.Run("run state errors are unsettled", func(t *testing.T) {
		client := &MockTestRunsClient{
			GetFunc: func(context.Context, string, string, string) (buildkite.TestRun, *buildkite.Response, error) {
				return buildkite.TestRun{}, nil, errors.New("boom")
			},
		}
		settled, err := failureSummaryTestsIngestionSettled(context.Background(), client, args, failureSummaryTestBuild(true))
		require.NoError(t, err)
		require.False(t, settled)
	})

	t.Run("finished build with finished runs is settled", func(t *testing.T) {
		settled, err := failureSummaryTestsIngestionSettled(context.Background(), finishedTestRunsClient(t), args, failureSummaryTestBuild(true))
		require.NoError(t, err)
		require.True(t, settled)
	})

	t.Run("every listed run is checked regardless of the scan cap", func(t *testing.T) {
		build := failureSummaryTestBuild(true)
		for i := 2; i <= defaultFailureSummaryTestRuns+1; i++ {
			build.TestEngine.Runs = append(build.TestEngine.Runs, buildkite.TestEngineRun{ID: fmt.Sprintf("run-%d", i), Suite: buildkite.TestEngineSuite{Slug: fmt.Sprintf("suite-%d", i)}})
		}
		var checked atomic.Int32
		client := &MockTestRunsClient{
			GetFunc: func(_ context.Context, _, _, runID string) (buildkite.TestRun, *buildkite.Response, error) {
				checked.Add(1)
				return buildkite.TestRun{ID: runID, State: "finished"}, &buildkite.Response{}, nil
			},
		}
		settled, err := failureSummaryTestsIngestionSettled(context.Background(), client, args, build)
		require.NoError(t, err)
		require.True(t, settled, "six finished runs settle even though the detail scan cap is five")
		require.Equal(t, len(build.TestEngine.Runs), int(checked.Load()))
	})

	t.Run("one unfinished run among many is unsettled", func(t *testing.T) {
		build := failureSummaryTestBuild(true)
		build.TestEngine.Runs = append(build.TestEngine.Runs, buildkite.TestEngineRun{ID: "run-2", Suite: buildkite.TestEngineSuite{Slug: "suite-2"}})
		client := &MockTestRunsClient{
			GetFunc: func(_ context.Context, _, _, runID string) (buildkite.TestRun, *buildkite.Response, error) {
				state := "finished"
				if runID == "run-2" {
					state = "running"
				}
				return buildkite.TestRun{ID: runID, State: state}, &buildkite.Response{}, nil
			},
		}
		settled, err := failureSummaryTestsIngestionSettled(context.Background(), client, args, build)
		require.NoError(t, err)
		require.False(t, settled)
	})
}

func TestLoadFailureJobTestsBudgetExhaustedJobStaysFound(t *testing.T) {
	args := GetBuildFailureSummaryArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1"}
	build := failureSummaryTestBuild(true)
	sourceJobs := []buildkite.Job{{ID: "job-first", State: "failed"}, {ID: "job-second", State: "failed"}}
	deps := ToolDependencies{
		BuildTestsClient: &MockBuildTestsClient{
			ListFunc: func(_ context.Context, _, _ string, opt *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
				switch opt.Tags {
				case "build.job_id:job-first,result:^failed":
					return []buildkite.TestWithMetrics{{Test: buildkite.Test{ID: "test-a", Name: "a"}}}, &buildkite.Response{}, nil
				case "build.job_id:job-second,result:^failed":
					return []buildkite.TestWithMetrics{{Test: buildkite.Test{ID: "test-b", Name: "b"}}}, &buildkite.Response{}, nil
				default:
					return nil, nil, fmt.Errorf("unexpected tags: %s", opt.Tags)
				}
			},
		},
	}

	jobs := make([]FailureSummaryJob, 2)
	warnings, err := loadFailureJobTests(context.Background(), deps, args, build, sourceJobs, jobs, 1, defaultFailureSummaryTestRuns, true)
	require.NoError(t, err)
	require.Empty(t, warnings)

	require.Equal(t, failedTestsStatusFound, jobs[0].FailedTestsStatus)
	require.False(t, jobs[0].FailedTestsTruncated)
	require.Len(t, jobs[0].FailedTests, 1)
	require.Empty(t, jobs[0].FailedTestsHint)

	require.Equal(t, failedTestsStatusFound, jobs[1].FailedTestsStatus, "a job starved by the budget is still found, not none_recorded")
	require.True(t, jobs[1].FailedTestsTruncated)
	require.Empty(t, jobs[1].FailedTests)
	require.Equal(t, fmt.Sprintf(failedTestsHintBudgetExhausted, "job-second"), jobs[1].FailedTestsHint)
	require.NotContains(t, jobs[1].FailedTestsHint, "outside its tests")
	require.Contains(t, jobs[1].FailedTestsHint, `state "enabled"`, "the manual retry must carry the same muted-test exclusion")
}

func TestTestSuiteSlugFromURL(t *testing.T) {
	require.Equal(t, "suite-1", testSuiteSlugFromURL("https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-1/tests/test-a"))
	require.Empty(t, testSuiteSlugFromURL("https://api.buildkite.com/v2/analytics/organizations/org/tests/test-a"))
	require.Empty(t, testSuiteSlugFromURL(""))
}

func TestLimitJSONValueFlagsTruncatedFailedTestEntries(t *testing.T) {
	long := strings.Repeat("x", 200)
	root := map[string]any{
		"jobs": []any{map[string]any{
			"failed_tests": []any{
				map[string]any{"test_id": "cut-reason", "failure_reason": long},
				map[string]any{"test_id": "cut-expanded", "failure_expanded": []any{map[string]any{"expanded": []any{long}}}},
				map[string]any{"test_id": "intact", "failure_reason": "short"},
			},
		}},
	}

	limitedValue, truncated := limitJSONValue(root, 16, "")
	require.True(t, truncated)
	entries := limitedValue.(map[string]any)["jobs"].([]any)[0].(map[string]any)["failed_tests"].([]any)

	cutReason := entries[0].(map[string]any)
	require.Len(t, cutReason["failure_reason"], 16)
	require.Equal(t, true, cutReason["content_truncated"], "a shortened failure_reason must be flagged on its entry")

	cutExpanded := entries[1].(map[string]any)
	require.Equal(t, true, cutExpanded["content_truncated"], "a shortened expanded line must be flagged on its entry")

	intact := entries[2].(map[string]any)
	require.NotContains(t, intact, "content_truncated")
}

func TestGetBuildFailureSummaryLoweredContentLimitFlagsTruncatedFailedTest(t *testing.T) {
	reason := strings.Repeat("assertion detail ", 200)
	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return failureSummaryTestBuild(true), &buildkite.Response{}, nil
		},
	}
	buildTestsClient := &MockBuildTestsClient{
		ListFunc: func(context.Context, string, string, *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error) {
			return []buildkite.TestWithMetrics{
				{Test: buildkite.Test{ID: "test-a", Name: "a", URL: "https://api.buildkite.com/v2/analytics/organizations/org/suites/suite-1/tests/test-a"}},
			}, &buildkite.Response{}, nil
		},
	}
	executionsClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(context.Context, string, string, string, *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			return []buildkite.FailedExecution{{TestID: "test-a", FailureReason: reason}}, &buildkite.Response{}, nil
		},
	}

	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:         buildsClient,
		JobsClient:           failureSummaryTestJobsClient("job-failed"),
		BuildTestsClient:     buildTestsClient,
		TestExecutionsClient: executionsClient,
		TestRunsClient:       finishedTestRunsClient(t),
	})
	_, handler, _ := GetBuildFailureSummary()
	noLogs := false

	full, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42", IncludeLogs: &noLogs,
	})
	require.NoError(t, err)
	var fullSummary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, full).Text), &fullSummary))
	require.Len(t, fullSummary.Jobs[0].FailedTests, 1)
	require.Equal(t, reason, fullSummary.Jobs[0].FailedTests[0].FailureReason)
	require.False(t, fullSummary.Jobs[0].FailedTests[0].ContentTruncated)

	requested := len(getTextResult(t, full).Text) - 1024
	lowered, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42", IncludeLogs: &noLogs, ContentLimitBytes: requested,
	})
	require.NoError(t, err)
	text := getTextResult(t, lowered).Text
	require.LessOrEqual(t, len(text), requested)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(text), &summary))
	require.True(t, summary.ContentTruncated)
	require.Len(t, summary.Jobs[0].FailedTests, 1, "the entry should be shortened, not dropped")
	entry := summary.Jobs[0].FailedTests[0]
	require.Less(t, len(entry.FailureReason), len(reason))
	require.True(t, entry.ContentTruncated, "a failure_reason cut by content_limit_bytes must be flagged")
}

func TestApplyFailureSummaryContentLimitsBoundsFailedTestContent(t *testing.T) {
	summary := &BuildFailureSummary{
		Jobs: []FailureSummaryJob{{
			FailedTests: []FailureSummaryFailedTest{{
				TestID:        "test",
				Name:          strings.Repeat("n", 2*failureSummaryEntryContentByteLimit),
				FailureReason: strings.Repeat("r", 2*failureSummaryEntryContentByteLimit),
				FailureExpanded: []buildkite.FailureExpanded{{
					Backtrace: []string{strings.Repeat("b", 2*failureSummaryEntryContentByteLimit)},
				}},
			}},
		}},
	}

	applyFailureSummaryContentLimits(summary)

	limited := summary.Jobs[0].FailedTests[0]
	require.True(t, summary.ContentTruncated)
	require.True(t, limited.ContentTruncated)
	require.LessOrEqual(t, len(limited.FailureReason), failureSummaryEntryContentByteLimit)
	require.LessOrEqual(t, len(limited.Name), failureSummaryEntryContentByteLimit)
	require.LessOrEqual(t, len(limited.FailureExpanded[0].Backtrace[0]), failureSummaryEntryContentByteLimit)
}
