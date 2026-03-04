package toolsets

import (
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestToolDefinition_IsReadOnly(t *testing.T) {
	tests := []struct {
		name     string
		tool     ToolDefinition
		expected bool
	}{
		{
			name: "read-only tool with hint set to true",
			tool: ToolDefinition{
				Tool: mcp.Tool{
					Annotations: &mcp.ToolAnnotations{
						ReadOnlyHint: true,
					},
				},
			},
			expected: true,
		},
		{
			name: "read-write tool with hint set to false",
			tool: ToolDefinition{
				Tool: mcp.Tool{
					Annotations: &mcp.ToolAnnotations{
						ReadOnlyHint: false,
					},
				},
			},
			expected: false,
		},
		{
			name: "tool with no annotations",
			tool: ToolDefinition{
				Tool: mcp.Tool{},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.tool.IsReadOnly()
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestToolset_GetReadOnlyTools(t *testing.T) {
	readOnlyTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-only-tool",
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: true,
			},
		},
	}

	readWriteTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-write-tool",
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: false,
			},
		},
	}

	noHintTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "no-hint-tool",
		},
	}

	toolset := Toolset{
		Name:        "Test Toolset",
		Description: "Test toolset",
		Tools:       []ToolDefinition{readOnlyTool, readWriteTool, noHintTool},
	}

	readOnlyTools := toolset.GetReadOnlyTools()

	require.Len(t, readOnlyTools, 1)
	assert.Equal(t, "read-only-tool", readOnlyTools[0].Tool.Name)
}

func TestToolset_GetAllTools(t *testing.T) {
	assert := require.New(t)

	tools := []ToolDefinition{
		{Tool: mcp.Tool{Name: "tool1"}},
		{Tool: mcp.Tool{Name: "tool2"}},
	}

	toolset := Toolset{
		Name:        "Test Toolset",
		Description: "Test toolset",
		Tools:       tools,
	}

	allTools := toolset.GetAllTools()
	assert.Len(allTools, len(tools))
}

func TestToolset_GetRequiredScopes(t *testing.T) {
	assert := require.New(t)

	toolset := Toolset{
		Tools: []ToolDefinition{
			{RequiredScopes: []string{"read_builds", "write_builds"}},
			{RequiredScopes: []string{"read_pipelines", "read_builds"}},
			{RequiredScopes: []string{}},
		},
	}

	scopes := toolset.GetRequiredScopes()

	expected := []string{"read_builds", "read_pipelines", "write_builds"}
	assert.Equal(expected, scopes)
}

func TestNewToolsetRegistry(t *testing.T) {
	assert := require.New(t)

	registry := NewToolsetRegistry()

	assert.NotNil(registry)
	assert.NotNil(registry.toolsets)
	assert.Empty(registry.toolsets)
}

func TestToolsetRegistry_Register(t *testing.T) {
	assert := require.New(t)

	registry := NewToolsetRegistry()
	toolset := Toolset{
		Name:        "Test Toolset",
		Description: "A test toolset",
		Tools:       []ToolDefinition{},
	}

	registry.Register("test", toolset)

	retrievedToolset, exists := registry.toolsets["test"]
	assert.True(exists)
	assert.Equal(toolset, retrievedToolset)
}

func TestToolsetRegistry_RegisterToolsets(t *testing.T) {
	assert := require.New(t)

	registry := NewToolsetRegistry()
	toolsets := map[string]Toolset{
		"test1": {Name: "Test 1"},
		"test2": {Name: "Test 2"},
	}

	registry.RegisterToolsets(toolsets)

	assert.Len(registry.toolsets, 2)
	assert.Equal(toolsets["test1"], registry.toolsets["test1"])
	assert.Equal(toolsets["test2"], registry.toolsets["test2"])
}

func TestToolsetRegistry_Get(t *testing.T) {
	registry := NewToolsetRegistry()
	toolset := Toolset{Name: "Test Toolset"}
	registry.Register("test", toolset)

	t.Run("existing toolset", func(t *testing.T) {
		assert := require.New(t)
		result, exists := registry.Get("test")
		assert.True(exists)
		assert.Equal(toolset, result)
	})

	t.Run("non-existing toolset", func(t *testing.T) {
		assert := require.New(t)
		result, exists := registry.Get("nonexistent")
		assert.False(exists)
		assert.Equal(Toolset{}, result)
	})
}

