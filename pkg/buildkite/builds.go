package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/cenkalti/backoff/v5"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

type BuildsClient interface {
	Get(ctx context.Context, org, pipelineSlug, buildNumber string, options *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error)
	ListByPipeline(ctx context.Context, org, pipelineSlug string, options *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
	Create(ctx context.Context, org string, pipeline string, b buildkite.CreateBuild) (buildkite.Build, *buildkite.Response, error)
}

// JobSummary represents a summary of jobs grouped by state, with finished jobs classified as passed/failed
type JobSummary struct {
	Total   int            `json:"total"`
	ByState map[string]int `json:"by_state"`
}

// BuildSummary - Essential fields (~85% token reduction)
type BuildSummary struct {
	ID        string               `json:"id"`
	Number    int                  `json:"number"`
	State     string               `json:"state"`
	Branch    string               `json:"branch"`
	Commit    string               `json:"commit"`
	Message   string               `json:"message"`
	WebURL    string               `json:"web_url"`
	CreatedAt *buildkite.Timestamp `json:"created_at"`
	JobsTotal int                  `json:"jobs_total"`
}

// BuildDetail - Medium detail (~60% token reduction)
type BuildDetail struct {
	BuildSummary                      // Embed summary fields
	Source       string               `json:"source"`
	Author       buildkite.Author     `json:"author"`
	StartedAt    *buildkite.Timestamp `json:"started_at"`
	FinishedAt   *buildkite.Timestamp `json:"finished_at"`
	JobSummary   *JobSummary          `json:"job_summary"`
	// Exclude: Jobs[], Env{}, MetaData{}, Pipeline{}, TestEngine{}
}

// BuildWithSummary represents a build with job summary and optionally full job details
type BuildWithSummary struct {
	buildkite.Build
	JobSummary *JobSummary `json:"job_summary"`
}

// ListBuildsArgs struct with enhanced filtering
type ListBuildsArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	Branch       string `json:"branch"`
	State        string `json:"state"`
	Commit       string `json:"commit"`
	Creator      string `json:"creator"`
	DetailLevel  string `json:"detail_level"` // summary, detailed, full
	Page         int    `json:"page"`
	PerPage      int    `json:"per_page"`
}

// GetBuildArgs struct
type GetBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	DetailLevel  string `json:"detail_level"`  // summary, detailed, full
	JobState     string `json:"job_state"`     // NEW: comma-separated states
	IncludeAgent bool   `json:"include_agent"` // NEW: include full agent details
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
		JobsTotal: len(build.Jobs),
	}
}

