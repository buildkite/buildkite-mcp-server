package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
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
			require.True(t, options.IncludeTestEngine)
			return buildkite.Build{
				ID:      "build-id",
				Number:  42,
				State:   "failing",
				Branch:  "main",
				Commit:  "abc123",
				Message: "Fix tests",
				TestEngine: &buildkite.TestEngineProperty{Runs: []buildkite.TestEngineRun{{
					ID:    "run-1",
					Suite: buildkite.TestEngineSuite{Slug: "suite-1"},
				}}},
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
				require.Equal(t, []string{"failed", "timed_out"}, options.State)
				require.Equal(t, defaultFailureSummaryJobs+1, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "job-failed", Name: "compile", State: "failed", Command: "make build", ExitStatus: testPtr(1)},
					{ID: "job-promised", Name: "tests", State: "running", PromisedExitStatus: &promisedExitStatus},
					{ID: "job-running", Name: "unrelated", State: "running"},
				}}, &buildkite.Response{}, nil
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

	type testExecutionCall struct {
		org, suite, runID string
		options           buildkite.FailedExecutionsOptions
	}
	var testExecutionCallsMu sync.Mutex
	var testExecutionCalls []testExecutionCall
	testExecutionsClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(_ context.Context, org, suite, runID string, options *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			testExecutionCallsMu.Lock()
			testExecutionCalls = append(testExecutionCalls, testExecutionCall{org: org, suite: suite, runID: runID, options: *options})
			testExecutionCallsMu.Unlock()
			return []buildkite.FailedExecution{{
				ExecutionID:   "execution-1",
				TestName:      "TestWidgets",
				FailureReason: "expected 2, got 3",
			}}, &buildkite.Response{NextPage: 2}, nil
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
		BuildsClient:         buildsClient,
		JobsClient:           jobsClient,
		AnnotationsClient:    annotationsClient,
		TestExecutionsClient: testExecutionsClient,
		BuildkiteLogsClient:  logsClient,
	})

	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:              "org",
		PipelineSlug:         "pipeline",
		BuildNumber:          "42",
		LogTail:              2,
		MaxFailedTestsPerRun: 7,
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
	require.Len(t, summary.Jobs, 3)
	require.False(t, summary.JobsTruncated)

	require.Equal(t, "job-failed", summary.Jobs[0].ID)
	require.Equal(t, int64(3), summary.Jobs[0].LogTotalRows)
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

	require.Len(t, summary.TestRuns, 1)
	require.Equal(t, "suite-1", summary.TestRuns[0].TestSuiteSlug)
	require.True(t, summary.TestRuns[0].Truncated)
	require.Equal(t, "TestWidgets", summary.TestRuns[0].FailedExecutions[0].TestName)
	require.False(t, summary.TestRunsTruncated)
	require.True(t, summary.FailedTestsTruncated)

	testExecutionCallsMu.Lock()
	require.Equal(t, []testExecutionCall{{
		org: "org", suite: "suite-1", runID: "run-1",
		options: buildkite.FailedExecutionsOptions{Page: 1, PerPage: 7},
	}}, testExecutionCalls)
	testExecutionCallsMu.Unlock()

	logCallsMu.Lock()
	require.Equal(t, map[string][]logCall{
		"job-failed":   {{ttl: 30 * time.Second}},
		"job-promised": {{ttl: 30 * time.Second}},
	}, logCalls)
	logCallsMu.Unlock()
}

func TestGetBuildFailureSummaryPrioritizesFailuresBeforeBrokenJobs(t *testing.T) {
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
				require.Equal(t, []string{"failed", "timed_out"}, options.State)
				require.Equal(t, 3, options.PerPage)
				return buildkite.JobsList{Items: []buildkite.Job{
					{ID: "failed", State: "failed"},
					{ID: "promised", State: "running", PromisedExitStatus: &promisedExitStatus},
				}}, &buildkite.Response{}, nil
			case "broken":
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
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1", MaxJobs: 2,
		IncludeLogs: &include, IncludeAnnotations: &include, IncludeFailedTests: &include,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, []string{"failed", "promised"}, []string{summary.Jobs[0].ID, summary.Jobs[1].ID})
	require.True(t, summary.JobsTruncated)
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
				require.Equal(t, []string{"failed", "timed_out"}, options.State)
				return buildkite.JobsList{Items: []buildkite.Job{{ID: "timed-out", State: "timed_out"}}}, &buildkite.Response{}, nil
			case "broken":
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
		IncludeAnnotations: &include, IncludeFailedTests: &include,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Len(t, summary.Jobs, 1)
	require.Equal(t, "timed_out", summary.Jobs[0].State)
	require.Equal(t, []string{"running", "job timed out"}, []string{summary.Jobs[0].LogTail[0].C, summary.Jobs[0].LogTail[1].C})
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
		IncludeFailedTests: &include,
	})

	require.NoError(t, err)
	require.False(t, callResult.IsError)
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Equal(t, "passed", summary.Build.State)
	require.Empty(t, summary.Jobs)
	require.Empty(t, summary.Annotations)
	require.Empty(t, summary.TestRuns)
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

