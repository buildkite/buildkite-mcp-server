package buildkite

import (
	"context"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

const annotationSummaryPageSize = 100

// Timing budget for wait_for_build. MCP clients commonly enforce a 60 second
// request timeout that server progress notifications do not reset, see
// https://github.com/modelcontextprotocol/typescript-sdk/issues/245. The poll
// window plus the trailing annotation fetch stay inside that budget, so the
// call always returns rather than being killed client-side; the caller waits
// longer by invoking the tool again.
//
// These are vars only so tests can shorten them; they are not runtime tunable.
var (
	// waitForBuildMaxDuration bounds the polling loop and the API calls it makes.
	waitForBuildMaxDuration = 45 * time.Second
	// waitForBuildPollInterval is how often the build is re-checked while waiting.
	waitForBuildPollInterval = 5 * time.Second
	// waitForBuildAnnotationTimeout bounds the single annotation fetch made once
	// the poll window closes.
	waitForBuildAnnotationTimeout = 10 * time.Second
)

// MaxWaitForBuildBudget is the longest a single wait_for_build call can take:
// the poll window plus the annotation fetch that follows it. wait_for_build is
// deliberately the slowest tool here, so any transport imposing its own write
// deadline must allow at least this long or it will close the connection before
// the handler can respond.
func MaxWaitForBuildBudget() time.Duration {
	return waitForBuildMaxDuration + waitForBuildAnnotationTimeout
}

type BuildsClient interface {
	Get(ctx context.Context, org, pipelineSlug, buildNumber string, options *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error)
	ListByOrg(ctx context.Context, org string, options *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
	ListByPipeline(ctx context.Context, org, pipelineSlug string, options *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
	Create(ctx context.Context, org string, pipeline string, b buildkite.CreateBuild) (buildkite.Build, *buildkite.Response, error)
	Cancel(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error)
	Rebuild(ctx context.Context, org, pipeline, buildNumber string) (buildkite.Build, error)
}

// BuildSummary - Essential build fields for list responses
type BuildSummary struct {
	ID        string               `json:"id"`
	Number    int                  `json:"number"`
	State     string               `json:"state"`
	Branch    string               `json:"branch"`
	Commit    string               `json:"commit"`
	Message   string               `json:"message"`
	WebURL    string               `json:"web_url"`
	CreatedAt *buildkite.Timestamp `json:"created_at"`
}

// AnnotationSummary contains enough metadata to identify and fetch an
// annotation without including its potentially large body.
type AnnotationSummary struct {
	ID       string `json:"id"`
	Context  string `json:"context"`
	Style    string `json:"style"`
	Scope    string `json:"scope"`
	JobID    string `json:"job_id,omitempty"`
	Priority int    `json:"priority"`
}

// BuildDetail includes useful build metadata and annotation summaries while
// omitting jobs, env, pipeline configuration, and annotation bodies.
type BuildDetail struct {
	BuildSummary
	Blocked              bool                          `json:"blocked"`
	Author               buildkite.Author              `json:"author"`
	ScheduledAt          *buildkite.Timestamp          `json:"scheduled_at,omitempty"`
	StartedAt            *buildkite.Timestamp          `json:"started_at,omitempty"`
	FinishedAt           *buildkite.Timestamp          `json:"finished_at,omitempty"`
	MetaData             map[string]string             `json:"meta_data,omitempty"`
	Creator              buildkite.Creator             `json:"creator"`
	Source               string                        `json:"source,omitempty"`
	RebuiltFrom          *buildkite.RebuiltFrom        `json:"rebuilt_from,omitempty"`
	PullRequest          *buildkite.PullRequest        `json:"pull_request,omitempty"`
	TriggeredFrom        *buildkite.TriggeredFrom      `json:"triggered_from,omitempty"`
	TestEngine           *buildkite.TestEngineProperty `json:"test_engine,omitempty"`
	Annotations          []AnnotationSummary           `json:"annotations"`
	AnnotationsTruncated bool                          `json:"annotations_truncated,omitempty"`
}

// ListBuildsArgs struct with enhanced filtering
type ListBuildsArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug,omitempty" jsonschema:"Filter builds by pipeline. When omitted, lists builds across all pipelines in the organization"`
	Branch       string `json:"branch,omitempty" jsonschema:"Filter builds by git branch name"`
	State        string `json:"state,omitempty" jsonschema:"Filter builds by state (scheduled, running, passed, failed, canceled, skipped)"`
	Commit       string `json:"commit,omitempty" jsonschema:"Filter builds by specific commit SHA"`
	Creator      string `json:"creator,omitempty" jsonschema:"Filter builds by build creator"`
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
}

// GetBuildArgs struct
type GetBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
}

// GetBuildTestEngineRunsArgs struct
type GetBuildTestEngineRunsArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
}

