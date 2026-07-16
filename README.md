# buildkite-mcp-server

[![Build status](https://badge.buildkite.com/79fefd75bc7f1898fb35249f7ebd8541a99beef6776e7da1b4.svg?branch=main)](https://buildkite.com/buildkite/buildkite-mcp-server)

> **[Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) server exposing Buildkite data (pipelines, builds, jobs, tests) to AI tooling and editors.**

Full documentation is available at [buildkite.com/docs/apis/mcp-server](https://buildkite.com/docs/apis/mcp-server).

---

## Library Usage

The exported Go API of this module should be considered unstable, and subject to breaking changes as we evolve this project.

---

## Security

To ensure the MCP server is run in a secure environment, we recommend running it in a container.

This image is built from [cgr.dev/chainguard/static](https://images.chainguard.dev/directory/image/static/versions) and runs as an unprivileged user.

### Passing identity headers through HTTP mode

Self-hosted HTTP deployments can forward selected headers from each inbound MCP request to the Buildkite API:

```bash
BUILDKITE_API_TOKEN=bkua_xxx \
  buildkite-mcp-server http \
  --passthrough-http-header X-User-Identity
```

Repeat `--passthrough-http-header` to allow more than one header, or set a comma-separated `BUILDKITE_PASSTHROUGH_HTTP_HEADERS` value. Only explicitly allowed headers are forwarded, and only to the origin configured by `BUILDKITE_BASE_URL`. They are removed from requests redirected elsewhere.

To authenticate each MCP request with its own Buildkite API token, allow `Authorization` and omit the process-wide token:

```bash
BUILDKITE_PASSTHROUGH_HTTP_HEADERS=Authorization \
  buildkite-mcp-server http
```

In this mode every `/mcp` request must contain exactly one non-empty `Authorization` header. Missing credentials return HTTP 401; the server never falls back to a shared API token. The reverse proxy in front of the MCP server is responsible for authenticating callers and setting or validating any forwarded identity headers.

Header passthrough is not available in stdio mode. Before serving job logs, the server verifies that the current caller can access the job log. This check is performed for every log-tool request, including when the log data is already cached.

---

## Contributing

Development guidelines are in [`DEVELOPMENT.md`](DEVELOPMENT.md).

---

## License

MIT © Buildkite

SPDX-License-Identifier: MIT
