package server

import (
	"context"
	"errors"
	"strings"

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
				SignalUnauthorized(ctx)
				if cb != nil {
					cb()
				}
			}
			return result, err
		}
	}
}

// instructionSection is one paragraph of the server instructions, optionally
// gated on a toolset being enabled. An empty toolset means always included.
type instructionSection struct {
	toolset string
	text    string
}

var instructionSections = []instructionSection{
	{text: "This is the Buildkite MCP Server. It provides access to the Buildkite CI/CD API, enabling you to manage and inspect pipelines, builds, jobs, logs, clusters, tests, artifacts, and annotations."},
	{
		toolset: toolsets.ToolsetUser,
		text:    "Start here: Before using most tools, call user_token_organization to retrieve the organization slug. Nearly every other tool requires the org_slug parameter, and this call is the fastest way to discover it.",
	},
	{
		toolset: toolsets.ToolsetSkills,
		text:    "Skill discovery: Always call list_skills early in a session — it's cheap (names and one-line descriptions only) and surfaces guidance not visible in any tool's name or schema. When a task matches a listed skill (e.g. debugging a build failure, tuning search_logs), call load_skill for that guide — it covers parameter tuning, caching behavior, and details beyond the summaries below.",
	},
	{text: "Authorization: Tools available depend on the scopes granted to the configured API token. A 401 response from a tool means the token lacks the required scope for that operation."},
	{text: "Common pitfalls:\n\nbuild_number is a sequential integer string (e.g. \"42\"), not a UUID. Build, job, artifact, and log tools all require this identifier — do not use the build's UUID id field."},
	{
		toolset: toolsets.ToolsetBuilds,
		text:    "Job state \"broken\" means the job did not run because something inside the build prevented execution: an if conditional evaluated to false, a branch filter did not match, or an upstream dependency failed. It does not mean the job's command failed. Distinguish: broken = build configuration or dependencies prevented execution; failed = job ran but exited non-zero; skipped = external factor (e.g. a newer build superseded it). When both failed and broken jobs are present, investigate failed upstream jobs first.",
	},
	{
		toolset: toolsets.ToolsetLogs,
		text:    "Log investigation order: start with tail_logs to see recent output (cheapest, catches most failures), then search_logs with a pattern and limit for targeted investigation, and only use read_logs with seek and limit for deep sequential inspection. Avoid calling read_logs without a limit on large logs.",
	},
	{
		toolset: toolsets.ToolsetAnnotations,
		text:    "Annotation scope: when creating an annotation with scope \"job\", job_id is required. If job_id is provided but scope is left as the default \"build\", the job_id is silently ignored.",
	},
}

// BuildkiteServerInstructions builds the instructions sent to MCP clients
// describing how to use this server's tools, including only the paragraphs
// relevant to enabledToolsets. Exported so other services embedding or
// re-implementing Buildkite MCP tools (e.g. custom/internal MCP servers) can
// reuse the same guidance instead of maintaining their own copy.
func BuildkiteServerInstructions(enabledToolsets []string) string {
	parts := make([]string, 0, len(instructionSections))
	for _, s := range instructionSections {
		if s.toolset == "" || toolsets.IsToolsetEnabled(enabledToolsets, s.toolset) {
			parts = append(parts, s.text)
		}
	}
	return strings.Join(parts, "\n\n")
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
	}, &mcp.ServerOptions{
		Instructions: BuildkiteServerInstructions(cfg.EnabledToolsets),
	})

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

	// Register prompts
	s.AddPrompt(&mcp.Prompt{
		Name:        "user_token_organization_prompt",
		Description: "When asked for detail of a user's pipelines start by looking up the user's token organization",
	}, buildkite.HandleUserTokenOrganizationPrompt)

	reportIssuePrompt, reportIssueHandler := buildkite.NewReportIssuePrompt(version)
	s.AddPrompt(reportIssuePrompt, reportIssueHandler)

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
