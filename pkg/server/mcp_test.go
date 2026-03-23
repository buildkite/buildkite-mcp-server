package server

import (
	"context"
	"errors"
	"testing"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

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
