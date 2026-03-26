package toolsets

import (
	"fmt"
	"slices"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ToolDefinition wraps an MCP tool with additional metadata
type ToolDefinition struct {
	Tool           mcp.Tool
	Register       func(s *mcp.Server) // registers this tool on the server
	RequiredScopes []string            // Buildkite API token scopes required for this tool
}

// IsReadOnly returns true if the tool is read-only
func (td ToolDefinition) IsReadOnly() bool {
	if td.Tool.Annotations == nil {
		return false
	}
	return td.Tool.Annotations.ReadOnlyHint
}

// Toolset represents a logical grouping of related tools
type Toolset struct {
	Name        string
	Description string
	Tools       []ToolDefinition
}

// GetReadOnlyTools returns only the read-only tools from this toolset
func (ts Toolset) GetReadOnlyTools() []ToolDefinition {
	var readOnlyTools []ToolDefinition
	for _, tool := range ts.Tools {
		if tool.IsReadOnly() {
			readOnlyTools = append(readOnlyTools, tool)
		}
	}
	return readOnlyTools
}

// GetAllTools returns all tools from this toolset
func (ts Toolset) GetAllTools() []ToolDefinition {
	return ts.Tools
}

// GetRequiredScopes returns all unique scopes required by tools in this toolset
func (ts Toolset) GetRequiredScopes() []string {
	scopeMap := make(map[string]bool)
	for _, tool := range ts.Tools {
		for _, scope := range tool.RequiredScopes {
			scopeMap[scope] = true
		}
	}

	scopes := make([]string, 0, len(scopeMap))
	for scope := range scopeMap {
		scopes = append(scopes, scope)
	}
	slices.Sort(scopes)
	return scopes
}

// ToolsetRegistry manages the registration and discovery of toolsets.
// It is safe for concurrent reads after initialization, but concurrent
// writes are not supported. In typical usage, the registry is populated
// once at server startup via RegisterToolsets and then only read.
type ToolsetRegistry struct {
	toolsets map[string]Toolset
}

// NewToolsetRegistry creates a new toolset registry
func NewToolsetRegistry() *ToolsetRegistry {
	return &ToolsetRegistry{
		toolsets: make(map[string]Toolset),
	}
}

// Register adds a toolset to the registry
func (tr *ToolsetRegistry) Register(name string, toolset Toolset) {
	tr.toolsets[name] = toolset
}

func (tr *ToolsetRegistry) RegisterToolsets(toolsets map[string]Toolset) {
	for name, toolset := range toolsets {
		tr.Register(name, toolset)
	}
}

// Get retrieves a toolset by name
func (tr *ToolsetRegistry) Get(name string) (Toolset, bool) {
	toolset, exists := tr.toolsets[name]
	return toolset, exists
}

// GetToolsForToolsets returns tools from specified toolset names, optionally filtering for read-only
func (tr *ToolsetRegistry) GetToolsForToolsets(toolsetNames []string, readOnlyMode bool) []ToolDefinition {
	var tools []ToolDefinition

	for _, name := range toolsetNames {
		if toolset, exists := tr.toolsets[name]; exists {
			if readOnlyMode {
				tools = append(tools, toolset.GetReadOnlyTools()...)
			} else {
				tools = append(tools, toolset.GetAllTools()...)
			}
		}
	}

	return tools
}

// List returns all registered toolset names
func (tr *ToolsetRegistry) List() []string {
	names := make([]string, 0, len(tr.toolsets))
	for name := range tr.toolsets {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// GetEnabledTools returns tools from enabled toolsets, optionally filtering for read-only
func (tr *ToolsetRegistry) GetEnabledTools(enabledToolsets []string, readOnlyMode bool) []ToolDefinition {
	var tools []ToolDefinition

	// If "all" is specified, enable all toolsets
	if slices.Contains(enabledToolsets, "all") {
		enabledToolsets = tr.List()
	}

	for _, toolsetName := range enabledToolsets {
		if toolset, exists := tr.toolsets[toolsetName]; exists {
			if readOnlyMode {
				tools = append(tools, toolset.GetReadOnlyTools()...)
			} else {
				tools = append(tools, toolset.GetAllTools()...)
			}
		}
	}

	return tools
}

// GetAllTools returns all tools across all toolsets
func (tr *ToolsetRegistry) GetAllTools() []ToolDefinition {
	var tools []ToolDefinition
	for _, toolset := range tr.toolsets {
		tools = append(tools, toolset.Tools...)
	}
	return tools
}

// ToolsetMetadata provides information about a toolset for introspection
type ToolsetMetadata struct {
	Name          string `json:"name"`
	Description   string `json:"description"`
	ToolCount     int    `json:"tool_count"`
	ReadOnlyCount int    `json:"read_only_count"`
}

// GetMetadata returns metadata for all registered toolsets
func (tr *ToolsetRegistry) GetMetadata() []ToolsetMetadata {
	metadata := make([]ToolsetMetadata, 0, len(tr.toolsets))

	for name, toolset := range tr.toolsets {
		readOnlyCount := len(toolset.GetReadOnlyTools())
		metadata = append(metadata, ToolsetMetadata{
			Name:          name,
			Description:   toolset.Description,
			ToolCount:     len(toolset.Tools),
			ReadOnlyCount: readOnlyCount,
		})
	}

	// Sort by name for consistency
	slices.SortFunc(metadata, func(a, b ToolsetMetadata) int {
		if a.Name < b.Name {
			return -1
		} else if a.Name > b.Name {
			return 1
		}
		return 0
	})

	return metadata
}

// GetRequiredScopes returns all unique scopes required by enabled toolsets
func (tr *ToolsetRegistry) GetRequiredScopes(enabledToolsets []string, readOnlyMode bool) []string {
	scopeMap := make(map[string]bool)

	// If "all" is specified, enable all toolsets
	if slices.Contains(enabledToolsets, "all") {
		enabledToolsets = tr.List()
	}

	for _, toolsetName := range enabledToolsets {
		if toolset, exists := tr.toolsets[toolsetName]; exists {
			var tools []ToolDefinition
			if readOnlyMode {
				tools = toolset.GetReadOnlyTools()
			} else {
				tools = toolset.GetAllTools()
			}

			for _, tool := range tools {
				for _, scope := range tool.RequiredScopes {
					scopeMap[scope] = true
				}
			}
		}
	}

	scopes := make([]string, 0, len(scopeMap))
	for scope := range scopeMap {
		scopes = append(scopes, scope)
	}
	slices.Sort(scopes)
	return scopes
}

// NewTool creates a new tool definition
func NewTool(tool mcp.Tool, register func(s *mcp.Server), scopes []string) ToolDefinition {
	return ToolDefinition{
		Tool:           tool,
		Register:       register,
		RequiredScopes: scopes,
	}
}

const (
	ToolsetAll         = "all" // Special name to enable all toolsets
	ToolsetClusters    = "clusters"
	ToolsetPipelines   = "pipelines"
	ToolsetBuilds      = "builds"
	ToolsetArtifacts   = "artifacts"
	ToolsetLogs        = "logs"
	ToolsetTests       = "tests"
	ToolsetAnnotations = "annotations"
	ToolsetUser        = "user"
)

var ValidToolsets = []string{
	ToolsetAll,
	ToolsetClusters,
	ToolsetPipelines,
	ToolsetBuilds,
	ToolsetArtifacts,
	ToolsetLogs,
	ToolsetTests,
	ToolsetAnnotations,
	ToolsetUser,
}

// IsValidToolset checks if a toolset name is valid
func IsValidToolset(name string) bool {
	return slices.Contains(ValidToolsets, name)
}

// ValidateToolsets checks if all toolset names are valid
func ValidateToolsets(names []string) error {
	invalidToolsets := []string{}

	for _, name := range names {
		if !IsValidToolset(name) {
			invalidToolsets = append(invalidToolsets, name)
		}
	}
	if len(invalidToolsets) > 0 {
		return fmt.Errorf("invalid toolset names: %v", invalidToolsets)
	}
	return nil
}

// newToolDef creates a ToolDefinition from a zero-arg function returning (tool, handler, scopes).
// The generic parameters In and Out match the typed handler signature.
func newToolDef[In, Out any](toolFunc func() (mcp.Tool, mcp.ToolHandlerFor[In, Out], []string)) ToolDefinition {
	tool, handler, scopes := toolFunc()
	return ToolDefinition{
		Tool: tool,
		Register: func(s *mcp.Server) {
			mcp.AddTool(s, &tool, handler)
		},
		RequiredScopes: scopes,
	}
}

// CreateBuiltinToolsets creates the default toolsets with all available tools.
// Tool functions retrieve their dependencies from the request context at call time.
func CreateBuiltinToolsets() map[string]Toolset {
	return map[string]Toolset{
		ToolsetClusters: {
			Name:        "Cluster Management",
			Description: "Tools for managing Buildkite clusters and cluster queues",
			Tools: []ToolDefinition{
				newToolDef(buildkite.GetCluster),
				newToolDef(buildkite.ListClusters),
				newToolDef(buildkite.CreateCluster),
				newToolDef(buildkite.UpdateCluster),
				newToolDef(buildkite.GetClusterQueue),
				newToolDef(buildkite.ListClusterQueues),
				newToolDef(buildkite.CreateClusterQueue),
				newToolDef(buildkite.UpdateClusterQueue),
				newToolDef(buildkite.PauseClusterQueueDispatch),
				newToolDef(buildkite.ResumeClusterQueueDispatch),
			},
		},
		ToolsetPipelines: {
			Name:        "Pipeline Management",
			Description: "Tools for managing Buildkite pipelines",
			Tools: []ToolDefinition{
				newToolDef(buildkite.GetPipeline),
				newToolDef(buildkite.ListPipelines),
				newToolDef(buildkite.CreatePipeline),
				newToolDef(buildkite.UpdatePipeline),
			},
		},
		ToolsetBuilds: {
			Name:        "Build Operations",
			Description: "Tools for managing builds and jobs",
			Tools: []ToolDefinition{
				newToolDef(buildkite.ListBuilds),
				newToolDef(buildkite.GetBuild),
				newToolDef(buildkite.GetBuildTestEngineRuns),
				newToolDef(buildkite.CreateBuild),
				newToolDef(buildkite.UnblockJob),
			},
		},
		ToolsetArtifacts: {
			Name:        "Artifact Management",
			Description: "Tools for managing build artifacts",
			Tools: []ToolDefinition{
				newToolDef(buildkite.ListArtifactsForBuild),
				newToolDef(buildkite.ListArtifactsForJob),
				newToolDef(buildkite.GetArtifact),
			},
		},
		ToolsetTests: {
			Name:        "Test Engine",
			Description: "Tools for managing test runs and test results",
			Tools: []ToolDefinition{
				newToolDef(buildkite.ListTestRuns),
				newToolDef(buildkite.GetTestRun),
				newToolDef(buildkite.GetFailedTestExecutions),
				newToolDef(buildkite.GetTest),
			},
		},
		ToolsetLogs: {
			Name:        "Log Management",
			Description: "Tools for searching, reading, and analyzing job logs",
			Tools: []ToolDefinition{
				newToolDef(buildkite.SearchLogs),
				newToolDef(buildkite.TailLogs),
				newToolDef(buildkite.ReadLogs),
			},
		},
		ToolsetAnnotations: {
			Name:        "Annotation Management",
			Description: "Tools for managing build annotations",
			Tools: []ToolDefinition{
				newToolDef(buildkite.ListAnnotations),
			},
		},
		ToolsetUser: {
			Name:        "User & Organization",
			Description: "Tools for user and organization information",
			Tools: []ToolDefinition{
				newToolDef(buildkite.CurrentUser),
				newToolDef(buildkite.UserTokenOrganization),
				newToolDef(buildkite.AccessToken),
			},
		},
	}
}
