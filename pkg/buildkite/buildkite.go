package buildkite

import (
	"bytes"
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
	sanitized, err := marshalSanitizedJSON(result)
	if err != nil {
		return utils.NewToolResultError(err.Error()), nil, nil
	}

	return mcpSanitizedTextResult(span, sanitized)
}

func mcpTextResultWithByteLimit(span trace.Span, result any, limit int) (*mcp.CallToolResult, any, error) {
	sanitized, err := marshalSanitizedJSON(result)
	if err != nil {
		return utils.NewToolResultError(err.Error()), nil, nil
	}

	sanitized, err = limitSanitizedJSONPayload(sanitized, limit)
	if err != nil {
		return utils.NewToolResultError(fmt.Sprintf("failed to limit result: %v", err)), nil, nil
	}

	return mcpSanitizedTextResult(span, sanitized)
}

func marshalSanitizedJSON(result any) ([]byte, error) {
	r, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result: %v", err)
	}

	sanitized, err := sanitize.SanitizeJSONBytes(r)
	if err != nil {
		return nil, fmt.Errorf("failed to sanitize result: %v", err)
	}
	return sanitized, nil
}

func mcpSanitizedTextResult(span trace.Span, sanitized []byte) (*mcp.CallToolResult, any, error) {
	span.SetAttributes(
		attribute.Int("estimated_tokens", tokens.EstimateTokens(string(sanitized))),
	)

	return utils.NewToolResultText(string(sanitized)), nil, nil
}

func limitSanitizedJSONPayload(payload []byte, limit int) ([]byte, error) {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("decode sanitized JSON: %w", err)
	}

	root, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object")
	}
	if _, ok := root["content_limit_bytes"]; ok {
		root["content_limit_bytes"] = limit
	}

	// content_bytes is part of the payload, so stabilize it before checking the
	// serialized size.
	bounded, err := marshalJSONWithContentBytes(root)
	if err != nil {
		return nil, err
	}
	if len(bounded) <= limit {
		return bounded, nil
	}

	root["content_truncated"] = true
	// Array items carry collection-specific semantics and truncation metadata.
	// The generic limiter must preserve them; callers must reduce semantic
	// collections before serialization.
	if candidate, candidateErr := marshalLimitedJSON(root, 0); candidateErr != nil {
		return nil, candidateErr
	} else if len(candidate) > limit {
		return nil, fmt.Errorf("JSON structure exceeds %d byte limit", limit)
	}

	// Search using the actual encoded size so quotes, backslashes, newlines, and
	// other JSON escaping are included in the final budget.
	low, high := 0, maxJSONStringBytes(root)
	var best []byte
	for low <= high {
		mid := low + (high-low)/2
		candidate, candidateErr := marshalLimitedJSON(root, mid)
		if candidateErr != nil {
			return nil, candidateErr
		}
		if len(candidate) <= limit {
			best = candidate
			low = mid + 1
		} else {
			high = mid - 1
		}
	}
	if best == nil {
		return nil, fmt.Errorf("JSON structure exceeds %d byte limit", limit)
	}
	return best, nil
}

func marshalLimitedJSON(value map[string]any, stringLimit int) ([]byte, error) {
	limitedValue, _ := limitJSONValue(value, stringLimit, "")
	limited, ok := limitedValue.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("expected a JSON object")
	}
	return marshalJSONWithContentBytes(limited)
}

func limitJSONValue(value any, stringLimit int, context string) (any, bool) {
	switch value := value.(type) {
	case string:
		limited, truncated := truncateUTF8Bytes(value, stringLimit)
		return limited, truncated
	case []any:
		limited := make([]any, len(value))
		truncated := false
		for i := range limited {
			var itemTruncated bool
			limited[i], itemTruncated = limitJSONValue(value[i], stringLimit, context)
			truncated = truncated || itemTruncated
		}
		return limited, truncated
	case map[string]any:
		limited := make(map[string]any, len(value))
		truncated := false
		bodyTruncated := false
		logContentTruncated := false
		contentTruncated := false
		for key, item := range value {
			var itemTruncated bool
			limited[key], itemTruncated = limitJSONValue(item, stringLimit, key)
			truncated = truncated || itemTruncated
			if !itemTruncated {
				continue
			}
			switch {
			case context == "annotations" && key == "body_html":
				bodyTruncated = true
			case context == "jobs" && (key == "log_tail" || key == "log_error"):
				logContentTruncated = true
			case context == "test_runs" && (key == "failed_executions" || key == "error"):
				contentTruncated = true
			}
		}
		if bodyTruncated {
			limited["body_truncated"] = true
		}
		if logContentTruncated {
			limited["log_content_truncated"] = true
		}
		if truncated && (context == "failed_executions" || context == "log_tail") {
			contentTruncated = true
		}
		if contentTruncated {
			limited["content_truncated"] = true
		}
		return limited, truncated
	default:
		return value, false
	}
}

func maxJSONStringBytes(value any) int {
	switch value := value.(type) {
	case string:
		return len(value)
	case []any:
		maximum := 0
		for _, item := range value {
			maximum = max(maximum, maxJSONStringBytes(item))
		}
		return maximum
	case map[string]any:
		maximum := 0
		for _, item := range value {
			maximum = max(maximum, maxJSONStringBytes(item))
		}
		return maximum
	default:
		return 0
	}
}

func marshalJSONWithContentBytes(value map[string]any) ([]byte, error) {
	if _, ok := value["content_bytes"]; !ok {
		return json.Marshal(value)
	}

	value["content_bytes"] = 0
	for {
		payload, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("marshal limited JSON: %w", err)
		}
		if value["content_bytes"] == len(payload) {
			return payload, nil
		}
		value["content_bytes"] = len(payload)
	}
}
