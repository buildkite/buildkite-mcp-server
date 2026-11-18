package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/alecthomas/kong"
	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-mcp-server/internal/headerpassthrough"
	buildkitetools "github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/recording"
	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	gobuildkite "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMinimumAccessCLI(t *testing.T) {
	for _, command := range []string{"stdio", "http"} {
		for _, tt := range []struct {
			name     string
			env      string
			flags    []string
			minimum  bool
			readOnly bool
		}{
			{name: "default unchanged"},
			{name: "flag", flags: []string{"--minimum-access"}, minimum: true},
			{name: "environment", env: "true", minimum: true},
			{name: "explicit false overrides environment", env: "true", flags: []string{"--minimum-access=false"}},
			{name: "both restrictions", flags: []string{"--minimum-access", "--read-only"}, minimum: true, readOnly: true},
		} {
			t.Run(command+"/"+tt.name, func(t *testing.T) {
				t.Setenv("BUILDKITE_MINIMUM_ACCESS", tt.env)
				if tt.env == "" {
					require.NoError(t, os.Unsetenv("BUILDKITE_MINIMUM_ACCESS"))
				}
				t.Setenv("BUILDKITE_READ_ONLY", "false")
				model := cli
				parser, err := kong.New(&model)
				require.NoError(t, err)
				_, err = parser.Parse(append([]string{command}, tt.flags...))
				require.NoError(t, err)
				if command == "stdio" {
					require.Equal(t, tt.minimum, model.Stdio.MinimumAccess)
					require.Equal(t, tt.readOnly, model.Stdio.ReadOnly)
				} else {
					require.Equal(t, tt.minimum, model.HTTP.MinimumAccess)
					require.Equal(t, tt.readOnly, model.HTTP.ReadOnly)
				}
			})
		}
	}
}

func TestResolveAPITokenForModePreservesExistingAuthentication(t *testing.T) {
	t.Run("static token", func(t *testing.T) {
		token, err := resolveAPITokenForMode(nil, "", "shared-token", "")
		require.NoError(t, err)
		require.Equal(t, "shared-token", token)
	})

	t.Run("missing static token", func(t *testing.T) {
		_, err := resolveAPITokenForMode(nil, "", "", "")
		require.ErrorContains(t, err, "must specify either --api-token or --api-token-from-1password")
	})

	t.Run("replay does not require token", func(t *testing.T) {
		token, err := resolveAPITokenForMode(nil, "session.har", "", "")
		require.NoError(t, err)
		require.Empty(t, token)
	})
}

func TestResolveAPITokenForModeUsesPerRequestAuthorization(t *testing.T) {
	config, err := headerpassthrough.New([]string{"Authorization"}, nil, "https://api.buildkite.com/")
	require.NoError(t, err)

	token, err := resolveAPITokenForMode(config, "", "", "")
	require.NoError(t, err)
	require.Empty(t, token)

	_, err = resolveAPITokenForMode(config, "", "shared-token", "")
	require.ErrorContains(t, err, "cannot configure a fixed Buildkite API token")

	_, err = resolveAPITokenForMode(config, "", "", "op://vault/item/token")
	require.ErrorContains(t, err, "cannot configure a fixed Buildkite API token")
}

