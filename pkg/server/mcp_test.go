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
		buildNumber      = "build_number is a sequential"
		jobStateBroken   = `Job state "broken"`
		logInvestigation = "Log investigation order:"
		annotationScope  = "Annotation scope:"
		failureSummary   = "Build failure investigation:"
	)

	always := []string{authorization, buildNumber}

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
			want:    append(append([]string{}, always...), startHere, skillDiscovery, failureSummary, jobStateBroken, logInvestigation, annotationScope),
		},
		{
			name:    "builds alone",
			enabled: []string{"builds"},
			want:    append(append([]string{}, always...), jobStateBroken),
			notWant: []string{startHere, skillDiscovery, failureSummary, logInvestigation, annotationScope},
		},
		{
			name:    "skills alone",
			enabled: []string{"skills"},
			want:    append(append([]string{}, always...), skillDiscovery),
			notWant: []string{startHere, failureSummary, jobStateBroken, logInvestigation, annotationScope},
		},
		{
			name:    "user alone",
			enabled: []string{"user"},
			want:    append(append([]string{}, always...), startHere),
			notWant: []string{skillDiscovery, failureSummary, jobStateBroken, logInvestigation, annotationScope},
		},
		{
			name:     "all toolsets, read-only, omits annotation scope",
			enabled:  []string{"all"},
			readOnly: true,
			want:     append(append([]string{}, always...), startHere, skillDiscovery, failureSummary, jobStateBroken, logInvestigation),
			notWant:  []string{annotationScope},
		},
		{
			name:    "investigations alone",
			enabled: []string{"investigations"},
			want:    append(append([]string{}, always...), failureSummary),
			notWant: []string{startHere, skillDiscovery, jobStateBroken, logInvestigation, annotationScope},
		},
		{
			name:     "annotations toolset, read-only, omits annotation scope",
			enabled:  []string{"annotations"},
			readOnly: true,
			want:     append([]string{}, always...),
			notWant:  []string{annotationScope},
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
