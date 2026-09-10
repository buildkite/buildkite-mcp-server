package buildkite

import (
	"context"
	"fmt"
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
	failureSummaryEntryContentByteLimit  = 4 * 1024
	failureSummaryLogJobContentByteLimit = 64 * 1024
	failureSummaryLogContentByteLimit    = 128 * 1024
	failureSummaryAnnotationContentLimit = 64 * 1024
	failureSummaryContentByteLimit       = failureSummaryLogContentByteLimit + failureSummaryAnnotationContentLimit
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
	LogTail            int   `json:"log_tail,omitempty" jsonschema:"Log lines to include for each failed, timed-out, canceled, or promised-failing job (default 50, max 200)"`
	MaxJobs            int   `json:"max_jobs,omitempty" jsonschema:"Maximum terminal problem or downstream-failed jobs to return (default 10, server may enforce a lower maximum, absolute max 50)"`
	MaxAnnotations     int   `json:"max_annotations,omitempty" jsonschema:"Maximum error or warning annotations to return (default 20, max 100); the server scans at most 500 total annotations"`
	ContentLimitBytes  int   `json:"content_limit_bytes,omitempty" jsonschema:"Maximum bytes for the response payload (default and max 196608); lower it to fit clients with small tool-result limits, combining with log_tail and max_jobs for finer trimming"`
	IncludeLogs        *bool `json:"include_logs,omitempty" jsonschema:"Include a bounded log tail for failed, timed-out, canceled, and promised-failing jobs (default true)"`
	IncludeAnnotations *bool `json:"include_annotations,omitempty" jsonschema:"Include error and warning annotation bodies (default true)"`
}

// FailureSummaryJobStateCounts wraps buildkite.JobStateCounts and adds
// Truncated, which is always false — the API returns a complete server-side
// tally, so callers can rely on it without paginating list_jobs.
type FailureSummaryJobStateCounts struct {
	buildkite.JobStateCounts
	Truncated bool `json:"truncated"`
}

type BuildFailureSummaryBuild struct {
	BuildSummary
	Blocked bool `json:"blocked"`
	// JobStateCounts tallies every job in the build by state, so the jobs
	// below can be confirmed as the build's only problems without listing
	// jobs. Omitted when the API does not return it.
	JobStateCounts *FailureSummaryJobStateCounts `json:"job_state_counts,omitempty"`
	ScheduledAt    *buildkite.Timestamp          `json:"scheduled_at,omitempty"`
	StartedAt      *buildkite.Timestamp          `json:"started_at,omitempty"`
	FinishedAt     *buildkite.Timestamp          `json:"finished_at,omitempty"`
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
}

type FailureSummaryAnnotation struct {
	AnnotationSummary
	BodyHTML      string `json:"body_html"`
	BodyTruncated bool   `json:"body_truncated,omitempty"`
}

type BuildFailureSummary struct {
	Build                BuildFailureSummaryBuild   `json:"build"`
	Jobs                 []FailureSummaryJob        `json:"jobs"`
	JobLimit             int                        `json:"job_limit"`
	JobsTruncated        bool                       `json:"jobs_truncated,omitempty"`
	Annotations          []FailureSummaryAnnotation `json:"annotations,omitempty"`
	AnnotationsTruncated bool                       `json:"annotations_truncated,omitempty"`
	ContentBytes         int                        `json:"content_bytes"`
	ContentLimitBytes    int                        `json:"content_limit_bytes"`
	ContentTruncated     bool                       `json:"content_truncated,omitempty"`
	Warnings             []string                   `json:"warnings,omitempty"`
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
	var jobStateCounts *FailureSummaryJobStateCounts
	if build.JobStateCounts != nil {
		jobStateCounts = &FailureSummaryJobStateCounts{JobStateCounts: *build.JobStateCounts}
	}
	return BuildFailureSummaryBuild{
		BuildSummary:   summarizeBuild(build),
		Blocked:        build.Blocked,
		JobStateCounts: jobStateCounts,
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
// long-standing behavior). Failed test executions and annotations are only
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

	floor, err := failureSummaryStructureBytes(result, limit)
	if err != nil {
		return err
	}
	if floor <= limit {
		return nil
	}
	return shrinkFailureSummaryToFit(result, limit, len(result.Annotations), failureSummaryWithAnnotationLimit, failureSummaryStructureBytes)
}

func GetBuildFailureSummary() (mcp.Tool, mcp.ToolHandlerFor[GetBuildFailureSummaryArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_build_failure_summary",
		Description: "Diagnose a Buildkite build failure in one call. Returns build.state, build.job_state_counts tallying every job in the build by state (when present, use it to confirm the returned problem jobs are the build's only problems without calling list_jobs), terminal problem jobs, downstream failed or broken jobs, promised failures from running jobs, and size-bounded diagnostic content from logs and annotations. Annotation content is in the body_html field; there is no body field. Start with this tool before calling individual job, log, or annotation tools.",
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
		contentLimit := boundedValue(args.ContentLimitBytes, failureSummaryContentByteLimit, failureSummaryContentByteLimit)

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
			attribute.String("build_number", args.BuildNumber),
			attribute.Int("log_tail", logTail),
			attribute.Int("max_jobs", maxJobs),
			attribute.Int("content_limit_bytes", contentLimit),
			attribute.Bool("include_logs", defaultTrue(args.IncludeLogs)),
			attribute.Bool("include_annotations", defaultTrue(args.IncludeAnnotations)),
		)

		build, _, err := deps.BuildsClient.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.BuildGetOptions{
			BuildsListOptions: buildkite.BuildsListOptions{
				ExcludeJobs:     true,
				ExcludePipeline: true,
			},
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

		applyFailureSummaryContentLimits(&result)
		if err := limitFailureSummaryCollections(&result, contentLimit); err != nil {
			return utils.NewToolResultError(fmt.Sprintf("failed to limit failure summary logs: %v", err)), nil, nil
		}

		span.SetAttributes(
			attribute.Int("failure_job_count", len(result.Jobs)),
			attribute.Int("annotation_count", len(result.Annotations)),
		)

		return mcpTextResultWithByteLimit(span, &result, contentLimit)
	}, []string{"read_builds", "read_build_logs"}
}
