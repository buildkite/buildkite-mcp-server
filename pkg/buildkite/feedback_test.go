package buildkite

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func TestNewReportIssuePrompt(t *testing.T) {
	require := require.New(t)

	prompt, handler := NewReportIssuePrompt("1.2.3")

	require.Equal("report_issue", prompt.Name)
	require.NotEmpty(prompt.Description)

	result, err := handler(context.Background(), &mcp.GetPromptRequest{
		Params: &mcp.GetPromptParams{Name: "report_issue"},
	})
	require.NoError(err)
	require.Len(result.Messages, 1)

	text, ok := result.Messages[0].Content.(*mcp.TextContent)
	require.True(ok)
	require.Contains(text.Text, "buildkite-mcp-server version: 1.2.3")
	require.Contains(text.Text, "MCP client: unknown")
	require.Contains(text.Text, "[REDACTED]")
	require.Contains(text.Text, "https://github.com/buildkite/buildkite-mcp-server/issues/new")
}
