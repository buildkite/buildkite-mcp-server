package buildkite

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/buildkite/go-buildkite/v5"
	"github.com/stretchr/testify/require"
)

type mockUsagesClient struct {
	listUsage func(context.Context, string, string, *UsageListOptions) ([]json.RawMessage, *buildkite.Response, error)
}

func (m *mockUsagesClient) ListUsage(ctx context.Context, org, resource string, opts *UsageListOptions) ([]json.RawMessage, *buildkite.Response, error) {
	return m.listUsage(ctx, org, resource, opts)
}

var _ UsagesClient = (*mockUsagesClient)(nil)

func TestListOrganizationUsage(t *testing.T) {
	var gotPath string
	var gotQuery url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotQuery = r.URL.Query()
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Link", `<https://api.buildkite.com/v2/organizations/acme/usages/artifact_storage?from=2026-11-01&page=3&per_page=2&to=2026-11-03>; rel="next"`)
		_, _ = w.Write([]byte(`[{"date":"2026-11-01","pipeline_uuid":"p1","bytes":9007199254740993},{"date":"2026-11-01","pipeline_uuid":"p2","bytes":1}]`))
	}))
	defer srv.Close()

	client, err := buildkite.NewOpts(buildkite.WithTokenAuth("fake-token"), buildkite.WithBaseURL(srv.URL+"/"))
	require.NoError(t, err)
	ctx := ContextWithDeps(context.Background(), ToolDependencies{UsagesClient: &BuildkiteClientAdapter{Client: client}})

	tool, handler, scopes := ListOrganizationUsage()
	require.Equal(t, "list_organization_usage", tool.Name)
	require.True(t, tool.Annotations.ReadOnlyHint)
	require.Equal(t, []string{"read_usages"}, scopes)

	result, _, err := handler(ctx, createMCPRequest(t, map[string]any{}), ListOrganizationUsageArgs{
		OrgSlug: "acme", Resource: "artifact_storage", From: "2026-11-01", To: "2026-11-03", Page: 2, PerPage: 2,
	})
	require.NoError(t, err)
	require.False(t, result.IsError)

	require.Equal(t, "/v2/organizations/acme/usages/artifact_storage", gotPath)
	require.Equal(t, url.Values{"from": {"2026-11-01"}, "to": {"2026-11-03"}, "page": {"2"}, "per_page": {"2"}}, gotQuery)

	text := getTextResult(t, result).Text
	require.Contains(t, text, "9007199254740993", "large counters keep their exact values")
	requireJSONPathEqual(t, text, "p2", "items", 1, "pipeline_uuid")
	require.Contains(t, text, `page=3`)
}

func TestListOrganizationUsageDefaultsToCurrentPeriodAndFirstPage(t *testing.T) {
	var gotOpts *UsageListOptions
	client := &mockUsagesClient{
		listUsage: func(_ context.Context, org, resource string, opts *UsageListOptions) ([]json.RawMessage, *buildkite.Response, error) {
			gotOpts = opts
			return nil, &buildkite.Response{Response: &http.Response{Header: http.Header{}}}, nil
		},
	}

	_, handler, _ := ListOrganizationUsage()
	result, _, err := handler(usagesContext(client), createMCPRequest(t, map[string]any{}), ListOrganizationUsageArgs{OrgSlug: "acme", Resource: "active_users"})
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Equal(t, &UsageListOptions{ListOptions: buildkite.ListOptions{Page: 1, PerPage: 100}}, gotOpts)
	requireJSONPathEqual(t, getTextResult(t, result).Text, []any{}, "items")
}

func usagesContext(client UsagesClient) context.Context {
	return ContextWithDeps(context.Background(), ToolDependencies{UsagesClient: client})
}