func TestRecordingDoesNotCapturePassthroughHeaders(t *testing.T) {
	receivedHeaders := make(chan http.Header, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders <- r.Header.Clone()
		w.WriteHeader(http.StatusNoContent)
	}))
	t.Cleanup(api.Close)

	config, err := headerpassthrough.New([]string{"X-Identity", "Cookie"}, nil, api.URL)
	require.NoError(t, err)
	harPath := filepath.Join(t.TempDir(), "session.har")
	transport, err := newAPITransport(config, harPath, "", "test")
	require.NoError(t, err)
	client := &http.Client{Transport: transport}

	handler := config.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		req, requestErr := http.NewRequestWithContext(r.Context(), http.MethodGet, api.URL+"/v2/user", nil)
		if !assert.NoError(t, requestErr) {
			http.Error(w, requestErr.Error(), http.StatusInternalServerError)
			return
		}
		resp, requestErr := client.Do(req)
		if !assert.NoError(t, requestErr) {
			http.Error(w, requestErr.Error(), http.StatusBadGateway)
			return
		}
		resp.Body.Close()
		w.WriteHeader(http.StatusNoContent)
	}))
	inbound := httptest.NewRequest(http.MethodPost, "/mcp", nil)
	inbound.Header.Set("X-Identity", "user-123")
	inbound.Header.Set("Cookie", "session-secret")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, inbound)
	require.Equal(t, http.StatusNoContent, recorder.Code)
	received := <-receivedHeaders
	require.Equal(t, "user-123", received.Get("X-Identity"))
	require.Equal(t, "session-secret", received.Get("Cookie"))

	har, err := recording.LoadHAR(harPath)
	require.NoError(t, err)
	require.Len(t, har.Log.Entries, 1)
	for _, header := range har.Log.Entries[0].Request.Headers {
		require.NotEqual(t, "X-Identity", header.Name)
		require.NotEqual(t, "Cookie", header.Name)
	}
}

