package commands

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/buildkite"
	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/rs/zerolog/log"
)

type StdioCmd struct {
	EnabledToolsets []string `help:"Comma-separated list of toolsets to enable (e.g., 'pipelines,builds,clusters'). Use 'all' to enable all toolsets." default:"all" env:"BUILDKITE_TOOLSETS"`
	ReadOnly        bool     `help:"Enable read-only mode, which filters out write operations from all toolsets." default:"false" env:"BUILDKITE_READ_ONLY"`
}

func (c *StdioCmd) Run(ctx context.Context, globals *Globals) error {
	if err := toolsets.ValidateToolsets(c.EnabledToolsets); err != nil {
		return err
	}

	deps := buildkite.ToolDependencies{
		BuildsClient:         globals.Client.Builds,
		PipelinesClient:      globals.Client.Pipelines,
		ClustersClient:       globals.Client.Clusters,
		ClusterQueuesClient:  globals.Client.ClusterQueues,
		ArtifactsClient:      &buildkite.BuildkiteClientAdapter{Client: globals.Client},
		AnnotationsClient:    globals.Client.Annotations,
		OrganizationsClient:  globals.Client.Organizations,
		UserClient:           globals.Client.User,
		AccessTokensClient:   globals.Client.AccessTokens,
		JobsClient:           globals.Client.Jobs,
		TestRunsClient:       globals.Client.TestRuns,
		TestExecutionsClient: globals.Client.TestRuns,
		TestsClient:          globals.Client.Tests,
		BuildkiteLogsClient:  globals.BuildkiteLogsClient,
	}

	log.Info().Msg("Starting MCP server over stdio")
	ctx = log.Logger.WithContext(ctx)

	s := server.NewMCPServer(globals.Version, deps,
		server.WithReadOnly(c.ReadOnly),
		server.WithToolsets(c.EnabledToolsets...))

	return s.Run(ctx, &mcp.StdioTransport{})
}
