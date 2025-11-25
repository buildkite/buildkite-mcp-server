package toolsets

import (
	"context"
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

type ToolSearchArgs struct {
	Query string `json:"query"`
}

// ToolSearch returns the Tool Search tool definition and handler
func ToolSearch(registry *ToolsetRegistry) (mcp.Tool, server.ToolHandlerFunc, []string) {
	tool := mcp.NewTool("search_tools",
		mcp.WithDescription("Search for tools by name or description. Use this to discover available tools for your task."),
		mcp.WithString("query",
			mcp.Description("Search query (e.g., 'pipeline', 'artifact', 'log analysis')"),
			mcp.Required(),
		),
	)

	handler := func(ctx context.Context, request mcp.CallToolRequest, args ToolSearchArgs) (*mcp.CallToolResult, error) {
		// Search for matching tools
		results := registry.SearchTools(args.Query, 10) // Limit to 10 results

		type searchResult struct {
			Name        string `json:"name"`
			Description string `json:"description"`
			Toolset     string `json:"toolset"`
			ReadOnly    bool   `json:"read_only"`
		}

		var output []searchResult
		for _, tool := range results {
			// Find toolset name
			toolsetName := "unknown"
			for name, ts := range registry.toolsets {
				for _, t := range ts.Tools {
					if t.Tool.Name == tool.Tool.Name {
						toolsetName = name
						break
					}
				}
				if toolsetName != "unknown" {
					break
				}
			}

			output = append(output, searchResult{
				Name:        tool.Tool.Name,
				Description: tool.Tool.Description,
				Toolset:     toolsetName,
				ReadOnly:    tool.IsReadOnly(),
			})
		}

		data, _ := json.Marshal(output)
		return mcp.NewToolResultText(string(data)), nil
	}

	return tool, mcp.NewTypedToolHandler(handler), []string{} // No required scopes
}
