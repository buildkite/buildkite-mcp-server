package commands

import (
	"net/http"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
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
			got := parseToolsetsHeader(tt.header)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPerRequestServerFactory_DefaultsWhenNoHeaders(t *testing.T) {
	factory := newPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestPerRequestServerFactory_ToolsetsHeader(t *testing.T) {
	factory := newPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderToolsets, "pipelines,builds")

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestPerRequestServerFactory_ReadOnlyHeader(t *testing.T) {
	factory := newPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderReadOnly, "true")

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestPerRequestServerFactory_ReadOnlyHeaderCaseInsensitive(t *testing.T) {
	factory := newPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderReadOnly, "TRUE")

	srv := factory(req)
	require.NotNil(t, srv)
}

func TestPerRequestServerFactory_InvalidToolsetsFallsBackToDefaults(t *testing.T) {
	factory := newPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderToolsets, "invalid_toolset,also_invalid")

	// Should not panic; falls back to defaults
	srv := factory(req)
	require.NotNil(t, srv)
}

func TestPerRequestServerFactory_BothHeaders(t *testing.T) {
	factory := newPerRequestServerFactory("test", emptyDeps(), []string{"all"}, false)

	req, err := http.NewRequest(http.MethodPost, "/mcp", nil)
	require.NoError(t, err)
	req.Header.Set(HeaderToolsets, "pipelines")
	req.Header.Set(HeaderReadOnly, "true")

	srv := factory(req)
	require.NotNil(t, srv)
}
