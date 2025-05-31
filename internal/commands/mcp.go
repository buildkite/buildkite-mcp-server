package commands

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/internal/buildkite"
	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

func NewMCPServer(ctx context.Context, globals *Globals) *server.MCPServer {
	s := server.NewMCPServer(
		"buildkite-mcp-server",
		globals.Version,
		server.WithToolCapabilities(true),
		server.WithPromptCapabilities(true),
		server.WithHooks(trace.NewHooks()),
		server.WithLogging())

	// add the logger to the context
	ctx = globals.Logger.WithContext(ctx)

	log.Ctx(ctx).Info().Str("version", globals.Version).Msg("Starting Buildkite MCP server")

	s.AddTools(BuildkiteTools(ctx, globals.Client)...)

	s.AddPrompt(mcp.NewPrompt("user_token_organization_prompt",
		mcp.WithPromptDescription("When asked for detail of a users pipelines start by looking up the user's token organization"),
	), buildkite.HandleUserTokenOrganizationPrompt)

	return s
}

func BuildkiteTools(ctx context.Context, client *gobuildkite.Client) []server.ServerTool {
	// Create a client adapter so that we can use a mock or true client
	clientAdapter := &buildkite.BuildkiteClientAdapter{Client: client}

	var tools []server.ServerTool

	addTool := func(tool mcp.Tool, handler server.ToolHandlerFunc) []server.ServerTool {
		return append(tools, server.ServerTool{Tool: tool, Handler: handler})
	}

	// Cluster tools
	tools = addTool(buildkite.GetCluster(ctx, client.Clusters))
	tools = addTool(buildkite.ListClusters(ctx, client.Clusters))

	// Queue tools
	tools = addTool(buildkite.GetClusterQueue(ctx, client.ClusterQueues))
	tools = addTool(buildkite.ListClusterQueues(ctx, client.ClusterQueues))

	// Pipeline tools
	tools = addTool(buildkite.GetPipeline(ctx, client.Pipelines))
	tools = addTool(buildkite.ListPipelines(ctx, client.Pipelines))

	// Build tools
	tools = addTool(buildkite.ListBuilds(ctx, client.Builds))
	tools = addTool(buildkite.GetBuild(ctx, client.Builds))

	// User tools
	tools = addTool(buildkite.CurrentUser(ctx, client.User))
	tools = addTool(buildkite.UserTokenOrganization(ctx, client.Organizations))

	// Other tools
	tools = addTool(buildkite.GetJobLogs(ctx, client))
	tools = addTool(buildkite.AccessToken(ctx, client.AccessTokens))
	tools = addTool(buildkite.ListArtifacts(ctx, clientAdapter))
	tools = addTool(buildkite.GetArtifact(ctx, clientAdapter))

	return tools
}
