package buildkite

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
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

// BuildWithSummary represents a build with job summary and optionally full job details
type BuildWithSummary struct {
	buildkite.Build
	JobSummary *JobSummary `json:"job_summary"`
}

func ListBuilds(ctx context.Context, client BuildsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("list_builds",
			mcp.WithDescription("List all builds for a pipeline with their status, commit information, and metadata"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
				mcp.Description("The slug of the pipeline"),
			),
			mcp.WithString("branch",
				mcp.Description("Filter builds by git branch name"),
			),
			withPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "List Builds",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListBuilds")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := request.RequireString("pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			branch := request.GetString("branch", "")

			paginationParams, err := optionalPaginationParams(request)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("branch", branch),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			options := &buildkite.BuildsListOptions{
				ExcludeJobs:     true,
				ExcludePipeline: true,
				ListOptions:     paginationParams,
			}
			if branch != "" {
				options.Branch = []string{branch}
			}

			builds, resp, err := client.ListByPipeline(ctx, org, pipelineSlug, options)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			result := PaginatedResult[buildkite.Build]{
				Items: builds,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			r, err := json.Marshal(&result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal builds: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetBuildTestEngineRuns(ctx context.Context, client BuildsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_build_test_engine_runs",
			mcp.WithDescription("Get test engine runs data for a specific build in Buildkite. This can be used to look up Test Runs."),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
				mcp.Description("The slug of the pipeline"),
			),
			mcp.WithString("build_number",
				mcp.Required(),
				mcp.Description("The number of the build"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Build Test Engine Runs",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetBuildTestEngineRuns")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := request.RequireString("pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			buildNumber, err := request.RequireString("build_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("build_number", buildNumber),
			)

			build, _, err := client.Get(ctx, org, pipelineSlug, buildNumber, &buildkite.BuildGetOptions{
				IncludeTestEngine: true,
			})
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			// Extract just the test engine runs data
			var testEngineRuns []buildkite.TestEngineRun
			if build.TestEngine != nil {
				testEngineRuns = build.TestEngine.Runs
			}

			r, err := json.Marshal(&testEngineRuns)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal test engine runs: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetBuild(ctx context.Context, client BuildsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_build",
			mcp.WithDescription("Get detailed information about a specific build including its jobs, timing, and execution details"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
				mcp.Description("The slug of the pipeline"),
			),
			mcp.WithString("build_number",
				mcp.Required(),
				mcp.Description("The number of the build"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Build",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetBuild")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := request.RequireString("pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			buildNumber, err := request.RequireString("build_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("build_number", buildNumber),
			)

			build, _, err := client.Get(ctx, org, pipelineSlug, buildNumber, &buildkite.BuildGetOptions{
				IncludeTestEngine: true,
			})
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			// Create job summary
			jobSummary := &JobSummary{
				Total:   len(build.Jobs),
				ByState: make(map[string]int),
			}

			for _, job := range build.Jobs {
				if job.State == "" {
					continue
				}

				jobSummary.ByState[job.State]++
			}

			// Create build with summary - always exclude job details
			buildWithSummary := BuildWithSummary{
				Build:      build,
				JobSummary: jobSummary,
			}
			buildWithSummary.Jobs = nil

			r, err := json.Marshal(&buildWithSummary)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal build: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

type Entry struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type CreateBuildArgs struct {
	Org          string
	PipelineSlug string
	Commit       string
	Branch       string
	Message      string
	Environment  []Entry
	MetaData     []Entry
}

func CreateBuild(ctx context.Context, client BuildsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[CreateBuildArgs]) {
	return mcp.NewTool("create_build",
			mcp.WithDescription("Trigger a new build on a Buildkite pipeline for a specific commit and branch, with optional environment variables, metadata, and author information"),
			mcp.WithString("org",
				mcp.Required(),
				mcp.Description("The organization slug for the owner of the pipeline"),
			),
			mcp.WithString("pipeline_slug",
				mcp.Required(),
				mcp.Description("The slug of the pipeline"),
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
			mcp.WithArray("environment",
				mcp.Items(
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"key": map[string]any{
								"type":        "string",
								"description": "The name of the environment variable",
								"required":    true,
							},
							"value": map[string]any{
								"type":        "string",
								"description": "The value of the environment variable",
								"required":    true,
							},
						},
					},
				),
				mcp.Description("Environment variables to set for the build")),
			mcp.WithArray("metadata",
				mcp.Items(
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"key": map[string]any{
								"type":        "string",
								"description": "The name of the environment variable",
								"required":    true,
							},
							"value": map[string]any{
								"type":        "string",
								"description": "The value of the environment variable",
								"required":    true,
							},
						},
					},
				),
				mcp.Description("Environment variables to set for the build")),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Create Build",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args CreateBuildArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.CreateBuild")
			defer span.End()

			org, err := request.RequireString("org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := request.RequireString("pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			createBuild := buildkite.CreateBuild{
				Commit:   args.Commit,
				Branch:   args.Branch,
				Message:  args.Message,
				Env:      convertEntries(args.Environment),
				MetaData: convertEntries(args.MetaData),
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
			)

			build, _, err := client.Create(ctx, org, pipelineSlug, createBuild)
			if err != nil {
				var errResp *buildkite.ErrorResponse
				if errors.As(err, &errResp) {
					if errResp.RawBody != nil {
						return mcp.NewToolResultError(string(errResp.RawBody)), nil
					}
				}

				return mcp.NewToolResultError(err.Error()), nil
			}

			r, err := json.Marshal(&build)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal created build: %w", err)
			}
			return mcp.NewToolResultText(string(r)), nil
		}
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