func TestMCPToolCallPassesRequestHeaderToBuildkiteAPI(t *testing.T) {
	type receivedRequest struct {
		path    string
		headers http.Header
	}
	receivedRequests := make(chan receivedRequest, 1)
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedRequests <- receivedRequest{path: r.URL.Path, headers: r.Header.Clone()}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"user-id","name":"Test User"}`))
	}))
	t.Cleanup(api.Close)

	config, err := headerpassthrough.New([]string{"X-Identity"}, nil, api.URL)
	require.NoError(t, err)
	apiTransport, err := newAPITransport(config, "", "", "test")
	require.NoError(t, err)
	apiClient := &http.Client{Transport: apiTransport}
	client, err := gobuildkite.NewOpts(
		gobuildkite.WithHTTPClient(apiClient),
		gobuildkite.WithBaseURL(api.URL+"/"),
	)
	require.NoError(t, err)

	deps := buildkitetools.ToolDependencies{UserClient: client.User}
	factory := server.NewPerRequestServerFactory("test", deps, []string{toolsets.ToolsetUser}, true)
	handler := config.WrapHandler(server.NewHTTPUnauthorizedHandler(
		mcp.NewStreamableHTTPHandler(factory, &mcp.StreamableHTTPOptions{Stateless: true}),
		`Bearer realm="buildkite"`,
	))
	mcpServer := httptest.NewServer(handler)
	t.Cleanup(mcpServer.Close)

	mcpHTTPClient := mcpServer.Client()
	mcpHTTPClient.Transport = requestHeaderTransport{
		next:  mcpHTTPClient.Transport,
		name:  "X-Identity",
		value: "user-123",
	}
	transport := &mcp.StreamableClientTransport{
		Endpoint:             mcpServer.URL,
		HTTPClient:           mcpHTTPClient,
		DisableStandaloneSSE: true,
	}
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(context.Background(), transport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "current_user",
		Arguments: map[string]any{
			"telemetry": map[string]any{
				"context": "Fetching the current Buildkite user to verify request header passthrough behavior during the authenticated integration test flow.",
			},
		},
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.NotEmpty(t, result.Content)
	received := <-receivedRequests
	require.Equal(t, "/v2/user", received.path)
	require.Equal(t, "user-123", received.headers.Get("X-Identity"))
}

// These are the eval inputs, not copies of the SDK's HTTP-fixture tests.
// Catch fixture/query drift before spending model calls on an invalid scenario.
func TestSchedulerEvalReplayFixtures(t *testing.T) {
	for _, tc := range []struct {
		fixture                    string
		pools                      int
		waiting, leased, completed int64
		failed, errored            int64
		errorMessage               string
		callError                  string
	}{
		{fixture: "completed", pools: 3, completed: 41},
		{fixture: "planning-failure", pools: 2, completed: 11},
		{fixture: "backlog", pools: 1, waiting: 11, leased: 8, completed: 23},
		{fixture: "completed-failures", pools: 1, completed: 19, failed: 2, errored: 1},
		{fixture: "idle-waiting", pools: 1, waiting: 7},
		{fixture: "errored-drained", pools: 1, waiting: 7},
		{fixture: "mixed-pages", pools: 31, waiting: 7, completed: 330},
		{fixture: "unauthorized", callError: "buildkite: unauthorized"},
		{fixture: "forbidden", errorMessage: "read_test_pool"},
		{fixture: "unavailable", errorMessage: "No test pool found"},
		{fixture: "empty"},
		{fixture: "ed656de2-completed-pool.sanitised"},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			harPath := filepath.Join("..", "..", "evals", "fixtures", "test-scheduler", tc.fixture+".har")
			transport, err := newAPITransport(nil, "", harPath, "test")
			require.NoError(t, err)
			client, err := gobuildkite.NewOpts(gobuildkite.WithHTTPClient(&http.Client{Transport: transport}))
			require.NoError(t, err)
			logClient, err := buildkitelogs.NewClient(context.Background(), client, "file://"+t.TempDir())
			require.NoError(t, err)
			t.Cleanup(func() { _ = logClient.Close() })
			srv := server.NewMCPServer("test", buildkitetools.ToolDependencies{
				BuildsClient: client.Builds, PipelinesClient: client.Pipelines,
				AnnotationsClient: client.Annotations, TestPoolsClient: client.TestPools,
				OrganizationsClient: client.Organizations, JobsClient: client.Jobs,
				BuildTestsClient: client.BuildTests, AccessTokensClient: client.AccessTokens,
				BuildkiteLogsClient: logClient,
			}, server.WithToolsets("builds", "pipelines", "tests", "user", "annotations", "investigations", "logs"), server.WithReadOnly(true))
			ctx := context.Background()
			st, ct := mcp.NewInMemoryTransports()
			ss, err := srv.Connect(ctx, st, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = ss.Close() })
			session, err := mcp.NewClient(&mcp.Implementation{Name: "fixture-check", Version: "test"}, nil).Connect(ctx, ct, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close() })
			call := func(name string, args map[string]any, out any) *mcp.CallToolResult {
				t.Helper()
				result, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
				require.NoError(t, callErr)
				if !result.IsError {
					require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), out))
				}
				return result
			}
			if tc.fixture == "ed656de2-completed-pool.sanitised" {
				// This recording has only supplied-ID detail and metrics reads;
				// do not manufacture organization, build or list responses.
				input := map[string]any{
					"org_slug":   "example-org",
					"build_uuid": "00000000-0000-4000-8000-000000000003",
					"pool_uuid":  "00000000-0000-4000-8000-000000000004",
				}
				for _, observedAt := range []string{
					"2026-11-18T20:25:11.046Z",
					"2026-11-18T20:25:21.954Z",
					"2026-11-18T20:25:23.866Z",
				} {
					var detail gobuildkite.TestPool
					require.False(t, call("get_test_pool", input, &detail).IsError)
					require.Equal(t, input["pool_uuid"], detail.ID)
					require.Equal(t, input["build_uuid"], detail.BuildID)
					require.Equal(t, "00000000-0000-4000-8000-000000000001", detail.OrganizationID)
					require.Equal(t, "00000000-0000-4000-8000-000000000002", detail.PipelineID)
					require.Equal(t, "00000000-0000-4000-8000-000000000005", detail.SuiteID)
					require.Equal(t, "consumed", detail.State)
					require.Nil(t, detail.Error)
					require.Len(t, detail.MutedTests, 120)
					require.NotNil(t, detail.AttemptPolicy.Single)
					require.Equal(t, 2, detail.AttemptPolicy.Single.MaxAttempts)
					require.Nil(t, detail.AttemptPolicy.Single.MinFailed)
					require.Nil(t, detail.AttemptPolicy.Single.InitialAttempts)
					require.Equal(t, 300, detail.Lease.MaxAttempts)
					require.InDelta(t, 100000, detail.Lease.Costs["duration_p50_ms"], 0)
					require.NotNil(t, detail.Parallelism)
					require.Equal(t, 61, *detail.Parallelism)
					var metrics gobuildkite.TestPoolMetrics
					require.False(t, call("get_test_pool_metrics", input, &metrics).IsError)
					require.Equal(t, detail.ID, metrics.Pool.ID)
					require.Equal(t, detail.State, metrics.Pool.State)
					require.True(t, metrics.Pool.Drained)
					require.NotNil(t, metrics.ObservedAt)
					require.Equal(t, observedAt, metrics.ObservedAt.Format(time.RFC3339Nano))
					require.NotNil(t, metrics.Pool.ExpiresAt)
					require.Equal(t, "2026-11-19T01:05:27.372Z", metrics.Pool.ExpiresAt.Format(time.RFC3339Nano))
					require.Equal(t, detail.ExpiresAt, metrics.Pool.ExpiresAt)
					require.Equal(t, int64(145), metrics.Entries.Total)
					require.Equal(t, int64(145), metrics.Attempts.Total)
					require.Equal(t, int64(145), metrics.Attempts.States["completed"].Count)
					for _, state := range []string{"waiting", "leased", "canceled"} {
						require.Zero(t, metrics.Attempts.States[state].Count)
					}
					require.Equal(t, map[string]int64{"passed": 145, "failed": 0, "errored": 0}, metrics.Attempts.Results)
					require.InDelta(t, 6458672, metrics.Attempts.States["completed"].Costs["duration_p50_ms"], 0)
					require.Equal(t, gobuildkite.TestPoolMetricsLeases{}, metrics.Leases)
					require.Equal(t, gobuildkite.TestPoolMetricsTiming{}, metrics.Timing)
				}
				return
			}
			var organization gobuildkite.Organization
			require.False(t, call("user_token_organization", map[string]any{}, &organization).IsError)
			require.Equal(t, "scheduler-evals", organization.Slug)
			var pipeline gobuildkite.Pipeline
			require.False(t, call("get_pipeline", map[string]any{"org_slug": "scheduler-evals", "pipeline_slug": "sample"}, &pipeline).IsError)
			var build buildkitetools.BuildDetail
			require.False(t, call("get_build", map[string]any{"org_slug": "scheduler-evals", "pipeline_slug": "sample", "build_number": "901"}, &build).IsError)
			args := map[string]any{"org_slug": "scheduler-evals", "pipeline_uuid": pipeline.ID, "build_uuid": build.ID}
			// Exercise exploration paths that missed replay data in #450 and #451.
			if tc.fixture == "backlog" {
				var summary buildkitetools.BuildFailureSummary
				require.False(t, call("get_build_failure_summary", map[string]any{"org_slug": "scheduler-evals", "pipeline_slug": "sample", "build_number": "901"}, &summary).IsError)
				require.Equal(t, "running", summary.Build.State)
				require.Empty(t, summary.Jobs)
				require.Empty(t, summary.Warnings)
				var jobID string
				for _, perPage := range []int{0, 100} {
					var jobs buildkitetools.JobListResult[buildkitetools.JobDetail]
					require.False(t, call("list_jobs", map[string]any{"org_slug": "scheduler-evals", "pipeline_slug": "sample", "build_number": "901", "detail_level": "detailed", "per_page": perPage}, &jobs).IsError)
					require.Len(t, jobs.Items, 1)
					require.Equal(t, "running", jobs.Items[0].State)
					jobID = jobs.Items[0].ID
				}
				// An isolated cold cache exercises HEAD, job status, raw log retrieval and parsing.
				var logs struct {
					Entries []buildkitetools.TerseLogEntry `json:"entries"`
				}
				require.False(t, call("read_logs", map[string]any{"org_slug": "scheduler-evals", "pipeline_slug": "sample", "build_number": "901", "job_id": jobID, "limit": 0}, &logs).IsError)
				require.Equal(t, []buildkitetools.TerseLogEntry{
					{C: "Starting test worker", RN: 0},
					{C: "Test environment loaded", RN: 1},
					{C: "Test worker process running", RN: 2},
				}, logs.Entries)
				var annotations buildkitetools.PaginatedResult[gobuildkite.Annotation]
				require.False(t, call("list_annotations", map[string]any{"org_slug": "scheduler-evals", "pipeline_slug": "sample", "build_number": "901"}, &annotations).IsError)
				require.Empty(t, annotations.Items)
				var tests buildkitetools.PaginatedResult[gobuildkite.TestWithMetrics]
				require.False(t, call("list_tests_for_build", map[string]any{"org_slug": "scheduler-evals", "build_uuid": build.ID, "per_page": 100}, &tests).IsError)
				require.Empty(t, tests.Items)
			}
			if tc.fixture == "forbidden" {
				var token gobuildkite.AccessToken
				require.False(t, call("access_token", map[string]any{}, &token).IsError)
				require.Contains(t, token.Scopes, "read_builds")
				require.NotContains(t, token.Scopes, "read_test_pool")
			}
			if tc.callError != "" {
				// Authentication failures propagate as MCP call errors, not tool-result content.
				for name, input := range map[string]map[string]any{"list_test_pools": args, "access_token": {}} {
					_, callErr := session.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: input})
					require.ErrorContains(t, callErr, tc.callError)
					require.ErrorIs(t, callErr, buildkitetools.ErrUnauthorized)
				}
				return
			}
			var pools int
			var waiting, leased, completed, failed, errored int64
			for {
				var page buildkitetools.TestPoolsPage
				result := call("list_test_pools", args, &page)
				if tc.errorMessage != "" {
					require.True(t, result.IsError)
					require.Contains(t, result.Content[0].(*mcp.TextContent).Text, tc.errorMessage)
					return
				}
				require.False(t, result.IsError, "%v", result.Content)
				pools += len(page.Items)
				for _, p := range page.Items {
					input := map[string]any{"org_slug": "scheduler-evals", "build_uuid": build.ID, "pool_uuid": p.ID}
					var detail gobuildkite.TestPool
					require.False(t, call("get_test_pool", input, &detail).IsError)
					require.Equal(t, build.ID, detail.BuildID)
					if detail.State == "errored" {
						require.NotNil(t, detail.Error)
						require.Equal(t, "Test pool failed.", detail.Error.Message)
					}
					var metrics gobuildkite.TestPoolMetrics
					require.False(t, call("get_test_pool_metrics", input, &metrics).IsError)
					require.Equal(t, p.ID, metrics.Pool.ID)
					require.NotNil(t, metrics.ObservedAt)
					// Rails/bkgo contract: states partition attempts; results classify
					// completed attempts; both live and expired leases still hold leased work.
					states := metrics.Attempts.States
					require.Equal(t, metrics.Attempts.Total, states["waiting"].Count+states["leased"].Count+states["completed"].Count+states["canceled"].Count)
					require.Equal(t, states["completed"].Count, metrics.Attempts.Results["passed"]+metrics.Attempts.Results["failed"]+metrics.Attempts.Results["errored"])
					require.Equal(t, states["leased"].Count, metrics.Leases.Active.AttemptCount+metrics.Leases.Expired.AttemptCount)
					finalized := metrics.Pool.State == "consumed" || (metrics.Pool.State == "errored" && metrics.Pool.Drained)
					if states["waiting"].Count == 0 || finalized {
						require.Nil(t, metrics.Timing.OldestWaitingAttemptCreatedAt)
					} else {
						require.NotNil(t, metrics.Timing.OldestWaitingAttemptCreatedAt)
						require.False(t, metrics.Timing.OldestWaitingAttemptCreatedAt.After(metrics.ObservedAt.Time))
					}
					if metrics.Leases.Active.Count == 0 {
						require.Zero(t, metrics.Leases.Active.AttemptCount)
						require.Nil(t, metrics.Timing.OldestActiveLeaseStartedAt)
						require.Nil(t, metrics.Timing.NextActiveLeaseExpiresAt)
					} else {
						require.GreaterOrEqual(t, metrics.Leases.Active.AttemptCount, metrics.Leases.Active.Count)
						require.NotNil(t, metrics.Timing.OldestActiveLeaseStartedAt)
						require.NotNil(t, metrics.Timing.NextActiveLeaseExpiresAt)
						require.False(t, metrics.Timing.OldestActiveLeaseStartedAt.After(metrics.ObservedAt.Time))
						require.True(t, metrics.Timing.NextActiveLeaseExpiresAt.After(metrics.ObservedAt.Time))
					}
					if metrics.Leases.Expired.Count == 0 {
						require.Zero(t, metrics.Leases.Expired.AttemptCount)
						require.Nil(t, metrics.Timing.OldestExpiredLeaseStartedAt)
					} else {
						require.GreaterOrEqual(t, metrics.Leases.Expired.AttemptCount, metrics.Leases.Expired.Count)
						require.NotNil(t, metrics.Timing.OldestExpiredLeaseStartedAt)
						require.False(t, metrics.Timing.OldestExpiredLeaseStartedAt.After(metrics.ObservedAt.Time))
					}
					if states["waiting"].Count+states["leased"].Count > 0 && metrics.Pool.State == "consuming" {
						require.False(t, metrics.Pool.Drained)
					}
					require.Equal(t, time.Date(2026, time.November, 17, 3, 0, 0, 0, time.UTC), metrics.ObservedAt.Time)
					waiting += metrics.Attempts.States["waiting"].Count
					leased += metrics.Attempts.States["leased"].Count
					completed += metrics.Attempts.States["completed"].Count
					failed += metrics.Attempts.Results["failed"]
					errored += metrics.Attempts.Results["errored"]
				}
				if page.Links.Next == "" {
					break
				}
				next, parseErr := url.Parse(string(page.Links.Next))
				require.NoError(t, parseErr)
				args["after"] = next.Query().Get("after")
			}
			if tc.fixture == "planning-failure" {
				var other gobuildkite.Build
				require.False(t, call("get_build", map[string]any{"org_slug": "scheduler-evals", "pipeline_slug": "sample", "build_number": "900"}, &other).IsError)
				require.NotEqual(t, build.ID, other.ID)
				var page buildkitetools.TestPoolsPage
				require.False(t, call("list_test_pools", map[string]any{"org_slug": "scheduler-evals", "pipeline_uuid": pipeline.ID, "build_uuid": other.ID, "key": "features"}, &page).IsError)
				require.Len(t, page.Items, 1)
				require.Equal(t, "33333333-3333-4333-8333-000000000099", page.Items[0].ID)
				var detail gobuildkite.TestPool
				require.False(t, call("get_test_pool", map[string]any{"org_slug": "scheduler-evals", "build_uuid": other.ID, "pool_uuid": page.Items[0].ID}, &detail).IsError)
				require.Equal(t, other.ID, detail.BuildID)
				require.NotNil(t, page.Items[0].Error)
				require.Equal(t, "Test pool failed.", page.Items[0].Error.Message)
			}
			require.Equal(t, tc.pools, pools)
			require.Equal(t, tc.waiting, waiting)
			require.Equal(t, tc.leased, leased)
			require.Equal(t, tc.completed, completed)
			require.Equal(t, tc.failed, failed)
			require.Equal(t, tc.errored, errored)
		})
	}
}

type requestHeaderTransport struct {
	next        http.RoundTripper
	name, value string
}

func (t requestHeaderTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.Header = req.Header.Clone()
	cloned.Header.Set(t.name, t.value)
	return t.next.RoundTrip(cloned)
}
