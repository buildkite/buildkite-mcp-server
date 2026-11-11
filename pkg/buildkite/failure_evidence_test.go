package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetBuildFailureEvidence(t *testing.T) {
	for _, includeTests := range []bool{false, true} {
		t.Run(fmt.Sprint(includeTests), func(t *testing.T) {
			calls := 0
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				assert.Equal(t, "/v2/organizations/org/pipelines/pipeline/builds/42/failure-evidence", r.URL.Path)
				assert.Equal(t, "3", r.URL.Query().Get("job_limit"))
				if includeTests {
					assert.Equal(t, "test_results", r.URL.Query().Get("include"))
				} else {
					assert.False(t, r.URL.Query().Has("include"))
				}
				_, _ = fmt.Fprint(w, `{"schema_version":1,"build":{"number":42,"state":"failed"},"jobs":[{"id":"job-id","exit_status":2,"omissions":{"logs":"not_collected"},"annotations":[{"body":"Expected <value>, got 7","style":"error","scope":"job"}],"test_results":{"status":"not_requested"}}],"jobs_truncated":true,"job_limit":3,"annotations":[]}`)
			}))
			defer api.Close()
			client, err := buildkite.NewClient()
			require.NoError(t, err)
			client.BaseURL, err = url.Parse(api.URL + "/")
			require.NoError(t, err)
			ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client.Builds})
			tool, handler, scopes := GetBuildFailureEvidence()
			require.True(t, tool.Annotations.ReadOnlyHint)
			require.Equal(t, []string{"read_builds"}, scopes)
			limit := 3
			result, _, err := handler(ctx, createMCPRequest(t, nil), GetBuildFailureEvidenceArgs{
				OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42", JobLimit: &limit, IncludeTestResults: includeTests,
			})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Equal(t, 1, calls)
			var evidence buildkite.BuildFailureEvidence
			require.NoError(t, json.Unmarshal([]byte(result.Content[0].(*mcp.TextContent).Text), &evidence))
			require.Equal(t, 42, evidence.Build.Number)
			require.True(t, evidence.JobsTruncated)
			require.Equal(t, "Expected <value>, got 7", evidence.Jobs[0].Annotations[0].Body)
			require.Equal(t, "not_collected", evidence.Jobs[0].Omissions["logs"])
		})
	}
}

func TestGetBuildFailureEvidenceRejectsInvalidLimits(t *testing.T) {
	_, handler, _ := GetBuildFailureEvidence()
	for _, limit := range []int{-1, 0, 51} {
		result, _, err := handler(context.Background(), createMCPRequest(t, nil), GetBuildFailureEvidenceArgs{JobLimit: &limit})
		require.NoError(t, err)
		require.True(t, result.IsError)
	}
}

func TestGetBuildFailureEvidenceDoesNotFallback(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, "10", r.URL.Query().Get("job_limit"))
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"message":"Failure evidence is unavailable"}`)
			}))
			defer api.Close()
			client, err := buildkite.NewClient()
			require.NoError(t, err)
			client.BaseURL, err = url.Parse(api.URL + "/")
			require.NoError(t, err)
			ctx := ContextWithDeps(context.Background(), ToolDependencies{BuildsClient: client.Builds})
			_, handler, _ := GetBuildFailureEvidence()
			result, _, err := handler(ctx, createMCPRequest(t, nil), GetBuildFailureEvidenceArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42"})
			require.NoError(t, err)
			require.True(t, result.IsError)
		})
	}
}
