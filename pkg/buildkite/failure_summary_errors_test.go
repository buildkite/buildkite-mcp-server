package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func TestGetBuildFailureSummaryJobErrors(t *testing.T) {
	const firstURL = "https://api.buildkite.com/v2/organizations/org/pipelines/pipeline/builds/42/jobs/lint/errors?per_page=5"
	const nextURL = firstURL + "&after=cursor"
	const capture = `{"uuid":"error-id","code":"lint","message":"2 findings","context":{"total":2,"findings":[{"file":"src/one.ts","line":7,"rule":"no-unused-vars","message":"unused value"},{"file":"src/two.ts","line":31,"rule":"no-undef","message":"unknown name"}]}}`

	for _, tc := range []struct {
		name       string
		status     int
		body       string
		limit      int
		command    string
		disabled   bool
		wantStatus string
		wantItems  int
		truncated  bool
	}{
		{name: "grouped findings", body: `{"items":[` + capture + `]}`, wantStatus: "found", wantItems: 1},
		{name: "next page", body: `{"items":[` + capture + `],"links":{"self":"` + firstURL + `","next":"` + nextURL + `"}}`, wantStatus: "found", wantItems: 1, truncated: true},
		{name: "no capture", body: `{"items":[]}`, wantStatus: "none_recorded"},
		{name: "forbidden", status: 403, body: `{"message":"Forbidden"}`, wantStatus: "unavailable"},
		{name: "not enabled", status: 404, body: `{"message":"Not Found"}`, wantStatus: "unavailable"},
		{name: "API failure", status: 500, body: `{"message":"Internal error"}`, wantStatus: "unavailable"},
		{name: "invalid response", body: `{}`, wantStatus: "unavailable"},
		{name: "reauthenticate", status: 401, body: `{"message":"Unauthorized"}`},
		{name: "disabled", disabled: true},
		{name: "oversized capture", body: `{"items":[{"context":{"findings":[{"message":"` + strings.Repeat("x", 70*1024) + `"}]}}],"links":{"self":"` + firstURL + `"}}`, wantStatus: "found", truncated: true},
		{name: "response budget", limit: 2500, body: `{"items":[` + capture + `,{"message":"` + strings.Repeat("x", 8000) + `"}],"links":{"self":"` + firstURL + `","next":"` + nextURL + `"}}`, wantStatus: "found", wantItems: 1, truncated: true},
		{name: "string budget preserves pagination and status", limit: 2500, command: strings.Repeat("long command ", 1000), body: `{"items":[` + capture + `],"links":{"self":"` + firstURL + `","next":"` + nextURL + `"}}`, wantStatus: "found", truncated: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != http.MethodGet || r.URL.EscapedPath() != "/proxy/v2/organizations/org%3F/pipelines/pipeline%23/builds/42/jobs/lint/errors" || r.URL.RawQuery != "per_page=5" || r.Header.Get("Authorization") != "Bearer fake-token" {
					t.Errorf("unexpected request: %s %s", r.Method, r.URL)
				}
				w.Header().Set("Content-Type", "application/json")
				if tc.status != 0 {
					w.WriteHeader(tc.status)
				}
				fmt.Fprint(w, tc.body)
			}))
			defer srv.Close()
			client, err := buildkite.NewOpts(buildkite.WithBaseURL(srv.URL+"/proxy/"), buildkite.WithTokenAuth("fake-token"))
			require.NoError(t, err)
			ctx := ContextWithDeps(context.Background(), ToolDependencies{
				BuildsClient: &MockBuildsClient{GetFunc: func(_ context.Context, _, _, _ string, options *buildkite.BuildGetOptions) (buildkite.Build, *buildkite.Response, error) {
					require.True(t, options.ExcludeJobs)
					return buildkite.Build{Number: 42, State: "failed", JobStateCounts: &buildkite.JobStateCounts{Total: 507, States: map[string]int{"failed": 1, "passed": 282, "broken": 224}}}, nil, nil
				}},
				JobsClient: &MockJobsClient{ListByBuildFunc: func(_ context.Context, _, _, _ string, options *buildkite.JobsListOptions) (buildkite.JobsList, *buildkite.Response, error) {
					require.False(t, *options.IncludeRetriedJobs)
					require.LessOrEqual(t, options.PerPage, 11)
					switch options.State[0] {
					case "failed":
						return buildkite.JobsList{Items: []buildkite.Job{{ID: "lint", State: "failed", SoftFailed: true, Command: tc.command}}}, nil, nil
					case "canceled", "waiting_failed":
						return buildkite.JobsList{}, nil, nil
					case "broken":
						return buildkite.JobsList{Items: []buildkite.Job{{ID: "not-run", State: "broken"}}}, nil, nil
					default:
						t.Fatalf("unexpected job inventory: %v", options.State)
						return buildkite.JobsList{}, nil, nil
					}
				}},
				JobErrorsClient:     &BuildkiteClientAdapter{Client: client},
				BuildkiteLogsClient: &MockBuildkiteLogsClient{}, // Any log read panics: logs were explicitly disabled.
			})
			_, handler, _ := GetBuildFailureSummary()
			var includeJobErrors *bool
			if tc.disabled {
				includeJobErrors = boolPtr(false)
			}
			result, _, err := handler(ctx, createMCPRequest(t, nil), GetBuildFailureSummaryArgs{
				OrgSlug: "org?", PipelineSlug: "pipeline#", BuildNumber: "42",
				IncludeLogs: boolPtr(false), IncludeJobErrors: includeJobErrors, ContentLimitBytes: tc.limit,
			})
			if tc.status == 401 {
				require.ErrorIs(t, err, ErrUnauthorized)
				require.Nil(t, result)
				return
			}
			require.NoError(t, err)
			require.False(t, result.IsError, "%s", getTextResult(t, result).Text)
			text := getTextResult(t, result).Text
			var summary BuildFailureSummary
			require.NoError(t, json.Unmarshal([]byte(text), &summary))
			require.Equal(t, "failed", summary.Build.State)
			require.Equal(t, 507, summary.Build.JobStateCounts.Total)
			require.Equal(t, map[string]int{"failed": 1, "passed": 282, "broken": 224}, summary.Build.JobStateCounts.States)
			require.Len(t, summary.Jobs, 2)
			require.True(t, summary.Jobs[0].SoftFailed)
			require.Equal(t, "failed", summary.Jobs[0].State)
			require.Nil(t, summary.Jobs[1].JobErrors)
			if tc.disabled {
				require.Nil(t, summary.Jobs[0].JobErrors)
				require.Zero(t, requests.Load())
				return
			}
			require.EqualValues(t, 1, requests.Load())
			page := summary.Jobs[0].JobErrors
			require.Equal(t, tc.wantStatus, page.Status)
			require.Len(t, page.Items, tc.wantItems)
			require.Equal(t, tc.truncated, page.Truncated)
			if tc.wantItems > 0 {
				require.JSONEq(t, `{"total":2,"findings":[{"file":"src/one.ts","line":7,"rule":"no-unused-vars","message":"unused value"},{"file":"src/two.ts","line":31,"rule":"no-undef","message":"unknown name"}]}`, mustMarshalJSON(t, page.Items[0].Context))
			}
			if tc.wantStatus == "none_recorded" {
				require.Contains(t, page.Hint, "does not mean the job succeeded")
			}
			if tc.wantStatus == "unavailable" {
				require.NotEmpty(t, page.Error)
				require.Contains(t, page.Hint, "use logs")
			}
			if tc.truncated {
				require.Equal(t, firstURL, page.URL)
				if tc.name != "oversized capture" {
					require.Equal(t, nextURL, page.NextURL)
				}
			}
			if tc.limit > 0 || tc.name == "oversized capture" {
				require.True(t, page.ContentTruncated)
				require.True(t, summary.ContentTruncated)
			}
			if tc.limit > 0 {
				require.LessOrEqual(t, len(text), tc.limit)
			}
			require.Equal(t, len(text), summary.ContentBytes)
		})
	}
}

func mustMarshalJSON(t *testing.T, value any) string {
	t.Helper()
	data, err := json.Marshal(value)
	require.NoError(t, err)
	return string(data)
}
