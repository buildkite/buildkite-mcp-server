package server

import (
	"net/http"
	"strings"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
)

const (
	// HeaderToolsets is the HTTP header for specifying which toolsets to enable per request.
	// Value is a comma-separated list of toolset names (e.g., "pipelines,builds").
	HeaderToolsets = "X-Buildkite-Toolsets"

	// HeaderReadOnly is the HTTP header for enabling read-only mode per request.
	// Value should be "true" to enable read-only mode.
	HeaderReadOnly = "X-Buildkite-Read-Only"
)

// NewPerRequestServerFactory returns a function that creates an mcp.Server per HTTP request.
// It reads X-Buildkite-Toolsets and X-Buildkite-Read-Only headers from the request,
// falling back to the provided defaults when headers are absent.
func NewPerRequestServerFactory(
	version string,
	deps buildkite.ToolDependencies,
	defaultToolsets []string,
	defaultReadOnly bool,
) func(*http.Request) *mcp.Server {
	return func(r *http.Request) *mcp.Server {
		enabledToolsets := defaultToolsets
		readOnly := defaultReadOnly

		if header := r.Header.Get(HeaderToolsets); header != "" {
			parsed := ParseToolsetsHeader(header)
			if err := toolsets.ValidateToolsets(parsed); err != nil {
				log.Warn().Err(err).Str("header", header).Msg("Invalid toolsets in header, using server defaults")
			} else {
				enabledToolsets = parsed
			}
		}

		if header := r.Header.Get(HeaderReadOnly); header != "" {
			readOnly = strings.EqualFold(strings.TrimSpace(header), "true")
		}

		return NewMCPServer(version, deps,
			WithToolsets(enabledToolsets...),
			WithReadOnly(readOnly),
		)
	}
}

// ParseToolsetsHeader parses a comma-separated list of toolset names from a header value.
func ParseToolsetsHeader(header string) []string {
	var result []string
	for _, part := range strings.Split(header, ",") {
		trimmed := strings.TrimSpace(part)
		if trimmed != "" {
			result = append(result, trimmed)
		}
	}
	return result
}
