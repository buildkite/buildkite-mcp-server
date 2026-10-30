package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
	"github.com/stretchr/testify/require"
)

func TestBuildkiteServerInstructions(t *testing.T) {
	const (
		startHere        = "Start here:"
		skillDiscovery   = "Skill discovery:"
		authorization    = "Authorization:"
		invalidToken     = "A 401 response means the token is invalid, expired, or revoked" //nolint:gosec // documentation assertion, not a credential
		forbiddenAccess  = "A 403 response means the credentials were accepted but access was denied"
		buildNumber      = "build_number is a sequential"
		jobStateBroken   = `Job state "broken"`
		jobOutputLinks   = "Job output links:"
		buildSelection   = "Build selection after a push:"
		dynamicUploads   = "Dynamic pipeline uploads:"
		logInvestigation = "Log investigation order:"
		annotationScope  = "Annotation scope:"
		failureSummary   = "Build failure investigation:"
		softFailures     = "Soft failures:"
	)

	always := []string{authorization, invalidToken, forbiddenAccess, buildNumber}

	tests := []struct {
		name     string
		enabled  []string
		readOnly bool
		want     []string
		notWant  []string
	}{
		{
			name:    "all toolsets includes every section",
			enabled: []string{"all"},
			want:    append(append([]string{}, always...), startHere, skillDiscovery, failureSummary, softFailures, jobStateBroken, jobOutputLinks, buildSelection, dynamicUploads, logInvestigation, annotationScope),
		},
		{
			name:    "builds alone",
			enabled: []string{"builds"},
			want:    append(append([]string{}, always...), softFailures, jobStateBroken, jobOutputLinks, buildSelection, dynamicUploads),
			notWant: []string{startHere, skillDiscovery, failureSummary, logInvestigation, annotationScope},
		},
		{
			name:    "retries alone",
			enabled: []string{"retries"},
			want:    append(append([]string{}, always...), softFailures, jobStateBroken, jobOutputLinks, buildSelection, logInvestigation),
			notWant: []string{startHere, skillDiscovery, failureSummary, dynamicUploads, annotationScope},
		},
		{
			name:    "skills alone",
			enabled: []string{"skills"},
			want:    append(append([]string{}, always...), skillDiscovery),
			notWant: []string{startHere, failureSummary, softFailures, jobStateBroken, jobOutputLinks, buildSelection, dynamicUploads, logInvestigation, annotationScope},
		},
		{
			name:    "user alone",
			enabled: []string{"user"},
			want:    append(append([]string{}, always...), startHere),
			notWant: []string{skillDiscovery, failureSummary, softFailures, jobStateBroken, jobOutputLinks, buildSelection, dynamicUploads, logInvestigation, annotationScope},
		},
		{
			name:     "all toolsets, read-only, omits annotation scope",
			enabled:  []string{"all"},
			readOnly: true,
			want:     append(append([]string{}, always...), startHere, skillDiscovery, failureSummary, softFailures, jobStateBroken, jobOutputLinks, buildSelection, dynamicUploads, logInvestigation),
			notWant:  []string{annotationScope},
		},
		{
			name:    "investigations alone",
			enabled: []string{"investigations"},
			want:    append(append([]string{}, always...), failureSummary, softFailures),
			notWant: []string{startHere, skillDiscovery, jobStateBroken, jobOutputLinks, buildSelection, dynamicUploads, logInvestigation, annotationScope},
		},
		{
			name:     "annotations toolset, read-only, omits annotation scope",
			enabled:  []string{"annotations"},
			readOnly: true,
			want:     append([]string{}, always...),
			notWant:  []string{softFailures, jobOutputLinks, buildSelection, dynamicUploads, annotationScope},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			got := BuildkiteServerInstructions(tt.enabled, tt.readOnly)

			for _, w := range tt.want {
				assert.Contains(got, w)
			}
			for _, nw := range tt.notWant {
				assert.NotContains(got, nw)
			}
		})
	}
}