// Helper functions for build conversion

// summarizeBuild converts a buildkite.Build to BuildSummary
func summarizeBuild(build buildkite.Build) BuildSummary {
	return BuildSummary{
		ID:        build.ID,
		Number:    build.Number,
		State:     build.State,
		Branch:    build.Branch,
		Commit:    build.Commit,
		Message:   build.Message,
		WebURL:    build.WebURL,
		CreatedAt: build.CreatedAt,
	}
}

func detailBuild(build buildkite.Build, annotations []buildkite.Annotation, annotationsTruncated bool) BuildDetail {
	return BuildDetail{
		BuildSummary:         summarizeBuild(build),
		Blocked:              build.Blocked,
		Author:               build.Author,
		ScheduledAt:          build.ScheduledAt,
		StartedAt:            build.StartedAt,
		FinishedAt:           build.FinishedAt,
		MetaData:             build.MetaData,
		Creator:              build.Creator,
		Source:               build.Source,
		RebuiltFrom:          build.RebuiltFrom,
		PullRequest:          build.PullRequest,
		TriggeredFrom:        build.TriggeredFrom,
		TestEngine:           build.TestEngine,
		Annotations:          summarizeAnnotations(annotations),
		AnnotationsTruncated: annotationsTruncated,
	}
}

func summarizeAnnotations(annotations []buildkite.Annotation) []AnnotationSummary {
	summaries := make([]AnnotationSummary, len(annotations))
	for i, annotation := range annotations {
		summaries[i] = AnnotationSummary{
			ID:       annotation.ID,
			Context:  annotation.Context,
			Style:    annotation.Style,
			Scope:    annotation.Scope,
			JobID:    annotation.JobID,
			Priority: annotation.Priority,
		}
	}
	return summaries
}

// createPaginatedBuildResult creates a paginated result with the appropriate converter
func createPaginatedBuildResult[T any](builds []buildkite.Build, converter func(buildkite.Build) T, headers map[string]string) PaginatedResult[T] {
	items := make([]T, len(builds))
	for i, build := range builds {
		items[i] = converter(build)
	}

	return PaginatedResult[T]{
		Items:   items,
		Headers: headers,
	}
}

func ListBuilds() (mcp.Tool, mcp.ToolHandlerFor[ListBuildsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_builds",
			Description: "List builds for a pipeline or across all pipelines in an organization, returning a lightweight summary of each build. When pipeline_slug is omitted, lists builds across all pipelines in the organization. Jobs are not included — use list_jobs or get_job for job detail",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Builds",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListBuildsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListBuilds")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("branch", args.Branch),
				attribute.String("state", args.State),
				attribute.String("commit", args.Commit),
				attribute.String("creator", args.Creator),
				attribute.Int("page", args.Page),
				attribute.Int("per_page", args.PerPage),
			)

			// Set default pagination
			page := args.Page
			if page == 0 {
				page = 1
			}
			perPage := args.PerPage
			if perPage == 0 {
				perPage = 30
			}

			// Builds are returned as lightweight summaries; jobs and pipeline
			// detail are excluded. Use list_jobs/get_job for job detail.
			options := &buildkite.BuildsListOptions{
				ExcludeJobs:     true,
				ExcludePipeline: true,
				ListOptions: buildkite.ListOptions{
					Page:    page,
					PerPage: perPage,
				},
			}

			// Apply filters
			if args.Branch != "" {
				options.Branch = []string{args.Branch}
			}
			if args.State != "" {
				options.State = []string{args.State}
			}
			if args.Commit != "" {
				options.Commit = args.Commit
			}
			if args.Creator != "" {
				options.Creator = args.Creator
			}

			deps := DepsFromContext(ctx)
			var builds []buildkite.Build
			var resp *buildkite.Response
			var err error
			if args.PipelineSlug != "" {
				builds, resp, err = deps.BuildsClient.ListByPipeline(ctx, args.OrgSlug, args.PipelineSlug, options)
			} else {
				builds, resp, err = deps.BuildsClient.ListByOrg(ctx, args.OrgSlug, options)
			}
			if err != nil {
				return handleBuildkiteError(err)
			}

			headers := map[string]string{
				"Link": resp.Header.Get("Link"),
			}

			result := createPaginatedBuildResult(builds, summarizeBuild, headers)

			return mcpTextResult(span, result)
		}, []string{"read_builds"}
}

