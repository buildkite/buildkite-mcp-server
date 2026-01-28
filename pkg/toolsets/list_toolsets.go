package toolsets

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ListToolsets returns the list_toolsets tool definition and handler
func ListToolsets(registry *ToolsetRegistry) (mcp.Tool, server.ToolHandlerFunc, []string) {
	tool := mcp.NewTool("list_toolsets",
		mcp.WithDescription("List all available toolsets and their descriptions. Use this to browse tool categories before searching."),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		metadata := registry.GetMetadata()

		data, err := json.Marshal(metadata)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal toolset metadata: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}

	return tool, handler, []string{} // No required scopes
}
