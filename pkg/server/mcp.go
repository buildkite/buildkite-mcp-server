package server

import (
	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
)

// ToolsetOption configures toolset behavior
type ToolsetOption func(*ToolsetConfig)

// ToolsetConfig holds configuration for toolset selection and behavior
type ToolsetConfig struct {
	EnabledToolsets []string
	ReadOnly        bool
}

// WithToolsets enables specific toolsets
func WithToolsets(toolsets ...string) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.EnabledToolsets = toolsets
	}
}

// WithReadOnly enables read-only mode which filters out write operations
func WithReadOnly(readOnly bool) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.ReadOnly = readOnly
	}
}

// NewMCPServer creates a new MCP server with the given configuration
func NewMCPServer(version string, deps buildkite.ToolDependencies, opts ...ToolsetOption) *mcp.Server {
	cfg := &ToolsetConfig{
		EnabledToolsets: []string{"all"},
		ReadOnly:        false,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	s := mcp.NewServer(&mcp.Implementation{
		Name:    "buildkite-mcp-server",
		Version: version,
	}, nil)

	log.Info().Str("version", version).Msg("Starting Buildkite MCP server")

	// Add middleware
	s.AddReceivingMiddleware(
		trace.NewMiddleware(),
		buildkite.InjectDepsMiddleware(deps),
	)

	// Register tools
	RegisterTools(s, cfg)

	// Register prompt
	s.AddPrompt(&mcp.Prompt{
		Name:        "user_token_organization_prompt",
		Description: "When asked for detail of a users pipelines start by looking up the user's token organization",
	}, buildkite.HandleUserTokenOrganizationPrompt)

	// Register resource
	s.AddResource(&mcp.Resource{
		URI:         "buildkite://debug-logs-guide",
		Name:        "Debug Logs Guide",
		Description: "Comprehensive guide for debugging Buildkite build failures using logs",
	}, buildkite.HandleDebugLogsGuideResource)

	return s
}

// RegisterTools registers tools from enabled toolsets onto the server
func RegisterTools(s *mcp.Server, cfg *ToolsetConfig) {
	registry := toolsets.NewToolsetRegistry()
	registry.RegisterToolsets(toolsets.CreateBuiltinToolsets())

	enabledTools := registry.GetEnabledTools(cfg.EnabledToolsets, cfg.ReadOnly)

	for _, toolDef := range enabledTools {
		toolDef.Register(s)
	}

	scopes := registry.GetRequiredScopes(cfg.EnabledToolsets, cfg.ReadOnly)

	log.Info().
		Strs("enabled_toolsets", cfg.EnabledToolsets).
		Bool("read_only", cfg.ReadOnly).
		Int("tool_count", len(enabledTools)).
		Strs("required_scopes", scopes).
		Msg("Registered tools from toolsets")
}
