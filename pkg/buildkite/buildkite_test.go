package buildkite

import (
	"encoding/json"
	"strings"
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

func TestLimitSanitizedJSONPayloadPreservesArrayItems(t *testing.T) {
	items := make([]map[string]any, 8)
	for i := range items {
		items[i] = map[string]any{"index": i, "content": strings.Repeat("x", 100)}
	}
	payload, err := json.Marshal(map[string]any{
		"content_bytes":       0,
		"content_limit_bytes": 400,
		"items":               items,
	})
	require.NoError(t, err)

	limited, err := limitSanitizedJSONPayload(payload, 400)
	require.NoError(t, err)
	require.LessOrEqual(t, len(limited), 400)

	var result struct {
		Items            []map[string]any `json:"items"`
		ContentTruncated bool             `json:"content_truncated"`
	}
	require.NoError(t, json.Unmarshal(limited, &result))
	require.Len(t, result.Items, len(items))
	require.True(t, result.ContentTruncated)
}

func TestLimitSanitizedJSONPayloadRejectsOversizedArrayStructure(t *testing.T) {
	items := make([]int, 1_000)
	payload, err := json.Marshal(map[string]any{"items": items})
	require.NoError(t, err)

	_, err = limitSanitizedJSONPayload(payload, 100)
	require.ErrorContains(t, err, "JSON structure exceeds 100 byte limit")
}

func TestLimitSanitizedJSONPayloadUpdatesNestedTruncationMetadata(t *testing.T) {
	content := strings.Repeat("<", 1_000)
	payload, err := json.Marshal(map[string]any{
		"content_bytes":       0,
		"content_limit_bytes": 512,
		"annotations": []any{map[string]any{
			"body_html": content,
		}},
		"jobs": []any{map[string]any{
			"log_tail": []any{map[string]any{"c": content, "rn": 1}},
		}},
		"test_runs": []any{map[string]any{
			"failed_executions": []any{map[string]any{"failure_reason": content}},
		}},
	})
	require.NoError(t, err)

	limited, err := limitSanitizedJSONPayload(payload, 512)
	require.NoError(t, err)
	require.LessOrEqual(t, len(limited), 512)

	var result struct {
		ContentTruncated bool `json:"content_truncated"`
		Annotations      []struct {
			BodyHTML      string `json:"body_html"`
			BodyTruncated bool   `json:"body_truncated"`
		} `json:"annotations"`
		Jobs []struct {
			LogContentTruncated bool `json:"log_content_truncated"`
			LogTail             []struct {
				ContentTruncated bool `json:"content_truncated"`
			} `json:"log_tail"`
		} `json:"jobs"`
		TestRuns []struct {
			ContentTruncated bool `json:"content_truncated"`
			FailedExecutions []struct {
				ContentTruncated bool `json:"content_truncated"`
			} `json:"failed_executions"`
		} `json:"test_runs"`
	}
	require.NoError(t, json.Unmarshal(limited, &result))
	require.True(t, result.ContentTruncated)
	require.True(t, result.Annotations[0].BodyTruncated)
	require.Less(t, len(result.Annotations[0].BodyHTML), len(content))
	require.True(t, result.Jobs[0].LogContentTruncated)
	require.True(t, result.Jobs[0].LogTail[0].ContentTruncated)
	require.True(t, result.TestRuns[0].ContentTruncated)
	require.True(t, result.TestRuns[0].FailedExecutions[0].ContentTruncated)
}
