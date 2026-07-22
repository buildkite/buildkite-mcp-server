package server

import (
	"context"
	"net/http"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func emptyDeps() buildkite.ToolDependencies {
	return buildkite.ToolDependencies{}
}

func TestParseToolsetsHeader(t *testing.T) {
	tests := []struct {
		name   string
		header string
		want   []string
	}{
		{
			name:   "single toolset",
			header: "pipelines",
			want:   []string{"pipelines"},
		},
		{
			name:   "multiple toolsets",
			header: "pipelines,builds,clusters",
			want:   []string{"pipelines", "builds", "clusters"},
		},
		{
			name:   "with spaces",
			header: " pipelines , builds , clusters ",
			want:   []string{"pipelines", "builds", "clusters"},
		},
		{
			name:   "empty parts ignored",
			header: "pipelines,,builds",
			want:   []string{"pipelines", "builds"},
		},
		{
			name:   "empty string",
			header: "",
			want:   nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseToolsetsHeader(tt.header)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestNewPerRequestServerFactory_DefaultsWhenNoHeaders(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestNewPerRequestServerFactory_ToolsetsHeader(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderToolsets, "pipelines,builds")

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestNewPerRequestServerFactory_ReadOnlyHeader(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderReadOnly, "true")

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestNewPerRequestServerFactory_ReadOnlyHeaderCaseInsensitive(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderReadOnly, "TRUE")

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestNewPerRequestServerFactory_InvalidToolsetsFallsBackToDefaults(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderToolsets, "invalid_toolset,also_invalid")

	// Should not panic; falls back to defaults
	srv := factory(req)
	require.NotNil(t, srv)
}

func TestNewPerRequestServerFactory_BothHeaders(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderToolsets, "pipelines")
	req.Header.Set(HeaderReadOnly, "true")

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestNewPerRequestServerFactory_DisabledToolsetsCannotBeReenabled(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false, toolsets.ToolsetLogs)

	for _, tt := range []struct {
		name           string
		toolsetsHeader string
	}{
		{name: "server defaults"},
		{name: "per-request override", toolsetsHeader: toolsets.ToolsetLogs},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
			require.NoError(t, err)
			req.Header.Set(HeaderToolsets, tt.toolsetsHeader)

			toolNames := listToolNames(t, factory(req))
			require.NotContains(t, toolNames, "search_logs")
			require.NotContains(t, toolNames, "tail_logs")
			require.NotContains(t, toolNames, "read_logs")
			if tt.toolsetsHeader == "" {
				require.Contains(t, toolNames, "get_build")
			}
		})
	}
}

func listToolNames(t *testing.T, server *mcp.Server) []string {
	t.Helper()
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := server.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	clientSession, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = clientSession.Close() })

	result, err := clientSession.ListTools(ctx, nil)
	require.NoError(t, err)
	names := make([]string, 0, len(result.Tools))
	for _, tool := range result.Tools {
		names = append(names, tool.Name)
	}
	return names
}
