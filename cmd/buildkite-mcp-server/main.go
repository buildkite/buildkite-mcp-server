package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/alecthomas/kong"
	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-mcp-server/internal/commands"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/mattn/go-isatty"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

var (
	version = "dev"

	cli struct {
		Stdio                 commands.StdioCmd `cmd:"" help:"stdio mcp server."`
		HTTP                  commands.HTTPCmd  `cmd:"" help:"http mcp server. (pass --use-sse to use SSE transport"`
		Tools                 commands.ToolsCmd `cmd:"" help:"list available tools." hidden:""`
		APIToken              string            `help:"The Buildkite API token to use." env:"BUILDKITE_API_TOKEN"`
		APITokenFrom1Password string            `help:"The 1Password item to read the Buildkite API token from. Format: 'op://vault/item/field'" env:"BUILDKITE_API_TOKEN_FROM_1PASSWORD"`
		BaseURL               string            `help:"The base URL of the Buildkite API to use." env:"BUILDKITE_BASE_URL" default:"https://api.buildkite.com/"`
		CacheURL              string            `help:"The blob storage URL for job logs cache." env:"BKLOG_CACHE_URL"`
		Debug                 bool              `help:"Enable debug mode." env:"DEBUG"`
		OTELExporter          string            `help:"OpenTelemetry exporter to enable. Options are 'http/protobuf', 'grpc', or 'noop'." enum:"http/protobuf, grpc, noop" env:"OTEL_EXPORTER_OTLP_PROTOCOL" default:"noop"`
		HTTPHeaders           []string          `help:"Additional HTTP headers to send with every request. Format: 'Key: Value'" name:"http-header" env:"BUILDKITE_HTTP_HEADERS"`
		Version               kong.VersionFlag
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

	// Parse additional headers into a map
	headers := commands.ParseHeaders(cli.HTTPHeaders)

	// resolve the api token from either the token or 1password flag
	apiToken, err := commands.ResolveAPIToken(cli.APIToken, cli.APITokenFrom1Password)
	if err != nil {
		return fmt.Errorf("failed to resolve Buildkite API token: %w", err)
	}

	client, err := gobuildkite.NewOpts(
		gobuildkite.WithTokenAuth(apiToken),
		gobuildkite.WithUserAgent(commands.UserAgent(version)),
		gobuildkite.WithHTTPClient(trace.NewHTTPClientWithHeaders(headers)),
		gobuildkite.WithBaseURL(cli.BaseURL),
	)
	if err != nil {
		return fmt.Errorf("failed to create buildkite client: %w", err)
	}

	// Create ParquetClient with cache URL from flag/env (uses upstream library's high-level client)
	buildkiteLogsClient, err := buildkitelogs.NewClient(ctx, client, cli.CacheURL)
	if err != nil {
		return fmt.Errorf("failed to create buildkite logs client: %w", err)
	}

	buildkiteLogsClient.Hooks().AddAfterCacheCheck(func(ctx context.Context, result *buildkitelogs.CacheCheckResult) {
		log.Ctx(ctx).Debug().Str("org", result.Org).Str("pipeline", result.Pipeline).Str("build", result.Build).Str("job", result.Job).Dur("time_taken", result.Duration).Msg("Checked job logs cache")
	})

	buildkiteLogsClient.Hooks().AddAfterLogDownload(func(ctx context.Context, result *buildkitelogs.LogDownloadResult) {
		log.Ctx(ctx).Debug().Str("org", result.Org).Str("pipeline", result.Pipeline).Str("build", result.Build).Str("job", result.Job).Dur("time_taken", result.Duration).Msg("Downloaded and cached job logs")
	})

	buildkiteLogsClient.Hooks().AddAfterLogParsing(func(ctx context.Context, result *buildkitelogs.LogParsingResult) {
		log.Ctx(ctx).Debug().Str("org", result.Org).Str("pipeline", result.Pipeline).Str("build", result.Build).Str("job", result.Job).Dur("time_taken", result.Duration).Msg("Parsed logs to Parquet")
	})

	buildkiteLogsClient.Hooks().AddAfterBlobStorage(func(ctx context.Context, result *buildkitelogs.BlobStorageResult) {
		log.Ctx(ctx).Debug().Str("org", result.Org).Str("pipeline", result.Pipeline).Str("build", result.Build).Str("job", result.Job).Dur("time_taken", result.Duration).Msg("Stored logs to blob storage")
	})

	return cmd.Run(&commands.Globals{Version: version, Client: client, BuildkiteLogsClient: buildkiteLogsClient})
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
