# Development

This contains some notes on developing this software locally.

# prerequisites

* [goreleaser](http://goreleaser.com)
* [go 1.24](https://go.dev)

# building

List the available make targets.

```
make help
```

## Local Build

Build the binary locally.

```bash
make build
```

## Check the code

Check the code for style and correctness and running tests.

```bash
make check
```

## Copy it to your path

Copy it to your path.

## Docker

### Local Development

Build the Docker image using the local development Dockerfile:

```bash
docker build -t buildkite/buildkite-mcp-server:dev -f Dockerfile.local .
```

Run the container:

```bash
docker run -i --rm -e BUILDKITE_API_TOKEN="your-token" buildkite/buildkite-mcp-server:dev
```

# Adding a new Tool

1. Implement a tool following the patterns in the [internal/buildkite](internal/buildkite) package - mostly delegating to [go-buildkite](https://github.com/buildkite/go-buildkite) and returning JSON. We can play with nicer formatting later and see if it helps.
2. Register the tool here in the [internal/stdio](internal/commands/stdio.go) file.
3. Update the README tool list.
4. Profit!

# Validating tools locally

When developing and testing the tools, and verifying their configuration https://github.com/modelcontextprotocol/inspector is very helpful.

```
make
npx @modelcontextprotocol/inspector@latest buildkite-mcp-server stdio
```

Then log into the web UI and hit connect.

# Publishing a release

- Draft a new release on GitHub: https://github.com/buildkite/buildkite-mcp-server/releases/new
- Select a new tag version, bumping the minor or patch versions as appropriate. This project is pre-1.0, so we don't make strong compatibility guarantees.
- Generate release notes
- Save the release as a draft, and mention internal contributors on Slack before publishing
- Publish the release

A Buildkite pipeline will then automatically invoke the publishing pipeline, including publishing to GitHub Container Registry, Docker Hub, and update binaries to the GitHub release assets.

# Manually releasing to GitHub Container Registry

This process is automated by the CI pipeline, however you can manually release by following these steps:

To push docker images GHCR you will need to login, you will need to generate a legacy GitHub PSK to do a release locally. This will be entered in the command below.

```
docker login ghcr.io --username $(gh api user --jq '.login')
```

Publish a release in GitHub, use the "generate changelog" button to build the changelog, this will create a tag for the release.

Fetch tags and pull down the `main` branch, then run GoReleaser at the root of the repository.

```
git fetch && git pull
GITHUB_TOKEN=$(gh auth token) goreleaser release
```

# Recording and replaying API calls for offline evals

The server can record every Buildkite API call it makes to an [HTTP Archive (HAR)](https://en.wikipedia.org/wiki/HAR_(file_format)) file, then replay that file later without a network connection. This is useful for running LLM evals reproducibly — record one real session, then run multiple models (or prompt variants) against the exact same API responses.

## Record a session

Pass `--record <path>` when starting the server. The file is created immediately (to catch permission errors early) and each API response is appended as it is made.

```bash
BUILDKITE_API_TOKEN=bkua_xxx buildkite-mcp-server stdio --record ./testdata/my-scenario.har
```

Drive the server with your MCP client or an LLM as normal. When the session ends the HAR file contains every request/response pair.

A few things to note about the recorded file:
- `Authorization` headers are stripped before writing, so the file is safe to commit.
- Binary responses (gzip logs, artifacts) are stored as base64 with `"encoding": "base64"`.
- POST/PUT request bodies are stored in `postData` so distinct writes to the same endpoint are matched correctly on replay.

## Replay offline

Pass `--replay <path>` to serve responses from a previously recorded HAR file. No API token is required.

```bash
buildkite-mcp-server stdio --replay ./testdata/my-scenario.har
```

Replay matches requests by **method + URL** (plus request body for write methods), not by call order, so the LLM can reach the same endpoints in any sequence. If the same URL appears more than once in the HAR (e.g. paginated requests), each call consumes the next recorded entry for that URL in the order they were recorded.

The server returns a clear error if a tool makes a request for which no HAR entry exists, making it easy to detect when a scenario is incomplete.

## Creating error scenarios

Because the HAR format is plain JSON you can hand-edit a recorded file to simulate failure cases:

- Change a `"status": 200` to `"status": 404` and update the `"text"` body.
- Delete an entry entirely to trigger a "no recorded entry" error for that call.
- Duplicate an entry to simulate a retry.

Standard HAR viewers (Chrome DevTools, [HAR Analyzer](https://toolbox.googleapps.com/apps/har_analyzer/)) can open the files for inspection.

# Tracing

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