// detailBuild converts a buildkite.Build to BuildDetail with job summary
// filteredJobs is used for job_summary stats, while build.Jobs is used for jobs_total
func detailBuild(build buildkite.Build, filteredJobs []buildkite.Job) BuildDetail {
	summary := summarizeBuild(build)

	// Create job summary from filtered jobs
	jobSummary := &JobSummary{
		Total:   len(filteredJobs),
		ByState: make(map[string]int),
	}

	for _, job := range filteredJobs {
		if job.State == "" {
			continue
		}
		jobSummary.ByState[job.State]++
	}

	return BuildDetail{
		BuildSummary: summary, // jobs_total reflects ALL jobs (unfiltered)
		Source:       build.Source,
		Author:       build.Author,
		StartedAt:    build.StartedAt,
		FinishedAt:   build.FinishedAt,
		JobSummary:   jobSummary, // job_summary reflects filtered jobs
	}
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

func ListBuilds(client BuildsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[ListBuildsArgs], scopes []string) {
	return mcp.NewTool("list_builds",
			mcp.WithDescription("List all builds for a pipeline with their status, commit information, and metadata"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
			),
			mcp.WithString("branch",
				mcp.Description("Filter builds by git branch name"),
			),
			mcp.WithString("state",
				mcp.Description("Filter builds by state. Supports actual states (scheduled, running, passed, failed, canceled, skipped, etc.)"),
			),
			mcp.WithString("commit",
				mcp.Description("Filter builds by specific commit SHA"),
			),
			mcp.WithString("creator",
				mcp.Description("Filter builds by build creator"),
			),
			mcp.WithString("detail_level",
				mcp.Description("Response detail level: 'summary' (essential fields), 'detailed' (medium detail), or 'full' (complete build data). Default: 'summary'"),
			),
			mcp.WithNumber("page",
				mcp.Description("Page number for pagination (min 1)"),
			),
			mcp.WithNumber("per_page",
				mcp.Description("Results per page for pagination (min 1, max 100)"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Builds",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args ListBuildsArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListBuilds")
			defer span.End()

			// Validate required parameters
			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug parameter is required"), nil
			}
			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug parameter is required"), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("branch", args.Branch),
				attribute.String("state", args.State),
				attribute.String("commit", args.Commit),
				attribute.String("creator", args.Creator),
				attribute.String("detail_level", args.DetailLevel),
				attribute.Int("page", args.Page),
				attribute.Int("per_page", args.PerPage),
			)

			// Set default detail level
			detailLevel := args.DetailLevel
			if detailLevel == "" {
				detailLevel = "summary"
			}

			// Set default pagination
			page := args.Page
			if page == 0 {
				page = 1
			}
			perPage := args.PerPage
			if perPage == 0 {
				perPage = 30
			}

			options := &buildkite.BuildsListOptions{
				ListOptions: buildkite.ListOptions{
					Page:    page,
					PerPage: perPage,
				},
			}

			// Set exclusions based on detail level
			switch detailLevel {
			case "summary":
				options.ExcludeJobs = true
				options.ExcludePipeline = true
			case "detailed":
				options.ExcludeJobs = true
				options.ExcludePipeline = true
			case "full":
				// Include everything
			default:
				return mcp.NewToolResultError("detail_level must be 'summary', 'detailed', or 'full'"), nil
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

			builds, resp, err := client.ListByPipeline(ctx, args.OrgSlug, args.PipelineSlug, options)
			if err != nil {
				return handleAPIError(err), nil
			}

			headers := map[string]string{
				"Link": resp.Header.Get("Link"),
			}

			var result any
			switch detailLevel {
			case "summary":
				result = createPaginatedBuildResult(builds, summarizeBuild, headers)
			case "detailed":
				// For list_builds, use all jobs (no filtering)
				result = createPaginatedBuildResult(builds, func(b buildkite.Build) BuildDetail {
					return detailBuild(b, b.Jobs)
				}, headers)
			case "full":
				result = PaginatedResult[buildkite.Build]{
					Items:   builds,
					Headers: headers,
				}
			}

			r, err := json.Marshal(result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal builds: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}, []string{"read_builds"}
}

func GetBuildTestEngineRuns(client BuildsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[GetBuildTestEngineRunsArgs], scopes []string) {
	return mcp.NewTool("get_build_test_engine_runs",
			mcp.WithDescription("Get test engine runs data for a specific build in Buildkite. This can be used to look up Test Runs."),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
			),
			mcp.WithString("build_number",
				mcp.Required(),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Build Test Engine Runs",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args GetBuildTestEngineRunsArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetBuildTestEngineRuns")
			defer span.End()

			// Validate required parameters
			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug parameter is required"), nil
			}
			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug parameter is required"), nil
			}
			if args.BuildNumber == "" {
				return mcp.NewToolResultError("build_number parameter is required"), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
			)

			build, _, err := client.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.BuildGetOptions{
				IncludeTestEngine: true,
			})
			if err != nil {
				return handleAPIError(err), nil
			}

			// Extract just the test engine runs data
			var testEngineRuns []buildkite.TestEngineRun
			if build.TestEngine != nil {
				testEngineRuns = build.TestEngine.Runs
			}

			return mcpTextResult(span, &testEngineRuns)
		}, []string{"read_builds"}
}

