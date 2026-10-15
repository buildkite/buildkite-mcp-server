package trace

import (
	"context"
	"strconv"
	"sync/atomic"
)

// ToolCall identifies the MCP tool call a context belongs to, so work done
// on its behalf (e.g. outbound Buildkite API requests) can be attributed to it.
type ToolCall struct {
	// Name is the MCP tool name, e.g. "get_build".
	Name string
	// ID is unique per tool call within this server process.
	ID string
}

type toolCallKey struct{}

var toolCallCounter atomic.Uint64

// WithToolCall returns a context carrying tc.
func WithToolCall(ctx context.Context, tc ToolCall) context.Context {
	return context.WithValue(ctx, toolCallKey{}, tc)
}

// ToolCallFromContext returns the tool call ctx belongs to, if any.
func ToolCallFromContext(ctx context.Context) (ToolCall, bool) {
	tc, ok := ctx.Value(toolCallKey{}).(ToolCall)
	return tc, ok
}

// newToolCall returns a ToolCall for name with the next process-unique ID.
func newToolCall(name string) ToolCall {
	return ToolCall{Name: name, ID: strconv.FormatUint(toolCallCounter.Add(1), 10)}
}
