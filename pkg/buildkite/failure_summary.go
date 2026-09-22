package buildkite

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

const (
	defaultFailureSummaryLogTail         = 50
	maxFailureSummaryLogTail             = 200
	defaultFailureSummaryJobs            = 10
	maxFailureSummaryJobs                = 50
	defaultFailureSummaryAnnotations     = 20
	maxFailureSummaryAnnotations         = 100
	failureSummaryAnnotationPageSize     = 100
	failureSummaryAnnotationScanPages    = 5
	defaultFailureSummaryTestRuns        = 5
	maxFailureSummaryTestRuns            = 20
	defaultFailureSummaryFailedTests     = 50
	maxFailureSummaryFailedTests         = 100
	failureSummaryRunExecutionsPageSize  = 100
	failureSummaryEntryContentByteLimit  = 4 * 1024
	failureSummaryTestByteLimit          = 16 * 1024
	failureSummaryLogJobContentByteLimit = 64 * 1024
	failureSummaryLogContentByteLimit    = 128 * 1024
	failureSummaryAnnotationContentLimit = 64 * 1024
	failureSummaryTestContentByteLimit   = 64 * 1024
	failureSummaryContentByteLimit       = failureSummaryLogContentByteLimit + failureSummaryAnnotationContentLimit + failureSummaryTestContentByteLimit
	failureSummaryConcurrency            = 4
)

// GetBuildFailureSummaryArgs controls the amount of diagnostic context returned
// by get_build_failure_summary. Pointer booleans let omitted values default to
// true while still allowing callers to disable an optional section.
type GetBuildFailureSummaryArgs struct {
	ToolInput
	OrgSlug                string `json:"org_slug"`
	PipelineSlug           string `json:"pipeline_slug"`
	BuildNumber            string `json:"build_number"`
	LogTail                int    `json:"log_tail,omitempty" jsonschema:"Log lines to include for each failed, timed-out, canceled, or promised-failing job (default 50, max 200)"`
	MaxJobs                int    `json:"max_jobs,omitempty" jsonschema:"Maximum terminal problem or downstream-failed jobs to return (default 10, server may enforce a lower maximum, absolute max 50)"`
	MaxAnnotations         int    `json:"max_annotations,omitempty" jsonschema:"Maximum error or warning annotations to return (default 20, max 100); the server scans at most 500 total annotations"`
	MaxTestRuns            int    `json:"max_test_runs,omitempty" jsonschema:"Maximum Test Engine runs to scan for failure details of the returned failed tests (default 5, max 20); the runs holding the most returned failed tests are scanned first"`
	MaxFailedTests         int    `json:"max_failed_tests,omitempty" jsonschema:"Maximum failed Test Engine tests to return for the build (default 50, max 100)"`
	ContentLimitBytes      int    `json:"content_limit_bytes,omitempty" jsonschema:"Maximum bytes for the response payload (default and max 262144); lower it to fit clients with small tool-result limits, combining with log_tail and max_jobs for finer trimming"`
	IncludeLogs            *bool  `json:"include_logs,omitempty" jsonschema:"Include a bounded log tail for failed, timed-out, canceled, and promised-failing jobs (default true)"`
	IncludeAnnotations     *bool  `json:"include_annotations,omitempty" jsonschema:"Include error and warning annotation bodies (default true)"`
	IncludeFailedTests     *bool  `json:"include_failed_tests,omitempty" jsonschema:"Include Test Engine tests whose executions in this build all failed, when the build has Test Engine runs (default true)"`
	IncludeFailureExpanded bool   `json:"include_failure_expanded,omitempty" jsonschema:"Include expanded test failure details such as stack traces within the summary's bounded test-content budget"`
}

type BuildFailureSummaryBuild struct {
	BuildSummary
	Blocked bool `json:"blocked"`
	// JobStateCounts tallies every job in the build by state, so the jobs
	// below can be confirmed as the build's only problems without listing
	// jobs. Omitted when the API does not return it.
	JobStateCounts *buildkite.JobStateCounts `json:"job_state_counts,omitempty"`
	ScheduledAt    *buildkite.Timestamp      `json:"scheduled_at,omitempty"`
	StartedAt      *buildkite.Timestamp      `json:"started_at,omitempty"`
	FinishedAt     *buildkite.Timestamp      `json:"finished_at,omitempty"`
}

type FailureSummaryLogEntry struct {
	TerseLogEntry
	ContentTruncated bool `json:"content_truncated,omitempty"`
}

type FailureSummaryJob struct {
	JobSummary
	PromisedExitStatus   *int                     `json:"promised_exit_status,omitempty"`
	PromisedExitStatusAt *buildkite.Timestamp     `json:"promised_exit_status_at,omitempty"`
	ExpiredAt            *buildkite.Timestamp     `json:"expired_at,omitempty"`
	LogTail              []FailureSummaryLogEntry `json:"log_tail,omitempty"`
	LogTotalRows         int64                    `json:"log_total_rows,omitempty"`
	LogTruncated         bool                     `json:"log_truncated,omitempty"`
	LogContentTruncated  bool                     `json:"log_content_truncated,omitempty"`
	LogEntriesOmitted    int                      `json:"log_entries_omitted,omitempty"`
	LogError             string                   `json:"log_error,omitempty"`
	// Failed-test fields are set only on terminal failed and timed-out jobs
	// when the build has Test Engine data. FailedTestsStatus is always one of
	// the failedTestsStatus* values on those jobs; FailedTestsHint carries the
	// reaction guidance whenever the list is absent, empty, or may still grow.
	FailedTests          []FailureSummaryFailedTest `json:"failed_tests,omitempty"`
	FailedTestsStatus    string                     `json:"failed_tests_status,omitempty"`
	FailedTestsHint      string                     `json:"failed_tests_hint,omitempty"`
	FailedTestsTruncated bool                       `json:"failed_tests_truncated,omitempty"`
}

type FailureSummaryAnnotation struct {
	AnnotationSummary
	BodyHTML      string `json:"body_html"`
	BodyTruncated bool   `json:"body_truncated,omitempty"`
}

// FailureSummaryFailedTest is a Test Engine test whose executions within one
// terminally failed job all failed — tests rescued by a job retry never enter
// (their jobs are not queried) and tests rescued by an in-job framework retry
// are excluded by the per-job "result:^failed" tag filter. Failure detail
// fields come from the newest matching failed execution found while scanning
// the Test Engine runs that hold the returned tests, most affected runs
// first. When no execution was matched for the test — its run was past the
// scan cap, past the first page of the run's failed executions, unlisted on
// the build, or the lookup failed — failure_detail_status says so, and
// get_failed_executions with test_suite_slug and run_id fetches the detail.
type FailureSummaryFailedTest struct {
	TestID              string                      `json:"test_id"`
	Name                string                      `json:"name,omitempty"`
	Scope               string                      `json:"scope,omitempty"`
	Location            string                      `json:"location,omitempty"`
	FileName            string                      `json:"file_name,omitempty"`
	WebURL              string                      `json:"web_url,omitempty"`
	TestSuiteSlug       string                      `json:"test_suite_slug,omitempty"`
	RunID               string                      `json:"run_id,omitempty"`
	FailureReason       string                      `json:"failure_reason,omitempty"`
	FailureExpanded     []buildkite.FailureExpanded `json:"failure_expanded,omitempty"`
	FailureDetailStatus string                      `json:"failure_detail_status,omitempty"`
	ContentTruncated    bool                        `json:"content_truncated,omitempty"`
}

