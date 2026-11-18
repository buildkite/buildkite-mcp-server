package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestPoolsClient interface {
	List(context.Context, string, *buildkite.TestPoolsListOptions) (buildkite.TestPoolsList, *buildkite.Response, error)
	Get(context.Context, string, string, string) (buildkite.TestPool, *buildkite.Response, error)
	GetMetrics(context.Context, string, string, string) (buildkite.TestPoolMetrics, *buildkite.Response, error)
}

type ListTestPoolsArgs struct {
	ToolInput
	OrgSlug      string `json:"org_slug"`
	PipelineUUID string `json:"pipeline_uuid" jsonschema:"Pipeline id from get_pipeline, not its slug"`
	BuildUUID    string `json:"build_uuid" jsonschema:"Build id from get_build or list_builds, not the sequential build number"`
	Key          string `json:"key,omitempty" jsonschema:"Case-insensitive exact pool key match"`
	After        string `json:"after,omitempty" jsonschema:"Opaque after cursor from links.next; mutually exclusive with before. Retain the same pipeline, build and key filters"`
	Before       string `json:"before,omitempty" jsonschema:"Opaque before cursor from links.prev; mutually exclusive with after. Retain the same pipeline, build and key filters"`
	PerPage      int    `json:"per_page,omitempty" jsonschema:"Results per page (1-100; omitted or 0 uses 30)"`
}

type GetTestPoolArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	PoolUUID  string `json:"pool_uuid" jsonschema:"Pool id returned by list_test_pools or supplied by the user; never invent a placeholder id to probe access errors"`
	BuildUUID string `json:"build_uuid" jsonschema:"UUID of the pool's build, not its sequential build number"`
}

// TestPoolSummary avoids repeating policies and potentially large muted-test
// snapshots for every pool on a discovery page. GetTestPool returns detail.
type TestPoolSummary struct {
	ID          string                   `json:"id"`
	SuiteID     string                   `json:"suite_id"`
	Key         string                   `json:"key"`
	State       string                   `json:"state"`
	ExpiresAt   *buildkite.Timestamp     `json:"expires_at"`
	Error       *buildkite.TestPoolError `json:"error,omitempty"`
	Parallelism *int                     `json:"parallelism,omitempty"`
}

type TestPoolsPage struct {
	Items []TestPoolSummary            `json:"items"`
	Links buildkite.TestPoolsListLinks `json:"links"`
}

func ListTestPools() (mcp.Tool, mcp.ToolHandlerFor[ListTestPoolsArgs, any], []string) {
	return mcp.Tool{
		Name:        "list_test_pools",
		Description: "Find Test Scheduler pools for one pipeline/build before inspecting pool errors or workload progress. Returns one page of summaries and cursor links; use get_test_pool for policies and optional planning detail, get_test_pool_metrics for progress. No automatic pagination. A successful empty list means no accessible, unexpired pools were returned, not that tests passed or access was denied. Requires read_test_pool and scheduler API enablement; 403 can explicitly report a missing scope, while 404 does not distinguish missing, inaccessible, expired or gated resources.",
		Annotations: &mcp.ToolAnnotations{Title: "List Test Pools", ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args ListTestPoolsArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.ListTestPools")
		defer span.End()
		if args.After != "" && args.Before != "" {
			return utils.NewToolResultError("'after' and 'before' are mutually exclusive; provide at most one"), nil, nil
		}
		if args.PerPage < 0 || args.PerPage > 100 {
			return utils.NewToolResultError("per_page must be between 1 and 100, or 0 for the default of 30"), nil, nil
		}
		perPage := args.PerPage
		if perPage == 0 {
			perPage = 30
		}
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("pipeline_uuid", args.PipelineUUID),
			attribute.String("build_uuid", args.BuildUUID), attribute.Int("per_page", perPage))
		deps := DepsFromContext(ctx)
		pools, _, err := deps.TestPoolsClient.List(ctx, args.OrgSlug, &buildkite.TestPoolsListOptions{
			PipelineID: args.PipelineUUID, BuildID: args.BuildUUID, Key: args.Key,
			After: args.After, Before: args.Before, PerPage: perPage,
		})
		if err != nil {
			return handleBuildkiteError(err)
		}
		page := TestPoolsPage{Items: make([]TestPoolSummary, 0, len(pools.Items)), Links: pools.Links}
		for _, pool := range pools.Items {
			page.Items = append(page.Items, TestPoolSummary{
				ID: pool.ID, SuiteID: pool.SuiteID, Key: pool.Key, State: pool.State,
				ExpiresAt: pool.ExpiresAt, Error: pool.Error, Parallelism: pool.Parallelism,
			})
		}
		span.SetAttributes(attribute.Int("item_count", len(page.Items)))
		return mcpTextResult(span, &page)
	}, []string{"read_test_pool"}
}

