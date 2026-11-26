package buildkite

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

func errorTailContext(t *testing.T, handler http.HandlerFunc) context.Context {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	client, err := buildkite.NewOpts(buildkite.WithTokenAuth("fake-token"), buildkite.WithBaseURL(srv.URL+"/"))
	require.NoError(t, err)
	return ContextWithDeps(context.Background(), ToolDependencies{ProgramStatusClient: &BuildkiteClientAdapter{Client: client}})
}

func TestGetJobErrorTail(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	ctx := errorTailContext(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"record":{"id":"","state":"error","app":"lint"},` +
			`"records":[{"id":"tests/unit","state":"error","app":"gotest"},{"id":"","state":"error","app":"lint"}],` +
			`"log":{"start_byte":2082,"end_byte":3181,"start":"step","content":"--- FAIL: TestParse\nlint: 2 problems"}}`))
	})

	tool, handler, scopes := GetJobErrorTail()
	require.Equal(t, "get_job_error_tail", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_build_logs"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobErrorTailArgs{
		OrgSlug: "acme", PipelineSlug: "web", BuildNumber: "19", JobID: "0190f2a1-0000-4000-8000-000000000001", Bytes: 2048,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Equal(t, "/v2/organizations/acme/pipelines/web/builds/19/jobs/0190f2a1-0000-4000-8000-000000000001/program_statuses/error_tail", gotPath)
	require.Equal(t, url.Values{"bytes": {"2048"}}, gotQuery)

	text := getTextResult(t, result).Text
	requireJSONPathEqual(t, text, "tests/unit", "records", 0, "id")
	requireJSONPathEqual(t, text, "lint", "record", "app")
	requireJSONPathEqual(t, text, "step", "log", "start")
}

func TestGetJobErrorTailUsesServerDefaultBytesAndPassesNoFailureThrough(t *testing.T) {
	var gotQuery url.Values
	ctx := errorTailContext(t, func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"record":null,"records":null,"log":null}`))
	})

	_, handler, _ := GetJobErrorTail()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobErrorTailArgs{OrgSlug: "acme", PipelineSlug: "web", BuildNumber: "18", JobID: "job"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Empty(t, gotQuery)
	requireJSONPathEqual(t, getTextResult(t, result).Text, nil, "record")
}

func TestGetJobErrorTailWithoutClientReturnsToolError(t *testing.T) {
	_, handler, _ := GetJobErrorTail()
	result, _, err := handler(ContextWithDeps(context.Background(), ToolDependencies{}), createMCPRequest(t, map[string]any{}), GetJobErrorTailArgs{OrgSlug: "acme", PipelineSlug: "web", BuildNumber: "18", JobID: "job"})
	require.NoError(t, err)
	require.True(t, result.IsError)
}

func TestGetJobErrorTailReturnsAPIErrorsAsToolErrors(t *testing.T) {
	ctx := errorTailContext(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"message":"The job log is being scanned; retry shortly"}`))
	})

	_, handler, _ := GetJobErrorTail()
	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), GetJobErrorTailArgs{OrgSlug: "acme", PipelineSlug: "web", BuildNumber: "18", JobID: "job"})
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, getTextResult(t, result).Text, "retry shortly")
}
