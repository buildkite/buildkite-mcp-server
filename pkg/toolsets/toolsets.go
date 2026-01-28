package toolsets

import (
	"fmt"
	"maps"
	"slices"
	"strings"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// ToolDefinition wraps an MCP tool with additional metadata
type ToolDefinition struct {
	Tool           mcp.Tool
	Handler        server.ToolHandlerFunc
	RequiredScopes []string // Buildkite API token scopes required for this tool
	DeferLoading   bool     // Mark tool for deferred loading
}

// IsReadOnly returns true if the tool is read-only
func (td ToolDefinition) IsReadOnly() bool {
	if td.Tool.Annotations.ReadOnlyHint == nil {
		return false
	}
	return *td.Tool.Annotations.ReadOnlyHint
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

// SearchResult contains rich metadata about a tool search match
type SearchResult struct {
	Tool           mcp.Tool
	ToolsetName    string
	MatchedIn      string // "name", "description", or "both"
	RequiredScopes []string
	ReadOnly       bool
}

// SearchToolsWithMetadata searches for tools matching the query across all toolsets
// Returns results with additional metadata including toolset name and match location.
// Toolsets are iterated in sorted order to ensure deterministic results.
func (tr *ToolsetRegistry) SearchToolsWithMetadata(query string, limit int) []SearchResult {
	var results []SearchResult
	queryLower := strings.ToLower(query)

	// Sort toolset names for deterministic iteration order
	toolsetNames := slices.Sorted(maps.Keys(tr.toolsets))

	for _, toolsetName := range toolsetNames {
		toolset := tr.toolsets[toolsetName]
		for _, toolDef := range toolset.Tools {
			nameMatch := strings.Contains(strings.ToLower(toolDef.Tool.Name), queryLower)
			descMatch := strings.Contains(strings.ToLower(toolDef.Tool.Description), queryLower)

			if nameMatch || descMatch {
				matchedIn := "description"
				if nameMatch && descMatch {
					matchedIn = "both"
				} else if nameMatch {
					matchedIn = "name"
				}

				results = append(results, SearchResult{
					Tool:           toolDef.Tool,
					ToolsetName:    toolsetName,
					MatchedIn:      matchedIn,
					RequiredScopes: toolDef.RequiredScopes,
					ReadOnly:       toolDef.IsReadOnly(),
				})
				if len(results) >= limit {
					break
				}
			}
		}
		if len(results) >= limit {
			break
		}
	}

	// Sort results alphabetically by tool name for deterministic output
	slices.SortFunc(results, func(a, b SearchResult) int {
		return strings.Compare(a.Tool.Name, b.Tool.Name)
	})

	return results
}

// GetAllTools returns all tools across all toolsets with defer_loading metadata
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

// NewTool creates a new tool definition with annotations based on access level
func NewTool(tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) ToolDefinition {
	return ToolDefinition{
		Tool:           tool,
		Handler:        handler,
		RequiredScopes: scopes,
		DeferLoading:   false, // Default: load immediately
	}
}

// NewDeferredTool creates a new tool definition that is loaded lazily
func NewDeferredTool(tool mcp.Tool, handler server.ToolHandlerFunc, scopes []string) ToolDefinition {
	td := NewTool(tool, handler, scopes)
	td.DeferLoading = true
	return td
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

// CreateBuiltinToolsets creates the default toolsets with all available tools
func CreateBuiltinToolsets(client *gobuildkite.Client, buildkiteLogsClient *buildkitelogs.Client) map[string]Toolset {
	// Create a client adapter for artifact tools
	clientAdapter := &buildkite.BuildkiteClientAdapter{Client: client}

	return map[string]Toolset{
		ToolsetClusters: {
			Name:        "Cluster Management",
			Description: "Tools for managing Buildkite clusters and cluster queues",
			Tools: []ToolDefinition{
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.GetCluster(client.Clusters) }),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.ListClusters(client.Clusters) }),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					return buildkite.GetClusterQueue(client.ClusterQueues)
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					return buildkite.ListClusterQueues(client.ClusterQueues)
				}),
			},
		},
		ToolsetPipelines: {
			Name:        "Pipeline Management",
			Description: "Tools for managing Buildkite pipelines",
			Tools: []ToolDefinition{
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.GetPipeline(client.Pipelines)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.ListPipelines(client.Pipelines)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.CreatePipeline(client.Pipelines)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.UpdatePipeline(client.Pipelines)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
			},
		},
		ToolsetBuilds: {
			Name:        "Build Operations",
			Description: "Tools for managing builds and jobs",
			Tools: []ToolDefinition{
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.ListBuilds(client.Builds)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.GetBuild(client.Builds)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.GetBuildTestEngineRuns(client.Builds)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.CreateBuild(client.Builds)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.WaitForBuild(client.Builds)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.UnblockJob(client.Jobs)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
			},
		},
		ToolsetArtifacts: {
			Name:        "Artifact Management",
			Description: "Tools for managing build artifacts",
			Tools: []ToolDefinition{
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					return buildkite.ListArtifactsForBuild(clientAdapter)
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					return buildkite.ListArtifactsForJob(clientAdapter)
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.GetArtifact(clientAdapter) }),
			},
		},
		ToolsetTests: {
			Name:        "Test Engine",
			Description: "Tools for managing test runs and test results",
			Tools: []ToolDefinition{
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.ListTestRuns(client.TestRuns) }),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.GetTestRun(client.TestRuns) }),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					return buildkite.GetFailedTestExecutions(client.TestRuns)
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.GetTest(client.Tests) }),
			},
		},
		ToolsetLogs: {
			Name:        "Log Management",
			Description: "Tools for searching, reading, and analyzing job logs",
			Tools: []ToolDefinition{
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.SearchLogs(buildkiteLogsClient)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.TailLogs(buildkiteLogsClient)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					tool, handler, scopes := buildkite.ReadLogs(buildkiteLogsClient)
					return tool, mcp.NewTypedToolHandler(handler), scopes
				}),
			},
		},
		ToolsetAnnotations: {
			Name:        "Annotation Management",
			Description: "Tools for managing build annotations",
			Tools: []ToolDefinition{
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					return buildkite.ListAnnotations(client.Annotations)
				}),
			},
		},
		ToolsetUser: {
			Name:        "User & Organization",
			Description: "Tools for user and organization information",
			Tools: []ToolDefinition{
				newToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.CurrentUser(client.User) }),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) {
					return buildkite.UserTokenOrganization(client.Organizations)
				}),
				newDeferredToolFromFunc(func() (mcp.Tool, server.ToolHandlerFunc, []string) { return buildkite.AccessToken(client.AccessTokens) }),
			},
		},
	}
}

// newToolFromFunc creates a new ToolDefinition from a function that returns (tool, handler, scopes)
func newToolFromFunc(toolFunc func() (mcp.Tool, server.ToolHandlerFunc, []string)) ToolDefinition {
	tool, handler, scopes := toolFunc()
	return NewTool(tool, handler, scopes)
}

// newDeferredToolFromFunc creates a new deferred ToolDefinition from a function
func newDeferredToolFromFunc(toolFunc func() (mcp.Tool, server.ToolHandlerFunc, []string)) ToolDefinition {
	tool, handler, scopes := toolFunc()
	return NewDeferredTool(tool, handler, scopes)
}