func GetBuildTestEngineRuns() (mcp.Tool, mcp.ToolHandlerFor[GetBuildTestEngineRunsArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_build_test_engine_runs",
			Description: "Get test engine runs data for a specific build in Buildkite. This can be used to look up Test Runs.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Build Test Engine Runs",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetBuildTestEngineRunsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetBuildTestEngineRuns")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
			)

			deps := DepsFromContext(ctx)
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

			// Extract just the test engine runs data
			var testEngineRuns []buildkite.TestEngineRun
			if build.TestEngine != nil {
				testEngineRuns = build.TestEngine.Runs
			}

			return mcpTextResult(span, &testEngineRuns)
		}, []string{"read_builds"}
}

func GetBuild() (mcp.Tool, mcp.ToolHandlerFor[GetBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_build",
			Description: "Get a single build with lightweight annotation summaries. Annotation bodies and jobs are not included — use list_annotations to read annotations, and list_jobs or get_job for job detail",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Build",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetBuild")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
			)

			// Jobs are excluded; use list_jobs/get_job for job detail.
			options := &buildkite.BuildGetOptions{
				BuildsListOptions: buildkite.BuildsListOptions{
					ExcludeJobs:     true,
					ExcludePipeline: true,
				},
				IncludeTestEngine: true,
			}

			deps := DepsFromContext(ctx)
			build, _, err := deps.BuildsClient.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
			if err != nil {
				return handleBuildkiteError(err)
			}

			annotations, annotationsTruncated, err := listAnnotationSummaries(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber)
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(
				attribute.Int("annotation_count", len(annotations)),
				attribute.Bool("annotations_truncated", annotationsTruncated),
			)

			result := detailBuild(build, annotations, annotationsTruncated)
			return mcpTextResult(span, &result)
		}, []string{"read_builds"}
}

// listAnnotationSummaries fetches the first page of body-less annotations for a
// build, reporting whether further pages were left behind.
func listAnnotationSummaries(ctx context.Context, orgSlug, pipelineSlug, buildNumber string) ([]buildkite.Annotation, bool, error) {
	deps := DepsFromContext(ctx)
	annotations, resp, err := deps.AnnotationsClient.ListByBuild(ctx, orgSlug, pipelineSlug, buildNumber, &buildkite.AnnotationListOptions{
		ListOptions: buildkite.ListOptions{Page: 1, PerPage: annotationSummaryPageSize},
		Scope:       "all",
		OmitBody:    boolPtr(true),
	})
	if err != nil {
		return nil, false, err
	}

	return annotations, resp != nil && resp.NextPage > 0, nil
}

type WaitForBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
}

// WaitForBuildResult reports whether the build settled within this call's
// polling window. When finished is false the build is still in progress and the
// caller should invoke the tool again to keep waiting.
//
// Build is only populated once finished is true. Waiting on a long build means
// repeating this call, and the build detail is near-identical every time, so
// interim responses carry just the fields that actually change. Callers that
// want detail mid-flight can ask for it directly with get_build.
type WaitForBuildResult struct {
	Finished bool   `json:"finished"`
	State    string `json:"state"`
	Number   int    `json:"number"`
	// WaitedSeconds is how long this single call waited, not the total across
	// retries. BuildElapsedSeconds covers that: it is measured from the build's
	// own start time, so it is independent of how many times the tool has been
	// called and is the field to judge a long-running build by. It is omitted
	// for a build that has not started yet.
	WaitedSeconds       int          `json:"waited_seconds"`
	BuildElapsedSeconds int          `json:"build_elapsed_seconds,omitempty"`
	Build               *BuildDetail `json:"build,omitempty"`
}

