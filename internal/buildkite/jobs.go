package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/buildkite/buildkite-mcp-server/internal/buildkite/joblogs"
	"github.com/buildkite/buildkite-mcp-server/internal/tokens"
	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
)

// withJobsPagination adds client-side pagination options to a tool with a max of 50 per page
func withJobsPagination() mcp.ToolOption {
	return func(tool *mcp.Tool) {
		mcp.WithNumber("page",
			mcp.Description("Page number for pagination (min 1)"),
			mcp.Min(1),
		)(tool)

		mcp.WithNumber("perPage",
			mcp.Description("Results per page for pagination (min 1, max 50)"),
			mcp.Min(1),
			mcp.Max(50),
		)(tool)
	}
}

func GetJobs(ctx context.Context, client BuildsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_jobs",
			mcp.WithDescription("Get all jobs for a specific build including their state, timing, commands, and execution details"),
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
			mcp.WithString("job_state",
				mcp.Description("Filter jobs by state. Supports actual states (scheduled, running, passed, failed, canceled, skipped, etc.)"),
			),
			mcp.WithBoolean("include_agent",
				mcp.Description("Include detailed agent information in the response. When false (default), only agent ID is included to reduce response size."),
			),
			withJobsPagination(),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Jobs",
				ReadOnlyHint: mcp.ToBoolPtr(true),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetJobs")
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

			jobStateFilter := request.GetString("job_state", "")
			includeAgent := request.GetBool("include_agent", false)

			// Get client-side pagination parameters (always enabled)
			paginationParams := getClientSidePaginationParams(request)

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("build_number", buildNumber),
				attribute.String("job_state", jobStateFilter),
				attribute.Bool("include_agent", includeAgent),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
			)

			build, resp, err := client.Get(ctx, org, pipelineSlug, buildNumber, &buildkite.BuildGetOptions{})
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get build: %s", string(body))), nil
			}

			jobs := build.Jobs

			// Filter jobs by state if specified
			if jobStateFilter != "" {
				filteredJobs := make([]buildkite.Job, 0)
				for _, job := range build.Jobs {
					if job.State == jobStateFilter {
						filteredJobs = append(filteredJobs, job)
					}
				}
				jobs = filteredJobs
			}

			// Remove agent details if not requested to reduce response size, but keep agent ID
			if !includeAgent {
				jobsWithoutAgent := make([]buildkite.Job, len(jobs))
				for i, job := range jobs {
					jobCopy := job
					// Keep only the agent ID, remove all other verbose agent details
					jobCopy.Agent = buildkite.Agent{ID: job.Agent.ID}
					jobsWithoutAgent[i] = jobCopy
				}
				jobs = jobsWithoutAgent
			}

			// Always apply client-side pagination
			result := applyClientSidePagination(jobs, paginationParams)
			r, err := json.Marshal(&result)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal jobs: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

