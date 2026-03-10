package buildkite

import (
	"encoding/json"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type PaginatedResult[T any] struct {
	Headers map[string]string `json:"headers"`
	Items   []T               `json:"items"`
}

// PaginationParams is embedded in tool args structs to provide pagination fields.
type PaginationParams struct {
	Page    int `json:"page"`
	PerPage int `json:"per_page"`
}

func paginationFromArgs(page, perPage int) buildkite.ListOptions {
	if page == 0 {
		page = 1
	}
	if perPage == 0 {
		perPage = 100
	}
	return buildkite.ListOptions{
		Page:    page,
		PerPage: perPage,
	}
}

// ClientSidePaginationParams represents parameters for client-side pagination
type ClientSidePaginationParams struct {
	Page    int
	PerPage int
}

// ClientSidePaginatedResult represents a paginated result for client-side pagination
type ClientSidePaginatedResult[T any] struct {
	Items      []T  `json:"items"`
	Page       int  `json:"page"`
	PerPage    int  `json:"per_page"`
	Total      int  `json:"total"`
	TotalPages int  `json:"total_pages"`
	HasNext    bool `json:"has_next"`
	HasPrev    bool `json:"has_prev"`
}

func clientSidePaginationFromArgs(page, perPage int) ClientSidePaginationParams {
	if page == 0 {
		page = 1
	}
	if perPage == 0 {
		perPage = 25
	}
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

func mcpTextResult(span trace.Span, result any) (*mcp.CallToolResult, any, error) {
	r, err := json.Marshal(result)
	if err != nil {
		return utils.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil, nil
	}

	span.SetAttributes(
		attribute.Int("estimated_tokens", tokens.EstimateTokens(string(r))),
	)

	return utils.NewToolResultText(string(r)), nil, nil
}
