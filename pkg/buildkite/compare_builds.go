package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

const comparisonJobPages = 10
const comparisonResultLimit = 100

type CompareBuildsArgs struct {
	ToolInput
	OrgSlug             string `json:"org_slug"`
	PipelineSlug        string `json:"pipeline_slug"`
	BuildNumber         string `json:"build_number" jsonschema:"Target build number, not a UUID"`
	BaselineBuildNumber string `json:"baseline_build_number,omitempty" jsonschema:"Baseline in the same pipeline. Omit to select the most recently created earlier successful build on the target branch (at most 500 candidates searched)."`
	IncludeLogs         *bool  `json:"include_logs,omitempty" jsonschema:"Include bounded target log tails for up to three newly failing jobs (default true)"`
}

// ComparisonJob describes the final attempt, not the sum of all retry attempts.
type ComparisonJob struct {
	ID                string                   `json:"id"`
	Name              string                   `json:"name"`
	State             string                   `json:"state"`
	SoftFailed        bool                     `json:"soft_failed,omitempty"`
	ExitStatus        *int                     `json:"exit_status,omitempty"`
	SignalReason      string                   `json:"signal_reason,omitempty"`
	RetriesCount      int                      `json:"retries_count"`
	WebURL            string                   `json:"web_url"`
	ExecutionSeconds  *float64                 `json:"execution_seconds,omitempty"`
	SchedulingSeconds *float64                 `json:"scheduling_seconds,omitempty"`
	LogTail           []FailureSummaryLogEntry `json:"log_tail,omitempty"`
	LogTruncated      bool                     `json:"log_truncated,omitempty"`
	LogError          string                   `json:"log_error,omitempty"`
}

type BuildStepComparison struct {
	Change                string         `json:"change"`
	MatchMethod           string         `json:"match_method,omitempty"`
	StepKey               string         `json:"step_key,omitempty"`
	Matrix                any            `json:"matrix,omitempty"`
	ParallelIndex         *int           `json:"parallel_index,omitempty"`
	ParallelTotal         *int           `json:"parallel_total,omitempty"`
	UnmatchedReason       string         `json:"unmatched_reason,omitempty"`
	Target                *ComparisonJob `json:"target,omitempty"`
	Baseline              *ComparisonJob `json:"baseline,omitempty"`
	ExecutionDeltaSeconds *float64       `json:"execution_delta_seconds,omitempty"`
}

type BuildComparison struct {
	Target            BuildSummary          `json:"target"`
	Baseline          *BuildSummary         `json:"baseline,omitempty"`
	BaselineSelection string                `json:"baseline_selection"`
	CandidatesScanned int                   `json:"candidates_scanned"`
	TargetJobs        int                   `json:"target_jobs"`
	BaselineJobs      int                   `json:"baseline_jobs"`
	ChangeCounts      map[string]int        `json:"change_counts"`
	Steps             []BuildStepComparison `json:"steps"`
	StepsOmitted      int                   `json:"steps_omitted"`
	Warnings          []string              `json:"warnings,omitempty"`
}

func comparisonDuration(start, end *buildkite.Timestamp) *float64 {
	if start == nil || end == nil || end.Before(start.Time) {
		return nil
	}
	seconds := end.Sub(start.Time).Seconds()
	return &seconds
}

func comparisonJob(job buildkite.Job, build BuildSummary) *ComparisonJob {
	webURL := job.WebURL
	if webURL == "" && job.Step != nil && job.Step.ID != "" {
		webURL = fmt.Sprintf("%s/list?sid=%s&tab=output", build.WebURL, job.Step.ID)
	}
	return &ComparisonJob{
		ID: job.ID, Name: job.Name, State: job.State, SoftFailed: job.SoftFailed,
		ExitStatus: job.ExitStatus, SignalReason: job.SignalReason, RetriesCount: job.RetriesCount,
		WebURL:            webURL,
		ExecutionSeconds:  comparisonDuration(job.StartedAt, job.FinishedAt),
		SchedulingSeconds: comparisonDuration(job.ScheduledAt, job.StartedAt),
	}
}

