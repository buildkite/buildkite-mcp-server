# Development

This guide covers building, testing, and running the Buildkite MCP server locally.

## Quick start

Install these prerequisites:

- [Git](https://git-scm.com/)
- [mise](https://mise.jdx.dev/)
- Make

Then clone and set up the repository:

```bash
git clone https://github.com/buildkite/buildkite-mcp-server.git
cd buildkite-mcp-server
mise trust
mise install
mise run setup
```

`mise install` installs the development toolchain pinned in `mise.toml`. This
project uses Go 1.26.6 for development and CI builds. The older Go version in
`go.mod` is intentionally retained as the minimum compatible toolchain version.

Verify the checkout and build the server:

```bash
mise run check
mise run build
```

The binary is written to `./buildkite-mcp-server`.

## Authentication and running locally

API-backed tools require a [Buildkite API access token](https://buildkite.com/user/api-access-tokens). Export the token in your shell; do not commit it to the repository:

```bash
export BUILDKITE_API_TOKEN="bkua_xxx"
mise run run
```

The server uses stdio transport by default. It may appear idle while it waits for
an MCP client to send requests. A token is not required when using offline replay.

The tracked `.envrc` contains an optional, commented 1Password command for
Buildkite employees who use `direnv`. Other contributors should export the token
through their preferred local secret manager or shell environment.

## Development commands

List the available mise tasks and Make targets:

```bash
mise tasks ls --local
mise exec -- make help
```

The primary commands are:

| Command | Purpose |
| --- | --- |
| `mise run setup` | Download Go modules and install the Git hooks |
| `mise run check` | Run lint, module tidiness, Go fix, and tests |
| `mise run lint-fix` | Run the linter and apply supported fixes |
| `mise run build` | Build `./buildkite-mcp-server` |
| `mise run run` | Run the server over stdio |

Use `mise exec -- make <target>` for less common Make targets. Running commands
through mise ensures they use the repository's pinned toolchain.

## Git hooks

`mise run setup` installs Lefthook's pre-commit hook. The hook runs lint,
`go mod tidy -diff`, and `go fix -diff ./...` using the shared Make targets.

Reinstall the hook directly if needed:

```bash
mise exec -- lefthook install
```

## Installing the binary

Install the binary into the Go binary directory:

```bash
mise exec -- make install
```

Ensure `$(go env GOPATH)/bin` is on `PATH` if you want to invoke
`buildkite-mcp-server` without an explicit path.

## Docker

Docker is optional for normal development.

### Local development

Build the Docker image using the local development Dockerfile:

```bash
docker build -t buildkite/buildkite-mcp-server:dev -f Dockerfile.local .
```

Run the container:

```bash
docker run -i --rm -e BUILDKITE_API_TOKEN="your-token" buildkite/buildkite-mcp-server:dev
```

## Adding a new tool

1. Implement the tool following the patterns in [`pkg/buildkite`](pkg/buildkite), generally delegating to [go-buildkite](https://github.com/buildkite/go-buildkite) and returning JSON.
2. Add the tool to the appropriate toolset in [`pkg/toolsets`](pkg/toolsets).
3. Add or update tests.
4. Update the tool documentation.
5. Run `mise run check`.

## Validating tools with MCP Inspector

[MCP Inspector](https://github.com/modelcontextprotocol/inspector) is useful for exercising tools and verifying their schemas. Node.js is included in the mise toolchain for this optional workflow.

```bash
mise run build
mise exec -- npx @modelcontextprotocol/inspector@latest ./buildkite-mcp-server stdio
```

Open the displayed web UI and select **Connect**. The package is fetched by
`npx`; it is not required for builds or tests.

## Recording and replaying API calls for offline evals

The server can record every Buildkite API call it makes to an [HTTP Archive (HAR)](https://en.wikipedia.org/wiki/HAR_(file_format)) file, then replay that file later without a network connection. This is useful for running LLM evals reproducibly — record one real session, then run multiple models (or prompt variants) against the exact same API responses.

### Record a session

Pass `--record <path>` when starting the server. The file is created immediately (to catch permission errors early) and each API response is appended as it is made.

```bash
BUILDKITE_API_TOKEN=bkua_xxx buildkite-mcp-server stdio --record ./testdata/my-scenario.har
```

Drive the server with your MCP client or an LLM as normal. When the session ends the HAR file contains every request/response pair.

A few things to note about the recorded file:
- `Authorization` headers are stripped before writing, but HAR files may still contain sensitive request and response data.
- Only commit HAR files containing secret values when they are required as test fixtures. Replace every real secret with a synthetic test value first; never commit real credentials or secret values.
- Binary responses (gzip logs, artifacts) are stored as base64 with `"encoding": "base64"`.
- POST/PUT request bodies are stored in `postData` so distinct writes to the same endpoint are matched correctly on replay.

### Replay offline

Pass `--replay <path>` to serve responses from a previously recorded HAR file. No API token is required.

```bash
buildkite-mcp-server stdio --replay ./testdata/my-scenario.har
```

Replay matches requests by **method + URL** (plus request body for write methods), not by call order, so the LLM can reach the same endpoints in any sequence. If the same URL appears more than once in the HAR (e.g. paginated requests), each call consumes the next recorded entry for that URL in the order they were recorded.

The server returns a clear error if a tool makes a request for which no HAR entry exists, making it easy to detect when a scenario is incomplete.

### Creating error scenarios

Because the HAR format is plain JSON you can hand-edit a recorded file to simulate failure cases:

- Change a `"status": 200` to `"status": 404` and update the `"text"` body.
- Delete an entry entirely to trigger a "no recorded entry" error for that call.
- Duplicate an entry to simulate a retry.

Standard HAR viewers (Chrome DevTools, [HAR Analyzer](https://toolbox.googleapps.com/apps/har_analyzer/)) can open the files for inspection.

### Known limitations

- **Full-file rewrite on every request.** Each API call re-marshals and rewrites the entire HAR file. This is fine for typical eval sessions (tens to low hundreds of calls) but will slow down recording for very large sessions. A future improvement would be to append a JSON line and only rewrite on close.

- **Job log blob storage is not captured.** Recording intercepts HTTP calls made through the Buildkite API client transport only. Job log fetches that go through `BKLOG_CACHE_URL` (the gocloud blob storage path) use a separate HTTP client and will not appear in the HAR. Evals that exercise log tools with caching enabled may therefore behave differently between record and replay — the log fetch will succeed during recording (real network) but fail during replay (no entry in the HAR). Disable the cache (`BKLOG_CACHE_URL` unset) when recording sessions intended for log-tool evals.

- **Transport errors are not recorded.** Only requests that receive an HTTP response are written to the HAR. If the underlying transport returns an error (connection refused, timeout, DNS failure), the call is not captured and the error is returned to the caller as normal. Replay cannot reproduce those failure modes.

## Tracing

To enable tracing in the MCP server you need to add some environment variables in the configuration, the example below is showing the claude desktop configuration paired with [honeycomb](https://honeycomb.io), however any OTEL service will work as long as it supports GRPC.

```json
{
    "mcpServers": {
        "buildkite": {
            "command": "buildkite-mcp-server",
            "args": [
                "stdio"
            ],
            "env": {
                "BUILDKITE_API_TOKEN": "bkua_xxxxx",
                "OTEL_SERVICE_NAME": "buildkite-mcp-server",
                "OTEL_EXPORTER_OTLP_PROTOCOL": "grpc",
                "OTEL_EXPORTER_OTLP_ENDPOINT": "https://api.honeycomb.io:443",
                "OTEL_EXPORTER_OTLP_HEADERS":"x-honeycomb-team=xxxxxx"
            }
        }
    }
}
```

## Maintainer release process

GoReleaser, `ko`, GitHub CLI, and Docker are included in or used alongside the
mise toolchain for release workflows. They are not required for normal
development.

### Publishing a release

1. Draft a [new GitHub release](https://github.com/buildkite/buildkite-mcp-server/releases/new).
2. Select a new tag, incrementing the minor or patch version as appropriate.
3. Generate and review the release notes. Agent Skills-compatible coding agents
   can invoke the repository's `release-notes` skill with the target version. Use
   a recent curated release as the layout reference, verify every pull request in
   the comparison range is represented, and review the complete Markdown before
   updating GitHub.
4. Save the release as a draft and notify internal contributors before publishing.
5. Publish the release.

The Buildkite pipeline publishes images to GitHub Container Registry and Docker
Hub, then adds binaries to the GitHub release.

### Manual GitHub Container Registry release

The CI pipeline normally performs this process. If a manual release is required,
authenticate GitHub CLI and Docker first:

```bash
docker login ghcr.io --username "$(gh api user --jq '.login')"
```

After publishing the GitHub release and its tag, update the local `main` branch
and run GoReleaser:

```bash
git fetch origin
git pull --ff-only origin main
GITHUB_TOKEN="$(gh auth token)" mise exec -- goreleaser release
```
