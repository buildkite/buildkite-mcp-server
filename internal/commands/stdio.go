package commands

import (
	"context"
	"fmt"

	"github.com/buildkite/buildkite-mcp-server/pkg/server"
	"github.com/buildkite/buildkite-mcp-server/pkg/toolsets"
	mcpserver "github.com/mark3labs/mcp-go/server"
	"github.com/rs/zerolog/log"
)

type StdioCmd struct {
	APIFlags
	EnabledToolsets []string `help:"Comma-separated list of toolsets to enable (e.g., 'pipelines,builds,clusters'). Use 'all' to enable all toolsets." default:"all" env:"BUILDKITE_TOOLSETS"`
	ReadOnly        bool     `help:"Enable read-only mode, which filters out write operations from all toolsets." default:"false" env:"BUILDKITE_READ_ONLY"`
}

func (c *StdioCmd) Run(ctx context.Context, globals *Globals) error {
	buildkiteClient, err := setupBuildkiteAPIClient(ctx, c.APIFlags, globals.Version)
	if err != nil {
		return fmt.Errorf("stdio server setup: %w", err)
	}

	buildkiteLogsClient, err := setupBuildkiteLogsClient(ctx, c.APIFlags, buildkiteClient)
	if err != nil {
		return fmt.Errorf("stdio server setup: %w", err)
	}

	// Validate the enabled toolsets
	if err := toolsets.ValidateToolsets(c.EnabledToolsets); err != nil {
		return err
	}

	s := server.NewMCPServer(globals.Version, buildkiteClient, buildkiteLogsClient,
		server.WithReadOnly(c.ReadOnly), server.WithToolsets(c.EnabledToolsets...))

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
