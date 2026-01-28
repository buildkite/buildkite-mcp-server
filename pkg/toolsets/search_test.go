package toolsets

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestToolSearch(t *testing.T) {
	registry := createTestRegistry()

	tool, handler, scopes := ToolSearch(registry)

	t.Run("tool definition", func(t *testing.T) {
		assert := require.New(t)
		assert.Equal("search_tools", tool.Name)
		assert.Contains(tool.Description, "Search for tools")
		assert.Empty(scopes)
	})

	t.Run("search by name", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"query": "list"}

		result, err := handler(context.Background(), request)
		assert.NoError(err)
		assert.NotNil(result)

		var output []searchResultOutput
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)
		assert.NotEmpty(output)

		// Check that results contain tools with "list" in name
		for _, r := range output {
			assert.Contains(r.Name, "list")
		}
	})

	t.Run("search by description", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"query": "pipeline"}

		result, err := handler(context.Background(), request)
		assert.NoError(err)
		assert.NotNil(result)

		var output []searchResultOutput
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)
		assert.NotEmpty(output)
	})

	t.Run("no results returns helpful message", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"query": "xyz123nonexistent"}

		result, err := handler(context.Background(), request)
		assert.NoError(err)
		assert.NotNil(result)

		text := result.Content[0].(mcp.TextContent).Text
		assert.Contains(text, "No tools found")
		assert.Contains(text, "build")
		assert.Contains(text, "pipeline")
	})

	t.Run("results include required scopes", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"query": "scoped"}

		result, err := handler(context.Background(), request)
		assert.NoError(err)

		var output []searchResultOutput
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)
		assert.NotEmpty(output)
		assert.NotEmpty(output[0].RequiredScopes)
	})

	t.Run("results include match location", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"query": "list"}

		result, err := handler(context.Background(), request)
		assert.NoError(err)

		var output []searchResultOutput
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)
		assert.NotEmpty(output)

		// All results should have a matched_in field
		for _, r := range output {
			assert.Contains([]string{"name", "description", "both"}, r.MatchedIn)
		}
	})

	t.Run("results are sorted alphabetically", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		request.Params.Arguments = map[string]any{"query": "list"}

		result, err := handler(context.Background(), request)
		assert.NoError(err)

		var output []searchResultOutput
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)

		// Verify sorted
		for i := 1; i < len(output); i++ {
			assert.LessOrEqual(output[i-1].Name, output[i].Name)
		}
	})
}

func TestSearchToolsWithMetadata(t *testing.T) {
	registry := createTestRegistry()

	t.Run("returns metadata with results", func(t *testing.T) {
		assert := require.New(t)

		results := registry.SearchToolsWithMetadata("list", 10)
		assert.NotEmpty(results)

		for _, r := range results {
			assert.NotEmpty(r.ToolsetName)
			assert.NotEmpty(r.MatchedIn)
			assert.Contains([]string{"name", "description", "both"}, r.MatchedIn)
		}
	})

	t.Run("matched in name", func(t *testing.T) {
		assert := require.New(t)

		results := registry.SearchToolsWithMetadata("scoped", 10)
		assert.NotEmpty(results)

		// "scoped-tool" matches in name only
		found := false
		for _, r := range results {
			if r.Tool.Name == "scoped-tool" {
				found = true
				assert.Equal("name", r.MatchedIn)
			}
		}
		assert.True(found)
	})

	t.Run("matched in both", func(t *testing.T) {
		assert := require.New(t)

		results := registry.SearchToolsWithMetadata("list", 10)
		assert.NotEmpty(results)

		// "list-tool" has "list" in both name and description
		found := false
		for _, r := range results {
			if r.Tool.Name == "list-tool" {
				found = true
				assert.Equal("both", r.MatchedIn)
			}
		}
		assert.True(found)
	})

	t.Run("respects limit", func(t *testing.T) {
		assert := require.New(t)

		results := registry.SearchToolsWithMetadata("tool", 2)
		assert.LessOrEqual(len(results), 2)
	})

	t.Run("results are sorted", func(t *testing.T) {
		assert := require.New(t)

		results := registry.SearchToolsWithMetadata("tool", 10)

		for i := 1; i < len(results); i++ {
			assert.LessOrEqual(results[i-1].Tool.Name, results[i].Tool.Name)
		}
	})

	t.Run("includes read-only status", func(t *testing.T) {
		assert := require.New(t)

		results := registry.SearchToolsWithMetadata("read-only", 10)
		assert.NotEmpty(results)

		found := false
		for _, r := range results {
			if r.Tool.Name == "read-only-tool" {
				found = true
				assert.True(r.ReadOnly)
			}
		}
		assert.True(found)
	})
}

// searchResultOutput is the expected JSON output structure
type searchResultOutput struct {
	Name           string   `json:"name"`
	Description    string   `json:"description"`
	Toolset        string   `json:"toolset"`
	ReadOnly       bool     `json:"read_only"`
	MatchedIn      string   `json:"matched_in"`
	RequiredScopes []string `json:"required_scopes"`
}

// createTestRegistry creates a registry with test tools
func createTestRegistry() *ToolsetRegistry {
	registry := NewToolsetRegistry()

	listTool := ToolDefinition{
		Tool: mcp.Tool{
			Name:        "list-tool",
			Description: "List items from a list",
		},
		RequiredScopes: []string{"read_items"},
	}

	scopedTool := ToolDefinition{
		Tool: mcp.Tool{
			Name:        "scoped-tool",
			Description: "A tool with scopes",
		},
		RequiredScopes: []string{"read_builds", "write_builds"},
	}

	readOnlyTool := ToolDefinition{
		Tool: mcp.Tool{
			Name:        "read-only-tool",
			Description: "A read-only tool",
			Annotations: mcp.ToolAnnotation{
				ReadOnlyHint: func() *bool { b := true; return &b }(),
			},
		},
		RequiredScopes: []string{"read_data"},
	}

	pipelineTool := ToolDefinition{
		Tool: mcp.Tool{
			Name:        "get-pipeline",
			Description: "Get pipeline details",
		},
	}

	toolset1 := Toolset{
		Name:        "Test Toolset 1",
		Description: "First test toolset",
		Tools:       []ToolDefinition{listTool, scopedTool},
	}

	toolset2 := Toolset{
		Name:        "Test Toolset 2",
		Description: "Second test toolset",
		Tools:       []ToolDefinition{readOnlyTool, pipelineTool},
	}

	registry.Register("test1", toolset1)
	registry.Register("test2", toolset2)

	return registry
}
