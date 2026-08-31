package buildkite

import (
	"context"
	"time"

	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
)

type TestsClient interface {
	Get(ctx context.Context, org, slug, testID string, opt *buildkite.TestsGetOptions) (buildkite.TestWithMetrics, *buildkite.Response, error)
	List(ctx context.Context, org, slug string, opt *buildkite.TestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error)
}

type BuildTestsClient interface {
	List(ctx context.Context, org, buildUUID string, opt *buildkite.BuildTestsListOptions) ([]buildkite.TestWithMetrics, *buildkite.Response, error)
}

type ListTestsArgs struct {
	ToolInput
	OrgSlug       string    `json:"org_slug"`
	TestSuiteSlug string    `json:"test_suite_slug"`
	Page          int       `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage       int       `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
	Period        string    `json:"period,omitempty" jsonschema:"Relative aggregation window, such as '7days' or '28days'. Whether selected by period or timestamps, the aggregation window cannot exceed the organization's maximum Test Engine window. Cannot be combined with min_timestamp or max_timestamp."`
	MinTimestamp  time.Time `json:"min_timestamp,omitzero" jsonschema:"Start of the aggregation window in RFC3339 format. When omitted, defaults to the organization's default Test Engine period before the current time."`
	MaxTimestamp  time.Time `json:"max_timestamp,omitzero" jsonschema:"End of the aggregation window in RFC3339 format. Defaults to the current time when omitted."`
	Labels        string    `json:"labels,omitempty" jsonschema:"Filter by comma-separated test labels. Prefix a label with '!' to exclude it, for example 'flaky,!slow'."`
	Branch        string    `json:"branch,omitempty" jsonschema:"Filter executions included in the metrics by branch. Prefix with '!' to exclude an exact branch or suffix with '*' to match by prefix, for example '!main' or 'feature*'. Use at most one operator."`
	Owners        string    `json:"owners,omitempty" jsonschema:"Filter by comma-separated test owner slugs. Prefix an owner with '!' to exclude it, for example 'payments,!platform'."`
	State         string    `json:"state,omitempty" jsonschema:"Filter by test state: 'enabled', 'muted', or 'skipped'."`
	Tags          string    `json:"tags,omitempty" jsonschema:"Filter by comma-separated execution tags in key:value form. Values support '!' for exclusion and '*' for prefix matching. Result values additionally support '~' for any matching execution and '^' for every execution matching, for example 'framework:!rspec,scm.branch:feature*,result:^passed'."`
	SortBy        string    `json:"sort_by,omitempty" jsonschema:"Metric used to sort results: 'duration_avg', 'duration_sum', 'duration_min', 'duration_max', or 'reliability'. Defaults to 'duration_avg'. Metrics cover only executions in the selected window."`
	Order         string    `json:"order,omitempty" jsonschema:"Sort direction: 'asc' or 'desc'. Defaults to 'desc'."`
}

type ListTestsForBuildArgs struct {
	ToolInput
	OrgSlug   string `json:"org_slug"`
	BuildUUID string `json:"build_uuid" jsonschema:"Buildkite build UUID. This is the build ID, not the pipeline build number."`
	Page      int    `json:"page,omitempty" jsonschema:"Page number for pagination (min 1)"`
	PerPage   int    `json:"per_page,omitempty" jsonschema:"Results per page for pagination (min 1, max 100)"`
	Labels    string `json:"labels,omitempty" jsonschema:"Filter by comma-separated test labels. Prefix a label with '!' to exclude it, for example 'flaky,!slow'."`
	Branch    string `json:"branch,omitempty" jsonschema:"Filter executions included in the metrics by branch. Prefix with '!' to exclude an exact branch or suffix with '*' for prefix matching, for example '!main' or 'feature*'. Use at most one operator."`
	Owners    string `json:"owners,omitempty" jsonschema:"Filter by comma-separated test owner slugs. Prefix an owner with '!' to exclude it, for example 'payments,!platform'."`
	State     string `json:"state,omitempty" jsonschema:"Filter by test state: 'enabled', 'muted', or 'skipped'."`
	Tags      string `json:"tags,omitempty" jsonschema:"Filter by comma-separated execution tags in key:value form. Values support '!' for exclusion and '*' for prefix matching. Result values additionally support '~' for any matching execution and '^' for every execution matching, for example 'framework:!rspec,scm.branch:feature*,result:^passed'."`
	SortBy    string `json:"sort_by,omitempty" jsonschema:"Metric used to sort results: 'duration_avg', 'duration_sum', 'duration_min', 'duration_max', or 'reliability'. Defaults to 'duration_avg'."`
	Order     string `json:"order,omitempty" jsonschema:"Sort direction: 'asc' or 'desc'. Defaults to 'desc'."`
}

type GetTestArgs struct {
	ToolInput
	OrgSlug       string `json:"org_slug"`
	TestSuiteSlug string `json:"test_suite_slug"`
	TestID        string `json:"test_id"`
}

