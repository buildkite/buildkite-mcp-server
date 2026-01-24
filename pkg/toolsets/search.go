package toolsets

import (
	"context"
	"encoding/json"
	"fmt"

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
		// Search for matching tools with metadata
		results := registry.SearchToolsWithMetadata(args.Query, 10) // Limit to 10 results

		if len(results) == 0 {
			return mcp.NewToolResultText(`{"results":[],"message":"No tools found. Try: 'build', 'pipeline', 'artifact', 'log', 'test', 'cluster'"}`), nil
		}

		type searchResult struct {
			Name           string   `json:"name"`
			Description    string   `json:"description"`
			Toolset        string   `json:"toolset"`
			ReadOnly       bool     `json:"read_only"`
			MatchedIn      string   `json:"matched_in"`
			RequiredScopes []string `json:"required_scopes"`
		}

		var output []searchResult
		for _, result := range results {
			scopes := result.RequiredScopes
			if scopes == nil {
				scopes = []string{}
			}
			output = append(output, searchResult{
				Name:           result.Tool.Name,
				Description:    result.Tool.Description,
				Toolset:        result.ToolsetName,
				ReadOnly:       result.ReadOnly,
				MatchedIn:      result.MatchedIn,
				RequiredScopes: scopes,
			})
		}

		data, err := json.Marshal(output)
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to marshal search results: %v", err)), nil
		}
		return mcp.NewToolResultText(string(data)), nil
	}

	return tool, mcp.NewTypedToolHandler(handler), []string{} // No required scopes
}