// Unkeyed jobs use exact names within their group and execution coordinates.
// The caller rejects duplicate identities on either side before matching.
// Separate namespaces prevent a name from matching an explicit step key.
func comparisonIdentity(job buildkite.Job) string {
	method, identifier, group := "step_key", job.StepKey, ""
	if identifier == "" {
		if strings.TrimSpace(job.Name) == "" {
			return ""
		}
		method, identifier, group = "name_fallback", job.Name, job.GroupKey
	}
	identity, err := json.Marshal([]any{method, identifier, group, job.Type, job.Matrix, job.ParallelGroupIndex, job.ParallelGroupTotal})
	if err != nil {
		return ""
	}
	return string(identity)
}

func comparisonFailed(job *ComparisonJob) bool {
	return job != nil && (job.State == "failed" || job.State == "timed_out" || job.State == "expired")
}

func compareJobOutcomes(target, baseline *ComparisonJob) string {
	switch {
	case baseline == nil:
		return "added"
	case target == nil:
		return "removed"
	case comparisonFailed(target) && baseline.State == "passed":
		return "newly_failing"
	case target.State == "passed" && comparisonFailed(baseline):
		return "recovered"
	case comparisonFailed(target) && comparisonFailed(baseline):
		return "still_failing"
	case target.State != baseline.State || target.SoftFailed != baseline.SoftFailed:
		return "state_changed"
	case target.RetriesCount != baseline.RetriesCount:
		return "retries_changed"
	default:
		return "unchanged"
	}
}

func compareBuildJobs(target, baseline []buildkite.Job, result *BuildComparison) {
	targetByKey, baselineByKey := map[string][]buildkite.Job{}, map[string][]buildkite.Job{}
	for _, job := range target {
		targetByKey[comparisonIdentity(job)] = append(targetByKey[comparisonIdentity(job)], job)
	}
	for _, job := range baseline {
		baselineByKey[comparisonIdentity(job)] = append(baselineByKey[comparisonIdentity(job)], job)
	}
	usedFallback := false
	appendStep := func(job buildkite.Job, t, b *ComparisonJob, reason string) {
		step := BuildStepComparison{StepKey: job.StepKey, Matrix: job.Matrix,
			ParallelIndex: job.ParallelGroupIndex, ParallelTotal: job.ParallelGroupTotal,
			Target: t, Baseline: b, UnmatchedReason: reason, Change: compareJobOutcomes(t, b)}
		if reason != "" {
			step.Change = "unmatched"
		}
		if reason == "" && t != nil && b != nil {
			step.MatchMethod = "step_key"
			if job.StepKey == "" {
				step.MatchMethod = "name_fallback"
				usedFallback = true
			}
		}
		if t != nil && b != nil && t.ExecutionSeconds != nil && b.ExecutionSeconds != nil {
			delta := *t.ExecutionSeconds - *b.ExecutionSeconds
			step.ExecutionDeltaSeconds = &delta
		}
		result.ChangeCounts[step.Change]++
		result.Steps = append(result.Steps, step)
	}
	for _, job := range target {
		key := comparisonIdentity(job)
		reason := ""
		if key == "" {
			reason = "missing step key and usable name"
		} else if len(targetByKey[key]) > 1 || len(baselineByKey[key]) > 1 {
			reason = "ambiguous step identity"
		}
		var b *ComparisonJob
		if reason == "" && len(baselineByKey[key]) == 1 {
			b = comparisonJob(baselineByKey[key][0], *result.Baseline)
		}
		appendStep(job, comparisonJob(job, result.Target), b, reason)
	}
	for _, job := range baseline {
		key := comparisonIdentity(job)
		reason := ""
		switch {
		case key == "":
			reason = "missing step key and usable name"
		case len(targetByKey[key]) > 1 || len(baselineByKey[key]) > 1:
			reason = "ambiguous step identity"
		case len(targetByKey[key]) == 1:
			continue
		}
		appendStep(job, nil, comparisonJob(job, *result.Baseline), reason)
	}
	if usedFallback {
		result.Warnings = append(result.Warnings, "Some unkeyed jobs were matched by unique exact name, type, group, matrix and parallel coordinates (match_method=name_fallback). These are heuristic matches, not stable step identities; renamed jobs appear as added/removed.")
	}
	// Outcomes first, then largest execution regressions within each category.
	priority := map[string]int{"newly_failing": 0, "recovered": 1, "still_failing": 2,
		"state_changed": 3, "retries_changed": 4, "added": 5, "removed": 6, "unmatched": 7, "unchanged": 8}
	sort.SliceStable(result.Steps, func(i, j int) bool {
		a, b := result.Steps[i], result.Steps[j]
		if priority[a.Change] != priority[b.Change] {
			return priority[a.Change] < priority[b.Change]
		}
		var da, db float64
		if a.ExecutionDeltaSeconds != nil {
			da = *a.ExecutionDeltaSeconds
		}
		if b.ExecutionDeltaSeconds != nil {
			db = *b.ExecutionDeltaSeconds
		}
		return da > db
	})
	if len(result.Steps) > comparisonResultLimit {
		result.StepsOmitted = len(result.Steps) - comparisonResultLimit
		result.Steps = result.Steps[:comparisonResultLimit]
		result.Warnings = append(result.Warnings, "Only the first 100 comparisons are shown; change_counts covers all jobs. Use list_jobs with step_key for drill-down.")
	}
}