// failureDetailStatusNotRetrieved marks a failed-test entry whose
// failure_reason and failure_expanded were not fetched, so a blank
// failure_reason is never read as "the test recorded no reason". It is the
// only value; a retrieved detail leaves the field empty.
const failureDetailStatusNotRetrieved = "not_retrieved"

// Failed-test statuses distinguish the three reasons a terminally failed job
// can show no failed tests — the job failed outside its tests, ingestion has
// not caught up, or the lookup itself failed — so an agent never mistakes
// absence for "its tests passed".
const (
	failedTestsStatusFound            = "found"
	failedTestsStatusNoneRecorded     = "none_recorded"
	failedTestsStatusIngestionPending = "ingestion_pending"
	failedTestsStatusUnavailable      = "unavailable"
)

// Hints ride alongside non-found statuses (and a found list that may still
// grow) to tell the agent how to react at the moment of ambiguity. They are
// deliberately absent from a complete found list, where the data speaks for
// itself.
const (
	failedTestsHintNoneRecorded     = "No terminally failed enabled tests are recorded for this job; it likely failed outside its tests (setup, infrastructure, timeout), never uploaded results, or only muted tests failed. Diagnose from log_tail; do not conclude its tests passed."
	failedTestsHintIngestionPending = "Test results are still being ingested; this list may be empty or incomplete right now. Diagnose from log_tail and re-call this tool after the build settles."
	failedTestsHintIngestionPartial = "Test results are still being ingested; more failed tests may appear."
	failedTestsHintUnavailable      = "The failed-test lookup failed for this job; test results may exist. Diagnose from log_tail. A 403 means the token lacks the read_suites scope."
	// failedTestsHintBudgetExhausted takes the job ID; it rides on a found
	// job whose entries were all displaced by earlier jobs' entries.
	failedTestsHintBudgetExhausted = "This job has failed tests, but max_failed_tests was used up by earlier jobs so none are listed here. Raise max_failed_tests, or call list_tests_for_build with state \"enabled\" and tags \"build.job_id:%s,result:^failed\" (the state filter keeps muted tests out, as this summary does)."
)

const (
	testEngineStatusActive = "active"
	testEngineStatusNoData = "no_data"
)

type BuildFailureSummary struct {
	Build                BuildFailureSummaryBuild   `json:"build"`
	Jobs                 []FailureSummaryJob        `json:"jobs"`
	JobLimit             int                        `json:"job_limit"`
	JobsTruncated        bool                       `json:"jobs_truncated,omitempty"`
	Annotations          []FailureSummaryAnnotation `json:"annotations,omitempty"`
	AnnotationsTruncated bool                       `json:"annotations_truncated,omitempty"`
	// TestEngine says whether the build has Test Engine data at all ("active"
	// or "no_data"), so an absent failed_tests section is never ambiguous
	// between "no tests" and "no Test Engine". Omitted when the failed-tests
	// section is disabled or unconfigured.
	TestEngine        string   `json:"test_engine,omitempty"`
	ContentBytes      int      `json:"content_bytes"`
	ContentLimitBytes int      `json:"content_limit_bytes"`
	ContentTruncated  bool     `json:"content_truncated,omitempty"`
	Warnings          []string `json:"warnings,omitempty"`
}

func defaultTrue(value *bool) bool {
	return value == nil || *value
}

func boundedValue(value, defaultValue, maxValue int) int {
	if value <= 0 {
		return defaultValue
	}
	return min(value, maxValue)
}

func boundedFailureSummaryJobs(value, configuredMax int) int {
	if configuredMax <= 0 {
		configuredMax = defaultFailureSummaryJobs
	}
	configuredMax = min(configuredMax, maxFailureSummaryJobs)
	return boundedValue(value, min(defaultFailureSummaryJobs, configuredMax), configuredMax)
}

func failureSummaryBuild(build buildkite.Build) BuildFailureSummaryBuild {
	return BuildFailureSummaryBuild{
		BuildSummary:   summarizeBuild(build),
		Blocked:        build.Blocked,
		JobStateCounts: build.JobStateCounts,
		ScheduledAt:    build.ScheduledAt,
		StartedAt:      build.StartedAt,
		FinishedAt:     build.FinishedAt,
	}
}

func isPrimaryFailureSummaryJob(job buildkite.Job) bool {
	switch job.State {
	case "failed", "timed_out", "expired":
		return true
	case "running":
		return job.PromisedExitStatus != nil && *job.PromisedExitStatus != 0
	default:
		return false
	}
}

func isCanceledFailureSummaryJob(job buildkite.Job) bool {
	return job.State == "canceled"
}

func isDownstreamFailureSummaryJob(job buildkite.Job) bool {
	switch job.State {
	case "broken", "waiting_failed", "blocked_failed", "unblocked_failed":
		return true
	default:
		return false
	}
}

func shouldReadFailureLog(job buildkite.Job) bool {
	return isCanceledFailureSummaryJob(job) || (job.State != "expired" && isPrimaryFailureSummaryJob(job))
}

func failureSummaryJob(job buildkite.Job) FailureSummaryJob {
	return FailureSummaryJob{
		JobSummary:           summarizeJob(job),
		PromisedExitStatus:   job.PromisedExitStatus,
		PromisedExitStatusAt: job.PromisedExitStatusAt,
		ExpiredAt:            job.ExpiredAt,
	}
}

func truncateUTF8Bytes(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	if limit <= 0 {
		return "", true
	}

	const ellipsis = "…"
	prefixLimit := limit
	if limit >= len(ellipsis) {
		prefixLimit -= len(ellipsis)
	} else {
		return utf8Prefix(value, limit), true
	}
	return utf8Prefix(value, prefixLimit) + ellipsis, true
}

func utf8Prefix(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	for limit > 0 && !utf8.RuneStart(value[limit]) {
		limit--
	}
	return value[:limit]
}

