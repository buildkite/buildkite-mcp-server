package server

import (
	"context"
	"errors"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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
			instructions := buildkiteServerInstructions([]string{"retries"}, tt.readOnly, tt.disabled)
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
	instructions := buildkiteServerInstructions([]string{"all"}, false, []string{"all"})
	for _, paragraph := range []string{"Start here:", "Skill discovery:", "Pipeline authoring:", "Build failure investigation:", "Log investigation order:"} {
		require.NotContains(t, instructions, paragraph)
	}
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