// buildElapsedSeconds reports how long the build has been going: its full
// duration once finished, or time so far while it is still running. Returns 0
// when the build has not started, which omits the field.
func buildElapsedSeconds(build buildkite.Build) int {
	if build.StartedAt == nil {
		return 0
	}

	end := time.Now()
	if build.FinishedAt != nil {
		end = build.FinishedAt.Time
	}

	elapsed := end.Sub(build.StartedAt.Time)
	if elapsed < 0 {
		return 0
	}

	return int(elapsed.Round(time.Second).Seconds())
}

func WaitForBuild() (mcp.Tool, mcp.ToolHandlerFor[WaitForBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "wait_for_build",
			Description: "Wait for a build to reach a terminal state (passed, failed, canceled, skipped, not_run, or blocked on a block step), polling for up to 45 seconds. Returns finished=true along with the build once it settles. If the build is still in progress when the window closes it returns finished=false and the current state only, with no build detail — call this tool again to keep waiting. Judge a long build by build_elapsed_seconds, which counts from the build's own start time and does not reset between calls; waited_seconds covers only the latest call. Do not wait indefinitely: after roughly ten consecutive calls, stop and report the build as still running with its elapsed time rather than continuing to poll. Jobs and annotation bodies are never included — use list_jobs or get_job for job detail, and list_annotations to read annotations",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Wait for Build",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args WaitForBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.WaitForBuild")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
			)

			// Jobs are excluded; use list_jobs/get_job for job detail.
			options := &buildkite.BuildGetOptions{
				BuildsListOptions: buildkite.BuildsListOptions{
					ExcludeJobs:     true,
					ExcludePipeline: true,
				},
				IncludeTestEngine: true,
			}

			// Bound the polling loop, and the API calls it makes, so the tool
			// always responds inside the client's request timeout.
			waitCtx, cancel := context.WithTimeout(ctx, waitForBuildMaxDuration)
			defer cancel()

			ticker := time.NewTicker(waitForBuildPollInterval)
			defer ticker.Stop()

			deps := DepsFromContext(ctx)
			started := time.Now()

			var build buildkite.Build
			var polled bool

		POLL:
			for {
				current, _, err := deps.BuildsClient.Get(waitCtx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
				if err != nil {
					// The caller has gone away, so there is no result to return and
					// no point paying for the annotation fetch a result would need.
					if ctx.Err() != nil {
						return handleBuildkiteError(err)
					}

					// Before the first successful poll there is no state to fall back
					// on, and an authentication failure will not resolve itself, so
					// both of those propagate.
					if !polled || isBuildkiteUnauthorized(err) {
						return handleBuildkiteError(err)
					}

					// Otherwise fall back to the last state we saw. This covers both
					// the wait deadline and a transient API error: the caller retries
					// either way, and reporting a build we just saw running beats
					// discarding it because one poll failed.
					log.Ctx(ctx).Warn().Err(err).
						Str("last_known_state", build.State).
						Msg("Build poll failed, returning last known state")

					break POLL
				}

				build, polled = current, true

				if isTerminalBuildState(build.State) {
					break POLL
				}

				select {
				case <-ticker.C:
				case <-waitCtx.Done():
					break POLL
				}
			}

			finished := isTerminalBuildState(build.State)
			waited := time.Since(started)

			log.Ctx(ctx).Info().
				Str("build_id", build.ID).
				Str("state", build.State).
				Bool("finished", finished).
				Dur("waited", waited).
				Msg("Finished waiting for build")

			span.SetAttributes(
				attribute.String("build_state", build.State),
				attribute.Bool("finished", finished),
			)

			result := WaitForBuildResult{
				Finished:            finished,
				State:               build.State,
				Number:              build.Number,
				WaitedSeconds:       int(waited.Round(time.Second).Seconds()),
				BuildElapsedSeconds: buildElapsedSeconds(build),
			}

			// Build detail, and the annotation fetch it needs, are only worth
			// paying for once the build has settled. Skipping them on interim
			// responses keeps a long wait from repeating the same static build
			// metadata on every retry.
			if finished {
				// This runs outside the poll window, so it carries its own budget.
				annCtx, annCancel := context.WithTimeout(ctx, waitForBuildAnnotationTimeout)
				defer annCancel()

				annotations, annotationsTruncated, err := listAnnotationSummaries(annCtx, args.OrgSlug, args.PipelineSlug, args.BuildNumber)
				if err != nil {
					return handleBuildkiteError(err)
				}

				detail := detailBuild(build, annotations, annotationsTruncated)
				result.Build = &detail
			}

			return mcpTextResult(span, &result)
		}, []string{"read_builds"}
}

