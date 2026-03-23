package server

import (
	"context"
	"errors"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

// ToolsetOption configures toolset behavior
type ToolsetOption func(*ToolsetConfig)

// ToolsetConfig holds configuration for toolset selection and behavior
type ToolsetConfig struct {
	EnabledToolsets []string
	ReadOnly        bool
	OnUnauthorized  func()
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

// WithOnUnauthorized registers a callback that fires when the Buildkite API returns a
// 401. Library consumers use this to invalidate stored tokens and trigger reauth.
func WithOnUnauthorized(cb func()) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.OnUnauthorized = cb
	}
}

// unauthorizedMiddleware intercepts ErrUnauthorized propagated from tool handlers.
// It signals the HTTP layer (if present) and calls the optional library callback.
func unauthorizedMiddleware(cb func()) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			result, err := next(ctx, method, req)
			if err != nil && errors.Is(err, buildkite.ErrUnauthorized) {
				log.Ctx(ctx).Warn().Msg("Buildkite API returned 401 unauthorized; token may be invalid or expired")
				signalUnauthorized(ctx)
				if cb != nil {
					cb()
				}
			}
			return result, err
		}
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
		injectLoggerMiddleware(log.Logger),
		trace.NewMiddleware(),
		buildkite.InjectDepsMiddleware(deps),
		unauthorizedMiddleware(cfg.OnUnauthorized),
	)

	// Register tools
	RegisterTools(s, cfg)

	// Register prompt
	s.AddPrompt(&mcp.Prompt{
		Name:        "user_token_organization_prompt",
		Description: "When asked for detail of a user's pipelines start by looking up the user's token organization",
	}, buildkite.HandleUserTokenOrganizationPrompt)

	// Register resource
	s.AddResource(&mcp.Resource{
		URI:         "buildkite://debug-logs-guide",
		Name:        "Debug Logs Guide",
		Description: "Comprehensive guide for debugging Buildkite build failures using logs",
	}, buildkite.HandleDebugLogsGuideResource)

	return s
}

// injectLoggerMiddleware returns middleware that injects a zerolog logger into the request context.
func injectLoggerMiddleware(logger zerolog.Logger) mcp.Middleware {
	return func(next mcp.MethodHandler) mcp.MethodHandler {
		return func(ctx context.Context, method string, req mcp.Request) (mcp.Result, error) {
			ctx = logger.WithContext(ctx)
			return next(ctx, method, req)
		}
	}
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
