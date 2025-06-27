package buildkite

import (
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func Test_optionalPaginationParams(t *testing.T) {
	tests := []struct {
		name      string
		args      map[string]any
		expected  buildkite.ListOptions
		expectErr bool
	}{
		{
			name: "valid pagination parameters",
			args: map[string]any{
				"page":    float64(1),
				"perPage": float64(25),
			},
			expected: buildkite.ListOptions{
				Page:    1,
				PerPage: 25,
			},
			expectErr: false,
		},
		{
			name: "missing pagination parameters should use new defaults (1 per page)",
			args: map[string]any{
				"name": "test-name",
			},
			expected: buildkite.ListOptions{
				Page:    1,
				PerPage: 1,
			},
			expectErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			req := createMCPRequest(t, tt.args)

			opts, err := optionalPaginationParams(req)
			if tt.expectErr {
				assert.Error(err)
			} else {
				assert.NoError(err)
				assert.Equal(tt.expected, opts)
			}
		})
	}
}

func Test_getClientSidePaginationParams(t *testing.T) {
	tests := []struct {
		name           string
		args           map[string]any
		expectedParams ClientSidePaginationParams
	}{
		{
			name: "valid pagination parameters",
			args: map[string]any{
				"page":    float64(2),
				"perPage": float64(10),
			},
			expectedParams: ClientSidePaginationParams{
				Page:    2,
				PerPage: 10,
			},
		},
		{
			name: "only page parameter",
			args: map[string]any{
				"page": float64(3),
			},
			expectedParams: ClientSidePaginationParams{
				Page:    3,
				PerPage: 25, // default
			},
		},
		{
			name: "only perPage parameter",
			args: map[string]any{
				"perPage": float64(50),
			},
			expectedParams: ClientSidePaginationParams{
				Page:    1, // default
				PerPage: 50,
			},
		},
		{
			name: "no pagination parameters",
			args: map[string]any{
				"name": "test-name",
			},
			expectedParams: ClientSidePaginationParams{
				Page:    1,  // default
				PerPage: 25, // default
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			req := createMCPRequest(t, tt.args)

			params := getClientSidePaginationParams(req)
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

func createMCPRequest(t *testing.T, args map[string]any) mcp.CallToolRequest {
	t.Helper()
	return mcp.CallToolRequest{
		Params: struct {
			Name      string    `json:"name"`
			Arguments any       `json:"arguments,omitempty"`
			Meta      *mcp.Meta `json:"_meta,omitempty"`
		}{
			Arguments: args,
		},
	}
}

func getTextResult(t *testing.T, result *mcp.CallToolResult) mcp.TextContent {
	t.Helper()
	textContent, ok := result.Content[0].(mcp.TextContent)
	if !ok {
		t.Error("expected text content")
		return mcp.TextContent{}
	}

	return textContent
}