// isTerminalBuildState reports whether a build state is settled, meaning further
// polling cannot change it without outside intervention. "blocked" counts: the
// build is waiting on a block step and will not move until a human unblocks it.
// See https://buildkite.com/docs/pipelines/configure/notifications#build-states
func isTerminalBuildState(state string) bool {
	switch state {
	case "passed", "failed", "canceled", "skipped", "not_run", "blocked":
		return true
	default:
		return false
	}
}

type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type CreateBuildArgs struct {
	OrgSlug             string  `json:"org_slug"`
	PipelineSlug        string  `json:"pipeline_slug"`
	Commit              string  `json:"commit" jsonschema:"The commit SHA to build"`
	Branch              string  `json:"branch"`
	Message             string  `json:"message"`
	IgnoreBranchFilters bool    `json:"ignore_branch_filters,omitempty" jsonschema:"Whether to ignore branch filters when triggering the build"`
	Environment         []Entry `json:"environment,omitempty" jsonschema:"Environment variables to set for the build"`
	MetaData            []Entry `json:"metadata,omitempty" jsonschema:"Meta-data values to set for the build"`
}

func CreateBuild() (mcp.Tool, mcp.ToolHandlerFor[CreateBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "create_build",
			Description: "Trigger a new build on a Buildkite pipeline for a specific commit and branch, with optional environment variables, metadata, and author information",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Create Build",
				DestructiveHint: boolPtr(false),
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args CreateBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreateBuild")
			defer span.End()

			createBuild := buildkite.CreateBuild{
				Commit:                      args.Commit,
				Branch:                      args.Branch,
				Message:                     args.Message,
				Env:                         convertEntries(args.Environment),
				MetaData:                    convertEntries(args.MetaData),
				IgnorePipelineBranchFilters: args.IgnoreBranchFilters,
			}

			span.SetAttributes(
				attribute.String("org", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.Bool("ignore_branch_filters", args.IgnoreBranchFilters),
			)

			deps := DepsFromContext(ctx)
			build, _, err := deps.BuildsClient.Create(ctx, args.OrgSlug, args.PipelineSlug, createBuild)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &build)
		}, []string{"write_builds"}
}

type CancelBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
}

func CancelBuild() (mcp.Tool, mcp.ToolHandlerFor[CancelBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "cancel_build",
			Description: "Cancel a running build on a Buildkite pipeline",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Cancel Build",
				DestructiveHint: boolPtr(true),
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args CancelBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.CancelBuild")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
			)

			deps := DepsFromContext(ctx)
			build, err := deps.BuildsClient.Cancel(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &build)
		}, []string{"write_builds"}
}

type RebuildBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
}

func RebuildBuild() (mcp.Tool, mcp.ToolHandlerFor[RebuildBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "rebuild_build",
			Description: "Rebuild/retry an entire build on a Buildkite pipeline",
			Annotations: &mcp.ToolAnnotations{
				Title:           "Rebuild Build",
				DestructiveHint: boolPtr(true),
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args RebuildBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.RebuildBuild")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
			)

			deps := DepsFromContext(ctx)
			build, err := deps.BuildsClient.Rebuild(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &build)
		}, []string{"write_builds"}
}

func convertEntries(entries []Entry) map[string]string {
	if entries == nil {
		return nil
	}

	result := make(map[string]string, len(entries))
	for _, entry := range entries {
		result[entry.Key] = entry.Value
	}
	return result
}
