package buildkite

import (
	"encoding/json"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/pkg/sanitize"
	"github.com/buildkite/buildkite-mcp-server/pkg/tokens"
	"github.com/buildkite/buildkite-mcp-server/pkg/utils"
	"github.com/buildkite/go-buildkite/v5"
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

func boolPtr(b bool) *bool {
	return &b
}

func mcpTextResult(span trace.Span, result any) (*mcp.CallToolResult, any, error) {
	r, err := json.Marshal(result)
	if err != nil {
		return utils.NewToolResultError(fmt.Sprintf("failed to marshal result: %v", err)), nil, nil
	}

	sanitized, err := sanitize.SanitizeJSONBytes(r)
	if err != nil {
		return utils.NewToolResultError(fmt.Sprintf("failed to sanitize result: %v", err)), nil, nil
	}

	span.SetAttributes(
		attribute.Int("estimated_tokens", tokens.EstimateTokens(string(sanitized))),
	)

	return utils.NewToolResultText(string(sanitized)), nil, nil
}