func failureSummaryAnnotations(annotations []buildkite.Annotation, limit int) ([]FailureSummaryAnnotation, bool) {
	results := make([]FailureSummaryAnnotation, 0, min(len(annotations), limit))
	truncated := false
	for _, annotation := range annotations {
		if annotation.Style != "error" && annotation.Style != "warning" {
			continue
		}
		if len(results) >= limit {
			truncated = true
			continue
		}
		body, bodyTruncated := truncateUTF8Bytes(annotation.BodyHTML, failureSummaryEntryContentByteLimit)
		results = append(results, FailureSummaryAnnotation{
			AnnotationSummary: summarizeAnnotations([]buildkite.Annotation{annotation})[0],
			BodyHTML:          body,
			BodyTruncated:     bodyTruncated,
		})
	}
	return results, truncated
}

func loadFailureAnnotations(ctx context.Context, client AnnotationsClient, args GetBuildFailureSummaryArgs, limit int) ([]FailureSummaryAnnotation, bool, error) {
	results := make([]FailureSummaryAnnotation, 0, limit)
	page := 1

	for pagesScanned := range failureSummaryAnnotationScanPages {
		annotations, response, err := client.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.AnnotationListOptions{
			ListOptions: buildkite.ListOptions{Page: page, PerPage: failureSummaryAnnotationPageSize},
			Scope:       "all",
		})
		if err != nil {
			if isBuildkiteUnauthorized(err) {
				return nil, false, ErrUnauthorized
			}
			return results, true, err
		}

		remaining := limit - len(results)
		pageResults, pageTruncated := failureSummaryAnnotations(annotations, remaining)
		results = append(results, pageResults...)

		hasNextPage := response != nil && response.NextPage > 0
		if pageTruncated {
			return results, true, nil
		}
		if len(results) >= limit {
			return results, hasNextPage, nil
		}
		if !hasNextPage {
			return results, false, nil
		}
		if pagesScanned+1 >= failureSummaryAnnotationScanPages {
			return results, true, nil
		}
		page = response.NextPage
	}

	return results, true, nil
}

func readFailureLogTail(ctx context.Context, client BuildkiteLogsClient, args GetBuildFailureSummaryArgs, job buildkite.Job, tail int) ([]FailureSummaryLogEntry, int64, bool, bool, int, error) {
	reader, err := newParquetReader(ctx, client, JobLogsBaseParams{
		OrgSlug:      args.OrgSlug,
		PipelineSlug: args.PipelineSlug,
		BuildNumber:  args.BuildNumber,
		JobID:        job.ID,
	})
	if err != nil {
		return nil, 0, false, false, 0, err
	}
	defer reader.Close()

	fileInfo, err := reader.GetFileInfo()
	if err != nil {
		return nil, 0, false, false, 0, fmt.Errorf("get log file info: %w", err)
	}

	startRow := max(fileInfo.RowCount-int64(tail), 0)
	entries := make([]FailureSummaryLogEntry, 0, min(int(fileInfo.RowCount-startRow), tail))
	contentTruncated := false
	for entry, readErr := range reader.SeekToRow(ctx, startRow) {
		if readErr != nil {
			return nil, fileInfo.RowCount, startRow > 0, contentTruncated, 0, fmt.Errorf("read log tail: %w", readErr)
		}
		terse := toTerseEntry(entry)
		var entryContentTruncated bool
		terse.C, entryContentTruncated = truncateUTF8Bytes(terse.C, failureSummaryEntryContentByteLimit)
		entries = append(entries, FailureSummaryLogEntry{TerseLogEntry: terse, ContentTruncated: entryContentTruncated})
		contentTruncated = contentTruncated || entryContentTruncated
	}

	entries, omitted, jobTruncated := boundFailureLogEntries(entries, failureSummaryLogJobContentByteLimit)
	return entries, fileInfo.RowCount, startRow > 0, contentTruncated || jobTruncated, omitted, nil
}

func boundFailureLogEntries(entries []FailureSummaryLogEntry, limit int) ([]FailureSummaryLogEntry, int, bool) {
	remaining := limit
	first := len(entries)
	contentTruncated := false
	for first > 0 {
		contentLength := len(entries[first-1].C)
		if contentLength > remaining {
			if remaining > 0 {
				entries[first-1].C, _ = truncateUTF8Bytes(entries[first-1].C, remaining)
				entries[first-1].ContentTruncated = true
				contentTruncated = true
				first--
			}
			break
		}
		remaining -= contentLength
		first--
	}

	if first == 0 {
		return entries, 0, contentTruncated
	}
	result := append([]FailureSummaryLogEntry(nil), entries[first:]...)
	return result, first, true
}

func loadFailureLogs(ctx context.Context, client BuildkiteLogsClient, args GetBuildFailureSummaryArgs, sourceJobs []buildkite.Job, jobs []FailureSummaryJob, tail int) error {
	semaphore := make(chan struct{}, failureSummaryConcurrency)
	unauthorized := make(chan error, len(sourceJobs))
	var waitGroup sync.WaitGroup

	for i := range sourceJobs {
		if !shouldReadFailureLog(sourceJobs[i]) {
			continue
		}

		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			entries, totalRows, truncated, contentTruncated, omitted, err := readFailureLogTail(ctx, client, args, sourceJobs[index], tail)
			if err != nil {
				if isBuildkiteUnauthorized(err) {
					unauthorized <- ErrUnauthorized
					return
				}
				jobs[index].LogError = err.Error()
				return
			}
			jobs[index].LogTail = entries
			jobs[index].LogTotalRows = totalRows
			jobs[index].LogTruncated = truncated
			jobs[index].LogContentTruncated = contentTruncated
			jobs[index].LogEntriesOmitted = omitted
		}(i)
	}

	waitGroup.Wait()
	select {
	case err := <-unauthorized:
		return err
	default:
		return nil
	}
}

// isFailureSummaryTestAnchorJob reports whether a job's Test Engine data is
// worth querying: only terminal failed and timed-out jobs ran tests whose
// failures can explain the build. Broken and waiting_failed jobs never ran,
// and canceled jobs were stopped by a person, not by their tests. Retried
// jobs never reach here — the job listing excludes them, which is what makes
// per-job test membership agree with the jobs view by construction.
func isFailureSummaryTestAnchorJob(job buildkite.Job) bool {
	return job.State == "failed" || job.State == "timed_out"
}

// testSuiteSlugFromURL extracts the suite slug from a test's API URL
// (".../suites/<slug>/tests/<id>"). The build tests API does not name the
// suite a test belongs to, and the slug is the drill-down handle an agent
// needs when failure detail is missing.
func testSuiteSlugFromURL(url string) string {
	_, after, found := strings.Cut(url, "/suites/")
	if !found {
		return ""
	}
	slug, _, _ := strings.Cut(after, "/")
	return slug
}