func TestRetryToolsetRegistration(t *testing.T) {
	inspection := []string{"list_builds", "get_build", "wait_for_build", "list_jobs", "get_job"}
	logs := []string{"search_logs", "tail_logs", "read_logs"}
	readTools := append(append([]string{}, inspection...), logs...)
	for _, tt := range []struct {
		name     string
		readOnly bool
		disabled []string
		expected []string
	}{
		{name: "retries", expected: append(append([]string{}, readTools...), "retry_job", "retry_failed_jobs")},
		{name: "read-only retries", readOnly: true, expected: readTools},
		{name: "logs disabled", disabled: []string{"logs"}, expected: append(append([]string{}, inspection...), "retry_job", "retry_failed_jobs")},
		{name: "read-only with logs disabled", readOnly: true, disabled: []string{"logs"}, expected: inspection},
		{name: "builds disabled", disabled: []string{"builds"}, expected: logs},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := NewMCPServer("test", buildkite.ToolDependencies{}, WithToolsets("retries"), WithReadOnly(tt.readOnly), WithDisabledToolsets(tt.disabled...))
			require.ElementsMatch(t, tt.expected, listToolNames(t, server))
			instructions := BuildkiteServerInstructions([]string{"retries"}, tt.readOnly, WithDisabledToolsets(tt.disabled...))
			if len(tt.disabled) > 0 && tt.disabled[0] == "logs" {
				require.NotContains(t, instructions, "Log investigation order:")
				require.Contains(t, instructions, "Build selection after a push:")
			}
			if len(tt.disabled) > 0 && tt.disabled[0] == "builds" {
				require.Contains(t, instructions, "Log investigation order:")
				require.NotContains(t, instructions, "Build selection after a push:")
				require.NotContains(t, instructions, "Job output links:")
			}
		})
	}
}

func TestDisabledAllToolsets(t *testing.T) {
	server := NewMCPServer("test", buildkite.ToolDependencies{}, WithDisabledToolsets("all"))
	require.Empty(t, listToolNames(t, server))
	instructions := BuildkiteServerInstructions([]string{"all"}, false, WithDisabledToolsets("all"))
	for _, paragraph := range []string{"Start here:", "Skill discovery:", "Pipeline authoring:", "Build failure investigation:", "Log investigation order:"} {
		require.NotContains(t, instructions, paragraph)
	}
}

func TestToolsetOverlapWarning(t *testing.T) {
	for _, tt := range []struct {
		name     string
		enabled  []string
		disabled []string
		readOnly bool
		minimum  bool
		header   string
		removed  []string
	}{
		{name: "shared tools excluded", enabled: []string{"retries"}, disabled: []string{"logs"}, removed: []string{"read_logs", "search_logs", "tail_logs"}},
		{name: "all toolsets", enabled: []string{"all"}, disabled: []string{"logs"}, removed: []string{"read_logs", "search_logs", "tail_logs"}},
		{name: "read-only", enabled: []string{"logs", "builds"}, disabled: []string{"retries"}, readOnly: true, removed: []string{"get_build", "get_job", "list_builds", "list_jobs", "read_logs", "search_logs", "tail_logs", "wait_for_build"}},
		{name: "duplicate selections", enabled: []string{"retries", "retries", "logs"}, disabled: []string{"logs", "logs"}, removed: []string{"read_logs", "search_logs", "tail_logs"}},
		{name: "HTTP override", enabled: []string{"user"}, disabled: []string{"logs"}, header: "retries,logs", removed: []string{"read_logs", "search_logs", "tail_logs"}},
		{name: "minimum access and disabled logs", enabled: []string{"retries"}, disabled: []string{"logs"}, readOnly: true, minimum: true, removed: []string{"read_logs", "search_logs", "tail_logs"}},
		{name: "harmless overlap", enabled: []string{"retries", "logs"}},
		{name: "unrelated exclusion", enabled: []string{"retries"}, disabled: []string{"user"}},
		{name: "own toolset disabled", enabled: []string{"logs", "user"}, disabled: []string{"logs"}},
		{name: "all disabled", enabled: []string{"all"}, disabled: []string{"all"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			previousLogger := log.Logger
			log.Logger = zerolog.New(&output).Level(zerolog.WarnLevel)
			t.Cleanup(func() { log.Logger = previousLogger })

			if tt.header != "" {
				factory := NewPerRequestServerFactory("test", buildkite.ToolDependencies{}, tt.enabled, tt.readOnly, WithDisabledToolsets(tt.disabled...), WithMinimumAccess(tt.minimum))
				req := httptest.NewRequest("POST", "/mcp", nil)
				req.Header.Set(HeaderToolsets, tt.header)
				factory(req)
			} else {
				srv := NewMCPServer("test", buildkite.ToolDependencies{}, WithToolsets(tt.enabled...), WithDisabledToolsets(tt.disabled...), WithReadOnly(tt.readOnly), WithMinimumAccess(tt.minimum))
				if tt.minimum {
					require.ElementsMatch(t, []string{"list_builds", "get_build", "wait_for_build", "list_jobs", "get_job"}, listToolNames(t, srv))
				}
			}

			if len(tt.removed) == 0 {
				require.Empty(t, output.String(), "no warning unless another enabled toolset loses tools")
				return
			}
			var warning struct {
				Level    string   `json:"level"`
				Message  string   `json:"message"`
				Disabled []string `json:"disabled_toolsets"`
				Removed  []string `json:"removed_tools"`
			}
			require.NoError(t, json.Unmarshal(output.Bytes(), &warning))
			require.Equal(t, "warn", warning.Level)
			require.Contains(t, warning.Message, "other enabled toolsets")
			require.Equal(t, tt.disabled, warning.Disabled)
			require.Equal(t, tt.removed, warning.Removed)
		})
	}
}

