package buildkite

import (
	"context"
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
	calls := 0
	payload := `{"schema_version":1,"build":{"number":42,"state":"failed"},"jobs":[{"id":"job-id","exit_status":2,"omissions":{"logs":"not_collected"},"annotations":[{"body":"Expected <value>, got 7","style":"error","scope":"job"}]}],"jobs_truncated":true,"job_limit":10,"annotations":[]}`
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		assert.Equal(t, "Bearer test-token", r.Header.Get("Authorization"))
		assert.Equal(t, "/v2/organizations/org/pipelines/pipeline/builds/42/failure-evidence", r.URL.Path)
		assert.Empty(t, r.URL.RawQuery)
		_, _ = fmt.Fprint(w, payload)
	}))
	defer api.Close()
	client, err := buildkite.NewClient(buildkite.WithTokenAuth("test-token"))
	require.NoError(t, err)
	client.BaseURL, err = url.Parse(api.URL + "/")
	require.NoError(t, err)
	ctx := ContextWithDeps(context.Background(), ToolDependencies{APIClient: client})
	tool, handler, scopes := GetBuildFailureEvidence()
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_builds"}, scopes)
	result, _, err := handler(ctx, createMCPRequest(t, nil), GetBuildFailureEvidenceArgs{
		OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42",
	})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, 1, calls)
	require.JSONEq(t, payload, result.Content[0].(*mcp.TextContent).Text)
}

func TestGetBuildFailureEvidenceDoesNotFallback(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusNotFound} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Empty(t, r.URL.RawQuery)
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, `{"message":"Failure evidence is unavailable"}`)
			}))
			defer api.Close()
			client, err := buildkite.NewClient()
			require.NoError(t, err)
			client.BaseURL, err = url.Parse(api.URL + "/")
			require.NoError(t, err)
			ctx := ContextWithDeps(context.Background(), ToolDependencies{APIClient: client})
			_, handler, _ := GetBuildFailureEvidence()
			result, _, err := handler(ctx, createMCPRequest(t, nil), GetBuildFailureEvidenceArgs{OrgSlug: "org", PipelineSlug: "pipeline", BuildNumber: "42"})
			require.NoError(t, err)
			require.True(t, result.IsError)
		})
	}
}
