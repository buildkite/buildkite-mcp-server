# buildkite-mcp-server

[![Build status](https://badge.buildkite.com/79fefd75bc7f1898fb35249f7ebd8541a99beef6776e7da1b4.svg?branch=main)](https://buildkite.com/buildkite/buildkite-mcp-server)

> **[Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) server exposing Buildkite data (pipelines, builds, jobs, tests) to AI tooling and editors.**

Full documentation is available at [buildkite.com/docs/apis/mcp-server](https://buildkite.com/docs/apis/mcp-server).

---

## Experimental: Tool Search

When using many MCP tools, context usage can become significant. The `--dynamic-toolsets` flag enables [Anthropic's Tool Search pattern](https://www.anthropic.com/engineering/mcp-toolsearch) which marks most tools for on-demand loading.

To use this feature:

1. Start the server with the `--dynamic-toolsets` flag:
   ```bash
   buildkite-mcp-server stdio --dynamic-toolsets
   ```

2. Enable tool search in Claude Code by setting the environment variable:
   ```bash
   export ENABLE_TOOL_SEARCH=true
   ```

With both enabled, tools will be loaded on-demand rather than all at once, significantly reducing context usage. Use the `list_toolsets` tool to browse available categories and `search_tools` to discover specific tools.

**Note:** This feature requires Claude Code support for the `defer_loading` hint. See [claude-code#12836](https://github.com/anthropics/claude-code/issues/12836) for details.

---

## Library Usage

The exported Go API of this module should be considered unstable, and subject to breaking changes as we evolve this project.

---

## Security

To ensure the MCP server is run in a secure environment, we recommend running it in a container.

This image is built from [cgr.dev/chainguard/static](https://images.chainguard.dev/directory/image/static/versions) and runs as an unprivileged user.

---

## Contributing

Development guidelines are in [`DEVELOPMENT.md`](DEVELOPMENT.md).

---

## License

MIT © Buildkite

SPDX-License-Identifier: MIT
