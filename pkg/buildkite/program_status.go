package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

const maxJobErrorTailBytes = 64 * 1024

type ProgramStatusClient interface {
	GetJobErrorTail(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, bytes int) (json.RawMessage, *buildkite.Response, error)
}

// GetJobErrorTail implements ProgramStatusClient. The response stays raw JSON
// so fields added by the API reach the caller without an SDK change.
func (a *BuildkiteClientAdapter) GetJobErrorTail(ctx context.Context, org, pipelineSlug, buildNumber, jobID string, bytes int) (json.RawMessage, *buildkite.Response, error) {
	path := fmt.Sprintf("v2/organizations/%s/pipelines/%s/builds/%s/jobs/%s/program_statuses/error_tail",
		url.PathEscape(org), url.PathEscape(pipelineSlug), url.PathEscape(buildNumber), url.PathEscape(jobID))
	if bytes > 0 {
		path += "?" + url.Values{"bytes": {fmt.Sprint(bytes)}}.Encode()
	}

	req, err := a.NewRequest(ctx, http.MethodGet, path, nil)
	if err != nil {
		return nil, nil, err
	}

	var body json.RawMessage
	resp, err := a.Do(req, &body)
	if err != nil {
		return nil, resp, err
	}

	return body, resp, nil
}

// jobErrorTail is the endpoint's response. Its log is plain text, so it skips
// HTML filtering, as job diagnostics output does.
type jobErrorTail struct{ json.RawMessage }

type GetJobErrorTailArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
	JobID        string `json:"job_id" jsonschema:"Buildkite job UUID"`
	Bytes        int    `json:"bytes,omitempty" jsonschema:"Maximum bytes of log text to return (1-65536, default 16384); the text keeps the last whole lines that fit"`
}

func GetJobErrorTail() (mcp.Tool, mcp.ToolHandlerFor[GetJobErrorTailArgs, any], []string) {
	schema, err := jsonschema.For[GetJobErrorTailArgs](nil)
	if err != nil {
		panic(fmt.Sprintf("generate job error tail input schema: %v", err))
	}
	schema.Properties["bytes"].Minimum = new(1.0)
	schema.Properties["bytes"].Maximum = new(float64(maxJobErrorTailBytes))

	return mcp.Tool{
		Name: "get_job_error_tail",
		Description: "Get where a finished job failed, from the OSC 7501 program status reports in its log, and the plain-text log of the failing step up to that point. " +
			"records lists every failure still open when the failing step ended, in log order, and record is the last of them; the log ends at record, or at the cancel for a blocked program. " +
			"Each entry has id (\"\" is the root record), state and app (\"buildkite-agent\" is the agent's own report for a command or hook). " +
			"state error means it failed; idle from the agent means the step was cancelled or timed out, not that it is waiting; blocked means a program was waiting on a person when the step was cancelled. " +
			"log.content has no colours or timestamps; log.start is \"step\" when it starts at the beginning of the failing step and \"limit\" when it was cut to fit. " +
			"Reports are what programs said about themselves, not verified root causes. " +
			"record, records and log are null when the job passed or no failure was reported; then use get_build_failure_summary or the log tools. " +
			"Returns an error while the job is still running, when its log is unavailable, or briefly while the log is first scanned (retry shortly). " +
			"Requires the read_build_logs scope. Returns not found when program status isn't enabled for the organization",
		InputSchema: schema,
		Annotations: &mcp.ToolAnnotations{
			Title:        "Get Job Error Tail",
			ReadOnlyHint: true,
		},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetJobErrorTailArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetJobErrorTail")
		defer span.End()

		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
			attribute.String("build_number", args.BuildNumber),
			attribute.String("job_id", args.JobID),
			attribute.Int("bytes", args.Bytes),
		)

		deps := DepsFromContext(ctx)
		if deps.ProgramStatusClient == nil {
			return utils.NewToolResultError("get_job_error_tail isn't available on this server"), nil, nil
		}
		body, _, err := deps.ProgramStatusClient.GetJobErrorTail(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, args.JobID, args.Bytes)
		if err != nil {
			return handleBuildkiteError(err)
		}

		return mcpTextResult(span, jobErrorTail{body})
	}, []string{"read_build_logs"}
}
