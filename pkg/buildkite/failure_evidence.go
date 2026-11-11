package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type GetBuildFailureEvidenceArgs struct {
	ToolInput
	OrgSlug            string `json:"org_slug"`
	PipelineSlug       string `json:"pipeline_slug"`
	BuildNumber        string `json:"build_number"`
	JobLimit           *int   `json:"job_limit,omitempty" jsonschema:"1–50; defaults to 10"`
	IncludeTestResults bool   `json:"include_test_results,omitempty" jsonschema:"Include caller-authorized Test Engine failures; requires read_suites. Defaults to false"`
}

func GetBuildFailureEvidence() (mcp.Tool, mcp.ToolHandlerFor[GetBuildFailureEvidenceArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_build_failure_evidence",
		Description: "Get a bounded, log-free evidence pack in one API request: terminal hard-failed script jobs ordered by finish time, warning/error annotations, and optional job-linked Test Engine failures. Excludes soft failures, superseded retries, canceled jobs and jobs that never ran. Use this compact path when annotations or test results may explain a failure; use tail_logs or get_build_failure_summary when logs or broader job coverage are needed. Empty evidence does not mean the build passed. Requires the failure-evidence API rollout for the organization; unavailable endpoints return an error, not a fallback summary.",
		Annotations: &mcp.ToolAnnotations{Title: "Get Build Failure Evidence", ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetBuildFailureEvidenceArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetBuildFailureEvidence")
		defer span.End()
		span.SetAttributes(
			attribute.String("org_slug", args.OrgSlug),
			attribute.String("pipeline_slug", args.PipelineSlug),
			attribute.String("build_number", args.BuildNumber),
		)
		limit := 10
		if args.JobLimit != nil {
			limit = *args.JobLimit
		}
		if limit < 1 || limit > 50 {
			return utils.NewToolResultError("job_limit must be an integer between 1 and 50"), nil, nil
		}
		options := &buildkite.BuildFailureEvidenceOptions{JobLimit: limit}
		if args.IncludeTestResults {
			options.Include = "test_results"
		}
		deps := DepsFromContext(ctx)
		evidence, _, err := deps.BuildsClient.GetFailureEvidence(ctx, args.OrgSlug, args.PipelineSlug, args.BuildNumber, options)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &evidence)
	}, []string{"read_builds"}
}
