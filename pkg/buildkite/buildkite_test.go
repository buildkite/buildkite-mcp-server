package buildkite

import (
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/require"
)

func Test_paginationFromArgs(t *testing.T) {
	tests := []struct {
		name      string
		page      int
		perPage   int
		expected  buildkiteListOptions
		expectErr bool
	}{
		{
			name:    "valid pagination parameters",
			page:    1,
			perPage: 25,
			expected: buildkiteListOptions{
				Page:    1,
				PerPage: 25,
			},
		},
		{
			name:    "missing pagination parameters should use new defaults (100 per page)",
			page:    0,
			perPage: 0,
			expected: buildkiteListOptions{
				Page:    1,
				PerPage: 100,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			opts := paginationFromArgs(tt.page, tt.perPage)
			assert.Equal(tt.expected.Page, opts.Page)
			assert.Equal(tt.expected.PerPage, opts.PerPage)
		})
	}
}

// buildkiteListOptions is a helper for test expectations
type buildkiteListOptions struct {
	Page    int
	PerPage int
}

func createMCPRequest(t *testing.T, args map[string]any) *mcp.CallToolRequest {
	t.Helper()
	argsJSON, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("failed to marshal args: %v", err)
	}
	return &mcp.CallToolRequest{
		Params: &mcp.CallToolParamsRaw{
			Arguments: argsJSON,
		},
	}
}

func getTextResult(t *testing.T, result *mcp.CallToolResult) *mcp.TextContent {
	t.Helper()
	textContent, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Error("expected text content")
		return &mcp.TextContent{}
	}

	return textContent
}

func testPtr[T any](value T) *T {
	return &value
}
