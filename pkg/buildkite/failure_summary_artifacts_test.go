package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

// failureSummaryArtifactsTestDeps returns a build whose problem jobs cover
// every artifact eligibility case: job-failed, job-timed-out and job-canceled
// ran; job-expired never reached an agent; job-broken never ran.
func failureSummaryArtifactsTestDeps(artifactsClient ArtifactsClient) ToolDependencies {
	return ToolDependencies{
		BuildsClient: &MockBuildsClient{
			GetFunc: func(context.Context, string, string, string, *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
				return buildkite.Build{Number: 1, State: "failed"}, &buildkite.Response{}, nil
			},
		},
		JobsClient: &MockJobsClient{
			ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
				switch options.State[0] {
				case "failed":
					return buildkite.JobsList{Items: []buildkite.Job{
						{ID: "job-failed", State: "failed"},
						{ID: "job-timed-out", State: "timed_out"},
						{ID: "job-expired", State: "expired"},
					}}, &buildkite.Response{}, nil
				case "canceled":
					return buildkite.JobsList{Items: []buildkite.Job{{ID: "job-canceled", State: "canceled"}}}, &buildkite.Response{}, nil
				case "broken":
					return buildkite.JobsList{Items: []buildkite.Job{{ID: "job-broken", State: "broken"}}}, &buildkite.Response{}, nil
				default:
					return buildkite.JobsList{}, &buildkite.Response{}, nil
				}
			},
		},
		ArtifactsClient: artifactsClient,
	}
}

func failureSummaryTestArtifacts(jobID string, count int) []buildkite.Artifact {
	artifacts := make([]buildkite.Artifact, count)
	for i := range artifacts {
		artifacts[i] = buildkite.Artifact{
			ID:          fmt.Sprintf("%s-artifact-%d", jobID, i),
			JobID:       jobID,
			State:       "finished",
			Path:        fmt.Sprintf("log/test/spec_%d.log", i),
			Filename:    fmt.Sprintf("spec_%d.log", i),
			MimeType:    "text/plain",
			FileSize:    1024,
			SHA1:        "abc123",
			DownloadURL: "https://api.buildkite.com/v2/download",
		}
	}
	return artifacts
}

// perJobArtifactsClient serves counts[jobID] artifacts per job, records the
// jobs it was asked about, and fails the test on any build-wide listing.
func perJobArtifactsClient(t *testing.T, counts map[string]int, nextPage map[string]bool) (*MockArtifactsClient, func() []string) {
	var mu sync.Mutex
	var requested []string
	client := &MockArtifactsClient{
		ListByBuildFunc: func(context.Context, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			t.Error("artifacts must be listed per job, not per build")
			return nil, nil, nil
		},
		ListByJobFunc: func(_ context.Context, org, pipeline, number, jobID string, opts *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			require.Equal(t, "org", org)
			require.Equal(t, "pipeline", pipeline)
			require.Equal(t, "1", number)
			require.Equal(t, 1, opts.Page)
			require.Equal(t, failureSummaryArtifactPageSize, opts.PerPage)
			mu.Lock()
			requested = append(requested, jobID)
			mu.Unlock()
			response := &buildkite.Response{}
			if nextPage[jobID] {
				response.NextPage = 2
			}
			return failureSummaryTestArtifacts(jobID, counts[jobID]), response, nil
		},
	}
	return client, func() []string {
		mu.Lock()
		defer mu.Unlock()
		return slices.Sorted(slices.Values(requested))
	}
}

func artifactIDs(artifacts []FailureSummaryArtifact) []string {
	ids := make([]string, len(artifacts))
	for i, artifact := range artifacts {
		ids[i] = artifact.ID
	}
	return ids
}

// summaryArtifactIDs lists every job's artifact ids in job order.
func summaryArtifactIDs(summary BuildFailureSummary) []string {
	ids := []string{}
	for _, job := range summary.Jobs {
		ids = append(ids, artifactIDs(job.Artifacts)...)
	}
	return ids
}

func summaryJob(t *testing.T, summary BuildFailureSummary, jobID string) FailureSummaryJob {
	t.Helper()
	for _, job := range summary.Jobs {
		if job.ID == jobID {
			return job
		}
	}
	t.Fatalf("job %s not in summary", jobID)
	return FailureSummaryJob{}
}

