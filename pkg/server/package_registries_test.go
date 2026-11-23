package server

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	buildkiteapi "github.com/buildkite/go-buildkite/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const updateRegistryTool = "update_package_registry"

func TestUpdatePackageRegistryDiscovery(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options []ToolsetOption
		visible bool
	}{
		{name: "default", visible: true},
		{name: "packages", options: []ToolsetOption{WithToolsets("packages")}, visible: true},
		{name: "cache registries", options: []ToolsetOption{WithToolsets("cache_registries")}},
		{name: "excluded", options: []ToolsetOption{WithDisabledToolsets("packages")}},
		{name: "read-only", options: []ToolsetOption{WithReadOnly(true)}},
		{name: "minimum access", options: []ToolsetOption{WithMinimumAccess(true)}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			names := listToolNames(t, NewMCPServer("test", emptyDeps(), tt.options...))
			require.NotContains(t, names, "update_package_registry_visibility")
			if tt.visible {
				require.Contains(t, names, updateRegistryTool)
			} else {
				require.NotContains(t, names, updateRegistryTool)
			}
		})
	}
}

func TestUpdatePackageRegistryThroughMCP(t *testing.T) {
	for _, tt := range []struct {
		name      string
		fields    string
		wantBody  string
		status    int
		response  string
		wantError string
	}{
		{name: "public", fields: `,"visibility":"public","telemetry":{"context":"The user wants packages to be downloadable without authentication and has requested that the package registry be made public."}`, wantBody: `{"visibility":"public"}`, status: 200, response: `{"slug":"widgets","public":true,"description":"unchanged"}`},
		{name: "private", fields: `,"visibility":"private"`, wantBody: `{"visibility":"private"}`, status: 200, response: `{"slug":"widgets","public":false,"description":null}`},
		{name: "metadata only", fields: `,"name":"Widget builds","description":"Build outputs","emoji":":package:","color":"#94cfff"`, wantBody: `{"name":"Widget builds","description":"Build outputs","emoji":":package:","color":"#94cfff"}`, status: 200, response: `{"slug":"widget-builds","public":false}`},
		{name: "clear metadata", fields: `,"description":null,"emoji":null,"color":null`, wantBody: `{"description":null,"emoji":null,"color":null}`, status: 200, response: `{"slug":"widgets","public":false,"description":null,"emoji":null,"color":null}`},
		{name: "explicit empty description", fields: `,"description":""`, wantBody: `{"description":""}`, status: 200, response: `{"slug":"widgets","public":false,"description":""}`},
		{name: "mixed visibility and metadata", fields: `,"visibility":"public","description":"Release artifacts"`, wantBody: `{"visibility":"public","description":"Release artifacts"}`, status: 200, response: `{"slug":"widgets","public":true,"description":"Release artifacts"}`},
		{name: "older API ignores visibility", fields: `,"visibility":"public","description":"Release artifacts"`, wantBody: `{"visibility":"public","description":"Release artifacts"}`, status: 200, response: `{"slug":"widgets","public":false,"description":"Release artifacts"}`, wantError: "The API did not confirm public registry visibility. It may not support visibility updates. Other supplied settings may already have been applied."},
		{name: "clear OIDC policy", fields: `,"oidc_policy":null`, wantBody: `{"oidc_policy":null}`, status: 200, response: `{"slug":"widgets","public":false,"oidc_policy":null}`},
		{name: "OIDC policy as YAML", fields: `,"oidc_policy":"[]\n"`, wantBody: `{"oidc_policy":"[]\n"}`, status: 200, response: `{"slug":"widgets","public":false,"oidc_policy":"[]\n"}`},
		{name: "OIDC policy as JSON", fields: `,"oidc_policy":[{"iss":"https://agent.example.com","scopes":["read_packages"],"claims":{"pipeline-slug":{"in":["widgets"]}}}]`, wantBody: `{"oidc_policy":[{"iss":"https://agent.example.com","scopes":["read_packages"],"claims":{"pipeline-slug":{"in":["widgets"]}}}]}`, status: 200, response: `{"slug":"widgets","public":false}`},
		{name: "case sensitive even with metadata", fields: `,"visibility":"Public","description":"must not update"`},
		{name: "no normalization", fields: `,"visibility":" private "`},
		{name: "no empty value", fields: `,"visibility":""`},
		{name: "no update fields", fields: `,"telemetry":{"context":"No registry update requested"}`},
		{name: "no null visibility even with metadata", fields: `,"visibility":null,"description":"must not update"`},
		{name: "no boolean", fields: `,"visibility":false`},
		{name: "no null name", fields: `,"name":null`},
		{name: "no numeric metadata", fields: `,"description":1`},
		{name: "no OIDC policy object", fields: `,"oidc_policy":{"iss":"https://agent.example.com"}`},
		{name: "no scalar OIDC policy statements", fields: `,"oidc_policy":[false]`},
		{name: "no unrelated settings", fields: `,"visibility":"private","ecosystem":"npm"`},
		{name: "unauthorized", fields: `,"visibility":"private"`, wantBody: `{"visibility":"private"}`, status: 401, response: `{"message":"Token expired"}`},
		{name: "forbidden", fields: `,"visibility":"private"`, wantBody: `{"visibility":"private"}`, status: 403, response: `{"message":"Forbidden"}`},
		{name: "not found", fields: `,"visibility":"private"`, wantBody: `{"visibility":"private"}`, status: 404, response: `{"message":"Not Found"}`},
		{name: "policy error", fields: `,"visibility":"public"`, wantBody: `{"visibility":"public"}`, status: 422, response: `{"message":"Validation Failed","errors":["Public registries are not allowed"]}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var calls atomic.Int32
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				assert.Equal(t, http.MethodPatch, r.Method)
				assert.Equal(t, "/v2/packages/organizations/acme/registries/widgets", r.URL.RequestURI())
				body, err := io.ReadAll(r.Body)
				assert.NoError(t, err)
				assert.JSONEq(t, tt.wantBody, string(body))
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tt.status)
				_, _ = io.WriteString(w, tt.response)
			}))
			t.Cleanup(api.Close)
			client, err := buildkiteapi.NewOpts(buildkiteapi.WithBaseURL(api.URL + "/"))
			require.NoError(t, err)
			deps := buildkite.ToolDependencies{PackageRegistriesClient: &buildkite.BuildkiteClientAdapter{Client: client}}
			srv := NewMCPServer("test", deps, WithToolsets("packages"))
			serverTransport, clientTransport := mcp.NewInMemoryTransports()
			serverSession, err := srv.Connect(t.Context(), serverTransport, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = serverSession.Close() })
			session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil).Connect(t.Context(), clientTransport, nil)
			require.NoError(t, err)
			t.Cleanup(func() { _ = session.Close() })
			result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      updateRegistryTool,
				Arguments: json.RawMessage(`{"org_slug":"acme","registry_slug":"widgets"` + tt.fields + `}`),
			})
			if tt.status == 0 {
				require.Zero(t, calls.Load(), "invalid arguments must not dispatch a PATCH")
				require.NoError(t, err)
				require.True(t, result.IsError)
				return
			}
			require.Equal(t, int32(1), calls.Load())
			if tt.status == http.StatusUnauthorized {
				require.ErrorIs(t, err, buildkite.ErrUnauthorized)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tt.status >= 400 || tt.wantError != "", result.IsError)
			require.Len(t, result.Content, 1)
			text, ok := result.Content[0].(*mcp.TextContent)
			require.True(t, ok)
			if tt.wantError != "" {
				require.Equal(t, tt.wantError, text.Text)
				return
			}
			require.JSONEq(t, tt.response, text.Text)
		})
	}
}