func TestToolsetRegistry_List(t *testing.T) {
	registry := NewToolsetRegistry()

	t.Run("empty registry", func(t *testing.T) {
		assert := require.New(t)

		names := registry.List()
		assert.Empty(names)
	})

	t.Run("registry with toolsets", func(t *testing.T) {
		assert := require.New(t)

		registry.Register("zebra", Toolset{})
		registry.Register("alpha", Toolset{})
		registry.Register("beta", Toolset{})

		names := registry.List()
		expected := []string{"alpha", "beta", "zebra"}
		assert.Equal(expected, names)
	})
}

func TestToolsetRegistry_GetToolsForToolsets(t *testing.T) {
	registry := NewToolsetRegistry()

	readOnlyTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-only-tool",
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: true,
			},
		},
	}

	readWriteTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-write-tool",
		},
	}

	anotherTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "another-tool",
		},
	}

	toolset1 := Toolset{
		Name:  "Toolset 1",
		Tools: []ToolDefinition{readOnlyTool, readWriteTool},
	}

	toolset2 := Toolset{
		Name:  "Toolset 2",
		Tools: []ToolDefinition{anotherTool},
	}

	registry.Register("toolset1", toolset1)
	registry.Register("toolset2", toolset2)

	t.Run("specific toolsets - all tools", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetToolsForToolsets([]string{"toolset1", "toolset2"}, false)
		assert.Len(tools, 3)

		toolNames := make([]string, len(tools))
		for i, tool := range tools {
			toolNames[i] = tool.Tool.Name
		}
		assert.Contains(toolNames, "read-only-tool")
		assert.Contains(toolNames, "read-write-tool")
		assert.Contains(toolNames, "another-tool")
	})

	t.Run("specific toolsets - read-only mode", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetToolsForToolsets([]string{"toolset1", "toolset2"}, true)
		assert.Len(tools, 1)
		assert.Equal("read-only-tool", tools[0].Tool.Name)
	})

	t.Run("single toolset", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetToolsForToolsets([]string{"toolset1"}, false)
		assert.Len(tools, 2)
	})

	t.Run("non-existent toolset", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetToolsForToolsets([]string{"nonexistent"}, false)
		assert.Empty(tools)
	})

	t.Run("empty toolset list", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetToolsForToolsets([]string{}, false)
		assert.Empty(tools)
	})

	t.Run("mixed valid and invalid toolsets", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetToolsForToolsets([]string{"toolset1", "nonexistent", "toolset2"}, false)
		assert.Len(tools, 3) // Only tools from valid toolsets
	})

	t.Run("scopes are available from returned tools", func(t *testing.T) {
		assert := require.New(t)

		// Create tools with specific scopes
		toolWithScopes := ToolDefinition{
			Tool: mcp.Tool{
				Name: "scoped-tool",
			},
			RequiredScopes: []string{"read_builds", "write_pipelines"},
		}

		anotherToolWithScopes := ToolDefinition{
			Tool: mcp.Tool{
				Name: "another-scoped-tool",
			},
			RequiredScopes: []string{"read_artifacts", "read_builds"},
		}

		scopedToolset := Toolset{
			Name:  "Scoped Toolset",
			Tools: []ToolDefinition{toolWithScopes, anotherToolWithScopes},
		}

		registry.Register("scoped", scopedToolset)

		tools := registry.GetToolsForToolsets([]string{"scoped"}, false)
		assert.Len(tools, 2)

		// Verify scopes are preserved
		var allScopes []string
		for _, tool := range tools {
			allScopes = append(allScopes, tool.RequiredScopes...)
		}

		assert.Contains(allScopes, "read_builds")
		assert.Contains(allScopes, "write_pipelines")
		assert.Contains(allScopes, "read_artifacts")

		// Verify specific tool scopes
		for _, tool := range tools {
			switch tool.Tool.Name {
			case "scoped-tool":
				assert.Equal([]string{"read_builds", "write_pipelines"}, tool.RequiredScopes)
			case "another-scoped-tool":
				assert.Equal([]string{"read_artifacts", "read_builds"}, tool.RequiredScopes)
			}
		}
	})
}

