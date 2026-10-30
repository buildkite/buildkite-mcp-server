package buildkite

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type SpanAggregatesClient interface {
	ListScopesBySuite(ctx context.Context, org, slug string, opt *buildkite.SpanAggregateScopesListOptions) ([]buildkite.SpanAggregateScope, *buildkite.Response, error)
	ListScopesByBuild(ctx context.Context, org, buildUUID string) ([]buildkite.SpanAggregateScope, *buildkite.Response, error)
	ListNamesBySuite(ctx context.Context, org, slug, scopeName string, opt *buildkite.SpanAggregateSuiteNamesListOptions) (buildkite.SpanAggregateNames, *buildkite.Response, error)
	ListNamesByBuild(ctx context.Context, org, buildUUID, scopeName string, opt *buildkite.SpanAggregateNamesListOptions) (buildkite.SpanAggregateNames, *buildkite.Response, error)
}

type ListSpanAggregateScopesArgs struct {
	ToolInput
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
	Period        string `json:"period,omitempty" jsonschema:"One of '1hour', '4hours', '1day', '7days', '14days', or '28days'; defaults to the API-selected period. Cannot exceed the organization's API history allowance."`
}

type ListSpanAggregateScopesForBuildArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	BuildUUID string `json:"build_uuid" jsonschema:"Buildkite build UUID, not the pipeline build number"`
}

type ListSpanAggregateNamesArgs struct {
	ToolInput
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
	ScopeName     string `json:"scope_name" jsonschema:"Exact instrumentation scope name; an empty string selects the unnamed scope"`
	Period        string `json:"period,omitempty" jsonschema:"One of '1hour', '4hours', '1day', '7days', '14days', or '28days'; defaults to the API-selected period. Cannot exceed the organization's API history allowance."`
	Sort          string `json:"sort,omitempty" jsonschema:"One of 'count', 'total', 'avg', 'p50', 'p95', or 'max'; prefix with '-' for descending order. Defaults to '-total'; ties sort by name ascending."`
	Limit         int    `json:"limit,omitempty" jsonschema:"Maximum span names to return (1-100); defaults to 20. span_names_truncated indicates more names exist."`
}

type ListSpanAggregateNamesForBuildArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	BuildUUID string `json:"build_uuid" jsonschema:"Buildkite build UUID, not the pipeline build number"`
	ScopeName string `json:"scope_name" jsonschema:"Exact instrumentation scope name; an empty string selects the unnamed scope"`
	Sort      string `json:"sort,omitempty" jsonschema:"One of 'count', 'total', 'avg', 'p50', 'p95', or 'max'; prefix with '-' for descending order. Defaults to '-total'; ties sort by name ascending."`
	Limit     int    `json:"limit,omitempty" jsonschema:"Maximum span names to return (1-100); defaults to 20. span_names_truncated indicates more names exist."`
}

func ListSpanAggregateScopes() (mcp.Tool, mcp.ToolHandlerFor[ListSpanAggregateScopesArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_span_aggregate_scopes",
			Description: "List OpenTelemetry instrumentation scopes for a Test Engine suite, ordered by total span duration descending. Start here for suite-wide bottlenecks; drill down with list_span_aggregate_names. Returns span counts and summed durations in nanoseconds, not wall-clock time. Durations are inclusive; nested spans overlap, so do not add their durations together. Aggregates have hourly precision and one-month retention. Requires OTel trace ingestion to be enabled. Results are not paginated.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Span Aggregate Scopes",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListSpanAggregateScopesArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListSpanAggregateScopes")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("period", args.Period),
			)

			deps := DepsFromContext(ctx)
			scopes, _, err := deps.SpanAggregatesClient.ListScopesBySuite(ctx, args.OrgSlug, args.TestSuiteSlug, &buildkite.SpanAggregateScopesListOptions{
				Period: args.Period,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(attribute.Int("item_count", len(scopes)))
			return mcpTextResult(span, &scopes)
		}, []string{"read_suites"}
}

func ListSpanAggregateScopesForBuild() (mcp.Tool, mcp.ToolHandlerFor[ListSpanAggregateScopesForBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_span_aggregate_scopes_for_build",
			Description: "List OpenTelemetry instrumentation scopes across a build's visible Test Engine suites, ordered by total span duration descending. Start here for build-wide bottlenecks; drill down with list_span_aggregate_names_for_build. Returns span counts and summed durations in nanoseconds, not wall-clock time. Durations are inclusive; nested spans overlap, so do not add their durations together. Requires OTel trace ingestion to be enabled and access to the build's pipeline. Results are not paginated.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Span Aggregate Scopes for Build",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListSpanAggregateScopesForBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListSpanAggregateScopesForBuild")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("build_uuid", args.BuildUUID),
			)

			deps := DepsFromContext(ctx)
			scopes, _, err := deps.SpanAggregatesClient.ListScopesByBuild(ctx, args.OrgSlug, args.BuildUUID)
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(attribute.Int("item_count", len(scopes)))
			return mcpTextResult(span, &scopes)
		}, []string{"read_suites"}
}

