package commands

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/internal/buildkite"
	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

type StdioCmd struct{}

func (c *StdioCmd) Run(ctx context.Context, globals *Globals) error {
	// Create a client adapter so that we can use a mock or true client
	clientAdapter := &buildkite.BuildkiteClientAdapter{Client: globals.Client}
	s := server.NewMCPServer(
		"github-mcp-server",
		globals.Version,
		server.WithResourceCapabilities(true, true),
		server.WithHooks(trace.NewHooks()),
		server.WithLogging())

	// add the logger to the context
	ctx = globals.Logger.WithContext(ctx)

	log.Ctx(ctx).Info().Str("version", globals.Version).Msg("Starting Buildkite MCP server")

	s.AddTool(buildkite.GetCluster(ctx, globals.Client.Clusters))
	s.AddTool(buildkite.ListClusters(ctx, globals.Client.Clusters))

	s.AddTool(buildkite.GetClusterQueue(ctx, globals.Client.ClusterQueues))
	s.AddTool(buildkite.ListClusterQueues(ctx, globals.Client.ClusterQueues))

	s.AddTool(buildkite.GetCluster(ctx, globals.Client.Clusters))
	s.AddTool(buildkite.ListClusters(ctx, globals.Client.Clusters))

	s.AddTool(buildkite.GetClusterQueue(ctx, globals.Client.ClusterQueues))
	s.AddTool(buildkite.ListClusterQueues(ctx, globals.Client.ClusterQueues))

	s.AddTool(buildkite.CreatePipeline(ctx, globals.Client.Pipelines))
	s.AddTool(buildkite.GetPipeline(ctx, globals.Client.Pipelines))
	s.AddTool(buildkite.ListPipelines(ctx, globals.Client.Pipelines))

	s.AddTool(buildkite.ListBuilds(ctx, globals.Client.Builds))
	s.AddTool(buildkite.GetBuild(ctx, globals.Client.Builds))

	s.AddTool(buildkite.CurrentUser(ctx, globals.Client.User))

	s.AddTool(buildkite.GetJobLogs(ctx, globals.Client))

	s.AddTool(buildkite.AccessToken(ctx, globals.Client.AccessTokens))

	s.AddTool(buildkite.ListArtifacts(ctx, clientAdapter))
	s.AddTool(buildkite.GetArtifact(ctx, clientAdapter))

	s.AddTool(buildkite.UserTokenOrganization(ctx, globals.Client.Organizations))

	s.AddPrompt(mcp.NewPrompt("user_token_organization_prompt",
		mcp.WithPromptDescription("When asked for detail of a users pipelines start by looking up the user's token organization"),
	), buildkite.HandleUserTokenOrganizationPrompt)

	return server.ServeStdio(s)
}