func TestToolsetRegistry_GetEnabledTools(t *testing.T) {
	registry := NewToolsetRegistry()

	readOnlyTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-only-tool",
			Annotations: &mcp.ToolAnnotations{
				ReadOnlyHint: true,
			},
		},
	}

	readWriteTool := ToolDefinition{
		Tool: mcp.Tool{
			Name: "read-write-tool",
		},
	}

	toolset1 := Toolset{
		Name:  "Toolset 1",
		Tools: []ToolDefinition{readOnlyTool},
	}

	toolset2 := Toolset{
		Name:  "Toolset 2",
		Tools: []ToolDefinition{readWriteTool},
	}

	registry.Register("toolset1", toolset1)
	registry.Register("toolset2", toolset2)

	t.Run("specific toolsets - all tools", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetEnabledTools([]string{"toolset1", "toolset2"}, false)
		assert.Len(tools, 2)
	})

	t.Run("specific toolsets - read-only mode", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetEnabledTools([]string{"toolset1", "toolset2"}, true)
		assert.Len(tools, 1)
		assert.Equal("read-only-tool", tools[0].Tool.Name)
	})

	t.Run("all toolsets enabled", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetEnabledTools([]string{"all"}, false)
		assert.Len(tools, 2)
	})

	t.Run("non-existent toolset", func(t *testing.T) {
		assert := require.New(t)
		tools := registry.GetEnabledTools([]string{"nonexistent"}, false)
		assert.Empty(tools)
	})
}

func TestToolsetRegistry_GetMetadata(t *testing.T) {
	registry := NewToolsetRegistry()

	t.Run("single toolset", func(t *testing.T) {
		assert := require.New(t)
		readOnlyTool := ToolDefinition{
			Tool: mcp.Tool{
				Annotations: &mcp.ToolAnnotations{
					ReadOnlyHint: true,
				},
			},
		}

		readWriteTool := ToolDefinition{
			Tool: mcp.Tool{},
		}

		toolset := Toolset{
			Name:        "Test Toolset",
			Description: "A test toolset",
			Tools:       []ToolDefinition{readOnlyTool, readWriteTool},
		}

		registry.Register("test", toolset)

		metadata := registry.GetMetadata()

		assert.Len(metadata, 1)
		assert.Equal("test", metadata[0].Name)
		assert.Equal("A test toolset", metadata[0].Description)
		assert.Equal(2, metadata[0].ToolCount)
		assert.Equal(1, metadata[0].ReadOnlyCount)
	})

	t.Run("multiple toolsets sorted by name", func(t *testing.T) {
		assert := require.New(t)
		// Clear registry for this test
		registry := NewToolsetRegistry()

		toolset1 := Toolset{
			Name:        "Zebra Toolset",
			Description: "Last in alphabetical order",
			Tools:       []ToolDefinition{{Tool: mcp.Tool{}}},
		}

		toolset2 := Toolset{
			Name:        "Alpha Toolset",
			Description: "First in alphabetical order",
			Tools: []ToolDefinition{
				{Tool: mcp.Tool{Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}},
				{Tool: mcp.Tool{}},
			},
		}

		toolset3 := Toolset{
			Name:        "Beta Toolset",
			Description: "Middle in alphabetical order",
			Tools:       []ToolDefinition{},
		}

		// Register in non-alphabetical order to test sorting
		registry.Register("zebra", toolset1)
		registry.Register("alpha", toolset2)
		registry.Register("beta", toolset3)

		metadata := registry.GetMetadata()

		assert.Len(metadata, 3)

		// Verify sorted by name (the metadata contains the registration key as Name)
		assert.Equal("alpha", metadata[0].Name)
		assert.Equal("First in alphabetical order", metadata[0].Description)
		assert.Equal(2, metadata[0].ToolCount)
		assert.Equal(1, metadata[0].ReadOnlyCount)

		assert.Equal("beta", metadata[1].Name)
		assert.Equal("Middle in alphabetical order", metadata[1].Description)
		assert.Equal(0, metadata[1].ToolCount)
		assert.Equal(0, metadata[1].ReadOnlyCount)

		assert.Equal("zebra", metadata[2].Name)
		assert.Equal("Last in alphabetical order", metadata[2].Description)
		assert.Equal(1, metadata[2].ToolCount)
		assert.Equal(0, metadata[2].ReadOnlyCount)
	})

	t.Run("empty registry", func(t *testing.T) {
		assert := require.New(t)
		emptyRegistry := NewToolsetRegistry()
		metadata := emptyRegistry.GetMetadata()
		assert.Empty(metadata)
	})
}