func ListSpanAggregateNames() (mcp.Tool, mcp.ToolHandlerFor[ListSpanAggregateNamesArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_span_aggregate_names",
			Description: "List top span-name statistics for an OpenTelemetry instrumentation scope in a Test Engine suite. Returns counts, summed durations, averages, approximate p50/p95 percentiles, maximum durations, and a truncation indicator. All durations are in nanoseconds. Durations are inclusive; nested spans overlap, so do not add their durations together. Aggregates have hourly precision and one-month retention. Requires OTel trace ingestion to be enabled. An unknown scope returns an empty list; results are not paginated.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Span Aggregate Names",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListSpanAggregateNamesArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListSpanAggregateNames")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("scope_name", args.ScopeName),
				attribute.String("period", args.Period),
				attribute.String("sort", args.Sort),
				attribute.Int("limit", args.Limit),
			)

			deps := DepsFromContext(ctx)
			names, _, err := deps.SpanAggregatesClient.ListNamesBySuite(ctx, args.OrgSlug, args.TestSuiteSlug, args.ScopeName, &buildkite.SpanAggregateSuiteNamesListOptions{
				SpanAggregateNamesListOptions: buildkite.SpanAggregateNamesListOptions{
					Sort:  args.Sort,
					Limit: args.Limit,
				},
				Period: args.Period,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(attribute.Int("item_count", len(names.SpanNames)))
			return mcpTextResult(span, &names)
		}, []string{"read_suites"}
}

func ListSpanAggregateNamesForBuild() (mcp.Tool, mcp.ToolHandlerFor[ListSpanAggregateNamesForBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_span_aggregate_names_for_build",
			Description: "List top span-name statistics for an OpenTelemetry instrumentation scope across a build's visible Test Engine suites. Returns counts, summed durations, averages, approximate p50/p95 percentiles, maximum durations, and a truncation indicator. All durations are in nanoseconds. Durations are inclusive; nested spans overlap, so do not add their durations together. Requires access to the build's pipeline. An unknown scope returns an empty list; results are not paginated.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Span Aggregate Names for Build",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListSpanAggregateNamesForBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListSpanAggregateNamesForBuild")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("build_uuid", args.BuildUUID),
				attribute.String("scope_name", args.ScopeName),
				attribute.String("sort", args.Sort),
				attribute.Int("limit", args.Limit),
			)

			deps := DepsFromContext(ctx)
			names, _, err := deps.SpanAggregatesClient.ListNamesByBuild(ctx, args.OrgSlug, args.BuildUUID, args.ScopeName, &buildkite.SpanAggregateNamesListOptions{
				Sort:  args.Sort,
				Limit: args.Limit,
			})
			if err != nil {
				return handleBuildkiteError(err)
			}

			span.SetAttributes(attribute.Int("item_count", len(names.SpanNames)))
			return mcpTextResult(span, &names)
		}, []string{"read_suites"}
}
