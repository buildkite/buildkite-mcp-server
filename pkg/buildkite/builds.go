package buildkite

import (
	"context"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type BuildsClient interface {
	Get(ctx context.Context, org, pipelineSlug, buildNumber string, options *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error)
	ListByOrg(ctx context.Context, org string, options *buildkite.BuildsListOptions) ([]buildkite.Build, *buildkite.Response, error)
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
	PipelineSlug string `json:"pipeline_slug,omitempty" jsonschema:"Filter builds by pipeline. When omitted\\, lists builds across all pipelines in the organization"`
	Branch       string `json:"branch,omitempty" jsonschema:"Filter builds by git branch name"`
	State        string `json:"state,omitempty" jsonschema:"Filter builds by state (scheduled\\, running\\, passed\\, failed\\, canceled\\, skipped)"`
	Commit       string `json:"commit,omitempty" jsonschema:"Filter builds by specific commit SHA"`
	Creator      string `json:"creator,omitempty" jsonschema:"Filter builds by build creator"`
	DetailLevel  string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary' (default)\\, 'detailed'\\, or 'full'"` // summary, detailed, full
	Page         int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1\\, max 100)"`
}

// GetBuildArgs struct
type GetBuildArgs struct {
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	DetailLevel  string `json:"detail_level,omitempty" jsonschema:"Response detail level: 'summary'\\, 'detailed' (default)\\, or 'full'"`
	JobState     string `json:"job_state,omitempty" jsonschema:"Filter jobs by state. Comma-separated for multiple states (e.g.\\, 'failed\\,broken\\,canceled')"`
	IncludeAgent bool   `json:"include_agent,omitempty" jsonschema:"Include full agent details in job objects. When false (default)\\, only agent.id is included"`
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

func ListBuilds() (mcp.Tool, mcp.ToolHandlerFor[ListBuildsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_builds",
			Description: "List builds for a pipeline or across all pipelines in an organization. When pipeline_slug is omitted, lists builds across all pipelines in the organization",
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
				// Only exclude pipeline when it's already known from the request
				if args.PipelineSlug != "" {
					options.ExcludePipeline = true
				}
			case "detailed":
				options.ExcludeJobs = true
				if args.PipelineSlug != "" {
					options.ExcludePipeline = true
				}
			case "full":
				// Include everything
			default:
				return utils.NewToolResultError("detail_level must be 'summary', 'detailed', or 'full'"), nil, nil
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
			Description: "Get detailed information about a specific build including its jobs, timing, and execution details",
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

			deps := DepsFromContext(ctx)
			build, _, err := deps.BuildsClient.Get(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
			if err != nil {
				return handleBuildkiteError(err)
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
				return utils.NewToolResultError("detail_level must be 'summary', 'detailed', or 'full'"), nil, nil
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
				Title: "Create Build",
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