func anyArtifactsTruncated(summary BuildFailureSummary) bool {
	for _, job := range summary.Jobs {
		if job.ArtifactsTruncated {
			return true
		}
	}
	return false
}

func callFailureSummaryForArtifacts(t *testing.T, deps ToolDependencies, args GetBuildFailureSummaryArgs) BuildFailureSummary {
	t.Helper()
	args.OrgSlug, args.PipelineSlug, args.BuildNumber = "org", "pipeline", "1"
	_, handler, _ := GetBuildFailureSummary()
	callResult, _, err := handler(ContextWithDeps(context.Background(), deps), createMCPRequest(t, map[string]any{}), args)
	require.NoError(t, err)
	require.False(t, callResult.IsError)
	var summary BuildFailureSummary
	require.NoError(t, json.Unmarshal([]byte(getTextResult(t, callResult).Text), &summary))
	return summary
}

func TestGetBuildFailureSummaryListsArtifactsOfJobsThatRan(t *testing.T) {
	client, requested := perJobArtifactsClient(t, map[string]int{
		"job-failed": 2, "job-timed-out": 1, "job-canceled": 1,
	}, nil)

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(client), GetBuildFailureSummaryArgs{})

	require.Equal(t, []string{"job-canceled", "job-failed", "job-timed-out"}, requested())
	require.Equal(t, []string{"job-failed-artifact-0", "job-failed-artifact-1"}, artifactIDs(summaryJob(t, summary, "job-failed").Artifacts))
	require.Equal(t, []string{"job-timed-out-artifact-0"}, artifactIDs(summaryJob(t, summary, "job-timed-out").Artifacts))
	require.Equal(t, []string{"job-canceled-artifact-0"}, artifactIDs(summaryJob(t, summary, "job-canceled").Artifacts))
	require.Empty(t, summaryJob(t, summary, "job-expired").Artifacts)
	require.Empty(t, summaryJob(t, summary, "job-broken").Artifacts)
	require.False(t, anyArtifactsTruncated(summary))
	require.Equal(t, FailureSummaryArtifact{
		ID:       "job-failed-artifact-0",
		Path:     "log/test/spec_0.log",
		MimeType: "text/plain",
		FileSize: 1024,
	}, summaryJob(t, summary, "job-failed").Artifacts[0])
	require.Empty(t, summary.Warnings)
}

func TestFailureSummaryArtifactCarriesOnlyFieldsNeededToFetch(t *testing.T) {
	// The parent job's id is the job_id get_artifact needs, so entries carry
	// no job_id, and no checksum, state or file name either.
	payload, err := json.Marshal(failureSummaryArtifacts(failureSummaryTestArtifacts("job-failed", 1))[0])
	require.NoError(t, err)

	var fields map[string]any
	require.NoError(t, json.Unmarshal(payload, &fields))
	require.ElementsMatch(t, []string{"id", "path", "mime_type", "file_size"}, slices.Collect(maps.Keys(fields)))
}

func TestGetBuildFailureSummaryArtifactBudgetIsSharedRoundRobin(t *testing.T) {
	// Build 204234 shape: the first failed job alone fills the default budget,
	// and the second job's useful log is its 4th artifact.
	client, _ := perJobArtifactsClient(t, map[string]int{"job-failed": 10, "job-timed-out": 10}, nil)

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(client), GetBuildFailureSummaryArgs{})

	require.Equal(t, []string{
		"job-failed-artifact-0", "job-failed-artifact-1", "job-failed-artifact-2", "job-failed-artifact-3", "job-failed-artifact-4",
		"job-timed-out-artifact-0", "job-timed-out-artifact-1", "job-timed-out-artifact-2", "job-timed-out-artifact-3", "job-timed-out-artifact-4",
	}, summaryArtifactIDs(summary))
	require.True(t, summaryJob(t, summary, "job-failed").ArtifactsTruncated)
	require.True(t, summaryJob(t, summary, "job-timed-out").ArtifactsTruncated)
}