func GetJobLogs(ctx context.Context, client *buildkite.Client) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_job_logs",
			mcp.WithDescription("Get the log output and metadata for a specific job, including content, size, and header timestamps. Automatically saves to file for large logs to avoid token limits."),
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
				mcp.Description("The build number"),
			),
			mcp.WithString("job_uuid",
				mcp.Required(),
				mcp.Description("The UUID of the job"),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Get Job Logs",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetJobLogs")
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

			jobUUID, err := request.RequireString("job_uuid")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("build_number", buildNumber),
				attribute.String("job_uuid", jobUUID),
			)

			// Get job logs from API
			joblog, resp, err := client.Jobs.GetJobLog(ctx, org, pipelineSlug, buildNumber, jobUUID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get job logs: %s", string(body))), nil
			}

			// the default logs that come from the API can be pretty dense with ANSI codes or HTML
			// so we can strip that out before returning it to the LLM
			processedLog, err := joblogs.Process(joblog)
			if err != nil {
				return nil, fmt.Errorf("failed to process job log: %w", err)
			}

			// Estimate tokens to decide delivery mode
			tokenCount := tokens.EstimateTokens(processedLog)
			span.SetAttributes(
				attribute.Int("token_count", tokenCount),
			)

			// if a threshold is set, we can use it to determine if we should switch to file mode
			// this allows us to handle large logs more efficiently
			// and avoid hitting token limits in the LLM
			// this is configurable via the BUILDKITE_MCP_LOG_TOKEN_THRESHOLD environment variable
			// if not set, we default to -1 which means no threshold
			if threshold, ok := getTokenThreshold(); ok {
				span.SetAttributes(attribute.Int("token_threshold", threshold))

				// Smart switching: use file mode for large logs
				if tokenCount > threshold {
					return handleLargeLogFile(ctx, processedLog, JobLogsResponse{
						TokenCount:  tokenCount,
						JobUUID:     jobUUID,
						BuildNumber: buildNumber,
						Reason:      fmt.Sprintf("Log exceeded %d token threshold", threshold),
					}, threshold)
				}
			}

			// Inline mode for small logs
			span.SetAttributes(attribute.String("delivery_mode", "inline"))

			response := JobLogsResponse{
				DeliveryMode: "inline",
				Content:      processedLog,
				TokenCount:   tokenCount,
				JobUUID:      jobUUID,
				BuildNumber:  buildNumber,
			}

			r, err := json.Marshal(&response)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal response: %w", err)
			}

			return mcp.NewToolResultText(string(r)), nil
		}
}

// JobLogsResponse represents the unified response for job logs
type JobLogsResponse struct {
	DeliveryMode  string `json:"delivery_mode"`             // "inline" | "file"
	Content       string `json:"content,omitempty"`         // Only for inline
	FilePath      string `json:"file_path,omitempty"`       // Only for file
	FileSizeBytes int64  `json:"file_size_bytes,omitempty"` // Only for file
	TokenCount    int    `json:"token_count"`               // Always included
	JobUUID       string `json:"job_uuid"`                  // Always included
	BuildNumber   string `json:"build_number"`              // Always included
	Reason        string `json:"reason,omitempty"`          // Why file mode was chosen
}

// getTokenThreshold returns the configurable token threshold for switching to file mode
func getTokenThreshold() (int, bool) {
	if thresholdStr := os.Getenv("BUILDKITE_MCP_LOG_TOKEN_THRESHOLD"); thresholdStr != "" {
		if threshold, err := strconv.Atoi(thresholdStr); err == nil && threshold > 0 {
			return threshold, true
		}
	}
	return -1, false
}

// handleLargeLogFile handles saving large logs to file
func handleLargeLogFile(ctx context.Context, processedLog string, response JobLogsResponse, threshold int) (*mcp.CallToolResult, error) {
	_, span := trace.Start(ctx, "buildkite.handleLargeLogFile")
	defer span.End()

	outputDir, err := os.MkdirTemp("", "buildkite-job-logs-") // Ensure the directory exists
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary output directory: %w", err)
	}

	filePath := filepath.Join(outputDir, "job.log")

	// Create and write file
	file, err := os.Create(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to create log file: %w", err)
	}
	defer file.Close()

	_, err = file.WriteString(processedLog)
	if err != nil {
		return nil, fmt.Errorf("failed to write log file: %w", err)
	}

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("failed to get file info: %w", err)
	}

	// Get absolute path for response
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to get absolute file path: %w", err)
	}

	span.SetAttributes(
		attribute.String("file_path", absPath),
		attribute.Int64("file_size_bytes", fileInfo.Size()),
	)

	response.DeliveryMode = "file"
	response.FilePath = absPath
	response.FileSizeBytes = fileInfo.Size()
	response.Reason = fmt.Sprintf("Log exceeded %d token threshold, saved to file", threshold)

	r, err := json.Marshal(&response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return mcp.NewToolResultText(string(r)), nil
}
