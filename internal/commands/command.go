package commands

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	"github.com/buildkite/buildkite-mcp-server/pkg/trace"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
)

type APIFlags struct {
	APIToken              string   `help:"The Buildkite API token to use." env:"BUILDKITE_API_TOKEN"`
	APITokenFrom1Password string   `help:"The 1Password item to read the Buildkite API token from. Format: 'op://vault/item/field'" env:"BUILDKITE_API_TOKEN_FROM_1PASSWORD"`
	BaseURL               string   `help:"The base URL of the Buildkite API to use." env:"BUILDKITE_BASE_URL" default:"https://api.buildkite.com/"`
	CacheURL              string   `help:"The blob storage URL for job logs cache." env:"BKLOG_CACHE_URL"`
	HTTPHeaders           []string `help:"Additional HTTP headers to send with every request. Format: 'Key: Value'" name:"http-header" env:"BUILDKITE_HTTP_HEADERS"`
}

type Globals struct {
	Version string
	Logger  zerolog.Logger
}

func UserAgent(version string) string {
	os := runtime.GOOS
	arch := runtime.GOARCH

	return fmt.Sprintf("buildkite-mcp-server/%s (%s; %s)", version, os, arch)
}

func ResolveAPIToken(token, tokenFrom1Password string) (string, error) {
	if token != "" && tokenFrom1Password != "" {
		return "", fmt.Errorf("cannot specify both --api-token and --api-token-from-1password")
	}
	if token == "" && tokenFrom1Password == "" {
		return "", fmt.Errorf("must specify either --api-token or --api-token-from-1password")
	}
	if token != "" {
		return token, nil
	}

	// Fetch the token from 1Password
	opToken, err := fetchTokenFrom1Password(tokenFrom1Password)
	if err != nil {
		return "", fmt.Errorf("failed to fetch API token from 1Password: %w", err)
	}
	return opToken, nil
}

func fetchTokenFrom1Password(opID string) (string, error) {
	// read the token using the 1Password CLI with `-n` to avoid a trailing newline
	out, err := exec.Command("op", "read", "-n", opID).Output()
	if err != nil {
		return "", expandExecErr(err)
	}

	log.Info().Msg("Fetched API token from 1Password")

	return string(out), nil
}

func expandExecErr(err error) error {
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return fmt.Errorf("command failed: %s", string(exitErr.Stderr))
	}
	return err
}

func setupBuildkiteAPIClient(cli APIFlags, version string) (*gobuildkite.Client, error) {
	// Parse additional headers into a map
	headers := ParseHeaders(cli.HTTPHeaders)

	// resolve the api token from either the token or 1password flag
	apiToken, err := ResolveAPIToken(cli.APIToken, cli.APITokenFrom1Password)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve Buildkite API token: %w", err)
	}

	client, err := gobuildkite.NewOpts(
		gobuildkite.WithTokenAuth(apiToken),
		gobuildkite.WithUserAgent(UserAgent(version)),
		gobuildkite.WithHTTPClient(trace.NewHTTPClientWithHeaders(headers)),
		gobuildkite.WithBaseURL(cli.BaseURL),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create buildkite client: %w", err)
	}
	return client, nil
}

func setupBuildkiteLogsClient(ctx context.Context, cli APIFlags, buildkiteClient *gobuildkite.Client) (*buildkitelogs.Client, error) {
	// Create ParquetClient with cache URL from flag/env (uses upstream library's high-level client)
	buildkiteLogsClient, err := buildkitelogs.NewClient(
		ctx,
		buildkiteClient,
		cli.CacheURL,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create buildkite logs client: %w", err)
	}
	return buildkiteLogsClient, nil
}