// failureSummaryTestsIngestionSettled reports whether the build's test data
// can still change: the build must be finished and every Test Engine run
// listed on the build must report state "finished". Any run lookup error
// counts as unsettled, so uncertainty always degrades toward claiming less.
// The run state is the API's own signal, not proof that ingestion is
// complete, so callers phrase a settled result as "no longer expected to
// change". A 401 propagates so the whole tool fails consistently.
func failureSummaryTestsIngestionSettled(ctx context.Context, client TestRunsClient, args GetBuildFailureSummaryArgs, build buildkite.Build) (bool, error) {
	if build.FinishedAt == nil || client == nil {
		return false, nil
	}

	runs := build.TestEngine.Runs
	finished := make([]bool, len(runs))
	runErrors := make([]error, len(runs))
	semaphore := make(chan struct{}, failureSummaryConcurrency)
	var waitGroup sync.WaitGroup
	for i := range runs {
		waitGroup.Add(1)
		go func(index int) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			run, _, err := client.Get(ctx, args.OrgSlug, runs[index].Suite.Slug, runs[index].ID)
			if err != nil {
				runErrors[index] = err
				return
			}
			finished[index] = run.State == "finished"
		}(i)
	}
	waitGroup.Wait()

	settled := true
	for i := range runs {
		if runErrors[i] != nil {
			if isBuildkiteUnauthorized(runErrors[i]) {
				return false, ErrUnauthorized
			}
			settled = false
			continue
		}
		settled = settled && finished[i]
	}
	return settled, nil
}

// loadFailureJobTests fills the failed-test fields of each terminal failed or
// timed-out job. Membership comes from one build tests query per anchor job,
// scoped by the automatic build.job_id execution tag, the result:^failed
// operator, and the enabled test state: only enabled tests whose every
// execution within that job failed. Muted tests are excluded because Test
// Engine soft-fails them, so they cannot be a build-failure cause. Job-level
// retries never enter (retried jobs are not anchors) and in-job framework
// retries are excluded by ^failed, whose per-job scope is lag-safe because a
// job's attempts arrive in one upload, so a failure and its rescue ingest
// together. Failure detail (failure_reason, failure_expanded) is then joined
// from a bounded scan of each run's failed executions, newest execution per
// test. Non-auth errors degrade to a per-job "unavailable" status plus a
// warning so a token without the read_suites scope still gets the rest of the
// summary.
func loadFailureJobTests(ctx context.Context, deps ToolDependencies, args GetBuildFailureSummaryArgs, build buildkite.Build, sourceJobs []buildkite.Job, jobs []FailureSummaryJob, maxTests, maxRuns int, settled bool) ([]string, error) {
	runIDBySuite := make(map[string]string, len(build.TestEngine.Runs))
	for _, run := range build.TestEngine.Runs {
		if _, exists := runIDBySuite[run.Suite.Slug]; !exists {
			runIDBySuite[run.Suite.Slug] = run.ID
		}
	}

	type anchorJobTests struct {
		job       buildkite.Job
		result    *FailureSummaryJob
		tests     []buildkite.TestWithMetrics
		truncated bool
		err       error
	}
	anchors := make([]*anchorJobTests, 0, len(sourceJobs))
	for i := range min(len(sourceJobs), len(jobs)) {
		job := sourceJobs[i] //nolint:gosec // i is bounded by min(len(sourceJobs), len(jobs))
		if isFailureSummaryTestAnchorJob(job) {
			anchors = append(anchors, &anchorJobTests{job: job, result: &jobs[i]}) //nolint:gosec // i is bounded by min(len(sourceJobs), len(jobs))
		}
	}
	if len(anchors) == 0 {
		return nil, nil
	}

	semaphore := make(chan struct{}, failureSummaryConcurrency)
	var waitGroup sync.WaitGroup
	for _, anchor := range anchors {
		waitGroup.Add(1)
		go func(anchor *anchorJobTests) {
			defer waitGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			// State "enabled" drops muted and skipped tests: a muted test still
			// runs and still records failed executions, but Test Engine treats
			// its failure as a soft fail that cannot fail the build, so it is
			// never a failure cause. The result tag alone would keep it.
			tests, response, err := deps.BuildTestsClient.List(ctx, args.OrgSlug, build.ID, &buildkite.BuildTestsListOptions{
				ListOptions: buildkite.ListOptions{Page: 1, PerPage: maxTests},
				State:       "enabled",
				Tags:        fmt.Sprintf("build.job_id:%s,result:^failed", anchor.job.ID),
			})
			if err != nil {
				anchor.err = err
				return
			}
			anchor.truncated = response != nil && response.NextPage > 0
			if len(tests) > maxTests {
				tests = tests[:maxTests]
				anchor.truncated = true
			}
			anchor.tests = tests
		}(anchor)
	}
	waitGroup.Wait()

	var warnings []string
	remaining := maxTests
	entriesByTestID := map[string][]*FailureSummaryFailedTest{}
	for _, anchor := range anchors {
		if anchor.err != nil {
			if isBuildkiteUnauthorized(anchor.err) {
				return nil, ErrUnauthorized
			}
			anchor.result.FailedTestsStatus = failedTestsStatusUnavailable
			anchor.result.FailedTestsHint = failedTestsHintUnavailable
			warnings = append(warnings, fmt.Sprintf("failed tests unavailable for job %s: %v", anchor.job.ID, anchor.err))
			continue
		}

		// Status is decided by what the API returned, before the build-wide
		// budget trims the list: a job whose failures were all pushed out by
		// earlier jobs is still "found", never "none_recorded".
		if len(anchor.tests) == 0 {
			anchor.result.FailedTestsTruncated = anchor.truncated
			if settled {
				anchor.result.FailedTestsStatus = failedTestsStatusNoneRecorded
				anchor.result.FailedTestsHint = failedTestsHintNoneRecorded
			} else {
				anchor.result.FailedTestsStatus = failedTestsStatusIngestionPending
				anchor.result.FailedTestsHint = failedTestsHintIngestionPending
			}
			continue
		}

		tests := anchor.tests
		if len(tests) > remaining {
			tests = tests[:remaining]
			anchor.truncated = true
		}
		remaining -= len(tests)
		anchor.result.FailedTestsTruncated = anchor.truncated
		anchor.result.FailedTestsStatus = failedTestsStatusFound
		switch {
		case len(tests) == 0:
			anchor.result.FailedTestsHint = fmt.Sprintf(failedTestsHintBudgetExhausted, anchor.job.ID)
			continue
		case !settled:
			anchor.result.FailedTestsHint = failedTestsHintIngestionPartial
		}
		anchor.result.FailedTests = make([]FailureSummaryFailedTest, len(tests))
		for j, test := range tests {
			suiteSlug := testSuiteSlugFromURL(test.URL)
			anchor.result.FailedTests[j] = FailureSummaryFailedTest{
				TestID:        test.ID,
				Name:          test.Name,
				Scope:         test.Scope,
				Location:      test.Location,
				FileName:      test.FileName,
				WebURL:        test.WebURL,
				TestSuiteSlug: suiteSlug,
				RunID:         runIDBySuite[suiteSlug],
			}
			entriesByTestID[test.ID] = append(entriesByTestID[test.ID], &anchor.result.FailedTests[j])
		}
	}

	if len(entriesByTestID) == 0 {
		return warnings, nil
	}
	if deps.TestExecutionsClient == nil {
		markFailureDetailNotRetrieved(entriesByTestID, nil)
		return warnings, nil
	}

	runs, affectedRuns := failureSummaryRunsToScan(build.TestEngine.Runs, entriesByTestID, maxRuns)
	if affectedRuns > len(runs) {
		warnings = append(warnings, fmt.Sprintf("test failure details cover only the %d most affected of %d Test Engine runs holding returned failed tests; raise max_test_runs to scan more", len(runs), affectedRuns))
	}

	executionsByRun := make([][]buildkite.FailedExecution, len(runs))
	runErrors := make([]error, len(runs))
	var joinGroup sync.WaitGroup
	for i := range runs {
		joinGroup.Add(1)
		go func(index int) {
			defer joinGroup.Done()
			semaphore <- struct{}{}
			defer func() { <-semaphore }()

			executions, _, executionsErr := deps.TestExecutionsClient.GetFailedExecutions(ctx, args.OrgSlug, runs[index].Suite.Slug, runs[index].ID, &buildkite.FailedExecutionsOptions{
				IncludeFailureExpanded: args.IncludeFailureExpanded,
				Page:                   1,
				PerPage:                failureSummaryRunExecutionsPageSize,
			})
			if executionsErr != nil {
				runErrors[index] = executionsErr
				return
			}
			executionsByRun[index] = executions
		}(i)
	}
	joinGroup.Wait()

	hasDetail := map[string]bool{}
	detailAt := map[string]*buildkite.Timestamp{}
	for i, run := range runs {
		if runErrors[i] != nil {
			if isBuildkiteUnauthorized(runErrors[i]) {
				return nil, ErrUnauthorized
			}
			warnings = append(warnings, fmt.Sprintf("test failure details unavailable for run %s: %v", run.ID, runErrors[i]))
			continue
		}
		for _, execution := range executionsByRun[i] {
			targets, ok := entriesByTestID[execution.TestID]
			if !ok {
				// A failure a retry rescued, an execution from a retried job,
				// or a test beyond the caps — not part of the returned set.
				continue
			}
			if hasDetail[execution.TestID] {
				newer := execution.CreatedAt != nil && (detailAt[execution.TestID] == nil || execution.CreatedAt.After(detailAt[execution.TestID].Time))
				if !newer {
					continue
				}
			}
			hasDetail[execution.TestID] = true
			detailAt[execution.TestID] = execution.CreatedAt
			for _, entry := range targets {
				entry.RunID = run.ID
				entry.TestSuiteSlug = run.Suite.Slug
				entry.FailureReason = execution.FailureReason
				entry.FailureExpanded = execution.FailureExpanded
			}
		}
	}
	markFailureDetailNotRetrieved(entriesByTestID, hasDetail)

	return warnings, nil
}