func TestGetBuildFailureSummaryBoundsTestEngineWork(t *testing.T) {
	runs := make([]buildkite.TestEngineRun, 4)
	for i := range runs {
		runs[i] = buildkite.TestEngineRun{
			ID:    fmt.Sprintf("run-%d", i+1),
			Suite: buildkite.TestEngineSuite{Slug: fmt.Sprintf("suite-%d", i+1)},
		}
	}

	buildsClient := &MockBuildsClient{
		GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
			return buildkite.Build{Number: 1, State: "failed", TestEngine: &buildkite.TestEngineProperty{Runs: runs}}, &buildkite.Response{}, nil
		},
	}
	jobsClient := &MockJobsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
			return buildkite.JobsList{}, &buildkite.Response{}, nil
		},
	}

	var callsMu sync.Mutex
	calls := map[string]int{}
	testExecutionsClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(_ context.Context, _, _, runID string, options *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			callsMu.Lock()
			calls[runID] = options.PerPage
			callsMu.Unlock()

			executions := make([]buildkite.FailedExecution, options.PerPage)
			for i := range executions {
				executions[i] = buildkite.FailedExecution{ExecutionID: fmt.Sprintf("%s-execution-%d", runID, i+1)}
			}
			response := &buildkite.Response{}
			if runID == "run-1" {
				response.NextPage = 2
			}
			return executions, response, nil
		},
	}

	include := false
	ctx := ContextWithDeps(context.Background(), ToolDependencies{
		BuildsClient:         buildsClient,
		JobsClient:           jobsClient,
		TestExecutionsClient: testExecutionsClient,
	})
	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
		OrgSlug:              "org",
		PipelineSlug:         "pipeline",
		BuildNumber:          "1",
		IncludeLogs:          &include,
		IncludeAnnotations:   &include,
		MaxTestRuns:          2,
		MaxFailedTests:       3,
		MaxFailedTestsPerRun: 2,
	})
	require.NoError(t, err)

	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	require.Len(t, summary.TestRuns, 2)
	require.True(t, summary.TestRunsTruncated)
	require.True(t, summary.FailedTestsTruncated)
	require.Len(t, summary.TestRuns[0].FailedExecutions, 2)
	require.Len(t, summary.TestRuns[1].FailedExecutions, 1)

	callsMu.Lock()
	require.Equal(t, map[string]int{"run-1": 2, "run-2": 2}, calls)
	callsMu.Unlock()
}

func TestLoadFailureTestRunsInspectsMaxRunsIndependentlyOfMaxTotal(t *testing.T) {
	runs := make([]buildkite.TestEngineRun, 20)
	for i := range runs {
		runs[i] = buildkite.TestEngineRun{
			ID:    fmt.Sprintf("run-%d", i+1),
			Suite: buildkite.TestEngineSuite{Slug: fmt.Sprintf("suite-%d", i+1)},
		}
	}

	var callsMu sync.Mutex
	calls := map[string]int{}
	client := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(_ context.Context, _, _, runID string, options *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			callsMu.Lock()
			calls[runID] = options.PerPage
			callsMu.Unlock()
			if runID == "run-20" {
				return []buildkite.FailedExecution{{ExecutionID: "execution-1"}}, &buildkite.Response{}, nil
			}
			return nil, &buildkite.Response{}, nil
		},
	}

	results, runsTruncated, failedTestsTruncated, err := loadFailureTestRuns(
		context.Background(), client, GetBuildFailureSummaryArgs{OrgSlug: "org"}, runs, 20, 20, 1,
	)

	require.NoError(t, err)
	require.Len(t, results, 20)
	require.False(t, runsTruncated)
	require.False(t, failedTestsTruncated)
	require.Empty(t, results[0].FailedExecutions)
	require.Equal(t, "execution-1", results[19].FailedExecutions[0].ExecutionID)
	callsMu.Lock()
	require.Len(t, calls, 20)
	for _, perPage := range calls {
		require.Equal(t, 1, perPage)
	}
	callsMu.Unlock()
}