func TestGetBuildFailureSummaryArtifactBudgetFlowsToJobsWithMore(t *testing.T) {
	client, _ := perJobArtifactsClient(t, map[string]int{"job-failed": 1, "job-timed-out": 5, "job-canceled": 1}, nil)

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(client), GetBuildFailureSummaryArgs{MaxArtifacts: 4})

	require.Equal(t, []string{
		"job-failed-artifact-0", "job-timed-out-artifact-0", "job-timed-out-artifact-1", "job-canceled-artifact-0",
	}, summaryArtifactIDs(summary))
	require.False(t, summaryJob(t, summary, "job-failed").ArtifactsTruncated)
	require.True(t, summaryJob(t, summary, "job-timed-out").ArtifactsTruncated)
	require.False(t, summaryJob(t, summary, "job-canceled").ArtifactsTruncated)
}

func TestGetBuildFailureSummaryArtifactsTruncatedWhenAJobHasMorePages(t *testing.T) {
	client, _ := perJobArtifactsClient(t, map[string]int{"job-canceled": 1}, map[string]bool{"job-canceled": true})

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(client), GetBuildFailureSummaryArgs{})

	require.Equal(t, []string{"job-canceled-artifact-0"}, summaryArtifactIDs(summary))
	require.True(t, summaryJob(t, summary, "job-canceled").ArtifactsTruncated)
}

func TestGetBuildFailureSummaryBoundsMaxArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		requested int
		wantKept  int
	}{
		{name: "custom", requested: 3, wantKept: 3},
		{name: "capped", requested: 500, wantKept: maxFailureSummaryArtifacts},
		{name: "default for negative", requested: -1, wantKept: defaultFailureSummaryArtifacts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client, requested := perJobArtifactsClient(t, map[string]int{"job-failed": 50, "job-timed-out": 50, "job-canceled": 50}, nil)

			summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(client), GetBuildFailureSummaryArgs{MaxArtifacts: tc.requested})
			require.Len(t, requested(), 3)
			require.Len(t, summaryArtifactIDs(summary), tc.wantKept)
			require.True(t, anyArtifactsTruncated(summary))
		})
	}
}

func TestGetBuildFailureSummaryCanDisableArtifacts(t *testing.T) {
	client, requested := perJobArtifactsClient(t, nil, nil)

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(client), GetBuildFailureSummaryArgs{IncludeArtifacts: boolPtr(false)})

	require.Empty(t, requested())
	require.Empty(t, summaryArtifactIDs(summary))
}

func TestGetBuildFailureSummaryArtifactErrorBecomesJobWarning(t *testing.T) {
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
	client := &MockArtifactsClient{
		ListByJobFunc: func(_ context.Context, _, _, _, jobID string, _ *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			if jobID == "job-timed-out" {
				return nil, nil, forbidden
			}
			return failureSummaryTestArtifacts(jobID, 1), &buildkite.Response{}, nil
		},
	}

	summary := callFailureSummaryForArtifacts(t, failureSummaryArtifactsTestDeps(client), GetBuildFailureSummaryArgs{})

	require.Equal(t, []string{"job-failed-artifact-0", "job-canceled-artifact-0"}, summaryArtifactIDs(summary))
	require.Empty(t, summaryJob(t, summary, "job-timed-out").Artifacts)
	require.Len(t, summary.Warnings, 1)
	require.Contains(t, summary.Warnings[0], "artifacts unavailable for job job-timed-out")
	require.Contains(t, summary.Warnings[0], forbidden.Message)
}

func TestLoadFailureArtifactsPropagatesUnauthorized(t *testing.T) {
	unauthorized := fmt.Errorf("wrapped API failure: %w", &buildkite.ErrorResponse{
		Response: &http.Response{
			StatusCode: http.StatusUnauthorized,
			Request: &http.Request{
				Method: http.MethodGet,
				URL:    &url.URL{Scheme: "https", Host: "api.buildkite.com"},
			},
		},
	})
	client := &MockArtifactsClient{
		ListByJobFunc: func(context.Context, string, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			return nil, nil, unauthorized
		},
	}

	_, err := loadFailureArtifacts(context.Background(), client, GetBuildFailureSummaryArgs{}, []buildkite.Job{{ID: "job", State: "failed"}}, make([]FailureSummaryJob, 1), 1)
	require.ErrorIs(t, err, ErrUnauthorized)
}