func loadComparisonJobs(ctx context.Context, client JobsClient, args CompareBuildsArgs, number string) ([]buildkite.Job, error) {
	includeRetried := false
	options := &buildkite.JobsListOptions{PerPage: 100, IncludeRetriedJobs: &includeRetried}
	var jobs []buildkite.Job
	for page := 0; page < comparisonJobPages; page++ {
		list, _, err := client.ListByBuild(ctx, args.OrgSlug, args.PipelineSlug, number, options)
		if err != nil {
			return nil, err
		}
		for _, job := range list.Items {
			if !job.Retried {
				jobs = append(jobs, job)
			}
		}
		if list.Links.Next == "" {
			return jobs, nil
		}
		options, err = list.Links.Next.ToOptions()
		if err != nil {
			return nil, err
		}
		options.PerPage = 100
		options.IncludeRetriedJobs = &includeRetried
	}
	return nil, fmt.Errorf("build %s exceeds the comparison scan limit of 1000 jobs; use list_jobs with step_key to compare individual steps. No partial added/removed classification was produced", number)
}

func CompareBuilds() (mcp.Tool, mcp.ToolHandlerFor[CompareBuildsArgs, any], []string) {
	return mcp.Tool{
		Name:        "compare_builds",
		Description: "Compare step outcomes, final-attempt retry counts and execution/scheduling times between builds in one pipeline. Defaults to the most recently created earlier successful build on the same branch; baseline_build_number overrides this. Matches step keys or, for unkeyed jobs, unique exact names with matching type, group, matrix and parallel coordinates. match_method identifies heuristic name_fallback matches; ambiguous or unnamed unkeyed jobs remain unmatched. Returns at most 100 comparisons, prioritizing failures, and optional bounded log evidence for three newly failing jobs. Scans at most 1000 jobs per build; refuses incomplete inventories. Historical co-occurrence does not prove a cause or that retrying is safe. Use get_build_failure_summary for deeper failure diagnosis.",
		Annotations: &mcp.ToolAnnotations{Title: "Compare Builds", ReadOnlyHint: true},
	}, func(ctx context.Context, _ *mcp.CallToolRequest, args CompareBuildsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.CompareBuilds")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
			attribute.String("build_number", args.BuildNumber),
			attribute.String("baseline_build_number", args.BaselineBuildNumber),
		)
		deps := DepsFromContext(ctx)
		for _, number := range []string{args.BuildNumber, args.BaselineBuildNumber} {
			if number == "" && args.BuildNumber != "" {
				continue
			}
			if n, err := strconv.Atoi(number); err != nil || n <= 0 {
				return utils.NewToolResultError("Build numbers must be positive integers, not UUIDs or URLs"), nil, nil
			}
		}
		options := &buildkite.BuildGetOptions{BuildsListOptions: buildkite.BuildsListOptions{ExcludeJobs: true, ExcludePipeline: true}}
		target, _, err := deps.BuildsClient.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
		if err != nil {
			return handleBuildkiteError(err)
		}
		result := BuildComparison{Target: summarizeBuild(target), BaselineSelection: "explicit",
			ChangeCounts: map[string]int{}, Steps: []BuildStepComparison{}}
		baselineNumber := args.BaselineBuildNumber
		if baselineNumber == "" {
			result.BaselineSelection = "most_recently_created_earlier_passed_build_on_same_branch"
			if target.CreatedAt == nil || target.Branch == "" {
				return utils.NewToolResultError("Target has no creation time or branch; provide baseline_build_number explicitly"), nil, nil
			}
			listOptions := &buildkite.BuildsListOptions{Branch: []string{target.Branch}, State: []string{"passed"},
				CreatedTo: target.CreatedAt.Time, ExcludeJobs: true, ExcludePipeline: true,
				ListOptions: buildkite.ListOptions{Page: 1, PerPage: 100}}
			for page := 0; page < 5 && baselineNumber == ""; page++ {
				builds, response, listErr := deps.BuildsClient.ListByPipeline(ctx, args.OrgSlug, args.PipelineSlug, listOptions)
				if listErr != nil {
					return handleBuildkiteError(listErr)
				}
				for _, b := range builds {
					result.CandidatesScanned++
					if b.Number < target.Number && b.State == "passed" && b.Branch == target.Branch && b.CreatedAt != nil && b.CreatedAt.Before(target.CreatedAt.Time) {
						baselineNumber = strconv.Itoa(b.Number)
						break
					}
				}
				if response == nil || response.NextPage == 0 {
					break
				}
				listOptions.Page = response.NextPage
			}
			if baselineNumber == "" {
				result.Warnings = append(result.Warnings, "No earlier successful build found on the target branch within the bounded search. Provide baseline_build_number explicitly; no comparison was performed.")
				return mcpTextResult(span, result)
			}
		}
		if n, _ := strconv.Atoi(baselineNumber); n == target.Number {
			return utils.NewToolResultError("Target and baseline must be different builds"), nil, nil
		}
		baseline, _, err := deps.BuildsClient.Get(ctx, args.OrgSlug, args.PipelineSlug, baselineNumber, options)
		if err != nil {
			return handleBuildkiteError(err)
		}
		baseSummary := summarizeBuild(baseline)
		result.Baseline = &baseSummary
		if baseline.Branch != target.Branch {
			result.Warnings = append(result.Warnings, "Explicit baseline is on a different branch.")
		}
		if target.FinishedAt == nil || baseline.FinishedAt == nil {
			result.Warnings = append(result.Warnings, "One or both builds are unfinished; jobs may change or be uploaded after this snapshot. Added/removed means present/absent in this snapshot only.")
		}
		targetJobs, err := loadComparisonJobs(ctx, deps.JobsClient, args, args.BuildNumber)
		if err != nil {
			return handleBuildkiteError(err)
		}
		baselineJobs, err := loadComparisonJobs(ctx, deps.JobsClient, args, baselineNumber)
		if err != nil {
			return handleBuildkiteError(err)
		}
		result.TargetJobs, result.BaselineJobs = len(targetJobs), len(baselineJobs)
		compareBuildJobs(targetJobs, baselineJobs, &result)
		result.Warnings = append(result.Warnings, "Timings cover final attempts only; scheduling_seconds is scheduled_at to started_at, not dependency/manual waiting. Execution deltas are not build wall-clock deltas. Still-failing steps may have different failure causes.")
		if defaultTrue(args.IncludeLogs) {
			loaded := 0
			for i := range result.Steps {
				step := &result.Steps[i]
				if step.Change != "newly_failing" || step.Target.State == "expired" {
					continue
				}
				if loaded == 3 {
					result.Warnings = append(result.Warnings, "Log evidence is limited to three newly failing jobs; use tail_logs with the remaining target job IDs.")
					break
				}
				loaded++
				if deps.BuildkiteLogsClient == nil {
					step.Target.LogError = "Log client unavailable; use tail_logs for this job."
					continue
				}
				entries, _, truncated, contentTruncated, _, logErr := readFailureLogTail(ctx, deps.BuildkiteLogsClient,
					GetBuildFailureSummaryArgs{OrgSlug: args.OrgSlug, PipelineSlug: args.PipelineSlug, BuildNumber: args.BuildNumber}, buildkite.Job{ID: step.Target.ID}, 20)
				if logErr != nil {
					step.Target.LogError = logErr.Error()
					continue
				}
				entries, _, bounded := boundFailureLogEntries(entries, 8*1024)
				step.Target.LogTail, step.Target.LogTruncated = entries, truncated || contentTruncated || bounded
			}
		}
		return mcpTextResult(span, result)
	}, []string{"read_builds", "read_build_logs"}
}
