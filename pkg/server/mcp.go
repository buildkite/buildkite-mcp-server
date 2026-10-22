package server

import (
	"context"
	"errors"
	"slices"
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
	EnabledToolsets  []string
	DisabledToolsets []string
	ReadOnly         bool
	MinimumAccess    bool
	OnUnauthorized   func()
}

// WithToolsets enables specific toolsets
func WithToolsets(toolsets ...string) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.EnabledToolsets = toolsets
	}
}

// WithDisabledToolsets excludes all members of the named toolsets, including
// tools shared with other enabled toolsets. Exclusions take precedence.
func WithDisabledToolsets(toolsets ...string) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.DisabledToolsets = toolsets
	}
}

// WithReadOnly enables read-only mode which filters out write operations
func WithReadOnly(readOnly bool) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.ReadOnly = readOnly
	}
}

// WithMinimumAccess restricts tools to read-only operations requiring only
// read_builds, read_pipelines and read_user (or no API scopes).
func WithMinimumAccess(minimumAccess bool) ToolsetOption {
	return func(cfg *ToolsetConfig) {
		cfg.MinimumAccess = minimumAccess
	}
}

func (cfg *ToolsetConfig) enabledTools(registry *toolsets.ToolsetRegistry) []toolsets.ToolDefinition {
	tools := registry.GetEnabledToolsWithExclusions(cfg.EnabledToolsets, cfg.ReadOnly, cfg.DisabledToolsets)
	if cfg.MinimumAccess {
		tools = slices.DeleteFunc(tools, func(tool toolsets.ToolDefinition) bool { return !tool.IsMinimumAccess() })
	}
	return tools
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
// gated on any of its toolsets being enabled. No toolsets means always included.
// writeOnly marks a paragraph that describes a write-only operation, excluded
// whenever read-only mode is active regardless of toolset.
type instructionSection struct {
	toolsets  []string
	tools     []string
	writeOnly bool
	text      string
}

var instructionSections = []instructionSection{
	{text: "This is the Buildkite MCP Server. It provides access to the Buildkite CI/CD API, enabling you to manage and inspect pipelines, builds, jobs, logs, clusters, tests, artifacts, and annotations."},
	{
		toolsets: []string{toolsets.ToolsetUser},
		tools:    []string{"user_token_organization"},
		text:     "Start here: Before using most tools, call user_token_organization to retrieve the organization slug. Nearly every other tool requires the org_slug parameter, and this call is the fastest way to discover it.",
	},
	{
		toolsets: []string{toolsets.ToolsetSkills},
		text:     "Skill discovery: Always call list_skills early in a session — it's cheap (names and one-line descriptions only) and surfaces guidance not visible in any tool's name or schema. When a task matches a listed skill, call load_skill for that guide — it covers parameter tuning, caching behavior, and details beyond the summaries below. Guides cover the full server; only follow steps for tools advertised in this session. Never call tools excluded by minimum-access mode or toolset settings.",
	},
	{text: "Authorization: Tools available depend on the scopes and organization access granted to the configured API token. A 401 response means the token is invalid, expired, or revoked and requires reauthentication. A 403 response means the credentials were accepted but access was denied, commonly because the token lacks a required scope or organization access, or the user lacks permission."},
	{text: "Common pitfalls:\n\nbuild_number is a sequential integer string (e.g. \"42\"), not a UUID. Build, job, artifact, and log tools all require this identifier — do not use the build's UUID id field."},
	{
		toolsets: []string{toolsets.ToolsetInvestigations},
		tools:    []string{"get_build_failure_summary"},
		text:     "Build failure investigation: start with get_build_failure_summary. It combines build state, blocking failed and canceled jobs, promised failures from running jobs, soft-failed jobs (listed after blocking failures and canceled jobs), structured Job Errors, relevant annotations, and failed tests in one response, without fetching logs by default; jobs that never ran (broken, waiting_failed, blocked_failed, unblocked_failed) fill the remaining max_jobs slots unless include_never_ran_jobs is false. Read the structured evidence first. If jobs_truncated is true, omitted jobs may still have captured errors; job_limit reports the applied cap. For complete coverage, increase max_jobs only if the server permits it; otherwise identify omitted jobs with list_jobs. Do not repeatedly request a larger summary beyond the cap. Use structured errors through available tools first, then focused logs when that evidence is unavailable or insufficient. Use tail_logs only for specific jobs needing more evidence, or include_logs=true for bounded tails. Missing errors do not mean success. Use individual build, job, annotation, and test tools only when the summary needs deeper inspection.",
	},
	{
		toolsets: []string{toolsets.ToolsetPipelines},
		text:     "Pipeline authoring: always check pipeline YAML with validate_pipeline before creating or updating a pipeline, or before committing changes to .buildkite/pipeline.yml. It validates against the official pipeline schema locally — the call itself never contacts the Buildkite API and works regardless of the configured token's scopes — and catches structural errors that would otherwise fail silently at upload time. A valid result does not guarantee runtime correctness (environment variable interpolation, plugin configuration, dynamically generated steps).",
	},
	{
		toolsets: []string{toolsets.ToolsetInvestigations},
		tools:    []string{"compare_builds"},
		text:     "Build comparison: use compare_builds when asked what changed since a build worked, or to compare two builds. Without an explicit baseline it selects an earlier successful build on the same pipeline and branch. Report the selected baseline and unmatched steps; shared failing steps do not establish a shared cause or justify a retry. Timings cover final attempts, not total retry cost or build wall-clock duration.",
	},
	{
		toolsets: []string{toolsets.ToolsetBuilds, toolsets.ToolsetRetries},
		tools:    []string{"list_jobs", "get_job"},
		text:     "Job state \"broken\" means the pipeline configuration decided, when the job was created, that it would not run: an `if` condition or `branches` filter did not match, `parallelism` was 0, or the step was marked `skip`. Broken is never caused by another job failing, never fails a build, and is normal in passing builds. Jobs stopped by a failed dependency are `waiting_failed`, `blocked_failed` or `unblocked_failed` instead; those appear only in builds that actually failed and are consequences of an upstream failure, not root causes. None of these four states ran, so they have no logs. `failed` = ran and exited non-zero; `skipped` = an external factor (e.g. a newer build superseded it). To explain a red build, investigate only `failed`, `timed_out`, `expired` jobs that are not `soft_failed`, as well as `running` jobs that have non-zero `promised_exit_status`.",
	},
	{
		toolsets: []string{toolsets.ToolsetBuilds, toolsets.ToolsetInvestigations, toolsets.ToolsetRetries},
		tools:    []string{"list_jobs", "get_build_failure_summary", "compare_builds"},
		text:     "Soft failures: a job with `soft_failed` true actually ran and exited unsuccessfully, but the step's `soft_fail` setting allowed that exit status, so the failure did not block the build. `soft_fail` may allow every non-zero exit or only listed `exit_status` values, so the same step can still fail the build with another status. Identify soft failures by the `soft_failed` field, not by step names (e.g. a security audit) or by a non-zero `exit_status` alone. Describe them as non-blocking, allowed failures, never as passing and never as proof the problem is harmless. When explaining a red build, prioritize the blocking failures; do not read logs, retry, or propose fixes for soft-failed jobs unless the user asks about them or evidence connects them to the reported problem. Non-blocking does not mean irrelevant: a soft-failed step still ran, can still be a dependency of later steps, and still contributes to build duration.",
	},
	{
		toolsets: []string{toolsets.ToolsetBuilds, toolsets.ToolsetRetries},
		tools:    []string{"list_jobs", "get_job"},
		text:     "Job output links: Job summaries expose a step ID as step_id; full job responses expose it as step.id. To link directly to that job's output, use https://buildkite.com/{org_slug}/{pipeline_slug}/builds/{build_number}/list?sid={step_id}&tab=output.",
	},
	{
		toolsets: []string{toolsets.ToolsetBuilds, toolsets.ToolsetRetries},
		tools:    []string{"list_builds"},
		text:     "Build selection after a push: resolve the pushed commit's full SHA and call list_builds with pipeline_slug and commit. Do not assume the newest build on the branch is for that push. Webhook-created builds may not appear immediately; if no exact commit match is returned, retry before concluding no build exists. Once found, use its build number with wait_for_build.",
	},
	{
		toolsets: []string{toolsets.ToolsetBuilds},
		tools:    []string{"list_step_uploads", "get_step_upload"},
		text:     "Dynamic pipeline uploads: steps added at runtime via `buildkite-agent pipeline upload` do not appear in the pipeline's static configuration. To inspect what was dynamically uploaded, call list_step_uploads (returns each upload's state, source_job_id, created_jobs_count, and rejection details), then get_step_upload with an upload_uuid to read its dynamic pipeline definition YAML (definition_yaml field). Large definitions are omitted from get_step_upload. Step-upload data is only available while the build is within its maximum lifetime (~30 days).",
	},
	{
		toolsets: []string{toolsets.ToolsetLogs, toolsets.ToolsetRetries},
		tools:    []string{"tail_logs", "search_logs", "read_logs"},
		text:     "Log investigation order: only fetch logs when the available structured evidence is missing, unavailable, or insufficient. For those jobs, start with tail_logs to see recent output; tails may contain cleanup rather than the cause. If needed, use search_logs with a pattern and limit for targeted investigation, and only use read_logs with seek and limit for deep sequential inspection. Stop when the evidence answers the question; do not fetch logs for every job by default. Avoid calling read_logs without a limit on large logs.",
	},
	{
		toolsets:  []string{toolsets.ToolsetAnnotations},
		writeOnly: true,
		text:      "Annotation scope: when creating an annotation with scope \"job\", job_id is required. If job_id is provided but scope is left as the default \"build\", the job_id is silently ignored.",
	},
}

// BuildkiteServerInstructions builds the instructions sent to MCP clients
// describing how to use this server's tools, including only the paragraphs
// relevant to enabledToolsets, readOnly mode and any additional options.
// Exported so other services embedding or re-implementing Buildkite MCP tools
// (e.g. custom/internal MCP servers) can reuse the same guidance.
func BuildkiteServerInstructions(enabledToolsets []string, readOnly bool, opts ...ToolsetOption) string {
	cfg := &ToolsetConfig{EnabledToolsets: enabledToolsets, ReadOnly: readOnly}
	for _, opt := range opts {
		opt(cfg)
	}
	return buildkiteServerInstructions(cfg)
}

func buildkiteServerInstructions(cfg *ToolsetConfig) string {
	var available map[string]bool
	if len(cfg.DisabledToolsets) > 0 || cfg.MinimumAccess {
		registry := toolsets.NewToolsetRegistry()
		registry.RegisterToolsets(toolsets.CreateBuiltinToolsets())
		available = make(map[string]bool)
		for _, tool := range cfg.enabledTools(registry) {
			available[tool.Tool.Name] = true
		}
	}
	enabledToolsets := withoutToolsets(cfg.EnabledToolsets, cfg.DisabledToolsets)
	parts := make([]string, 0, len(instructionSections))
	for _, s := range instructionSections {
		if s.writeOnly && (cfg.ReadOnly || cfg.MinimumAccess) {
			continue
		}
		if available != nil && len(s.tools) > 0 && !slices.ContainsFunc(s.tools, func(name string) bool { return available[name] }) {
			continue
		}
		if len(s.toolsets) == 0 || slices.ContainsFunc(s.toolsets, func(name string) bool {
			return toolsets.IsToolsetEnabled(enabledToolsets, name)
		}) {
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
		Instructions: buildkiteServerInstructions(cfg),
		// A non-nil empty Capabilities stops the SDK from advertising the
		// deprecated (as of MCP 2026-07-28) logging capability by default.
		// Tool, prompt, and resource capabilities are still inferred as they
		// are registered.
		Capabilities: &mcp.ServerCapabilities{},
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
	if !cfg.MinimumAccess {
		s.AddPrompt(&mcp.Prompt{
			Name:        "user_token_organization_prompt",
			Description: "When asked for detail of a user's pipelines start by looking up the user's token organization",
		}, buildkite.HandleUserTokenOrganizationPrompt)
	}

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

	enabledTools := cfg.enabledTools(registry)

	for _, toolDef := range enabledTools {
		toolDef.Register(s)
	}

	if len(cfg.DisabledToolsets) > 0 {
		// Ignore tools removed by disabling their own toolset; warn only when
		// exclusions also remove tools from other enabled toolsets.
		blocked := make(map[string]bool)
		for _, tool := range registry.GetEnabledTools(cfg.DisabledToolsets, false) {
			blocked[tool.Tool.Name] = true
		}
		var removedTools []string
		for _, tool := range registry.GetEnabledTools(withoutToolsets(cfg.EnabledToolsets, cfg.DisabledToolsets), cfg.ReadOnly) {
			if blocked[tool.Tool.Name] {
				removedTools = append(removedTools, tool.Tool.Name)
			}
		}
		if len(removedTools) > 0 {
			slices.Sort(removedTools)
			log.Warn().
				Strs("enabled_toolsets", cfg.EnabledToolsets).
				Strs("disabled_toolsets", cfg.DisabledToolsets).
				Strs("removed_tools", removedTools).
				Msg("Disabled toolsets removed tools from other enabled toolsets")
		}
	}

	if cfg.MinimumAccess {
		var removedTools []string
		removedScopes := make(map[string][]string)
		for _, tool := range registry.GetEnabledToolsWithExclusions(cfg.EnabledToolsets, cfg.ReadOnly, cfg.DisabledToolsets) {
			if !tool.IsMinimumAccess() {
				removedTools = append(removedTools, tool.Tool.Name)
				removedScopes[tool.Tool.Name] = tool.RequiredScopes
			}
		}
		if len(removedTools) > 0 {
			slices.Sort(removedTools)
			log.Warn().
				Strs("enabled_toolsets", cfg.EnabledToolsets).
				Bool("minimum_access", true).
				Bool("read_only", cfg.ReadOnly).
				Strs("removed_tools", removedTools).
				Interface("removed_tool_scopes", removedScopes).
				Msg("Minimum access removed tools requiring additional scopes or write access; exclusions take precedence")
		}
	}

	scopes := (toolsets.Toolset{Tools: enabledTools}).GetRequiredScopes()

	log.Info().
		Strs("enabled_toolsets", cfg.EnabledToolsets).
		Bool("read_only", cfg.ReadOnly).
		Bool("minimum_access", cfg.MinimumAccess).
		Int("tool_count", len(enabledTools)).
		Strs("required_scopes", scopes).
		Msg("Registered tools from toolsets")
}
