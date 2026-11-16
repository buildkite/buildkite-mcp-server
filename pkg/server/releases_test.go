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
		targetID    = "019a1dd8-a49b-70c5-99b8-b8f9f0ed6824"
		componentID = "019a1ddb-a210-7b03-b4a6-5fcd44828b31"
		instanceID  = "019a1dda-a210-7b03-b4a6-5fcd44828b31"
		artifactID  = "019a1ddc-a210-7b03-b4a6-5fcd44828b31"
		digestID    = "019a1ddd-a210-7b03-b4a6-5fcd44828b31"
		sha         = "sha256:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
		target      = `{"id":"` + targetID + `","graphql_id":"opaque-target","name":"prod","labels":{"region":"au"},"url":"https://api.buildkite.com/targets/prod","id_url":"https://api.buildkite.com/targets/by-id/` + targetID + `","created_at":"2026-11-08T00:00:00Z","archived_at":null}`
		component   = `{"id":"` + componentID + `","graphql_id":"opaque-component","name":"api","artifact":{"id":"` + artifactID + `","graphql_id":"opaque-artifact","name":"payments-api"},"url":"https://api.buildkite.com/components/api","id_url":"https://api.buildkite.com/components/by-id/` + componentID + `","created_at":"2026-11-08T00:00:00Z","archived_at":null}`
		instance    = `{"id":"` + instanceID + `","graphql_id":"opaque-instance","name":"api-blue","target_id":"` + targetID + `","component_id":"` + componentID + `","url":"https://api.buildkite.com/targets/prod/instances/api-blue","id_url":"https://api.buildkite.com/component_instance/by-id/` + instanceID + `","created_at":"2026-11-08T00:00:00Z","archived_at":null}`
		status      = `{"target_id":"` + targetID + `","instance":"api-blue","instance_id":"` + instanceID + `","present":false,"active_digest":null,"last_active_digest":"` + sha + `","first_seen_at":"2026-11-08T00:00:00Z","last_seen_at":"2026-11-08T01:00:00Z","sequence":4,"catalog_resolution":{"status":"unknown","reason":"instance_absent"}}`
		unobserved  = `{"target_id":"` + targetID + `","instance":"api-blue","instance_id":"` + instanceID + `","present":null,"active_digest":null,"last_active_digest":null,"first_seen_at":null,"last_seen_at":null,"sequence":null,"catalog_resolution":{"status":"unknown","reason":"no_active_version"}}`
		observation = `{"id":"` + componentID + `","graphql_id":"opaque-observation","target_id":"` + targetID + `","sequence":1,"event_id":"` + digestID + `","type":"ComponentInstanceVersionObserved","instance":"api-blue","digest":"` + sha + `","occurred_at":"2026-11-08T00:00:00Z","recorded_at":"2026-11-08T01:00:00Z","catalog_resolution":{"status":"resolved","reason":null}}`
		artifact    = `{"id":"` + artifactID + `","graphql_id":"opaque-artifact","name":"payments-api","url":"https://api.buildkite.com/artifacts/payments-api","id_url":"https://api.buildkite.com/artifacts/by-id/` + artifactID + `","created_at":"2026-11-08T00:00:00Z","archived_at":null}`
		version     = `{"id":"` + digestID + `","graphql_id":"opaque-version","digest":null,"source_url":null,"source_build":null,"pull_requests":[{"url":"https://github.com/acme/api/pull/2"}],"created_by":null,"url":"https://api.buildkite.com/artifact-versions/by-id/` + digestID + `","id_url":"https://api.buildkite.com/artifact-versions/by-id/` + digestID + `","created_at":"2026-11-08T00:00:00Z","yanked":true,"yanked_at":"2026-11-08T01:00:00Z","yank_reason":"withdrawn"}`
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
		{"list_release_components", nil, "components", nil, page(component)},
		{"get_release_component", map[string]any{"component": map[string]any{"id": componentID}}, "components/by-id/" + componentID, nil, component},
		{"list_release_component_instances", map[string]any{"label": map[string]string{"region": "au +", "empty": ""}, "after": "next", "per_page": 3}, "component_instance", url.Values{"label[region]": {"au +"}, "label[empty]": {""}, "after": {"next"}, "per_page": {"3"}}, page(instance)},
		{"list_release_component_instances", map[string]any{"target": map[string]any{"name": "prod"}, "label": map[string]string{"region": "au"}}, "targets/prod/instances", url.Values{"label[region]": {"au"}}, page(instance)},
		{"get_release_component_instance", map[string]any{"instance": map[string]any{"id": instanceID}}, "component_instance/by-id/" + instanceID, nil, instance},
		{"get_release_component_instance", map[string]any{"instance": map[string]any{"target": map[string]any{"id": targetID}, "name": "api-blue"}}, "targets/by-id/" + targetID + "/instances/api-blue", nil, instance},
		{"list_release_component_instance_statuses", map[string]any{"target": map[string]any{"id": targetID}}, "targets/by-id/" + targetID + "/instance-statuses", nil, page(status)},
		{"get_release_component_instance_status", map[string]any{"target": map[string]any{"id": targetID}, "instance": "api-blue"}, "targets/by-id/" + targetID + "/instance-statuses/api-blue", nil, unobserved},
		{"list_release_observations", map[string]any{"target": map[string]any{"id": targetID}, "instance": "api-blue", "type": "ComponentInstanceAppeared,ComponentInstanceVersionObserved", "before": "cursor-two"}, "targets/by-id/" + targetID + "/observations", url.Values{"instance": {"api-blue"}, "type": {"ComponentInstanceAppeared,ComponentInstanceVersionObserved"}, "before": {"cursor-two"}}, page(observation)},
		{"list_deployment_artifacts", nil, "artifacts", nil, page(artifact)},
		{"get_deployment_artifact", map[string]any{"artifact": map[string]any{"id": artifactID}}, "artifacts/by-id/" + artifactID, nil, artifact},
		{"list_deployment_artifact_versions", map[string]any{"artifact": map[string]any{"name": "payments-api"}, "before": "prior"}, "artifacts/payments-api/versions", url.Values{"before": {"prior"}}, page(version)},
		{"get_deployment_artifact_version", map[string]any{"version": map[string]any{"id": digestID}}, "artifact-versions/by-id/" + digestID, nil, version},
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
			args := map[string]any{"org_slug": "acme"}
			for key, value := range tt.args {
				args[key] = value
			}
			result, err := releasesSession(t, api.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: tt.tool, Arguments: args})
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
		w.WriteHeader(500)
	}))
	t.Cleanup(api.Close)
	session := releasesSession(t, api.URL)
	for _, tt := range []struct {
		tool string
		args map[string]any
	}{
		{"get_release_target", map[string]any{"target": map[string]any{}}},
		{"get_deployment_artifact", map[string]any{"artifact": map[string]any{"name": "payments", "id": "artifact-id"}}},
		{"get_release_target", map[string]any{"target": map[string]any{"name": "prod", "id": "target-id"}}},
		{"get_release_component", map[string]any{"component": map[string]any{"name": "api", "id": "component-id"}}},
		{"get_release_component_instance", map[string]any{"instance": map[string]any{"name": "api-blue"}}},
		{"get_deployment_artifact_version", map[string]any{"version": map[string]any{}}},
		{"list_release_targets", map[string]any{"after": "a", "before": "b"}},
		{"list_release_component_instances", map[string]any{"target": map[string]any{}}},
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
	for _, status := range []int{401, 403, 404, 422} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			const body = `{"message":"Releases access denied","errors":[{"field":"instance","code":"not_found"}]}`
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				_, _ = fmt.Fprint(w, body)
			}))
			t.Cleanup(api.Close)
			result, err := releasesSession(t, api.URL).CallTool(context.Background(), &mcp.CallToolParams{Name: "list_release_targets", Arguments: map[string]any{"org_slug": "acme"}})
			if status == 401 {
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
