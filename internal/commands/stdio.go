package commands

import (
	"context"

	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

type StdioCmd struct {
	EnabledToolsets []string `help:"Comma-separated list of toolsets to enable (e.g., 'pipelines,builds,clusters'). Use 'all' to enable all toolsets." default:"all" env:"BUILDKITE_TOOLSETS"`
	ReadOnly        bool     `help:"Enable read-only mode, which filters out write operations from all toolsets." default:"false" env:"BUILDKITE_READ_ONLY"`
	DynamicToolsets bool     `help:"Enable dynamic tool loading via Tool Search Tool (reduces context usage with Claude API beta)." default:"false" env:"BUILDKITE_DYNAMIC_TOOLSETS"`
}

func (c *StdioCmd) Run(ctx context.Context, globals *Globals) error {
	// Validate the enabled toolsets
	if err := toolsets.ValidateToolsets(c.EnabledToolsets); err != nil {
		return err
	}

	s := server.NewMCPServer(globals.Version, globals.Client, globals.BuildkiteLogsClient,
		server.WithReadOnly(c.ReadOnly),
		server.WithToolsets(c.EnabledToolsets...),
		server.WithDynamicToolsets(c.DynamicToolsets))

	return mcpserver.ServeStdio(s,
		mcpserver.WithStdioContextFunc(
			setupContext(globals),
		),
	)
}

func setupContext(globals *Globals) mcpserver.StdioContextFunc {
	return func(ctx context.Context) context.Context {
		log.Info().Msg("Starting MCP server over stdio")

		// add the logger to the context
		return log.Logger.WithContext(ctx)
	}
}