func TestMinimumAccessWarning(t *testing.T) {
	for _, tt := range []struct {
		name     string
		enabled  string
		readOnly bool
		removed  []string
	}{
		{name: "extra read and write scopes", enabled: "retries", removed: []string{"read_logs", "retry_failed_jobs", "retry_job", "search_logs", "tail_logs"}},
		{name: "also read-only", enabled: "retries", readOnly: true, removed: []string{"read_logs", "search_logs", "tail_logs"}},
		{name: "no conflicting permissions", enabled: "pipelines", readOnly: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var output bytes.Buffer
			previousLogger := log.Logger
			log.Logger = zerolog.New(&output).Level(zerolog.WarnLevel)
			t.Cleanup(func() { log.Logger = previousLogger })
			srv := NewMCPServer("test", buildkite.ToolDependencies{}, WithToolsets(tt.enabled), WithMinimumAccess(true), WithReadOnly(tt.readOnly))
			names := listToolNames(t, srv)
			for _, name := range tt.removed {
				require.NotContains(t, names, name)
			}
			if len(tt.removed) == 0 {
				require.Empty(t, output.String())
				return
			}
			var warning struct {
				Level   string              `json:"level"`
				Message string              `json:"message"`
				Enabled []string            `json:"enabled_toolsets"`
				Minimum bool                `json:"minimum_access"`
				Removed []string            `json:"removed_tools"`
				Scopes  map[string][]string `json:"removed_tool_scopes"`
			}
			require.NoError(t, json.Unmarshal(output.Bytes(), &warning))
			require.Equal(t, "warn", warning.Level)
			require.Contains(t, warning.Message, "exclusions take precedence")
			require.True(t, warning.Minimum)
			require.Equal(t, []string{tt.enabled}, warning.Enabled)
			require.Equal(t, tt.removed, warning.Removed)
			require.Equal(t, []string{"read_build_logs"}, warning.Scopes["read_logs"])
			if !tt.readOnly {
				require.Equal(t, []string{"retry_builds"}, warning.Scopes["retry_job"])
			}
		})
	}
}

func TestMinimumAccessRegistration(t *testing.T) {
	inspection := []string{"list_builds", "get_build", "wait_for_build", "list_jobs", "get_job"}
	all := append(append([]string{}, inspection...),
		"get_build_test_engine_runs", "list_step_uploads", "get_step_upload",
		"get_pipeline", "list_pipelines", "validate_pipeline", "list_pipeline_schedules", "get_pipeline_schedule", "get_pipeline_github_webhook_processing",
		"list_annotations", "current_user", "access_token", "list_skills", "load_skill")
	for _, tt := range []struct {
		name     string
		enabled  []string
		disabled []string
		readOnly bool
		expected []string
	}{
		{name: "all", enabled: []string{"all"}, expected: all},
		{name: "also read-only", enabled: []string{"all"}, readOnly: true, expected: all},
		{name: "overlapping toolsets", enabled: []string{"retries", "logs"}, expected: inspection},
		{name: "additional scopes only", enabled: []string{"clusters", "investigations"}},
		{name: "exclusions still apply", enabled: []string{"retries"}, disabled: []string{"builds"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			srv := NewMCPServer("test", buildkite.ToolDependencies{}, WithToolsets(tt.enabled...),
				WithMinimumAccess(true), WithReadOnly(tt.readOnly), WithDisabledToolsets(tt.disabled...))
			require.ElementsMatch(t, tt.expected, listToolNames(t, srv))
		})
	}
}