// failureSummaryRunsToScan picks the Test Engine runs whose failed executions
// are worth fetching for the returned failed tests. Runs are ranked by how
// many returned tests belong to their suite, most first (slug order breaks
// ties, so the choice is stable), and capped at maxRuns; a run holding none
// of the returned tests is never scanned, so a build with many suites and
// failures in one costs one call instead of maxRuns misses. Tests whose suite
// could not be read from their URL cannot be ranked, so when any exist and
// slots remain, unranked runs fill the slots in build order as a fallback.
// The second result is the number of runs holding returned tests, so the
// caller can say how many were left unscanned.
func failureSummaryRunsToScan(runs []buildkite.TestEngineRun, entriesByTestID map[string][]*FailureSummaryFailedTest, maxRuns int) ([]buildkite.TestEngineRun, int) {
	runBySuite := make(map[string]buildkite.TestEngineRun, len(runs))
	for _, run := range runs {
		if _, exists := runBySuite[run.Suite.Slug]; !exists {
			runBySuite[run.Suite.Slug] = run
		}
	}

	testsBySuite := map[string]int{}
	unranked := false
	for _, entries := range entriesByTestID {
		slug := ""
		if len(entries) > 0 {
			slug = entries[0].TestSuiteSlug
		}
		if _, listed := runBySuite[slug]; slug == "" || !listed {
			unranked = true
			continue
		}
		testsBySuite[slug]++
	}

	ranked := make([]string, 0, len(testsBySuite))
	for slug := range testsBySuite {
		ranked = append(ranked, slug)
	}
	slices.SortFunc(ranked, func(a, b string) int {
		if testsBySuite[a] != testsBySuite[b] {
			return testsBySuite[b] - testsBySuite[a]
		}
		return strings.Compare(a, b)
	})

	selected := make([]buildkite.TestEngineRun, 0, min(maxRuns, len(runs)))
	chosen := map[string]bool{}
	for _, slug := range ranked {
		if len(selected) >= maxRuns {
			break
		}
		selected = append(selected, runBySuite[slug])
		chosen[slug] = true
	}
	if unranked {
		for _, run := range runs {
			if len(selected) >= maxRuns {
				break
			}
			if chosen[run.Suite.Slug] {
				continue
			}
			selected = append(selected, run)
			chosen[run.Suite.Slug] = true
		}
	}
	return selected, len(ranked)
}

// markFailureDetailNotRetrieved labels every returned failed test that no
// scanned execution matched. hasDetail may be nil when no scan ran at all.
func markFailureDetailNotRetrieved(entriesByTestID map[string][]*FailureSummaryFailedTest, hasDetail map[string]bool) {
	for testID, entries := range entriesByTestID {
		if hasDetail[testID] {
			continue
		}
		for _, entry := range entries {
			entry.FailureDetailStatus = failureDetailStatusNotRetrieved
		}
	}
}

func limitFailureSummaryString(value string, fieldLimit int, entryRemaining, sectionRemaining *int) (string, bool) {
	limit := min(fieldLimit, *entryRemaining, *sectionRemaining)
	result, truncated := truncateUTF8Bytes(value, limit)
	used := len(result)
	*entryRemaining -= used
	*sectionRemaining -= used
	return result, truncated
}

func failureSummaryLogContentBytes(entries []FailureSummaryLogEntry) int {
	total := 0
	for _, entry := range entries {
		total += len(entry.C)
	}
	return total
}

func consumeFailureSummaryBytes(size int, entryRemaining, sectionRemaining *int) bool {
	if size > *entryRemaining || size > *sectionRemaining {
		return false
	}
	*entryRemaining -= size
	*sectionRemaining -= size
	return true
}

