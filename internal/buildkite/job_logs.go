package buildkite

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/buildkite/buildkite-mcp-server/internal/buildkite/joblogs"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func GetJobLogs(ctx context.Context, client *buildkite.Client) (tool mcp.Tool, handler server.ToolHandlerFunc) {
	return mcp.NewTool("get_job_logs",
			mcp.WithDescription("Get the logs of a job in a Buildkite build"),
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
		),
		func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			org, err := requiredParam[string](request, "org")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			pipelineSlug, err := requiredParam[string](request, "pipeline_slug")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			buildNumber, err := requiredParam[string](request, "build_number")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			jobUUID, err := requiredParam[string](request, "job_uuid")
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			joblog, resp, err := client.Jobs.GetJobLog(ctx, org, pipelineSlug, buildNumber, jobUUID)
			if err != nil {
				return mcp.NewToolResultError(err.Error()), nil
			}

			if resp.StatusCode != http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				if err != nil {
					return nil, fmt.Errorf("failed to read response body: %w", err)
				}
				return mcp.NewToolResultError(fmt.Sprintf("failed to get issue: %s", string(body))), nil
			}

			// the default logs that come from the API can be pretty dense with ANSI codes or HTML
			// so we can strip that out before returning it to the LLM
			processedLog, err := joblogs.Process(joblog)
			if err != nil {
				return nil, fmt.Errorf("failed process job log: %w", err)
			}

			return mcp.NewToolResultText(processedLog), nil
		}
}
