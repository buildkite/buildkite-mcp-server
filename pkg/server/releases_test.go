package server

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	buildkiteapi "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestReleasesReadToolsThroughMCP(t *testing.T) {
	const (
		pipelineID  = "019a1dd3-3870-7e50-8a6d-f16efba96342"
		targetID    = "019a1dd8-a49b-70c5-99b8-b8f9f0ed6824"
		componentID = "019a1ddb-a210-7b03-b4a6-5fcd44828b31"
		artifactID  = "019a1ddc-a210-7b03-b4a6-5fcd44828b31"
		versionID   = "019a1ddd-a210-7b03-b4a6-5fcd44828b31"
		target      = `{"id":"` + targetID + `","name":"prod","labels":{"region":"au"},"url":"https://api.buildkite.com/targets/prod","id_url":"https://api.buildkite.com/targets/by-id/` + targetID + `","created_at":"2026-11-08T00:00:00Z","archived_at":null}`
		component   = `{"id":"` + componentID + `","name":"api","target_id":"` + targetID + `","artifact":null,"url":"https://api.buildkite.com/components/api","id_url":"https://api.buildkite.com/components/by-id/` + componentID + `","created_at":"2026-11-08T00:00:00Z","archived_at":null}`
		status      = `{"target_id":"` + targetID + `","component":"api","component_id":"` + componentID + `","present":false,"active_version":null,"active_digest":null,"last_active_version":"1.2.0","first_seen_at":"2026-11-08T00:00:00Z","last_seen_at":"2026-11-08T01:00:00Z","sequence":4,"catalog_resolution":{"status":"unbound","artifact_version_id":null,"digest_check":"not_checked","reason":"no_artifact_binding"}}`
		unobserved  = `{"target_id":"` + targetID + `","component":"api","component_id":"` + componentID + `","present":null,"active_version":null,"active_digest":null,"last_active_version":null,"first_seen_at":null,"last_seen_at":null,"sequence":null,"catalog_resolution":{"status":"unbound","artifact_version_id":null,"digest_check":"not_checked","reason":"no_artifact_binding"}}`
		observation = `{"id":"` + componentID + `","target_id":"` + targetID + `","sequence":1,"event_id":"` + versionID + `","type":"ComponentAppeared","component":"api","occurred_at":"2026-11-08T00:00:00Z","recorded_at":"2026-11-08T01:00:00Z"}`
		artifact    = `{"id":"` + artifactID + `","name":"payments-api","version_scheme":"build_number","pipeline":{"id":"` + pipelineID + `"},"url":"https://api.buildkite.com/artifacts/payments-api","id_url":"https://api.buildkite.com/artifacts/by-id/` + artifactID + `","created_at":"2026-11-08T00:00:00Z","archived_at":null}`
		version     = `{"id":"` + versionID + `","version":"1.3.0+build.5","digest":null,"source_url":null,"source_build":null,"pull_requests":[],"created_by":null,"url":"https://api.buildkite.com/artifact-versions/version","id_url":"https://api.buildkite.com/artifact-versions/by-id/` + versionID + `","created_at":"2026-11-08T00:00:00Z","yanked":false,"yanked_at":null,"yank_reason":null}`
		comparison  = `{"artifact":{"id":"` + artifactID + `","name":"payments-api","version_scheme":"sha"},"left":{"id":"` + versionID + `","version":"abc123","digest":null,"yanked":false},"right":{"id":"` + componentID + `","version":"def456","digest":null,"yanked":true},"basis":null,"result":"unknown","reason":"no_precedence_for_scheme","same_version":false,"same_content":null}`
	)
	page := func(item string) string {
		return `{"items":[` + item + `],"links":{"self":"https://api.buildkite.com/current","next":"https://api.buildkite.com/current?after=next-cursor"}}`
	}
	tests := []struct {
		tool  string
		args  map[string]any
		path  string
		query url.Values
		body  string
	}{
		{"list_release_targets", map[string]any{"after": "cursor+one", "per_page": 2}, "targets", url.Values{"after": {"cursor+one"}, "per_page": {"2"}}, page(target)},
		{"get_release_target", map[string]any{"target": map[string]any{"id": targetID}}, "targets/by-id/" + targetID, nil, target},
		{"get_release_target", map[string]any{"target": map[string]any{"name": targetID}}, "targets/" + targetID, nil, target},
		{"list_release_components", map[string]any{"target": map[string]any{"name": "prod"}}, "targets/prod/components", nil, page(component)},
		{"get_release_component", map[string]any{"component": map[string]any{"id": componentID}}, "components/by-id/" + componentID, nil, component},
		{"list_release_component_statuses", map[string]any{"target": map[string]any{"id": targetID}}, "targets/by-id/" + targetID + "/component-statuses", nil, page(status)},
		{"get_release_component_status", map[string]any{"target": map[string]any{"id": targetID}, "component": "api"}, "targets/by-id/" + targetID + "/component-statuses/api", nil, unobserved},
		{"list_release_observations", map[string]any{"target": map[string]any{"id": targetID}, "component": "api", "type": "ComponentAppeared,ComponentDisappeared", "before": "cursor-two"}, "targets/by-id/" + targetID + "/observations", url.Values{"component": {"api"}, "type": {"ComponentAppeared,ComponentDisappeared"}, "before": {"cursor-two"}}, page(observation)},
		{"list_deployment_artifacts", nil, "artifacts", nil, page(artifact)},
		{"get_deployment_artifact", map[string]any{"artifact": map[string]any{"id": artifactID}}, "artifacts/by-id/" + artifactID, nil, artifact},
		{"list_deployment_artifact_versions", map[string]any{"artifact": map[string]any{"name": "payments-api"}, "order": "precedence", "direction": "asc", "newer_than": versionID, "suffix": "", "yanked": false}, "artifacts/payments-api/versions", url.Values{"order": {"precedence"}, "direction": {"asc"}, "newer_than": {versionID}, "suffix": {""}, "yanked": {"false"}}, `{"items":[],"links":{"self":"https://api.buildkite.com/current"},"ordering":{"basis":"build_number","direction":"asc"}}`},
		{"get_deployment_artifact_version", map[string]any{"version": map[string]any{"artifact": map[string]any{"id": artifactID}, "version": "1.3.0+build.5"}}, "artifacts/by-id/" + artifactID + "/versions/1.3.0+build.5", nil, version},
		{"compare_deployment_artifact_versions", map[string]any{"artifact": map[string]any{"name": "payments-api"}, "left": "1.3.0+build.5", "right_id": versionID}, "artifacts/payments-api/version-comparison", url.Values{"left": {"1.3.0+build.5"}, "right_id": {versionID}}, comparison},
		{"list_deployment_artifact_comparison_pull_requests", map[string]any{"artifact": map[string]any{"id": artifactID}, "left_id": versionID, "right": "1.2.0", "after": "pr-cursor", "per_page": 1}, "artifacts/by-id/" + artifactID + "/version-comparison/pull-requests", url.Values{"left_id": {versionID}, "right": {"1.2.0"}, "after": {"pr-cursor"}, "per_page": {"1"}}, page(`{"url":"https://github.com/acme/api/pull/2","provider":"github","repository_url":"https://github.com/acme/api","id":"2"}`)},
	}
	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodGet, r.Method)
				assert.Equal(t, "/v2/organizations/acme/"+tt.path, r.URL.Path)
				assert.Equal(t, tt.query.Encode(), r.URL.Query().Encode())
				w.Header().Set("Content-Type", "application/json")
				_, _ = fmt.Fprint(w, tt.body)
			}))
			t.Cleanup(api.Close)
			session := releasesSession(t, api.URL)
			args := map[string]any{"org_slug": "acme"}
			for key, value := range tt.args {
				args[key] = value
			}
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: args})
			require.NoError(t, err)
			require.False(t, result.IsError, "%v", result.Content)
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			require.JSONEq(t, tt.body, text.Text)
		})
	}
}