func TestLoadFailureTestRunsRedistributesUnusedCapacity(t *testing.T) {
	client := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(_ context.Context, _, _, runID string, options *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			require.Equal(t, 2, options.PerPage)
			if runID == "run-1" {
				return nil, &buildkite.Response{}, nil
			}
			return []buildkite.FailedExecution{{ExecutionID: "execution-1"}, {ExecutionID: "execution-2"}}, &buildkite.Response{}, nil
		},
	}
	runs := []buildkite.TestEngineRun{
		{ID: "run-1", Suite: buildkite.TestEngineSuite{Slug: "suite-1"}},
		{ID: "run-2", Suite: buildkite.TestEngineSuite{Slug: "suite-2"}},
	}

	results, runsTruncated, failedTestsTruncated, err := loadFailureTestRuns(
		context.Background(), client, GetBuildFailureSummaryArgs{OrgSlug: "org"}, runs, 2, 2, 3,
	)

	require.NoError(t, err)
	require.False(t, runsTruncated)
	require.False(t, failedTestsTruncated)
	require.Empty(t, results[0].FailedExecutions)
	require.Len(t, results[1].FailedExecutions, 2)
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

	t.Run("test executions", func(t *testing.T) {
		client := &MockTestExecutionsClient{
			GetFailedExecutionsFunc: func(context.Context, string, string, string, *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
				return nil, nil, unauthorized
			},
		}
		runs := []buildkite.TestEngineRun{{ID: "run", Suite: buildkite.TestEngineSuite{Slug: "suite"}}}

		_, _, _, err := loadFailureTestRuns(context.Background(), client, args, runs, 1, 1, 1)
		require.ErrorIs(t, err, ErrUnauthorized)
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

	testClient := &MockTestExecutionsClient{
		GetFailedExecutionsFunc: func(context.Context, string, string, string, *buildkite.FailedExecutionsOptions) ([]buildkite.FailedExecution, *buildkite.Response, error) {
			return nil, nil, errors.New("tests unavailable")
		},
	}
	runs, _, failedTestsTruncated, err := loadFailureTestRuns(context.Background(), testClient, args, []buildkite.TestEngineRun{{ID: "run", Suite: buildkite.TestEngineSuite{Slug: "suite"}}}, 1, 1, 1)
	require.NoError(t, err)
	require.Contains(t, runs[0].Error, "tests unavailable")
	require.True(t, runs[0].Truncated)
	require.True(t, failedTestsTruncated)
}

func TestReadFailureLogTailBoundsEntryContent(t *testing.T) {
	logPath := t.TempDir() + "/large.parquet"
	writeTestParquetFile(t, logPath, []string{strings.Repeat("é", failureSummaryEntryContentByteLimit)})
	client := &MockBuildkiteLogsClient{
		NewReaderFunc: func(context.Context, string, string, string, string, time.Duration, bool) (*buildkitelogs.ParquetReader, error) {
			return buildkitelogs.NewParquetReader(logPath), nil
		},
	}

	entries, _, _, contentTruncated, _, err := readFailureLogTail(context.Background(), client, GetBuildFailureSummaryArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
	}, buildkite.Job{ID: "job"}, 1)

	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.LessOrEqual(t, len(entries[0].C), failureSummaryEntryContentByteLimit)
	require.True(t, entries[0].ContentTruncated)
	require.True(t, contentTruncated)
	require.True(t, utf8.ValidString(entries[0].C))
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

func TestApplyFailureSummaryContentLimitsBoundsAggregateAndExpandedFailures(t *testing.T) {
	const jobs = 10
	const entriesPerJob = 50
	content := strings.Repeat("x", failureSummaryEntryContentByteLimit)
	result := BuildFailureSummary{
		Jobs:        make([]FailureSummaryJob, jobs),
		Annotations: make([]FailureSummaryAnnotation, 20),
		TestRuns: []FailureSummaryTestRun{{
			FailedExecutions: make([]FailureSummaryFailedExecution, 10),
		}},
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
	for i := range result.TestRuns[0].FailedExecutions {
		execution := &result.TestRuns[0].FailedExecutions[i]
		execution.FailureReason = content
		execution.FailureExpanded = []buildkite.FailureExpanded{{
			Backtrace: []string{content, content},
			Expanded:  []string{content, content},
		}}
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
	require.True(t, result.TestRuns[0].ContentTruncated)
	for _, execution := range result.TestRuns[0].FailedExecutions {
		require.True(t, execution.ContentTruncated)
	}
}

func TestLimitFailureExpandedBoundsEmptyArrayStructure(t *testing.T) {
	tests := map[string][]buildkite.FailureExpanded{
		"failure expanded items": make([]buildkite.FailureExpanded, failureSummaryContentByteLimit),
		"backtrace items": {{
			Backtrace: make([]string, failureSummaryContentByteLimit),
		}},
		"expanded items": {{
			Expanded: make([]string, failureSummaryContentByteLimit),
		}},
	}

	for name, values := range tests {
		t.Run(name, func(t *testing.T) {
			entryRemaining := failureSummaryExecutionByteLimit
			sectionRemaining := failureSummaryTestContentByteLimit
			limited, truncated := limitFailureExpanded(values, &entryRemaining, &sectionRemaining)

			require.True(t, truncated)
			require.GreaterOrEqual(t, entryRemaining, 0)
			require.GreaterOrEqual(t, sectionRemaining, 0)
			payload, err := json.Marshal(limited)
			require.NoError(t, err)
			require.LessOrEqual(t, len(payload), failureSummaryExecutionByteLimit)
		})
	}
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

	require.NoError(t, limitFailureSummaryLogCollections(&result, failureSummaryContentByteLimit))
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
