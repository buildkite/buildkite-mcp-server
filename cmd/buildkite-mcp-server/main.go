package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	"github.com/buildkite/buildkite-mcp-server/internal/commands"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version = "dev"

	cli struct {
		Stdio        commands.StdioCmd `cmd:"" help:"stdio mcp server."`
		HTTP         commands.HTTPCmd  `cmd:"" help:"http mcp server. (pass --use-sse to use SSE transport"`
		Tools        commands.ToolsCmd `cmd:"" help:"list available tools." hidden:""`
		Debug        bool              `help:"Enable debug mode." env:"DEBUG"`
		OTELExporter string            `help:"OpenTelemetry exporter to enable. Options are 'http/protobuf', 'grpc', or 'noop'." enum:"http/protobuf, grpc, noop" env:"OTEL_EXPORTER_OTLP_PROTOCOL" default:"noop"`
		Version      kong.VersionFlag
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

	log.Logger = setupLogger(cli.Debug)

	err := run(ctx, cmd)
	cmd.FatalIfErrorf(err)
}

func run(ctx context.Context, cmd *kong.Context) error {
	tp, err := trace.NewProvider(ctx, cli.OTELExporter, "buildkite-mcp-server", version)
	if err != nil {
		return fmt.Errorf("failed to create trace provider: %w", err)
	}
	defer func() {
		_ = tp.Shutdown(ctx)
	}()

	return cmd.Run(&commands.Globals{Version: version})
}

func setupLogger(debug bool) zerolog.Logger {
	var logger zerolog.Logger
	level := zerolog.InfoLevel
	if debug {
		level = zerolog.DebugLevel
	}

	logger = zerolog.New(os.Stderr).Level(level).With().Timestamp().Stack().Logger()

	// are we in an interactive terminal use a console writer
	if isatty.IsTerminal(os.Stdout.Fd()) {
		logger = logger.Output(zerolog.ConsoleWriter{Out: os.Stderr, FormatTimestamp: func(i any) string {
			return time.Now().Format(time.Stamp)
		}}).Level(level).With().Stack().Logger()
	}

	return logger
}