// build204234WaterfallJobArtifacts is the second failed job of build 204234
// in upload order: its per-test log is 4th and its screenshots 7th to 10th,
// behind runner noise.
func build204234WaterfallJobArtifacts() []buildkite.Artifact {
	artifact := func(id, path, mimeType string) buildkite.Artifact {
		return buildkite.Artifact{ID: id, JobID: "job-waterfall", Path: path, MimeType: mimeType}
	}
	return []buildkite.Artifact{
		artifact("development-log", "log/development.log", "text/plain"),
		artifact("ci-timing", "tmp/ci-timing-rspec-run-019f891f-b5b7-4ef3-a4db-731869dc5fa2.log", "text/plain"),
		artifact("rspec-json", "tmp/rspec-019f891f-b5b7-4ef3-a4db-731869dc5fa2-node-69-20260722092030.json", "application/json"),
		artifact("spec-log", "log/test/spec/features/viewing_build_waterfall_spec_line_30.log", "text/plain"),
		artifact("collector", "tmp/buildkite-test-collector-rspec-4ac2728d-e74a-4824-8e4c-54b224cbf6de.json.gz", "application/gzip"),
		artifact("state-json", "tmp/state/database/spec/features/viewing_build_waterfall_spec_line_30.json", "application/json"),
		artifact("screenshot-1-html", "tmp/capybara/screenshot_2026-07-22-09-23-49.262.html", "text/html"),
		artifact("screenshot-1-png", "tmp/capybara/screenshot_2026-07-22-09-23-49.262.png", "image/png"),
		artifact("screenshot-2-html", "tmp/capybara/screenshot_2026-07-22-09-24-04.365.html", "text/html"),
		artifact("screenshot-2-png", "tmp/capybara/screenshot_2026-07-22-09-24-04.365.png", "image/png"),
		artifact("test-log", "log/test.log", "text/plain"),
	}
}

func rankedFailureArtifactIDs(t *testing.T, failedTests []FailureSummaryFailedTest, limit int) []string {
	t.Helper()
	client := &MockArtifactsClient{
		ListByJobFunc: func(context.Context, string, string, string, string, *buildkite.ArtifactListOptions) ([]buildkite.Artifact, *buildkite.Response, error) {
			return build204234WaterfallJobArtifacts(), &buildkite.Response{}, nil
		},
	}
	jobs := []FailureSummaryJob{{FailedTests: failedTests}}
	_, err := loadFailureArtifacts(context.Background(), client, GetBuildFailureSummaryArgs{},
		[]buildkite.Job{{ID: "job-waterfall", State: "failed"}}, jobs, limit)
	require.NoError(t, err)
	return artifactIDs(jobs[0].Artifacts)
}

func TestLoadFailureArtifactsRanksFailedTestOutputFirst(t *testing.T) {
	failedTests := []FailureSummaryFailedTest{{FileName: "./spec/features/viewing_build_waterfall_spec.rb"}}

	require.Equal(t, []string{
		"spec-log", "state-json",
		"screenshot-1-html", "screenshot-1-png", "screenshot-2-html", "screenshot-2-png",
		"development-log", "ci-timing", "test-log",
		"rspec-json", "collector",
	}, rankedFailureArtifactIDs(t, failedTests, maxFailureSummaryArtifacts))
	// The eval's 5-per-job share now keeps the screenshot the agent needed.
	require.Equal(t, []string{"spec-log", "state-json", "screenshot-1-html", "screenshot-1-png", "screenshot-2-html"},
		rankedFailureArtifactIDs(t, failedTests, 5))
}

func TestLoadFailureArtifactsRanksWithoutFailedTests(t *testing.T) {
	// Without Test Engine data there are no stems: screenshots still lead,
	// then logs, keeping upload order within each rank.
	require.Equal(t, []string{
		"screenshot-1-html", "screenshot-1-png", "screenshot-2-html", "screenshot-2-png",
		"development-log", "ci-timing", "spec-log", "test-log",
		"rspec-json", "collector", "state-json",
	}, rankedFailureArtifactIDs(t, nil, maxFailureSummaryArtifacts))
}