func TestReleasesMCPRejectsInvalidSelectorsAndCursors(t *testing.T) {
	api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected API request: %s", r.URL)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(api.Close)
	session := releasesSession(t, api.URL)
	for _, tt := range []struct {
		tool string
		args map[string]any
	}{
		{"get_release_target", map[string]any{"target": map[string]any{}}},
		{"get_deployment_artifact", map[string]any{"artifact": map[string]any{"name": "payments", "id": "019a1dd3-3870-7e50-8a6d-f16efba96342"}}},
		{"get_release_target", map[string]any{"target": map[string]any{"name": "prod", "id": "019a1dd3-3870-7e50-8a6d-f16efba96342"}}},
		{"list_release_targets", map[string]any{"after": "a", "before": "b"}},
	} {
		t.Run(tt.tool, func(t *testing.T) {
			tt.args["org_slug"] = "acme"
			result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: tt.args})
			require.NoError(t, err)
			require.True(t, result.IsError)
		})
	}
}

func TestReleasesMCPPreservesAPIErrors(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound, http.StatusUnprocessableEntity} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const body = `{"message":"Releases access denied","errors":[{"field":"order","code":"no_precedence_for_scheme"}]}`
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, body)
			}))
			t.Cleanup(api.Close)
			result, err := releasesSession(t, api.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: "list_release_targets", Arguments: map[string]any{"org_slug": "acme"}})
			if status == http.StatusUnauthorized {
				require.Error(t, err)
				require.ErrorContains(t, err, "unauthorized")
				return
			}
			require.NoError(t, err)
			require.True(t, result.IsError)
			require.JSONEq(t, body, result.Content[0].(*mcp.TextContent).Text)
		})
	}
}

func releasesSession(t *testing.T, baseURL string) *mcp.ClientSession {
	t.Helper()
	api, err := buildkiteapi.NewClient(buildkiteapi.WithBaseURL(baseURL + "/"))
	require.NoError(t, err)
	server := NewMCPServer("test", buildkite.ToolDependencies{ReleasesClient: api.Releases}, WithToolsets("releases"), WithReadOnly(true))
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })
	return session
}
