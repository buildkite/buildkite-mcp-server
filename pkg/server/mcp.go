package server

import (
	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

// ToolsetOption configures toolset behavior
type ToolsetOption func(*ToolsetConfig)

// ToolsetConfig holds configuration for toolset selection and behavior
type ToolsetConfig struct {
	EnabledToolsets []string
	ReadOnly        bool
	DynamicToolsets bool // Enable/disable Tool Search Tool
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

// WithDynamicToolsets enables dynamic tool loading via Tool Search Tool
func WithDynamicToolsets(dynamicToolsets bool) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.DynamicToolsets = dynamicToolsets
	}
}

// NewMCPServer creates a new MCP server with the given configuration and toolsets
func NewMCPServer(version string, client *gobuildkite.Client, buildkiteLogsClient *buildkitelogs.Client, opts ...ToolsetOption) *server.MCPServer {
	// Default configuration
	cfg := &ToolsetConfig{
		EnabledToolsets: []string{"all"},
		ReadOnly:        false,
	}

	// Apply options
	for _, opt := range opts {
		opt(cfg)
	}

	s := server.NewMCPServer(
		"buildkite-mcp-server",
		version,
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithToolHandlerMiddleware(trace.ToolHandlerFunc),
		server.WithResourceHandlerMiddleware(trace.WithResourceHandlerFunc),
		server.WithHooks(trace.NewHooks()),
		server.WithLogging())

	log.Info().Str("version", version).Msg("Starting Buildkite MCP server")

	// Use toolset system with configuration
	s.AddTools(BuildkiteTools(client, buildkiteLogsClient,
		WithReadOnly(cfg.ReadOnly),
		WithToolsets(cfg.EnabledToolsets...),
		WithDynamicToolsets(cfg.DynamicToolsets))...)

	s.AddPrompt(mcp.NewPrompt("user_token_organization_prompt",
		mcp.WithPromptDescription("When asked for detail of a users pipelines start by looking up the user's token organization"),
	), buildkite.HandleUserTokenOrganizationPrompt)

	s.AddResource(mcp.NewResource(
		"debug-logs-guide",
		"Debug Logs Guide",
		mcp.WithResourceDescription("Comprehensive guide for debugging Buildkite build failures using logs"),
	), buildkite.HandleDebugLogsGuideResource)

	return s
}

// BuildkiteTools creates tools using the toolset system with functional options
func BuildkiteTools(client *gobuildkite.Client, buildkiteLogsClient *buildkitelogs.Client, opts ...ToolsetOption) []server.ServerTool {
	cfg := &ToolsetConfig{
		EnabledToolsets: []string{"all"},
		ReadOnly:        false,
	}

	for _, opt := range opts {
		opt(cfg)
	}

	registry := toolsets.NewToolsetRegistry()

	registry.RegisterToolsets(
		toolsets.CreateBuiltinToolsets(client, buildkiteLogsClient),
	)

	var serverTools []server.ServerTool

	// Add Tool Search Tool if dynamic toolsets are enabled
	if cfg.DynamicToolsets {
		searchTool, searchHandler, _ := toolsets.ToolSearch(registry)
		serverTools = append(serverTools, server.ServerTool{
			Tool:    searchTool,
			Handler: searchHandler,
		})
	}

	enabledTools := registry.GetEnabledTools(cfg.EnabledToolsets, cfg.ReadOnly)

	for _, toolDef := range enabledTools {
		tool := toolDef.Tool
		if cfg.DynamicToolsets {
			tool.DeferLoading = toolDef.DeferLoading
		} else {
			tool.DeferLoading = false
		}

		serverTools = append(serverTools, server.ServerTool{
			Tool:    tool,
			Handler: toolDef.Handler,
		})
	}

	scopes := registry.GetRequiredScopes(cfg.EnabledToolsets, cfg.ReadOnly)

	log.Info().
		Strs("enabled_toolsets", cfg.EnabledToolsets).
		Bool("read_only", cfg.ReadOnly).
		Bool("dynamic_toolsets", cfg.DynamicToolsets).
		Int("tool_count", len(serverTools)).
		Strs("required_scopes", scopes).
		Msg("Registered tools from toolsets")

	return serverTools
}
