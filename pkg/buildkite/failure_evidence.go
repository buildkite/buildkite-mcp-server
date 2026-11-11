package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

// Keep the experimental API payload local without adding a Go client release dependency.
type failureEvidence struct{ json.RawMessage }

type GetBuildFailureEvidenceArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineSlug string `json:"pipeline_slug"`
	BuildNumber  string `json:"build_number"`
}

func GetBuildFailureEvidence() (mcp.Tool, mcp.ToolHandlerFor[GetBuildFailureEvidenceArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_build_failure_evidence",
		Description: "Get a log-free evidence pack in one API request, capped by the backend at 10 jobs: terminal hard-failed script jobs ordered by finish time and warning/error annotations. Excludes Test Engine results, soft failures, superseded retries, canceled jobs and jobs that never ran. Use this compact path when annotations may explain a failure; use tail_logs or get_build_failure_summary when logs or broader job coverage are needed. Empty evidence does not mean the build passed. Requires the failure-evidence API rollout for the organization; unavailable endpoints return an error, not a fallback summary.",
		Annotations: &mcp.ToolAnnotations{Title: "Get Build Failure Evidence", ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetBuildFailureEvidenceArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetBuildFailureEvidence")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
			attribute.String("build_number", args.BuildNumber),
		)
		deps := DepsFromContext(ctx)
		path := fmt.Sprintf("v2/organizations/%s/pipelines/%s/builds/%s/failure-evidence",
			url.PathEscape(args.OrgSlug), url.PathEscape(args.PipelineSlug), url.PathEscape(args.BuildNumber))
		req, err := deps.APIClient.NewRequest(ctx, "GET", path, nil)
		if err != nil {
			return handleBuildkiteError(err)
		}
		var evidence failureEvidence
		_, err = deps.APIClient.Do(req, &evidence)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &evidence)
	}, []string{"read_builds"}
}