func TestFailedTestStems(t *testing.T) {
	require.Equal(t, []string{"search_records_spec", "viewing_build_waterfall_spec"}, failedTestStems([]FailureSummaryFailedTest{
		{FileName: "./spec/features/admin/search_records_spec.rb"},
		{FileName: "./spec/features/Viewing_Build_Waterfall_Spec.rb"},
		{FileName: "spec/features/admin/search_records_spec.rb"},
		{FileName: ""},
	}))
}

func TestGetBuildFailureSummaryNeverShortensArtifactIDs(t *testing.T) {
	client, _ := perJobArtifactsClient(t, map[string]int{"job-failed": 40, "job-timed-out": 40, "job-canceled": 40}, nil)
	wantIDs := map[string]bool{}
	for _, jobID := range []string{"job-failed", "job-timed-out", "job-canceled"} {
		for _, artifact := range failureSummaryTestArtifacts(jobID, 40) {
			wantIDs[artifact.ID] = true
		}
	}

	// Tight limits force the generic limiter to shorten strings below an
	// artifact ID's length; every artifact that survives must keep its whole
	// id, and its job its whole id, so get_artifact can fetch it.
	for limit := 2000; limit <= 8000; limit += 500 {
		t.Run(fmt.Sprint(limit), func(t *testing.T) {
			_, handler, _ := GetBuildFailureSummary()
			callResult, _, err := handler(ContextWithDeps(context.Background(), failureSummaryArtifactsTestDeps(client)), createMCPRequest(t, map[string]any{}), GetBuildFailureSummaryArgs{
				OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "1",
				MaxArtifacts: maxFailureSummaryArtifacts, ContentLimitBytes: limit,
			})
			require.NoError(t, err)
			require.False(t, callResult.IsError, getTextResult(t, callResult).Text)
			text := getTextResult(t, callResult).Text
			require.LessOrEqual(t, len(text), limit)

			var summary BuildFailureSummary
			require.NoError(t, json.Unmarshal([]byte(text), &summary))
			require.True(t, summary.ContentTruncated)
			for _, job := range summary.Jobs {
				for _, artifact := range job.Artifacts {
					require.Contains(t, []string{"job-failed", "job-timed-out", "job-canceled"}, job.ID, "job id was shortened")
					require.True(t, wantIDs[artifact.ID], "artifact id %q was shortened", artifact.ID)
					require.True(t, strings.HasPrefix(artifact.ID, job.ID+"-"), "artifact %q listed on the wrong job %q", artifact.ID, job.ID)
				}
			}
		})
	}
}

func TestLimitFailureSummaryCollectionsDropsArtifactsBeforeAnnotations(t *testing.T) {
	annotations := make([]FailureSummaryAnnotation, 3)
	for i := range annotations {
		annotations[i] = FailureSummaryAnnotation{
			AnnotationSummary: AnnotationSummary{ID: fmt.Sprintf("annotation-%d", i), Style: "error"},
			BodyHTML:          "failure",
		}
	}
	result := BuildFailureSummary{
		Build: BuildFailureSummaryBuild{BuildSummary: BuildSummary{Number: 1, State: "failed"}},
		Jobs: []FailureSummaryJob{{
			JobSummary: JobSummary{ID: "job-failed", State: "failed"},
			Artifacts:  failureSummaryArtifacts(failureSummaryTestArtifacts("job-failed", maxFailureSummaryArtifacts)),
		}},
		Annotations: annotations,
	}

	// A limit that fits every annotation plus a few artifacts at their
	// strings-emptied floor forces artifacts, and only artifacts, to shrink.
	fewArtifacts := failureSummaryWithArtifactLimit(&result, 5)
	limit, err := failureSummaryStructureBytes(&fewArtifacts, failureSummaryContentByteLimit)
	require.NoError(t, err)

	require.NoError(t, limitFailureSummaryCollections(&result, limit))

	require.Len(t, result.Annotations, len(annotations))
	require.False(t, result.AnnotationsTruncated)
	kept := result.Jobs[0].Artifacts
	require.GreaterOrEqual(t, len(kept), 5)
	require.Less(t, len(kept), maxFailureSummaryArtifacts)
	require.Equal(t, "job-failed-artifact-0", kept[0].ID, "the highest-ranked artifacts are kept")
	require.True(t, result.Jobs[0].ArtifactsTruncated)
	require.True(t, result.ContentTruncated)
}
