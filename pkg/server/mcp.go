package server

import (
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

func NewMCPServer(version string, client *gobuildkite.Client) *server.MCPServer {
	s := server.NewMCPServer(
		"buildkite-mcp-server",
		version,
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
		server.WithHooks(trace.NewHooks()),
		server.WithLogging())

	log.Info().Str("version", version).Msg("Starting Buildkite MCP server")

	s.AddTools(BuildkiteTools(client)...)

	s.AddPrompt(mcp.NewPrompt("user_token_organization_prompt",
		mcp.WithPromptDescription("When asked for detail of a users pipelines start by looking up the user's token organization"),
	), buildkite.HandleUserTokenOrganizationPrompt)

	return s
}

func BuildkiteTools(client *gobuildkite.Client) []server.ServerTool {
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
	tools = addTool(buildkite.GetPipeline(client.Pipelines))
	tools = addTool(buildkite.ListPipelines(client.Pipelines))
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
	tools = addTool(buildkite.GetJobLogs(client))
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

	// Other tools
	tools = addTool(buildkite.AccessToken(client.AccessTokens))

	return tools
}