func limitFailureExpanded(values []buildkite.FailureExpanded, entryRemaining, sectionRemaining *int) ([]buildkite.FailureExpanded, bool) {
	result := make([]buildkite.FailureExpanded, 0, len(values))
	truncated := false
	if len(values) > 0 && !consumeFailureSummaryBytes(len(`"failure_expanded":[]`), entryRemaining, sectionRemaining) {
		return result, true
	}
	for _, value := range values {
		objectBytes := len(`{}`)
		if len(result) > 0 {
			objectBytes++ // comma between failure_expanded items
		}
		if !consumeFailureSummaryBytes(objectBytes, entryRemaining, sectionRemaining) {
			truncated = true
			break
		}

		bounded := buildkite.FailureExpanded{}
		for _, backtrace := range value.Backtrace {
			structureBytes := len(`""`)
			if len(bounded.Backtrace) == 0 {
				structureBytes += len(`"backtrace":[]`)
			} else {
				structureBytes++ // comma between backtrace items
			}
			if !consumeFailureSummaryBytes(structureBytes, entryRemaining, sectionRemaining) {
				truncated = true
				break
			}
			line, lineTruncated := limitFailureSummaryString(backtrace, failureSummaryEntryContentByteLimit, entryRemaining, sectionRemaining)
			bounded.Backtrace = append(bounded.Backtrace, line)
			truncated = truncated || lineTruncated
		}
		if len(bounded.Backtrace) < len(value.Backtrace) {
			truncated = true
		}
		for _, expanded := range value.Expanded {
			structureBytes := len(`""`)
			if len(bounded.Expanded) == 0 {
				structureBytes += len(`"expanded":[]`)
				if len(bounded.Backtrace) > 0 {
					structureBytes++ // comma between object fields
				}
			} else {
				structureBytes++ // comma between expanded items
			}
			if !consumeFailureSummaryBytes(structureBytes, entryRemaining, sectionRemaining) {
				truncated = true
				break
			}
			line, lineTruncated := limitFailureSummaryString(expanded, failureSummaryEntryContentByteLimit, entryRemaining, sectionRemaining)
			bounded.Expanded = append(bounded.Expanded, line)
			truncated = truncated || lineTruncated
		}
		if len(bounded.Expanded) < len(value.Expanded) {
			truncated = true
		}
		result = append(result, bounded)
		if *entryRemaining <= 0 || *sectionRemaining <= 0 {
			if len(result) < len(values) {
				truncated = true
			}
			break
		}
	}
	return result, truncated
}

func limitFailureTest(test *FailureSummaryFailedTest, limit int, sectionRemaining *int) bool {
	entryRemaining := limit
	truncated := test.ContentTruncated
	fields := []*string{
		&test.FailureReason,
		&test.Name,
		&test.Scope,
		&test.Location,
	}
	for _, field := range fields {
		var fieldTruncated bool
		*field, fieldTruncated = limitFailureSummaryString(*field, failureSummaryEntryContentByteLimit, &entryRemaining, sectionRemaining)
		truncated = truncated || fieldTruncated
	}

	var expandedTruncated bool
	test.FailureExpanded, expandedTruncated = limitFailureExpanded(test.FailureExpanded, &entryRemaining, sectionRemaining)
	truncated = truncated || expandedTruncated
	test.ContentTruncated = truncated
	return truncated
}

func applyFailureSummaryContentLimits(result *BuildFailureSummary) {
	result.ContentLimitBytes = failureSummaryContentByteLimit

	logRemaining := failureSummaryLogContentByteLimit
	logItemsRemaining := 0
	for _, job := range result.Jobs {
		if len(job.LogTail) > 0 || job.LogError != "" {
			logItemsRemaining++
		}
	}
	for i := range result.Jobs {
		job := &result.Jobs[i]
		if len(job.LogTail) == 0 && job.LogError == "" {
			continue
		}
		itemLimit := min(failureSummaryLogJobContentByteLimit, logRemaining/logItemsRemaining)
		itemRemaining := itemLimit
		if job.LogError != "" {
			var truncated bool
			job.LogError, truncated = limitFailureSummaryString(job.LogError, failureSummaryEntryContentByteLimit, &itemRemaining, &logRemaining)
			job.LogContentTruncated = job.LogContentTruncated || truncated
		}
		entries, omitted, truncated := boundFailureLogEntries(job.LogTail, min(itemRemaining, logRemaining))
		used := failureSummaryLogContentBytes(entries)
		itemRemaining -= used
		logRemaining -= used
		job.LogTail = entries
		job.LogEntriesOmitted += omitted
		job.LogTruncated = job.LogTruncated || omitted > 0
		job.LogContentTruncated = job.LogContentTruncated || truncated
		result.ContentTruncated = result.ContentTruncated || job.LogContentTruncated
		logItemsRemaining--
	}

	annotationRemaining := failureSummaryAnnotationContentLimit
	for i := range result.Warnings {
		entryRemaining := failureSummaryEntryContentByteLimit
		var truncated bool
		result.Warnings[i], truncated = limitFailureSummaryString(result.Warnings[i], failureSummaryEntryContentByteLimit, &entryRemaining, &annotationRemaining)
		result.ContentTruncated = result.ContentTruncated || truncated
	}
	for i := range result.Annotations {
		annotation := &result.Annotations[i]
		entryRemaining := failureSummaryEntryContentByteLimit
		var truncated bool
		annotation.BodyHTML, truncated = limitFailureSummaryString(annotation.BodyHTML, failureSummaryEntryContentByteLimit, &entryRemaining, &annotationRemaining)
		annotation.BodyTruncated = annotation.BodyTruncated || truncated
		result.ContentTruncated = result.ContentTruncated || annotation.BodyTruncated
	}

	testRemaining := failureSummaryTestContentByteLimit
	testItemsRemaining := 0
	for _, job := range result.Jobs {
		testItemsRemaining += len(job.FailedTests)
	}
	for i := range result.Jobs {
		for j := range result.Jobs[i].FailedTests {
			itemLimit := min(failureSummaryTestByteLimit, testRemaining/testItemsRemaining)
			truncated := limitFailureTest(&result.Jobs[i].FailedTests[j], itemLimit, &testRemaining)
			result.ContentTruncated = result.ContentTruncated || truncated
			testItemsRemaining--
		}
	}
}

func failureSummaryWithLogEntryLimit(result *BuildFailureSummary, perJobLimit int) BuildFailureSummary {
	limited := *result
	limited.Jobs = append([]FailureSummaryJob(nil), result.Jobs...)
	for i := range limited.Jobs {
		entries := result.Jobs[i].LogTail
		if len(entries) <= perJobLimit {
			continue
		}

		omitted := len(entries) - perJobLimit
		limited.Jobs[i].LogTail = entries[omitted:]
		limited.Jobs[i].LogEntriesOmitted += omitted
		limited.Jobs[i].LogTruncated = true
		limited.Jobs[i].LogContentTruncated = true
		limited.ContentTruncated = true
	}
	return limited
}

