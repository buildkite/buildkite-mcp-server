package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/buildkite/buildkite-mcp-server/internal/buildkite/joblogs"
	"github.com/buildkite/buildkite-mcp-server/internal/tokens"
	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"go.opentelemetry.io/otel/attribute"
	oteltrace "go.opentelemetry.io/otel/trace"
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
			mcp.WithString("output_dir",
				mcp.Description("Directory to save log file when using file mode (defaults to system temp directory)"),
			),
			mcp.WithString("filename_prefix",
				mcp.Description("Prefix for log filename when using file mode (defaults to job UUID)"),
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

			outputDir := request.GetString("output_dir", "")
			filenamePrefix := request.GetString("filename_prefix", jobUUID)

			span.SetAttributes(
				attribute.String("org", org),
				attribute.String("pipeline_slug", pipelineSlug),
				attribute.String("build_number", buildNumber),
				attribute.String("job_uuid", jobUUID),
				attribute.String("output_dir", outputDir),
				attribute.String("filename_prefix", filenamePrefix),
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
			threshold := getTokenThreshold()

			span.SetAttributes(
				attribute.Int("token_count", tokenCount),
				attribute.Int("token_threshold", threshold),
			)

			// Smart switching: use file mode for large logs
			if tokenCount > threshold {
				return handleLargeLogFile(ctx, span, processedLog, tokenCount, org, pipelineSlug, buildNumber, jobUUID, outputDir, filenamePrefix, threshold)
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

// sanitizeFilename removes unsafe characters from filename components
var unsafeChars = regexp.MustCompile(`[^\w\-_.]`)

func sanitizeFilename(name string) string {
	return unsafeChars.ReplaceAllString(name, "_")
}

// JobLogsResponse represents the unified response for job logs
type JobLogsResponse struct {
	DeliveryMode    string `json:"delivery_mode"`              // "inline" | "file"
	Content         string `json:"content,omitempty"`          // Only for inline
	FilePath        string `json:"file_path,omitempty"`        // Only for file
	FileSizeBytes   int64  `json:"file_size_bytes,omitempty"`  // Only for file
	TokenCount      int    `json:"token_count"`                // Always included
	JobUUID         string `json:"job_uuid"`                   // Always included
	BuildNumber     string `json:"build_number"`               // Always included
	Reason          string `json:"reason,omitempty"`           // Why file mode was chosen
}

// getTokenThreshold returns the configurable token threshold for switching to file mode
func getTokenThreshold() int {
	if thresholdStr := os.Getenv("BUILDKITE_MCP_LOG_TOKEN_THRESHOLD"); thresholdStr != "" {
		if threshold, err := strconv.Atoi(thresholdStr); err == nil && threshold > 0 {
			return threshold
		}
	}
	return 12000 // Default threshold
}

// handleLargeLogFile handles saving large logs to file
func handleLargeLogFile(ctx context.Context, span oteltrace.Span, processedLog string, tokenCount int, org, pipelineSlug, buildNumber, jobUUID, outputDir, filenamePrefix string, threshold int) (*mcp.CallToolResult, error) {
	span.SetAttributes(attribute.String("delivery_mode", "file"))

	// Determine output directory
	if outputDir == "" {
		outputDir = os.TempDir()
	} else {
		// Clean and validate the path
		outputDir = filepath.Clean(outputDir)
		if strings.Contains(outputDir, "..") {
			return mcp.NewToolResultError("invalid output directory: path traversal not allowed"), nil
		}
	}

	// Verify directory exists and is writable, fallback to inline if file operations fail
	if info, err := os.Stat(outputDir); err != nil {
		// Fallback to inline mode on directory error
		response := JobLogsResponse{
			DeliveryMode: "inline",
			Content:      processedLog,
			TokenCount:   tokenCount,
			JobUUID:      jobUUID,
			BuildNumber:  buildNumber,
			Reason:       fmt.Sprintf("Intended file mode due to %d tokens > %d threshold, but fell back to inline due to directory error: %s", tokenCount, threshold, err.Error()),
		}
		r, err := json.Marshal(&response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal fallback response: %w", err)
		}
		return mcp.NewToolResultText(string(r)), nil
	} else if !info.IsDir() {
		// Fallback to inline mode if path is not a directory
		response := JobLogsResponse{
			DeliveryMode: "inline",
			Content:      processedLog,
			TokenCount:   tokenCount,
			JobUUID:      jobUUID,
			BuildNumber:  buildNumber,
			Reason:       fmt.Sprintf("Intended file mode due to %d tokens > %d threshold, but fell back to inline because output path is not a directory", tokenCount, threshold),
		}
		r, err := json.Marshal(&response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal fallback response: %w", err)
		}
		return mcp.NewToolResultText(string(r)), nil
	}

	// Generate safe filename
	timestamp := time.Now().Unix()
	sanitizedPrefix := sanitizeFilename(filenamePrefix)
	sanitizedBuildNumber := sanitizeFilename(buildNumber)
	filename := fmt.Sprintf("%s_%s_%s_%d.log", sanitizedPrefix, sanitizedBuildNumber, jobUUID, timestamp)
	filePath := filepath.Join(outputDir, filename)

	// Create and write file
	file, err := os.Create(filePath)
	if err != nil {
		// Fallback to inline mode on file creation error
		response := JobLogsResponse{
			DeliveryMode: "inline",
			Content:      processedLog,
			TokenCount:   tokenCount,
			JobUUID:      jobUUID,
			BuildNumber:  buildNumber,
			Reason:       fmt.Sprintf("Intended file mode due to %d tokens > %d threshold, but fell back to inline due to file creation error: %s", tokenCount, threshold, err.Error()),
		}
		r, err := json.Marshal(&response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal fallback response: %w", err)
		}
		return mcp.NewToolResultText(string(r)), nil
	}
	defer file.Close()

	_, err = file.WriteString(processedLog)
	if err != nil {
		// Clean up partial file and fallback to inline
		os.Remove(filePath)
		response := JobLogsResponse{
			DeliveryMode: "inline",
			Content:      processedLog,
			TokenCount:   tokenCount,
			JobUUID:      jobUUID,
			BuildNumber:  buildNumber,
			Reason:       fmt.Sprintf("Intended file mode due to %d tokens > %d threshold, but fell back to inline due to file write error: %s", tokenCount, threshold, err.Error()),
		}
		r, err := json.Marshal(&response)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal fallback response: %w", err)
		}
		return mcp.NewToolResultText(string(r)), nil
	}

	// Get file info
	fileInfo, err := file.Stat()
	if err != nil {
		return mcp.NewToolResultError(fmt.Sprintf("failed to get file info: %s", err.Error())), nil
	}

	// Get absolute path for response
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		absPath = filePath // fallback to relative path
	}

	span.SetAttributes(
		attribute.String("file_path", absPath),
		attribute.Int64("file_size_bytes", fileInfo.Size()),
	)

	response := JobLogsResponse{
		DeliveryMode:  "file",
		FilePath:      absPath,
		FileSizeBytes: fileInfo.Size(),
		TokenCount:    tokenCount,
		JobUUID:       jobUUID,
		BuildNumber:   buildNumber,
		Reason:        fmt.Sprintf("Log exceeded %d token threshold", threshold),
	}

	r, err := json.Marshal(&response)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal response: %w", err)
	}

	return mcp.NewToolResultText(string(r)), nil
}
