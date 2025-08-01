package server

import (
	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
	buildkitelogs "github.com/wolfeidau/buildkite-logs-parquet"
)

func fromTypeTool[T any](tool mcp.Tool, handler mcp.TypedToolHandlerFunc[T]) (mcp.Tool, server.ToolHandlerFunc) {
	return tool, mcp.NewTypedToolHandler(handler)
}

func NewMCPServer(version string, client *gobuildkite.Client, buildkiteLogsClient *buildkitelogs.Client) *server.MCPServer {
	s := server.NewMCPServer(
		"buildkite-mcp-server",
		version,
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
		server.WithHooks(trace.NewHooks()),
		server.WithLogging())

	log.Info().Str("version", version).Msg("Starting Buildkite MCP server")

	s.AddTools(BuildkiteTools(client, buildkiteLogsClient)...)

	s.AddPrompt(mcp.NewPrompt("user_token_organization_prompt",
		mcp.WithPromptDescription("When asked for detail of a users pipelines start by looking up the user's token organization"),
	), buildkite.HandleUserTokenOrganizationPrompt)

	return s
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
	tools = addTool(buildkite.ListBuilds(client.Builds))
	tools = addTool(buildkite.GetBuild(client.Builds))
	tools = addTool(buildkite.GetBuildTestEngineRuns(client.Builds))
	tools = addTool(
		fromTypeTool(buildkite.CreateBuild(client.Builds)),
	)

	// User tools
	tools = addTool(buildkite.CurrentUser(client.User))
	tools = addTool(buildkite.UserTokenOrganization(client.Organizations))

	// Job tools
	tools = addTool(buildkite.GetJobs(client.Builds))

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
