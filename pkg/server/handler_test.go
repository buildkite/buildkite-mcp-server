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
	for _, tt := range []struct {
		name           string
		toolsetsHeader string
		disabled       string
		absent         []string
		present        string
	}{
		{name: "server defaults", disabled: toolsets.ToolsetLogs, absent: []string{"search_logs", "tail_logs", "read_logs"}, present: "get_build"},
		{name: "per-request override", toolsetsHeader: toolsets.ToolsetLogs, disabled: toolsets.ToolsetLogs, absent: []string{"search_logs", "tail_logs", "read_logs"}},
		{name: "all override", toolsetsHeader: "all", disabled: toolsets.ToolsetLogs, absent: []string{"search_logs", "tail_logs", "read_logs"}, present: "retry_job"},
		{name: "retries override", toolsetsHeader: "retries", disabled: toolsets.ToolsetLogs, absent: []string{"search_logs", "tail_logs", "read_logs"}, present: "retry_job"},
		{name: "logs and retries override", toolsetsHeader: "logs,retries", disabled: toolsets.ToolsetLogs, absent: []string{"search_logs", "tail_logs", "read_logs"}, present: "retry_job"},
		{name: "invalid override", toolsetsHeader: "invalid", disabled: toolsets.ToolsetLogs, absent: []string{"search_logs", "tail_logs", "read_logs"}, present: "get_build"},
		{name: "builds disabled", toolsetsHeader: "retries", disabled: toolsets.ToolsetBuilds, absent: []string{"get_build", "list_jobs", "retry_job", "retry_failed_jobs"}, present: "tail_logs"},
		{name: "retries disabled", toolsetsHeader: "builds,logs", disabled: toolsets.ToolsetRetries, absent: []string{"get_build", "tail_logs", "retry_job", "retry_failed_jobs"}, present: "create_build"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false, WithDisabledToolsets(tt.disabled))
			req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
			require.NoError(t, err)
			req.Header.Set(HeaderToolsets, tt.toolsetsHeader)

			toolNames := listToolNames(t, factory(req))
			for _, name := range tt.absent {
				require.NotContains(t, toolNames, name)
			}
			if tt.present != "" {
				require.Contains(t, toolNames, tt.present)
			}
		})
	}
}

func TestNewPerRequestServerFactory_MinimumAccessCannotBeRelaxed(t *testing.T) {
	factory := NewPerRequestServerFactory("test", emptyDeps(), []string{"user"}, false, WithMinimumAccess(true))
	for _, tt := range []struct {
		name     string
		header   string
		expected []string
	}{
		{name: "defaults", expected: []string{"current_user", "access_token"}},
		{name: "overlapping toolsets", header: "retries,logs", expected: []string{"list_builds", "get_build", "wait_for_build", "list_jobs", "get_job"}},
		{name: "forbidden toolset", header: "cluster_secrets"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
			require.NoError(t, err)
			req.Header.Set(HeaderToolsets, tt.header)
			req.Header.Set(HeaderReadOnly, "false")
			require.ElementsMatch(t, tt.expected, listToolNames(t, factory(req)))
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