func GetBuild(client BuildsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[GetBuildArgs], scopes []string) {
	return mcp.NewTool("get_build",
			mcp.WithDescription("Get detailed information about a specific build including its jobs, timing, and execution details"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
			),
			mcp.WithString("build_number",
				mcp.Required(),
			),
			mcp.WithString("detail_level",
				mcp.Description("Response detail level: 'summary' (essential fields), 'detailed' (medium detail), or 'full' (complete build data). Default: 'detailed'"),
			),
			mcp.WithString("job_state",
				mcp.Description("Filter jobs by state. Comma-separated for multiple states (e.g., \"failed,broken,canceled\"). Valid states: scheduled, running, passed, failed, canceled, skipped, broken, waiting, waiting_failed, blocked, etc."),
			),
			mcp.WithBoolean("include_agent",
				mcp.Description("Include full agent details in job objects. When false (default), only agent.id is included to reduce response size."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Build",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args GetBuildArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetBuild")
			defer span.End()

			// Validate required parameters
			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug parameter is required"), nil
			}
			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug parameter is required"), nil
			}
			if args.BuildNumber == "" {
				return mcp.NewToolResultError("build_number parameter is required"), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("detail_level", args.DetailLevel),
				attribute.String("job_state", args.JobState),
				attribute.Bool("include_agent", args.IncludeAgent),
			)

			// Set default detail level
			detailLevel := args.DetailLevel
			if detailLevel == "" {
				detailLevel = "detailed"
			}

			// Configure build get options based on detail level
			options := &buildkite.BuildGetOptions{
				IncludeTestEngine: true,
			}

			build, _, err := client.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
			if err != nil {
				return handleAPIError(err), nil
			}

			// Parse job states filter
			var requestedStates map[string]bool
			if args.JobState != "" {
				states := strings.Split(args.JobState, ",")
				requestedStates = make(map[string]bool, len(states))
				for _, state := range states {
					requestedStates[strings.TrimSpace(state)] = true
				}
			}

			// Filter jobs if states specified
			jobs := build.Jobs
			if requestedStates != nil {
				filteredJobs := make([]buildkite.Job, 0)
				for _, job := range build.Jobs {
					if job.State != "" && requestedStates[job.State] {
						filteredJobs = append(filteredJobs, job)
					}
				}
				jobs = filteredJobs
			}

			// Strip agent details if not requested
			if !args.IncludeAgent && len(jobs) > 0 {
				jobsWithMinimalAgent := make([]buildkite.Job, len(jobs))
				for i, job := range jobs {
					jobCopy := job
					// Keep only agent ID, strip verbose details
					jobCopy.Agent = buildkite.Agent{ID: job.Agent.ID}
					jobsWithMinimalAgent[i] = jobCopy
				}
				jobs = jobsWithMinimalAgent
			}

			var result any
			switch detailLevel {
			case "summary":
				// Summary level ignores job filtering
				result = summarizeBuild(build)
			case "detailed":
				// Detailed level uses filtered jobs for job_summary
				result = detailBuild(build, jobs)
			case "full":
				// Full level returns build with filtered jobs
				buildCopy := build
				buildCopy.Pipeline = nil // reduce size by excluding pipeline details

				// Strip fields from jobs
				for i := range jobs {
					jobs[i].WebURL = ""       // not useful in MCP
					jobs[i].RawLogsURL = ""   // provided by another tool
					jobs[i].ArtifactsURL = "" // provided by another tool
					jobs[i].LogsURL = ""      // deprecated
					jobs[i].GraphQLID = ""    // random id not useful in the MCP
				}

				buildCopy.Jobs = jobs
				result = buildCopy
			default:
				return mcp.NewToolResultError("detail_level must be 'summary', 'detailed', or 'full'"), nil
			}

			return mcpTextResult(span, &result)
		}, []string{"read_builds"}
}

type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type CreateBuildArgs struct {
	OrgSlug             string  `json:"org_slug"`
	PipelineSlug        string  `json:"pipeline_slug"`
	Commit              string  `json:"commit"`
	Branch              string  `json:"branch"`
	Message             string  `json:"message"`
	IgnoreBranchFilters bool    `json:"ignore_branch_filters"`
	Environment         []Entry `json:"environment"`
	MetaData            []Entry `json:"metadata"`
}

func CreateBuild(client BuildsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[CreateBuildArgs], scopes []string) {
	return mcp.NewTool("create_build",
			mcp.WithDescription("Trigger a new build on a Buildkite pipeline for a specific commit and branch, with optional environment variables, metadata, and author information"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
			),
			mcp.WithString("commit",
				mcp.Required(),
				mcp.Description("The commit SHA to build"),
			),
			mcp.WithString("branch",
				mcp.Required(),
				mcp.Description("The branch to build"),
			),
			mcp.WithString("message",
				mcp.Required(),
				mcp.Description("The commit message for the build"),
			),
			mcp.WithBoolean("ignore_branch_filters",
				mcp.Description("Whether to ignore branch filters when triggering the build"),
				mcp.DefaultBool(false),
			),
			mcp.WithArray("environment",
				mcp.Items(
					map[string]any{
						"type":     "object",
						"required": []string{"key", "value"},
						"properties": map[string]any{
							"key": map[string]any{
								"type":        "string",
								"description": "The environment variable name",
							},
							"value": map[string]any{
								"type":        "string",
								"description": "The environment variable value",
							},
						},
					},
				),
				mcp.Description("Environment variables to set for the build")),
			mcp.WithArray("metadata",
				mcp.Items(
					map[string]any{
						"type":     "object",
						"required": []string{"key", "value"},
						"properties": map[string]any{
							"key": map[string]any{
								"type":        "string",
								"description": "The meta-data item key",
							},
							"value": map[string]any{
								"type":        "string",
								"description": "The meta-data item value",
							},
						},
					},
				),
				mcp.Description("Meta-data values to set for the build")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Create Build",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args CreateBuildArgs) (*mcp.CallToolResult, error) {
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

			build, _, err := client.Create(ctx, args.OrgSlug, args.PipelineSlug, createBuild)
			if err != nil {
				return handleAPIError(err), nil
			}

			return mcpTextResult(span, &build)
		}, []string{"write_builds"}
}

type WaitForBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	WaitTimeout  int    `json:"wait_timeout"`
}

