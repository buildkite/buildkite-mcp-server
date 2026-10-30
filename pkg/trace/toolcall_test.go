package trace

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolCallFromContextEmpty(t *testing.T) {
	_, ok := ToolCallFromContext(context.Background())
	require.False(t, ok)
}

func TestNewMiddlewareAddsToolCallToContext(t *testing.T) {
	assert := require.New(t)
	ctx := context.Background()

	var seen []ToolCall
	server := mcp.NewServer(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	server.AddReceivingMiddleware(NewMiddleware())
	mcp.AddTool(server, &mcp.Tool{Name: "ping"}, func(ctx context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		tc, ok := ToolCallFromContext(ctx)
		assert.True(ok)
		seen = append(seen, tc)
		return &mcp.CallToolResult{}, nil, nil
	})

	t1, t2 := mcp.NewInMemoryTransports()
	_, err := server.Connect(ctx, t1, nil)
	assert.NoError(err)
	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
	session, err := client.Connect(ctx, t2, nil)
	assert.NoError(err)
	defer session.Close()

	for range 2 {
		_, err = session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
		assert.NoError(err)
	}

	assert.Len(seen, 2)
	assert.Equal("ping", seen[0].Name)
	assert.Equal("ping", seen[1].Name)
	assert.NotEmpty(seen[0].ID)
	assert.NotEqual(seen[0].ID, seen[1].ID, "each tool call gets its own ID")
}
