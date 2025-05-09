package main

import (
	"context"
	"os"

	"github.com/alecthomas/kong"
	"github.com/buildkite/buildkite-mcp-server/internal/commands"
	"github.com/buildkite/buildkite-mcp-server/internal/trace"
	"github.com/buildkite/go-buildkite/v4"
	"github.com/rs/zerolog"
)

var (
	version = "dev"

	cli struct {
		Stdio    commands.StdioCmd `cmd:"" help:"stdio mcp server."`
		APIToken string            `help:"The Buildkite API token to use." env:"BUILDKITE_API_TOKEN"`
		Debug    bool              `help:"Enable debug mode."`
		Version  kong.VersionFlag
	}
)

func main() {
	ctx := context.Background()

	cmd := kong.Parse(&cli,
		kong.Name("buildkite-mcp-server"),
		kong.Description("A server that proxies requests to the Buildkite API."),
		kong.UsageOnError(),
		kong.Vars{
			"version": version,
		},
		kong.BindTo(ctx, (*context.Context)(nil)),
	)

	logger := zerolog.New(os.Stderr).With().Timestamp().Logger()

	if cli.Debug {
		logger = logger.Level(zerolog.DebugLevel).With().Caller().Logger()
	}

	tp, err := trace.NewProvider(ctx, "buildkite-mcp-server", version)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create trace provider")
	}
	defer func() {
		_ = tp.Shutdown(ctx)
	}()

	client, err := buildkite.NewOpts(
		buildkite.WithTokenAuth(cli.APIToken),
		buildkite.WithUserAgent(commands.UserAgent(version)),
		buildkite.WithHTTPClient(trace.NewHTTPClient()),
	)
	if err != nil {
		logger.Fatal().Err(err).Msg("failed to create buildkite client")
	}

	err = cmd.Run(&commands.Globals{Version: version, Client: client, Logger: logger})
	cmd.FatalIfErrorf(err)
}