func WaitForBuild(client BuildsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[WaitForBuildArgs], scopes []string) {
	return mcp.NewTool("wait_for_build",
			mcp.WithDescription("Wait for a specific build to complete"),
			mcp.WithString("org_slug",
				mcp.Required(),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
			),
			mcp.WithString("build_number",
				mcp.Required(),
			),
			mcp.WithNumber("wait_timeout",
				mcp.Description("Timeout in seconds to wait for job completion"),
				mcp.DefaultNumber(300), // 5 minutes
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Wait for Build",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args WaitForBuildArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.WaitForBuild")
			defer span.End()

			// Validate required parameters
			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug parameter is required"), nil
			}
			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug parameter is required"), nil
			}
			if args.BuildNumber == "" {
				return mcp.NewToolResultError("build_number parameter is required"), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.Int("wait_timeout", args.WaitTimeout),
			)

			build, _, err := client.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, &buildkite.BuildGetOptions{})
			if err != nil {
				return handleAPIError(err), nil
			}

			// wait for the build to enter a terminal state
			b := backoff.NewExponentialBackOff()
			b.InitialInterval = 5 * time.Second
			b.MaxInterval = 30 * time.Second

			ticker := backoff.NewTicker(b)
			defer ticker.Stop()

			ctx, cancel := context.WithTimeout(ctx, time.Duration(args.WaitTimeout)*time.Second)
			defer cancel()

			progressToken := request.Params.Meta.ProgressToken
			server := server.ServerFromContext(ctx)

		WAITLOOP:
			for {
				select {
				case <-ctx.Done():
					log.Ctx(ctx).Info().Msg("Context cancelled, stopping build wait loop")

					break WAITLOOP
				case <-ticker.C:
					build, _, err = client.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, nil)
					if err != nil {
						return handleAPIError(err), nil
					}

					log.Ctx(ctx).Info().Str("build_id", build.ID).Str("state", build.State).Int("job_count", len(build.Jobs)).Msg("Build status checked")

					if progressToken != nil {
						log.Ctx(ctx).Info().Any("progress_token", progressToken).Msg("Build progress token")

						total, remaining := completedJobs(build.Jobs)

						// TODO maybe some sort of adaptive backoff based on percentage complete
						if remaining == 1 {
							b.Reset()
						}

						err := server.SendNotificationToClient(
							ctx,
							"notifications/progress",
							map[string]any{
								"build_number":        build.Number,
								"status":              build.State,
								"total_job_count":     total,
								"remaining_job_count": remaining,
								"percentage_complete": calculatePercentage(total, remaining),
								"created_at":          getTimestampStringOrNil(build.CreatedAt),
								"started_at":          getTimestampStringOrNil(build.StartedAt),
							},
						)
						if err != nil {
							return nil, fmt.Errorf("failed to send notification: %w", err)
						}

					}

					if isTerminalState(build.State) {
						break WAITLOOP
					}
				}
			}

			// default to detailed, use all jobs (no filtering)
			result := detailBuild(build, build.Jobs)

			return mcpTextResult(span, &result)
		}, []string{"read_builds"}
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

func getTimestampStringOrNil(ts *buildkite.Timestamp) *string {
	if ts == nil {
		return nil
	}
	str := ts.Format(time.RFC3339)
	return &str
}

// see https://buildkite.com/docs/pipelines/configure/notifications#build-states
func isTerminalState(state string) bool {
	switch state {
	case "passed", "failed", "skipped", "canceled", "blocked":
		return true
	default:
		return false
	}
}

func completedJobs(jobs []buildkite.Job) (total int, remaining int) {
	total = len(jobs)
	for _, job := range jobs {
		if isTerminalState(job.State) {
			remaining++
		}
	}
	return total, remaining
}

// safely calculate the percentage complete
func calculatePercentage(total, remaining int) int {
	if total == 0 {
		return 0
	}
	return (total - remaining) * 100 / total
}