func marshalFailureSummaryWithContentBytes(result *BuildFailureSummary) ([]byte, error) {
	result.ContentBytes = 0
	for {
		payload, err := marshalSanitizedJSON(result)
		if err != nil {
			return nil, err
		}
		if result.ContentBytes == len(payload) {
			return payload, nil
		}
		result.ContentBytes = len(payload)
	}
}

// failureSummaryWithFailedTestLimit returns a copy of the summary where every
// job keeps at most its first perJobLimit failed tests.
func failureSummaryWithFailedTestLimit(result *BuildFailureSummary, perJobLimit int) BuildFailureSummary {
	limited := *result
	limited.Jobs = append([]FailureSummaryJob(nil), result.Jobs...)
	for i := range limited.Jobs {
		tests := result.Jobs[i].FailedTests
		if len(tests) <= perJobLimit {
			continue
		}

		limited.Jobs[i].FailedTests = append([]FailureSummaryFailedTest(nil), tests[:perJobLimit]...)
		limited.Jobs[i].FailedTestsTruncated = true
		limited.ContentTruncated = true
	}
	return limited
}

// failureSummaryWithAnnotationLimit returns a copy of the summary keeping at
// most the first maxAnnotations annotations (the API returns them in priority
// order).
func failureSummaryWithAnnotationLimit(result *BuildFailureSummary, maxAnnotations int) BuildFailureSummary {
	limited := *result
	if len(result.Annotations) <= maxAnnotations {
		return limited
	}

	limited.Annotations = append([]FailureSummaryAnnotation(nil), result.Annotations[:maxAnnotations]...)
	limited.AnnotationsTruncated = true
	limited.ContentTruncated = true
	return limited
}

// failureSummaryPayloadBytes measures a candidate by its full serialized
// size — the budget the per-job log trim targets.
func failureSummaryPayloadBytes(result *BuildFailureSummary, _ int) (int, error) {
	payload, err := marshalFailureSummaryWithContentBytes(result)
	if err != nil {
		return 0, err
	}
	return len(payload), nil
}

// failureSummaryStructureBytes measures a candidate by its strings-emptied
// floor — the smallest size the generic limiter can reach without dropping
// array items. Item-bearing collections only need reducing while this floor
// exceeds the limit; string overage is the generic limiter's job, and
// dropping items for it would discard diagnostics the limiter could keep.
func failureSummaryStructureBytes(result *BuildFailureSummary, limit int) (int, error) {
	payload, err := marshalFailureSummaryWithContentBytes(result)
	if err != nil {
		return 0, err
	}
	return payloadStructureBytes(payload, limit)
}

