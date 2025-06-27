package buildkite

import (
	"github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
)

type PaginatedResult[T any] struct {
	Headers map[string]string `json:"headers"`
	Items   []T               `json:"items"`
}

func optionalPaginationParams(r mcp.CallToolRequest) (buildkite.ListOptions, error) {
	page := r.GetInt("page", 1)
	perPage := r.GetInt("perPage", 1)
	return buildkite.ListOptions{
		Page:    page,
		PerPage: perPage,
	}, nil
}

func withPagination() mcp.ToolOption {
	return func(tool *mcp.Tool) {
		mcp.WithNumber("page",
			mcp.Description("Page number for pagination (min 1)"),
			mcp.Min(1),
		)(tool)

		mcp.WithNumber("perPage",
			mcp.Description("Results per page for pagination (min 1, max 100)"),
			mcp.Min(1),
			mcp.Max(100),
		)(tool)
	}
}

// ClientSidePaginationParams represents parameters for client-side pagination
type ClientSidePaginationParams struct {
	Page    int
	PerPage int
}

// ClientSidePaginatedResult represents a paginated result for client-side pagination
type ClientSidePaginatedResult[T any] struct {
	Items      []T    `json:"items"`
	Page       int    `json:"page"`
	PerPage    int    `json:"per_page"`
	Total      int    `json:"total"`
	TotalPages int    `json:"total_pages"`
	HasNext    bool   `json:"has_next"`
	HasPrev    bool   `json:"has_prev"`
}

// withClientSidePagination adds client-side pagination options to a tool
func withClientSidePagination() mcp.ToolOption {
	return func(tool *mcp.Tool) {
		mcp.WithNumber("page",
			mcp.Description("Page number for pagination (min 1)"),
			mcp.Min(1),
		)(tool)

		mcp.WithNumber("perPage",
			mcp.Description("Results per page for pagination (min 1, max 100)"),
			mcp.Min(1),
			mcp.Max(100),
		)(tool)
	}
}

// getClientSidePaginationParams extracts client-side pagination parameters from request
// Always returns pagination params with sensible defaults
func getClientSidePaginationParams(r mcp.CallToolRequest) ClientSidePaginationParams {
	page := r.GetInt("page", 1)
	perPage := r.GetInt("perPage", 25) // Default page size for client-side pagination
	
	return ClientSidePaginationParams{
		Page:    page,
		PerPage: perPage,
	}
}

// applyClientSidePagination applies client-side pagination to a slice of items
func applyClientSidePagination[T any](items []T, params ClientSidePaginationParams) ClientSidePaginatedResult[T] {
	total := len(items)
	totalPages := (total + params.PerPage - 1) / params.PerPage
	if totalPages == 0 {
		totalPages = 1
	}
	
	startIndex := (params.Page - 1) * params.PerPage
	endIndex := startIndex + params.PerPage
	
	var paginatedItems []T
	if startIndex >= total {
		paginatedItems = []T{}
	} else {
		if endIndex > total {
			endIndex = total
		}
		paginatedItems = items[startIndex:endIndex]
	}
	
	return ClientSidePaginatedResult[T]{
		Items:      paginatedItems,
		Page:       params.Page,
		PerPage:    params.PerPage,
		Total:      total,
		TotalPages: totalPages,
		HasNext:    params.Page < totalPages,
		HasPrev:    params.Page > 1,
	}
}