func TestMinimumAccessInstructionsAndPrompts(t *testing.T) {
	srv := NewMCPServer("test", buildkite.ToolDependencies{}, WithMinimumAccess(true))
	ctx := context.Background()
	serverTransport, clientTransport := mcp.NewInMemoryTransports()
	serverSession, err := srv.Connect(ctx, serverTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = serverSession.Close() })
	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = session.Close() })

	instructions := session.InitializeResult().Instructions
	for _, text := range []string{"user_token_organization", "get_build_failure_summary", "compare_builds", "search_logs", "Log investigation order:", "Annotation scope:"} {
		require.NotContains(t, instructions, text)
	}
	for _, text := range []string{"Skill discovery:", "Pipeline authoring:", "Build selection after a push:"} {
		require.Contains(t, instructions, text)
	}
	require.Contains(t, instructions, "only follow steps for tools advertised in this session")
	require.Equal(t, BuildkiteServerInstructions([]string{"all"}, false, WithMinimumAccess(true)), instructions)
	prompts, err := session.ListPrompts(ctx, nil)
	require.NoError(t, err)
	for _, prompt := range prompts.Prompts {
		require.NotEqual(t, "user_token_organization_prompt", prompt.Name)
	}

	guide, err := session.CallTool(ctx, &mcp.CallToolParams{
		Name: "load_skill",
		Arguments: map[string]any{
			"skill_name": "debug-logs-guide",
			"telemetry":  map[string]any{"context": "Loading the build debugging guide to check which investigation steps are supported by this minimum-access server session."},
		},
	})
	require.NoError(t, err)
	require.False(t, guide.IsError)
	require.Len(t, guide.Content, 1)
	text, ok := guide.Content[0].(*mcp.TextContent)
	require.True(t, ok)
	require.Contains(t, text.Text, "never call an unavailable tool")
	require.Contains(t, text.Text, "In minimum-access mode,")
	require.Contains(t, text.Text, "are unavailable. Skip the log workflow")
}

func TestUnauthorizedMiddleware_CallsCallbackOnUnauthorized(t *testing.T) {
	called := false
	middleware := unauthorizedMiddleware(func() { called = true })

	handler := middleware(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, buildkite.ErrUnauthorized
	})

	_, err := handler(context.Background(), "tools/call", nil)

	require.ErrorIs(t, err, buildkite.ErrUnauthorized)
	require.True(t, called, "OnUnauthorized callback must be invoked")
}

func TestUnauthorizedMiddleware_DoesNotCallCallbackOnOtherError(t *testing.T) {
	called := false
	middleware := unauthorizedMiddleware(func() { called = true })

	handler := middleware(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, errors.New("some other error")
	})

	_, err := handler(context.Background(), "tools/call", nil)

	require.Error(t, err)
	require.False(t, called, "OnUnauthorized callback must not fire for non-401 errors")
}

func TestUnauthorizedMiddleware_NilCallbackDoesNotPanic(t *testing.T) {
	middleware := unauthorizedMiddleware(nil)

	handler := middleware(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, buildkite.ErrUnauthorized
	})

	require.NotPanics(t, func() {
		_, _ = handler(context.Background(), "tools/call", nil)
	})
}

func TestUnauthorizedMiddleware_PassesThroughOnSuccess(t *testing.T) {
	called := false
	middleware := unauthorizedMiddleware(func() { called = true })

	handler := middleware(func(_ context.Context, _ string, _ mcp.Request) (mcp.Result, error) {
		return nil, nil
	})

	result, err := handler(context.Background(), "tools/call", nil)

	require.NoError(t, err)
	require.Nil(t, result)
	require.False(t, called)
}