// shrinkFailureSummaryToFit binary-searches the largest per-collection entry
// count whose measured size fits limit and replaces *result with that
// candidate (or the zero-entry candidate when nothing fits, so later
// reduction passes start from the smallest form). Entry counts map
// monotonically to size but not arithmetically — JSON escaping, conditional
// truncation metadata, and the self-referential content_bytes field all
// shift it — so each candidate is marshaled and measured.
func shrinkFailureSummaryToFit(result *BuildFailureSummary, limit, maxEntries int, withLimit func(*BuildFailureSummary, int) BuildFailureSummary, measure func(*BuildFailureSummary, int) (int, error)) error {
	if maxEntries == 0 {
		return nil
	}

	best := withLimit(result, 0)
	low, high := 0, maxEntries
	for low <= high {
		mid := low + (high-low)/2
		candidate := withLimit(result, mid)
		size, err := measure(&candidate, limit)
		if err != nil {
			return err
		}
		if size <= limit {
			best = candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}

	*result = best
	return nil
}

// limitFailureSummaryCollections reduces the summary's semantic collections
// so the generic payload limiter that runs afterwards can fit the limit by
// shortening strings alone — it never drops array items. Per-job log tails
// are trimmed against the full serialized size (newest lines kept, the
// long-standing behavior). Failed tests and annotations are only
// reduced while the strings-emptied structure floor exceeds the limit — the
// exact condition under which the generic limiter would fail — so a payload
// oversized by string content alone keeps its default collection membership.
func limitFailureSummaryCollections(result *BuildFailureSummary, limit int) error {
	size, err := failureSummaryPayloadBytes(result, limit)
	if err != nil {
		return err
	}
	if size <= limit {
		return nil
	}

	logEntries := 0
	for _, job := range result.Jobs {
		logEntries = max(logEntries, len(job.LogTail))
	}
	if err := shrinkFailureSummaryToFit(result, limit, logEntries, failureSummaryWithLogEntryLimit, failureSummaryPayloadBytes); err != nil {
		return err
	}

	structureReductions := []struct {
		maxEntries func(*BuildFailureSummary) int
		withLimit  func(*BuildFailureSummary, int) BuildFailureSummary
	}{
		{
			maxEntries: func(r *BuildFailureSummary) int {
				entries := 0
				for _, job := range r.Jobs {
					entries = max(entries, len(job.FailedTests))
				}
				return entries
			},
			withLimit: failureSummaryWithFailedTestLimit,
		},
		{
			maxEntries: func(r *BuildFailureSummary) int { return len(r.Annotations) },
			withLimit:  failureSummaryWithAnnotationLimit,
		},
	}

	for _, reduction := range structureReductions {
		floor, err := failureSummaryStructureBytes(result, limit)
		if err != nil {
			return err
		}
		if floor <= limit {
			return nil
		}
		if err := shrinkFailureSummaryToFit(result, limit, reduction.maxEntries(result), reduction.withLimit, failureSummaryStructureBytes); err != nil {
			return err
		}
	}
	return nil
}

func GetBuildFailureSummary() (mcp.Tool, mcp.ToolHandlerFor[GetBuildFailureSummaryArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_build_failure_summary",
		Description: "Diagnose a Buildkite build failure in one call. Returns build.state, build.job_state_counts tallying every job in the build by state (when present, use it to confirm the returned problem jobs are the build's only problems without calling list_jobs), terminal problem jobs, downstream failed or broken jobs, promised failures from running jobs, and size-bounded diagnostic content from logs, annotations, and failed Test Engine tests. Each terminal failed or timed-out job carries failed_tests (only tests whose every execution within that job failed, with failure_reason joined from the newest failed execution) and failed_tests_status ('found', 'none_recorded', 'ingestion_pending', or 'unavailable'); an empty or absent failed_tests list does NOT mean the job's tests passed — follow the job's failed_tests_hint and treat the job's log_tail as the authoritative fallback. A failed test with failure_detail_status 'not_retrieved' had no execution fetched; call get_failed_executions with its test_suite_slug and run_id for the detail. Annotation content is in the body_html field; there is no body field. Start with this tool before calling individual job, log, annotation, or test tools.",
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Build Failure Summary",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetBuildFailureSummaryArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetBuildFailureSummary")
		defer span.End()

		deps := DepsFromContext(ctx)
		logTail := boundedValue(args.LogTail, defaultFailureSummaryLogTail, maxFailureSummaryLogTail)
		maxJobs := boundedFailureSummaryJobs(args.MaxJobs, deps.FailureSummary.MaxJobs)
		maxAnnotations := boundedValue(args.MaxAnnotations, defaultFailureSummaryAnnotations, maxFailureSummaryAnnotations)
		maxTestRuns := boundedValue(args.MaxTestRuns, defaultFailureSummaryTestRuns, maxFailureSummaryTestRuns)
		maxFailedTests := boundedValue(args.MaxFailedTests, defaultFailureSummaryFailedTests, maxFailureSummaryFailedTests)
		contentLimit := boundedValue(args.ContentLimitBytes, failureSummaryContentByteLimit, failureSummaryContentByteLimit)

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
			attribute.String("build_number", args.BuildNumber),
			attribute.Int("log_tail", logTail),
			attribute.Int("max_jobs", maxJobs),
			attribute.Int("max_test_runs", maxTestRuns),
			attribute.Int("max_failed_tests", maxFailedTests),
			attribute.Int("content_limit_bytes", contentLimit),
			attribute.Bool("include_logs", defaultTrue(args.IncludeLogs)),
			attribute.Bool("include_annotations", defaultTrue(args.IncludeAnnotations)),
			attribute.Bool("include_failed_tests", defaultTrue(args.IncludeFailedTests)),
		)

		build, _, err := deps.BuildsClient.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.BuildGetOptions{
			BuildsListOptions: buildkite.BuildsListOptions{
				ExcludeJobs:     true,
				ExcludePipeline: true,
			},
			IncludeTestEngine: true,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		result := BuildFailureSummary{Build: failureSummaryBuild(build), JobLimit: maxJobs}

		includeRetriedJobs := false
		primaryJobsList, _, err := deps.JobsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.JobsListOptions{
			// The API's failed filter includes running jobs with a hard promised
			// failure. Querying running separately can include promises covered by
			// soft-fail or retry rules that do not put the build into failing.
			State:              []string{"failed", "timed_out", "expired"},
			IncludeRetriedJobs: &includeRetriedJobs,
			PerPage:            maxJobs + 1,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}

		sourceJobs := make([]buildkite.Job, 0, maxJobs)
		jobsTruncated := primaryJobsList.Links.Next != ""
		for _, job := range primaryJobsList.Items {
			if !isPrimaryFailureSummaryJob(job) {
				continue
			}
			if len(sourceJobs) < maxJobs {
				sourceJobs = append(sourceJobs, job)
			} else {
				jobsTruncated = true
			}
		}

		remainingJobs := maxJobs - len(sourceJobs)
		if remainingJobs > 0 || !jobsTruncated {
			canceledJobsList, _, listErr := deps.JobsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.JobsListOptions{
				State:              []string{"canceled"},
				IncludeRetriedJobs: &includeRetriedJobs,
				PerPage:            remainingJobs + 1,
			})
			if listErr != nil {
				return handleBuildkiteError(listErr)
			}
			jobsTruncated = jobsTruncated || canceledJobsList.Links.Next != ""
			for _, job := range canceledJobsList.Items {
				if !isCanceledFailureSummaryJob(job) {
					continue
				}
				if len(sourceJobs) < maxJobs {
					sourceJobs = append(sourceJobs, job)
				} else {
					jobsTruncated = true
				}
			}
		}

		remainingJobs = maxJobs - len(sourceJobs)
		if remainingJobs > 0 || !jobsTruncated {
			downstreamJobsList, _, listErr := deps.JobsClient.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.JobsListOptions{
				State:              []string{"broken", "waiting_failed", "blocked_failed", "unblocked_failed"},
				IncludeRetriedJobs: &includeRetriedJobs,
				PerPage:            remainingJobs + 1,
			})
			if listErr != nil {
				return handleBuildkiteError(listErr)
			}
			jobsTruncated = jobsTruncated || downstreamJobsList.Links.Next != ""
			for _, job := range downstreamJobsList.Items {
				if !isDownstreamFailureSummaryJob(job) {
					continue
				}
				if len(sourceJobs) < maxJobs {
					sourceJobs = append(sourceJobs, job)
				} else {
					jobsTruncated = true
				}
			}
		}
		result.Jobs = make([]FailureSummaryJob, len(sourceJobs))
		for i, job := range sourceJobs {
			result.Jobs[i] = failureSummaryJob(job)
		}
		result.JobsTruncated = jobsTruncated

		if defaultTrue(args.IncludeLogs) && deps.BuildkiteLogsClient != nil {
			if err := loadFailureLogs(ctx, deps.BuildkiteLogsClient, args, sourceJobs, result.Jobs, logTail); err != nil {
				return nil, nil, err
			}
		}

		if defaultTrue(args.IncludeAnnotations) && deps.AnnotationsClient != nil {
			result.Annotations, result.AnnotationsTruncated, err = loadFailureAnnotations(ctx, deps.AnnotationsClient, args, maxAnnotations)
			if err != nil {
				if isBuildkiteUnauthorized(err) {
					return nil, nil, ErrUnauthorized
				}
				result.Warnings = append(result.Warnings, fmt.Sprintf("annotations unavailable after partial scan: %v", err))
			}
		}

		if defaultTrue(args.IncludeFailedTests) && deps.BuildTestsClient != nil {
			if build.TestEngine == nil || len(build.TestEngine.Runs) == 0 {
				result.TestEngine = testEngineStatusNoData
			} else {
				result.TestEngine = testEngineStatusActive
				settled, settledErr := failureSummaryTestsIngestionSettled(ctx, deps.TestRunsClient, args, build)
				if settledErr != nil {
					return nil, nil, settledErr
				}
				testWarnings, testsErr := loadFailureJobTests(ctx, deps, args, build, sourceJobs, result.Jobs, maxFailedTests, maxTestRuns, settled)
				if testsErr != nil {
					return nil, nil, testsErr
				}
				result.Warnings = append(result.Warnings, testWarnings...)
			}
		}

		applyFailureSummaryContentLimits(&result)
		if err := limitFailureSummaryCollections(&result, contentLimit); err != nil {
			return utils.NewToolResultError(fmt.Sprintf("failed to limit failure summary logs: %v", err)), nil, nil
		}

		failedTestCount := 0
		for _, job := range result.Jobs {
			failedTestCount += len(job.FailedTests)
		}
		span.SetAttributes(
			attribute.Int("failure_job_count", len(result.Jobs)),
			attribute.Int("annotation_count", len(result.Annotations)),
			attribute.Int("failed_test_count", failedTestCount),
		)

		return mcpTextResultWithByteLimit(span, &result, contentLimit)
	}, []string{"read_builds", "read_build_logs", "read_suites"}
}
