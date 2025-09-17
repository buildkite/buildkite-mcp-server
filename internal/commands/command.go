package commands

import (
	"errors"
	"fmt"
	"os/exec"
	"runtime"

	buildkitelogs "github.com/buildkite/buildkite-logs"
	gobuildkite "github.com/buildkite/go-buildkite/v4"
	"github.com/rs/zerolog/log"
)

type Globals struct {
	Client              *gobuildkite.Client
	BuildkiteLogsClient *buildkitelogs.Client
	Version             string
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
