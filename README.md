# buildkite-mcp-server 🚀

[![Build status](https://badge.buildkite.com/79fefd75bc7f1898fb35249f7ebd8541a99beef6776e7da1b4.svg?branch=main)](https://buildkite.com/buildkite/buildkite-mcp-server)

> **[Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) server exposing Buildkite data (pipelines, builds, jobs, tests) to AI tooling and editors.**

---

## ⚡ TL;DR Quick-start

[Create a Buildkite API Token](https://buildkite.com/user/api-access-tokens/new?scopes[]=read_clusters&scopes[]=read_pipelines&scopes[]=read_builds&scopes[]=read_build_logs&scopes[]=read_user&scopes[]=read_organizations&scopes[]=read_artifacts&scopes[]=read_suites)

```bash
# Run via Docker with the token from above
docker run --pull=always -q -it --rm -e BUILDKITE_API_TOKEN=bkua_xxxxx buildkite/mcp-server stdio
```


---

## 🗂️ Table of Contents

- [Prerequisites](#️-prerequisites)
- [API Token Scopes](#-api-token-scopes)
- [Installation](#-installation)
- [Configuration & Usage](#️-configuration--usage)
- [Features](#️-tools--features)
- [Screenshots](#-screenshots)
- [Security](#-security)
- [Contributing](#-contributing)
- [License](#-license)

---

## 🛠️ Prerequisites

| Requirement | Notes |
|-------------|-------|
| Docker ≥ 20.x | Recommended path – run in an isolated container |
| **OR** Go ≥ 1.24 | Needed only for building natively |
| Buildkite API token | Create at https://buildkite.com/user/api-access-tokens |
| Internet access to `ghcr.io` | To pull the pre-built image |

---

## 🔑 API Token Scopes

### Full functionality

👉 **Quick add:** [Create token with Full functionality](https://buildkite.com/user/api-access-tokens/new?scopes[]=read_clusters&scopes[]=read_pipelines&scopes[]=read_builds&scopes[]=read_build_logs&scopes[]=read_user&scopes[]=read_organizations&scopes[]=read_artifacts&scopes[]=read_suites)

| Scope | Purpose |
|-------|---------|
| `read_clusters` | Access cluster & queue information |
| `read_pipelines` | Pipeline configuration |
| `read_builds` | Builds, jobs & annotations |
| `read_build_logs` | Job log output |
| `read_user` | Current user info |
| `read_organizations` | Organization details |
| `read_artifacts` | Build artifacts & metadata |
| `read_suites` | Buildkite Test Engine data |

### Minimum recommended

👉 **Quick add:** [Create token with Basic functionality](https://buildkite.com/user/api-access-tokens/new?scopes[]=read_builds&scopes[]=read_pipelines&scopes[]=read_user)

| Scope | Purpose |
|-------|---------|
| `read_builds` | Builds, jobs & annotations |
| `read_pipelines` | Pipeline information |
| `read_user` | User identification |

---

## 📦 Installation

### 1. Docker (recommended)

```bash
docker pull buildkite/mcp-server
```

Run:

```bash
docker run --pull=always -q -it --rm -e BUILDKITE_API_TOKEN=bkua_xxxxx buildkite/mcp-server stdio
```

### 2. Pre-built binary

Download the latest release from [GitHub Releases](https://github.com/buildkite/buildkite-mcp-server/releases). Binaries are fully-static and require no libc.

If you're on macOS, you can use [Homebrew](https://brew.sh):

```sh
brew install buildkite/buildkite/buildkite-mcp-server
```

### 3. Build from source

```bash
go install github.com/buildkite/buildkite-mcp-server@latest
# or
goreleaser build --snapshot --clean
# or
make build    # uses goreleaser (snapshot)
```

### 4. Docker Desktop

[![Add to Docker Desktop](https://img.shields.io/badge/Add%20to%20Docker%20Desktop-17191e?style=flat&logo=docker)](https://hub.docker.com/open-desktop?url=https://open.docker.com/dashboard/mcp/servers/id/buildkite/config?enable=true)

```sh
docker mcp server enable buildkite
```

View on [Docker MCP Hub](https://hub.docker.com/mcp/server/buildkite)

---

## ⚙️ Configuration & Usage

<!-- Keep this alphabetical -->

<details>
<summary><a href="https://ampcode.com">Amp</a></summary>

Docker (recommended):

```jsonc
# ~/.config/amp/settings.json
{
  "amp.mcpServers": {
    "buildkite": {
      "command": "docker",
      "args": [
        "run", "--pull=always", "-q",
        "-i", "--rm", "-e", "BUILDKITE_API_TOKEN",
        "buildkite/mcp-server", "stdio"
      ],
      "env": { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx" }
    }
  }
}
```

Local binary, with the [Job Log Token Threshold](#job-log-token-threshold) flag enabled:

```jsonc
# ~/.config/amp/settings.json
{
  "amp.mcpServers": {
    "buildkite": {
      "command": "buildkite-mcp-server",
      "args": ["stdio"],
      "env": { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx", "JOB_LOG_TOKEN_THRESHOLD": "2000" }
    }
  }
}
```
</details>

<details>
<summary>Claude Code</summary>

Docker (recommended):

```
claude mcp add buildkite -- docker run --pull=always -q --rm -i -e BUILDKITE_API_TOKEN=bkua_xxxxxxxx buildkite/mcp-server stdio
```

Local binary:

```
claude mcp add buildkite --env BUILDKITE_API_TOKEN=bkua_xxxxxxxx -- buildkite-mcp-server stdio
```
</details>

<details>
<summary>Claude Desktop</summary>

Docker (recommended):

```jsonc
{
  "mcpServers": {
    "buildkite": {
      "command": "docker",
      "args": [
        "run", "--pull=always", "-q",
        "-i", "--rm", "-e", "BUILDKITE_API_TOKEN",
        "buildkite/mcp-server", "stdio"
      ],
      "env": { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx" }
    }
  }
}
```

Local binary, with the [Job Log Token Threshold](#job-log-token-threshold) flag enabled:

```jsonc
{
  "mcpServers": {
    "buildkite": {
      "command": "buildkite-mcp-server",
      "args": ["stdio"],
      "env": { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx", "JOB_LOG_TOKEN_THRESHOLD": "2000" }
    }
  }
}
```
</details>

<details>
<summary>Cursor</summary>

[Add Buildkite MCP Server to Cursor](cursor://anysphere.cursor-deeplink/mcp/install?name=buildkite&config=eyJjb21tYW5kIjoiZG9ja2VyIHJ1biAtaSAtLXJtIC1lIEJVSUxES0lURV9BUElfVE9LRU4gZ2hjci5pby9idWlsZGtpdGUvYnVpbGRraXRlLW1jcC1zZXJ2ZXIgc3RkaW8ifQ%3D%3D)

Docker (recommended):

```jsonc
{
  "buildkite": {
    "command": "docker",
    "args": [
      "run", "--pull=always", "-q",
      "-i", "--rm",
      "-e", "BUILDKITE_API_TOKEN",
      "buildkite/mcp-server",
      "stdio"
    ]
  }
}
```

Local binary:

```jsonc
{
  "buildkite": {
    "command": "buildkite-mcp-server",
    "args": ["stdio"],
    "env": { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx" }
  }
}
```

Optional (Local binary with [Job Log Token Threshold](#job-log-token-threshold)):

```jsonc
{
  "buildkite": {
    "command": "buildkite-mcp-server",
    "args": ["stdio"],
    "env": {
      "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx",
      "JOB_LOG_TOKEN_THRESHOLD": "2000"
    }
  }
}
```

</details>

<details>
<summary><a href="https://block.github.io/goose/">Goose</a></summary>

Docker (recommended):

```yaml
extensions:
  fetch:
    name: Buildkite
    cmd: docker
    args: ["run", "--pull=always", "-q", "-i", "--rm", "-e", "BUILDKITE_API_TOKEN", "buildkite/mcp-server", "stdio"]
    enabled: true
    envs: { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx" }
    type: stdio
    timeout: 300
```

Local binary, with the [Job Log Token Threshold](#job-log-token-threshold) flag enabled:

```yaml
extensions:
  fetch:
    name: Buildkite
    cmd: buildkite-mcp-server
    args: [stdio]
    enabled: true
    envs: { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx", "JOB_LOG_TOKEN_THRESHOLD": "2000" }
    type: stdio
    timeout: 300
```
</details>
<details>
<summary>VS Code</summary>

```jsonc
{
  "inputs": [
    {
      "id": "BUILDKITE_API_TOKEN",
      "type": "promptString",
      "description": "Enter your Buildkite Access Token",
      "password": true
    }
  ],
  "servers": {
    "buildkite": {
      "command": "docker",
      "args": [
        "run", "--pull=always", "-q", 
        "-i", "--rm", "-e", "BUILDKITE_API_TOKEN",
        "buildkite/mcp-server", "stdio"
      ],
      "env": { "BUILDKITE_API_TOKEN": "${input:BUILDKITE_API_TOKEN}" }
    }
  }
}
```
</details>
<details>
<summary>Windsurf</summary>

```jsonc
{
  "mcpServers": {
    "buildkite": {
      "command": "docker",
      "args": [
        "run", "--pull=always", "-q", 
        "-i", "--rm", "-e", "BUILDKITE_API_TOKEN",
        "buildkite/mcp-server", "stdio"
      ],
      "env": { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx" }
    }
  }
}
```

Local binary, with the [Job Log Token Threshold](#job-log-token-threshold) flag enabled:

```jsonc
{
  "mcpServers": {
    "buildkite": {
      "command": "buildkite-mcp-server",
      "args": ["stdio"],
      "env": { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx", "JOB_LOG_TOKEN_THRESHOLD": "2000" }
    }
  }
}
```

</details>

<details>
<summary>Zed</summary>

There is a Zed [editor extension](https://zed.dev) available in the [official extension gallery](https://zed.dev/extensions?query=buildkite). During installation it will ask for an API token which will be added to your settings.

Or you can manually configure:

```jsonc
// ~/.config/zed/settings.json
{
  "context_servers": {
    "mcp-server-buildkite": {
      "settings": {
        "buildkite_api_token": "your-buildkite-token-here"
      }
    }
  }
}
```
</details>

---

## 🔧 Environment Variables

| Variable | Description | Default | Usage |
|----------|-------------|---------|-------|
| `BUILDKITE_API_TOKEN` | Your Buildkite API access token | Required | Authentication for all API requests |
| `HTTP_LISTEN_ADDR` | Address for HTTP server to listen on | `localhost:3000` | Used with `http` command |

---

<a name="tools"></a>
<a name="features"></a>
## 🛠️ Tools & Features

| Tool | Description |
|------|-------------|
| `get_cluster` | Get detailed information about a specific cluster including its name, description, default queue, and configuration |
| `list_clusters` | List all clusters in an organization with their names, descriptions, default queues, and creation details |
| `get_cluster_queue` | Get detailed information about a specific queue including its key, description, dispatch status, and hosted agent configuration |
| `list_cluster_queues` | List all queues in a cluster with their keys, descriptions, dispatch status, and agent configuration |
| `get_pipeline` | Get detailed information about a specific pipeline including its configuration, steps, environment variables, and build statistics |
| `list_pipelines` | List all pipelines in an organization with their basic details, build counts, and current status |
| `create_pipeline` | Set up a new CI/CD pipeline in Buildkite with YAML configuration, repository connection, and cluster assignment |
| `update_pipeline` | Modify an existing Buildkite pipeline's configuration, repository, settings, or metadata |
| `list_builds` | List all builds for a pipeline with their status, commit information, and metadata |
| `get_build` | Get detailed information about a specific build including its jobs, timing, and execution details |
| `get_build_test_engine_runs` | Get test engine runs data for a specific build in Buildkite. This can be used to look up Test Runs. |
| `create_build` | Trigger a new build on a Buildkite pipeline for a specific commit and branch, with optional environment variables, metadata, and author information |
| `current_user` | Get details about the user account that owns the API token, including name, email, avatar, and account creation date |
| `user_token_organization` | Get the organization associated with the user token used for this request |
| `get_jobs` | Get all jobs for a specific build including their state, timing, commands, and execution details |
| `list_artifacts` | List all artifacts for a build across all jobs, including file details, paths, sizes, MIME types, and download URLs |
| `get_artifact` | Get detailed information about a specific artifact including its metadata, file size, SHA-1 hash, and download URL |
| `list_annotations` | List all annotations for a build, including their context, style (success/info/warning/error), rendered HTML content, and creation timestamps |
| `list_test_runs` | List all test runs for a test suite in Buildkite Test Engine |
| `get_test_run` | Get a specific test run in Buildkite Test Engine |
| `get_failed_executions` | Get failed test executions for a specific test run in Buildkite Test Engine. Optionally get the expanded failure details such as full error messages and stack traces. |
| `get_test` | Get a specific test in Buildkite Test Engine. This provides additional metadata for failed test executions |
| `search_logs` | Search log entries using regex patterns with optional context lines |
| `tail_logs` | Show the last N entries from the log file |
| `get_logs_info` | Get metadata and statistics about the Parquet log file |
| `read_logs` | Read log entries from the file, optionally starting from a specific row number |
| `access_token` | Get information about the current API access token including its scopes and UUID |

---

## 🔍 Job Log Analysis Tools

Inspect Buildkite job logs in milliseconds, with full-text search, tail, and structured reads – all from one endpoint.

The server ships with four log analysis tools that convert Buildkite job output to structured Parquet data for efficient querying:

- **`search_logs`** – Regex search with context lines for debugging failures
- **`tail_logs`** – Show last N lines for recent errors and status checks
- **`read_logs`** – Stream log entries from specific positions
- **`get_logs_info`** – File metadata and statistics before reading content

### Smart Caching & Storage

The first request downloads and converts logs to Parquet format; subsequent requests are zero-API calls with near-instant response times. All tools return token-efficient JSON by default for optimal AI/LLM performance.

| Environment | Default Cache Location |
|-------------|----------------------|
| Desktop/Laptop | `file://$HOME/.bklog` |
| Docker/K8s/CI | `file:///tmp/bklog` |
| Custom override | `$BKLOG_CACHE_URL` (any [gocloud URL](https://gocloud.dev/concepts/urls/)) |

> **💡 Zero-config setup**: Don't set anything for local testing—the server auto-picks the right directory. Set `BKLOG_CACHE_URL` to override with S3 (`s3://bucket/path`), GCS, Azure, or custom storage backends.

**Examples:**
```bash
# Local development with persistent cache
export BKLOG_CACHE_URL="file:///Users/me/bklog-cache"

# Shared cache across build agents
export BKLOG_CACHE_URL="s3://ci-logs-cache/buildkite/"
```

---

## 🌐 Streamable HTTP / SSE transport

You can also run the MCP server using the Streamable HTTP Transport, and connect to the MCP server at <http://localhost:3000/mcp>.

```sh
buildkite-mcp-server http --api-token=${BUILDKITE_API_TOKEN}
```

Or with the legacy HTTP/SSE transport, and connect to the MCP server at <http://localhost:3000/sse>.

```sh
buildkite-mcp-server http --use-sse --api-token=${BUILDKITE_API_TOKEN}
```

You can also set the listen address via environment variable:

```sh
HTTP_LISTEN_ADDR="localhost:4321" buildkite-mcp-server http
```

To run the server with Streamable HTTP transport in a docker and expose on port 3000.

```sh
docker run --pull=always -q --rm -e BUILDKITE_API_TOKEN -e HTTP_LISTEN_ADDR=":3000" -p 127.0.0.1:3000:3000 buildkite/mcp-server http
```

> [!CAUTION]
> By default, [Docker will bind published ports on all interfaces](https://docs.docker.com/engine/network/#published-ports), making your MCP server accessible from any devices on your local network. We recommend using the default stdio transport when used locally, but if you must use HTTP/SSE, binding the forwarded port to `127.0.0.1`, as in the example above will prevent other devices on your local network from accessing the server.

## 📸 Screenshots

![Get Pipeline Tool](docs/images/get_pipeline.png)

---

## 🤖 AGENTS.md

We recommend adding a hint to your `AGENTS.md`, or equivalent agent configuration file, for example `CLAUDE.md` ect. This will typically be under an architecture section.

This hint will orientate the agent towards using the buildkite MCP to quickly diagnose build issues, or return project level CI/CD insights quickly. You should replace the organization, pipeline slug(s) and pipeline files based on your project.

```
- **CI/CD**: `buildkite` organization, `buildkite-mcp-server` pipeline slug for build and test (`.buildkite/pipeline.yml`), `buildkite-mcp-server-release` pipeline slug for releases (`.buildkite/pipeline.release.yml`)
```

---

## 📚 Library Usage

The exported Go API of this module should be considered unstable, and subject to breaking changes as we evolve this project.

---

## 🔒 Security

To ensure the MCP server is run in a secure environment, we recommend running it in a container.

This image is built from [cgr.dev/chainguard/static](https://images.chainguard.dev/directory/image/static/versions) and runs as an unprivileged user.

---

## 🤝 Contributing

Development guidelines are in [`DEVELOPMENT.md`](DEVELOPMENT.md).

Run the test suite:

```bash
go test ./...
```

---

## 📝 License

MIT © Buildkite

SPDX-License-Identifier: MIT
