package server

import (
	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

func fromTypeTool[T any](tool mcp.Tool, handler mcp.TypedToolHandlerFunc[T]) (mcp.Tool, server.ToolHandlerFunc) {
	return tool, mcp.NewTypedToolHandler(handler)
}

func NewMCPServer(version string, client *gobuildkite.Client, buildkiteLogsClient *buildkitelogs.Client, opts ...Option) *server.MCPServer {
	// apply functional options
	newMCPOptions := &options{}
	for _, opt := range opts {
		opt(newMCPOptions)
	}

	hooks := trace.NewHooks()
	if newMCPOptions.hooksCallback != nil {
		newMCPOptions.hooksCallback(hooks)
	}

	s := server.NewMCPServer(
		"buildkite-mcp-server",
		version,
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
		server.WithResourceCapabilities(true, true),
		server.WithHooks(hooks),
		server.WithLogging())

	log.Info().Str("version", version).Msg("Starting Buildkite MCP server")

	s.AddTools(BuildkiteTools(client, buildkiteLogsClient)...)

	s.AddPrompt(mcp.NewPrompt("user_token_organization_prompt",
		mcp.WithPromptDescription("When asked for detail of a users pipelines start by looking up the user's token organization"),
	), buildkite.HandleUserTokenOrganizationPrompt)

	s.AddResource(mcp.NewResource(
		"debug-logs-guide",
		"Debug Logs Guide",
		mcp.WithResourceDescription("Comprehensive guide for debugging Buildkite build failures using logs"),
	), buildkite.HandleDebugLogsGuideResource)

	return s
}

// Option is an optional parameter to the new MCP server
type Option func(*options)

type options struct {
	hooksCallback func(hooks *server.Hooks)
}

// WithHooks allows passing in a callback to modify the hooks in MCP server creation
func WithHooks(callback func(hooks *server.Hooks)) Option {
	return func(o *options) {
		o.hooksCallback = callback
	}
}

func BuildkiteTools(client *gobuildkite.Client, buildkiteLogsClient *buildkitelogs.Client) []server.ServerTool {
	// Create a client adapter so that we can use a mock or true client
	clientAdapter := &buildkite.BuildkiteClientAdapter{Client: client}

	var tools []server.ServerTool

	addTool := func(tool mcp.Tool, handler server.ToolHandlerFunc) []server.ServerTool {
		return append(tools, server.ServerTool{Tool: tool, Handler: handler})
	}

	// Cluster tools
	tools = addTool(buildkite.GetCluster(client.Clusters))
	tools = addTool(buildkite.ListClusters(client.Clusters))

	// Queue tools
	tools = addTool(buildkite.GetClusterQueue(client.ClusterQueues))
	tools = addTool(buildkite.ListClusterQueues(client.ClusterQueues))

	// Pipeline tools
	tools = addTool(
		fromTypeTool(buildkite.GetPipeline(client.Pipelines)),
	)
	tools = addTool(
		fromTypeTool(buildkite.ListPipelines(client.Pipelines)),
	)
	tools = addTool(
		fromTypeTool(buildkite.CreatePipeline(client.Pipelines)),
	)
	tools = addTool(
		fromTypeTool(buildkite.UpdatePipeline(client.Pipelines)),
	)

	// Build tools
	tools = addTool(
		fromTypeTool(buildkite.ListBuilds(client.Builds)),
	)
	tools = addTool(
		fromTypeTool(buildkite.GetBuild(client.Builds)),
	)
	tools = addTool(
		fromTypeTool(buildkite.GetBuildTestEngineRuns(client.Builds)),
	)
	tools = addTool(
		fromTypeTool(buildkite.CreateBuild(client.Builds)),
	)
	tools = addTool(
		fromTypeTool(buildkite.WaitForBuild(client.Builds)),
	)

	// User tools
	tools = addTool(buildkite.CurrentUser(client.User))
	tools = addTool(buildkite.UserTokenOrganization(client.Organizations))

	// Job tools
	tools = addTool(
		fromTypeTool(buildkite.GetJobs(client.Builds)),
	)
	tools = addTool(
		fromTypeTool(buildkite.UnblockJob(client.Jobs)),
	)

	// Artifacts tools
	tools = addTool(buildkite.ListArtifacts(clientAdapter))
	tools = addTool(buildkite.GetArtifact(clientAdapter))

	// Annotation tools
	tools = addTool(buildkite.ListAnnotations(client.Annotations))

	// Test Run tools
	tools = addTool(buildkite.ListTestRuns(client.TestRuns))
	tools = addTool(buildkite.GetTestRun(client.TestRuns))

	// Test Execution tools
	tools = addTool(buildkite.GetFailedTestExecutions(client.TestRuns))

	// Test tools
	tools = addTool(buildkite.GetTest(client.Tests))

	// Job Log tools (Parquet-based)
	tools = addTool(
		fromTypeTool(buildkite.SearchLogs(buildkiteLogsClient)),
	)
	tools = addTool(
		fromTypeTool(buildkite.TailLogs(buildkiteLogsClient)),
	)
	tools = addTool(
		fromTypeTool(buildkite.GetLogsInfo(buildkiteLogsClient)),
	)
	tools = addTool(
		fromTypeTool(buildkite.ReadLogs(buildkiteLogsClient)),
	)

	// Other tools
	tools = addTool(buildkite.AccessToken(client.AccessTokens))

	return tools
}