func TestToolsetRegistry_GetRequiredScopes(t *testing.T) {
	registry := NewToolsetRegistry()

	toolset1 := Toolset{
		Tools: []ToolDefinition{
			{RequiredScopes: []string{"read_builds", "write_builds"}},
		},
	}

	toolset2 := Toolset{
		Tools: []ToolDefinition{
			{RequiredScopes: []string{"read_pipelines", "read_builds"}},
		},
	}

	registry.Register("toolset1", toolset1)
	registry.Register("toolset2", toolset2)

	t.Run("specific toolsets", func(t *testing.T) {
		assert := require.New(t)
		scopes := registry.GetRequiredScopes([]string{"toolset1", "toolset2"}, false)
		expected := []string{"read_builds", "read_pipelines", "write_builds"}
		assert.Equal(expected, scopes)
	})

	t.Run("all toolsets", func(t *testing.T) {
		assert := require.New(t)
		scopes := registry.GetRequiredScopes([]string{"all"}, false)
		expected := []string{"read_builds", "read_pipelines", "write_builds"}
		assert.Equal(expected, scopes)
	})

	t.Run("read-only mode", func(t *testing.T) {
		assert := require.New(t)
		readOnlyTool := ToolDefinition{
			Tool: mcp.Tool{
				Annotations: &mcp.ToolAnnotations{
					ReadOnlyHint: true,
				},
			},
			RequiredScopes: []string{"read_only"},
		}

		readWriteTool := ToolDefinition{
			Tool:           mcp.Tool{},
			RequiredScopes: []string{"write_only"},
		}

		toolsetMixed := Toolset{
			Tools: []ToolDefinition{readOnlyTool, readWriteTool},
		}

		registry.Register("mixed", toolsetMixed)

		scopes := registry.GetRequiredScopes([]string{"mixed"}, true)
		expected := []string{"read_only"}
		assert.Equal(expected, scopes)
	})
}

func TestNewTool(t *testing.T) {
	assert := require.New(t)

	mockRegister := func(s *mcp.Server) {}

	tool := mcp.Tool{Name: "test-tool"}
	scopes := []string{"read_test", "write_test"}

	toolDef := NewTool(tool, mockRegister, scopes)

	assert.Equal(tool, toolDef.Tool)
	assert.NotNil(toolDef.Register)
	assert.Equal(scopes, toolDef.RequiredScopes)
}

func TestIsValidToolset(t *testing.T) {
	tests := []struct {
		name     string
		toolset  string
		expected bool
	}{
		{"valid toolset - all", "all", true},
		{"valid toolset - clusters", "clusters", true},
		{"valid toolset - pipelines", "pipelines", true},
		{"valid toolset - builds", "builds", true},
		{"valid toolset - artifacts", "artifacts", true},
		{"valid toolset - logs", "logs", true},
		{"valid toolset - tests", "tests", true},
		{"valid toolset - annotations", "annotations", true},
		{"valid toolset - user", "user", true},
		{"invalid toolset", "invalid", false},
		{"empty string", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert := require.New(t)
			result := IsValidToolset(tt.toolset)
			assert.Equal(tt.expected, result)
		})
	}
}

func TestValidateToolsets(t *testing.T) {
	t.Run("all valid toolsets", func(t *testing.T) {
		assert := require.New(t)
		valid := []string{"all", "clusters", "pipelines"}
		err := ValidateToolsets(valid)
		assert.NoError(err)
	})

	t.Run("some invalid toolsets", func(t *testing.T) {
		assert := require.New(t)
		mixed := []string{"clusters", "invalid", "pipelines", "another-invalid"}
		err := ValidateToolsets(mixed)
		assert.Error(err)
		assert.Contains(err.Error(), "invalid")
		assert.Contains(err.Error(), "another-invalid")
	})

	t.Run("empty slice", func(t *testing.T) {
		assert := require.New(t)
		err := ValidateToolsets([]string{})
		assert.NoError(err)
	})
}

func TestCreateBuiltinToolsets(t *testing.T) {
	assert := require.New(t)

	registry := NewToolsetRegistry()
	builtin := CreateBuiltinToolsets()
	registry.RegisterToolsets(builtin)

	// Check that expected toolsets are registered
	expectedToolsets := []string{"clusters", "pipelines", "builds", "artifacts", "logs", "tests", "annotations", "user"}
	for _, name := range expectedToolsets {
		_, exists := registry.Get(name)
		assert.True(exists, "expected toolset %s to be registered", name)
	}
}
