package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite/joblogs"
	"github.com/buildkite/buildkite-mcp-server/pkg/config"
	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
	"go.opentelemetry.io/otel/attribute"
)

// JobsClient defines the interface for job operations
type JobsClient interface {
	UnblockJob(ctx context.Context, org, pipeline, buildNumber, jobID string, opt *buildkite.JobUnblockOptions) (buildkite.Job, *buildkite.Response, error)
}

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

func GetJobs(client BuildsClient) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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

func GetJobLogs(client *buildkite.Client) (tool mcp.Tool, handler server.ToolHandlerFunc) {
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

			cfg := config.FromContext(ctx)

			// if a threshold is set, we can use it to determine if we should switch to file mode
			// this allows us to handle large logs more efficiently
			// and avoid hitting token limits in the LLM
			if threshold := cfg.JobLogTokenThreshold; threshold > 0 {
				span.SetAttributes(attribute.Int("token_threshold", threshold))

				// Smart switching: use file mode for large logs
				if tokenCount > threshold {

					log.Info().Int("token_count", tokenCount).Msg("Job log exceeds token threshold, switching to file mode")

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

// UnblockJobArgs represents the arguments for unblocking a job
type UnblockJobArgs struct {
	OrgSlug      string            `json:"org_slug"`
	PipelineSlug string            `json:"pipeline_slug"`
	BuildNumber  string            `json:"build_number"`
	JobID        string            `json:"job_id"`
	Fields       map[string]string `json:"fields,omitempty"`
}

func UnblockJob(client JobsClient) (tool mcp.Tool, handler mcp.TypedToolHandlerFunc[UnblockJobArgs]) {
	return mcp.NewTool("unblock_job",
			mcp.WithDescription("Unblock a blocked job in a build pipeline. This allows you to continue pipeline execution that is waiting on manual approval or input."),
			mcp.WithString("org_slug",
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
			mcp.WithString("job_id",
				mcp.Required(),
				mcp.Description("The UUID of the job to unblock"),
			),
			mcp.WithObject("fields",
				mcp.Description("Optional form field values to submit with the unblock request. This should be a map of field keys to their values as defined in the block step configuration."),
			),
			mcp.WithToolAnnotation(mcp.ToolAnnotation{
				Title:        "Unblock Job",
				ReadOnlyHint: mcp.ToBoolPtr(false),
			}),
		),
		func(ctx context.Context, request mcp.CallToolRequest, args UnblockJobArgs) (*mcp.CallToolResult, error) {
			ctx, span := trace.Start(ctx, "buildkite.UnblockJob")
			defer span.End()

			if args.OrgSlug == "" {
				return mcp.NewToolResultError("org_slug is required"), nil
			}
			if args.PipelineSlug == "" {
				return mcp.NewToolResultError("pipeline_slug is required"), nil
			}
			if args.BuildNumber == "" {
				return mcp.NewToolResultError("build_number is required"), nil
			}
			if args.JobID == "" {
				return mcp.NewToolResultError("job_id is required"), nil
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("pipeline_slug", args.PipelineSlug),
				attribute.String("build_number", args.BuildNumber),
				attribute.String("job_id", args.JobID),
			)

			// Prepare unblock options
			var unblockOptions *buildkite.JobUnblockOptions
			if args.Fields != nil && len(args.Fields) > 0 {
				unblockOptions = &buildkite.JobUnblockOptions{
					Fields: args.Fields,
				}
			}

			// Call the API to unblock the job
			job, resp, err := client.UnblockJob(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, unblockOptions)
			if err != nil {
				return mcp.NewToolResultError(fmt.Sprintf("failed to unblock job: %s", err.Error())), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to unblock job: %s", string(body))), nil
			}

			// Marshal the response
			r, err := json.Marshal(&job)
			if err != nil {
				return nil, fmt.Errorf("failed to marshal unblocked job: %w", err)
			}

			log.Info().
				Str("org_slug", args.OrgSlug).
				Str("pipeline_slug", args.PipelineSlug).
				Str("build_number", args.BuildNumber).
				Str("job_id", args.JobID).
				Msg("Successfully unblocked job")

			return mcp.NewToolResultText(string(r)), nil
		}
}