func ListTests() (mcp.Tool, mcp.ToolHandlerFor[ListTestsArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_tests",
			Description: "List tests in a Buildkite Test Engine suite with execution metrics aggregated over a selected time window. Supports filtering, metric sorting, and pagination.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Tests",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListTestsArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListTests")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)
			options := &buildkite.TestsListOptions{
				ListOptions:  paginationParams,
				Period:       args.Period,
				MinTimestamp: args.MinTimestamp,
				MaxTimestamp: args.MaxTimestamp,
				Labels:       args.Labels,
				Branch:       args.Branch,
				Owners:       args.Owners,
				State:        args.State,
				Tags:         args.Tags,
				SortBy:       args.SortBy,
				Order:        args.Order,
			}

			attributes := []attribute.KeyValue{
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
				attribute.String("period", args.Period),
				attribute.String("labels", args.Labels),
				attribute.String("branch", args.Branch),
				attribute.String("owners", args.Owners),
				attribute.String("state", args.State),
				attribute.String("tags", args.Tags),
				attribute.String("sort_by", args.SortBy),
				attribute.String("order", args.Order),
			}
			if !args.MinTimestamp.IsZero() {
				attributes = append(attributes, attribute.String("min_timestamp", args.MinTimestamp.Format(time.RFC3339)))
			}
			if !args.MaxTimestamp.IsZero() {
				attributes = append(attributes, attribute.String("max_timestamp", args.MaxTimestamp.Format(time.RFC3339)))
			}
			span.SetAttributes(attributes...)

			deps := DepsFromContext(ctx)
			tests, resp, err := deps.TestsClient.List(ctx, args.OrgSlug, args.TestSuiteSlug, options)
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.TestWithMetrics]{
				Items: tests,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(attribute.Int("item_count", len(tests)))

			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}

func ListTestsForBuild() (mcp.Tool, mcp.ToolHandlerFor[ListTestsForBuildArgs, any], []string) {
	return mcp.Tool{
			Name:        "list_tests_for_build",
			Description: "List tests and their execution metrics for a Buildkite build. Requires the build UUID (the build ID, not the pipeline build number) and supports filtering, metric sorting, and pagination.",
			Annotations: &mcp.ToolAnnotations{
				Title:        "List Tests for Build",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args ListTestsForBuildArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.ListTestsForBuild")
			defer span.End()

			paginationParams := paginationFromArgs(args.Page, args.PerPage)
			options := &buildkite.BuildTestsListOptions{
				ListOptions: paginationParams,
				Labels:      args.Labels,
				Branch:      args.Branch,
				Owners:      args.Owners,
				State:       args.State,
				Tags:        args.Tags,
				SortBy:      args.SortBy,
				Order:       args.Order,
			}

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("build_uuid", args.BuildUUID),
				attribute.Int("page", paginationParams.Page),
				attribute.Int("per_page", paginationParams.PerPage),
				attribute.String("labels", args.Labels),
				attribute.String("branch", args.Branch),
				attribute.String("owners", args.Owners),
				attribute.String("state", args.State),
				attribute.String("tags", args.Tags),
				attribute.String("sort_by", args.SortBy),
				attribute.String("order", args.Order),
			)

			deps := DepsFromContext(ctx)
			tests, resp, err := deps.BuildTestsClient.List(ctx, args.OrgSlug, args.BuildUUID, options)
			if err != nil {
				return handleBuildkiteError(err)
			}

			result := PaginatedResult[buildkite.TestWithMetrics]{
				Items: tests,
				Headers: map[string]string{
					"Link": resp.Header.Get("Link"),
				},
			}

			span.SetAttributes(attribute.Int("item_count", len(tests)))

			return mcpTextResult(span, &result)
		}, []string{"read_suites"}
}

func GetTest() (mcp.Tool, mcp.ToolHandlerFor[GetTestArgs, any], []string) {
	return mcp.Tool{
			Name:        "get_test",
			Description: "Get a specific test in Buildkite Test Engine. This provides additional metadata for failed test executions",
			Annotations: &mcp.ToolAnnotations{
				Title:        "Get Test",
				ReadOnlyHint: true,
			},
		},
		func(ctx context.Context, request *mcp.CallToolRequest, args GetTestArgs) (*mcp.CallToolResult, any, error) {
			ctx, span := trace.Start(ctx, "buildkite.GetTest")
			defer span.End()

			span.SetAttributes(
				attribute.String("org_slug", args.OrgSlug),
				attribute.String("test_suite_slug", args.TestSuiteSlug),
				attribute.String("test_id", args.TestID),
			)

			deps := DepsFromContext(ctx)
			test, _, err := deps.TestsClient.Get(ctx, args.OrgSlug, args.TestSuiteSlug, args.TestID, nil)
			if err != nil {
				return handleBuildkiteError(err)
			}

			return mcpTextResult(span, &test)
		}, []string{"read_suites"}
}
