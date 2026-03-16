package utils

import (
	"github.com/buildkite/buildkite-mcp-server/pkg/sanitize"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func NewToolResultText(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: message},
		},
	}
}

func NewToolResultError(message string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			&mcp.TextContent{Text: sanitize.SanitizePlainText(message)},
		},
		IsError: true,
	}
}
