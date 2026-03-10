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

func Test_getClientSidePaginationParams(t *testing.T) {
	tests := []struct {
		name           string
		page           int
		perPage        int
		expectedParams ClientSidePaginationParams
	}{
		{
			name:    "valid pagination parameters",
			page:    2,
			perPage: 10,
			expectedParams: ClientSidePaginationParams{
				Page:    2,
				PerPage: 10,
			},
		},
		{
			name:    "only page parameter",
			page:    3,
			perPage: 0,
			expectedParams: ClientSidePaginationParams{
				Page:    3,
				PerPage: 25, // default
			},
		},
		{
			name:    "only perPage parameter",
			page:    0,
			perPage: 50,
			expectedParams: ClientSidePaginationParams{
				Page:    1, // default
				PerPage: 50,
			},
		},
		{
			name:    "no pagination parameters",
			page:    0,
			perPage: 0,
			expectedParams: ClientSidePaginationParams{
				Page:    1,  // default
				PerPage: 25, // default
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			params := clientSidePaginationFromArgs(tt.page, tt.perPage)
			assert.Equal(tt.expectedParams, params)
		})
	}
}

func Test_applyClientSidePagination(t *testing.T) {
	tests := []struct {
		name           string
		items          []string
		params         ClientSidePaginationParams
		expectedResult ClientSidePaginatedResult[string]
	}{
		{
			name:  "first page with items",
			items: []string{"item1", "item2", "item3", "item4", "item5"},
			params: ClientSidePaginationParams{
				Page:    1,
				PerPage: 2,
			},
			expectedResult: ClientSidePaginatedResult[string]{
				Items:      []string{"item1", "item2"},
				Page:       1,
				PerPage:    2,
				Total:      5,
				TotalPages: 3,
				HasNext:    true,
				HasPrev:    false,
			},
		},
		{
			name:  "middle page",
			items: []string{"item1", "item2", "item3", "item4", "item5"},
			params: ClientSidePaginationParams{
				Page:    2,
				PerPage: 2,
			},
			expectedResult: ClientSidePaginatedResult[string]{
				Items:      []string{"item3", "item4"},
				Page:       2,
				PerPage:    2,
				Total:      5,
				TotalPages: 3,
				HasNext:    true,
				HasPrev:    true,
			},
		},
		{
			name:  "last page",
			items: []string{"item1", "item2", "item3", "item4", "item5"},
			params: ClientSidePaginationParams{
				Page:    3,
				PerPage: 2,
			},
			expectedResult: ClientSidePaginatedResult[string]{
				Items:      []string{"item5"},
				Page:       3,
				PerPage:    2,
				Total:      5,
				TotalPages: 3,
				HasNext:    false,
				HasPrev:    true,
			},
		},
		{
			name:  "page beyond available data",
			items: []string{"item1", "item2"},
			params: ClientSidePaginationParams{
				Page:    5,
				PerPage: 2,
			},
			expectedResult: ClientSidePaginatedResult[string]{
				Items:      []string{},
				Page:       5,
				PerPage:    2,
				Total:      2,
				TotalPages: 1,
				HasNext:    false,
				HasPrev:    true,
			},
		},
		{
			name:  "empty items",
			items: []string{},
			params: ClientSidePaginationParams{
				Page:    1,
				PerPage: 10,
			},
			expectedResult: ClientSidePaginatedResult[string]{
				Items:      []string{},
				Page:       1,
				PerPage:    10,
				Total:      0,
				TotalPages: 1,
				HasNext:    false,
				HasPrev:    false,
			},
		},
		{
			name:  "page size larger than total items",
			items: []string{"item1", "item2"},
			params: ClientSidePaginationParams{
				Page:    1,
				PerPage: 10,
			},
			expectedResult: ClientSidePaginatedResult[string]{
				Items:      []string{"item1", "item2"},
				Page:       1,
				PerPage:    10,
				Total:      2,
				TotalPages: 1,
				HasNext:    false,
				HasPrev:    false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			result := applyClientSidePagination(tt.items, tt.params)
			assert.Equal(tt.expectedResult, result)
		})
	}
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