func GetTestPool() (mcp.Tool, mcp.ToolHandlerFor[GetTestPoolArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_test_pool",
		Description: "Inspect one Test Scheduler pool's state, error, retry/lease policy, expiry and optional parallelism/muted-test planning snapshot. A generic 'Test pool failed.' error does not identify the failure phase or cause; do not label it a confirmed planning failure. max_attempts limits policy-created attempts, not how often an attempt was leased or executed. Muted tests are not the pending backlog. Use get_test_pool_metrics for aggregate workload progress; individual pending tests and lease operations are not exposed. Requires read_test_pool and build/suite access. A 404 can mean missing, inaccessible, expired or feature-gated data.",
		Annotations: &mcp.ToolAnnotations{Title: "Get Test Pool", ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetTestPoolArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetTestPool")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("pool_uuid", args.PoolUUID), attribute.String("build_uuid", args.BuildUUID))
		deps := DepsFromContext(ctx)
		pool, _, err := deps.TestPoolsClient.Get(ctx, args.OrgSlug, args.PoolUUID, args.BuildUUID)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &pool)
	}, []string{"read_test_pool"}
}

func GetTestPoolMetrics() (mcp.Tool, mcp.ToolHandlerFor[GetTestPoolArgs, any], []string) {
	return mcp.Tool{
		Name:        "get_test_pool_metrics",
		Description: "Inspect one Test Scheduler pool's aggregate entry/attempt totals, state/result counts, estimated workload, current lease holds and timing. Waiting attempts may have been requeued: current counts cannot prove never leased, never ran, stuck work, worker failure or ongoing progress. Leased attempts include holds under active AND expired leases; expired counts are not historical totals or proof of worker death. Summed p50 workload is neither elapsed duration nor ETA; lease workload budgets are not lease ages. The oldest waiting timestamp is attempt creation, not every attempt's age or uninterrupted wait. Scope findings and expiry claims to observed_at, not guaranteed current state or freshness; separate reads are not atomic. Neither drained=true nor zero active leases alone proves completion or success: an errored/drained pool can retain waiting work, with null timing. Aggregates cannot name individual pending tests. Requires read_test_pool; 404 can mean missing, inaccessible, expired or gated data.",
		Annotations: &mcp.ToolAnnotations{Title: "Get Test Pool Metrics", ReadOnlyHint: true},
	}, func(ctx context.Context, request *mcp.CallToolRequest, args GetTestPoolArgs) (*mcp.CallToolResult, any, error) {
		ctx, span := trace.Start(ctx, "buildkite.GetTestPoolMetrics")
		defer span.End()
		span.SetAttributes(attribute.String("org_slug", args.OrgSlug), attribute.String("pool_uuid", args.PoolUUID), attribute.String("build_uuid", args.BuildUUID))
		deps := DepsFromContext(ctx)
		metrics, _, err := deps.TestPoolsClient.GetMetrics(ctx, args.OrgSlug, args.PoolUUID, args.BuildUUID)
		if err != nil {
			return handleBuildkiteError(err)
		}
		return mcpTextResult(span, &metrics)
	}, []string{"read_test_pool"}
}
