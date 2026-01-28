package toolsets

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestListToolsets(t *testing.T) {
	registry := NewToolsetRegistry()

	readOnlyTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-only-tool",
			Annotations: mcp.ToolAnnotation{
				ReadOnlyHint: func() *bool { b := true; return &b }(),
			},
		},
	}

	readWriteTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-write-tool",
		},
	}

	toolset1 := Toolset{
		Name:        "Builds Toolset",
		Description: "Tools for managing builds",
		Tools:       []ToolDefinition{readOnlyTool, readWriteTool},
	}

	toolset2 := Toolset{
		Name:        "Artifacts Toolset",
		Description: "Tools for managing artifacts",
		Tools:       []ToolDefinition{readOnlyTool},
	}

	registry.Register("builds", toolset1)
	registry.Register("artifacts", toolset2)

	tool, handler, scopes := ListToolsets(registry)

	t.Run("tool definition", func(t *testing.T) {
		assert := require.New(t)
		assert.Equal("list_toolsets", tool.Name)
		assert.Contains(tool.Description, "List all available toolsets")
		assert.Empty(scopes)
	})

	t.Run("returns all toolsets", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		result, err := handler(context.Background(), request)
		assert.NoError(err)
		assert.NotNil(result)

		var output []ToolsetMetadata
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)
		assert.Len(output, 2)
	})

	t.Run("returns sorted metadata", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		result, err := handler(context.Background(), request)
		assert.NoError(err)

		var output []ToolsetMetadata
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)

		// Should be sorted alphabetically
		assert.Equal("artifacts", output[0].Name)
		assert.Equal("builds", output[1].Name)
	})

	t.Run("includes tool counts", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		result, err := handler(context.Background(), request)
		assert.NoError(err)

		var output []ToolsetMetadata
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)

		// Find builds toolset
		var buildsMeta ToolsetMetadata
		for _, m := range output {
			if m.Name == "builds" {
				buildsMeta = m
				break
			}
		}

		assert.Equal(2, buildsMeta.ToolCount)
		assert.Equal(1, buildsMeta.ReadOnlyCount)
	})

	t.Run("includes descriptions", func(t *testing.T) {
		assert := require.New(t)

		request := mcp.CallToolRequest{}
		result, err := handler(context.Background(), request)
		assert.NoError(err)

		var output []ToolsetMetadata
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)

		// Find artifacts toolset
		var artifactsMeta ToolsetMetadata
		for _, m := range output {
			if m.Name == "artifacts" {
				artifactsMeta = m
				break
			}
		}

		assert.Equal("Tools for managing artifacts", artifactsMeta.Description)
	})

	t.Run("empty registry", func(t *testing.T) {
		assert := require.New(t)

		emptyRegistry := NewToolsetRegistry()
		_, emptyHandler, _ := ListToolsets(emptyRegistry)

		request := mcp.CallToolRequest{}
		result, err := emptyHandler(context.Background(), request)
		assert.NoError(err)

		var output []ToolsetMetadata
		err = json.Unmarshal([]byte(result.Content[0].(mcp.TextContent).Text), &output)
		assert.NoError(err)
		assert.Empty(output)
	})
}
