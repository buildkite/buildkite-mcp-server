# buildkite-mcp-server

[![Build status](https://badge.buildkite.com/79fefd75bc7f1898fb35249f7ebd8541a99beef6776e7da1b4.svg?branch=main)](https://buildkite.com/buildkite/buildkite-mcp-server)

This is an [Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) server for [Buildkite](https://buildkite.com). The goal is to provide access to information from buildkite about pipelines, builds and jobs to tools such as [Claude Desktop](https://claude.ai/download), [GitHub Copilot](https://github.com/features/copilot) and other tools, or editors.

# Tools

* `get_cluster` - Get details of a buildkite cluster in an organization
* `list_clusters` - List all buildkite clusters in an organization
* `get_cluster_queue` - Get details of a buildkite cluster queue in an organization
* `list_cluster_queues` - List all buildkite queues in a cluster
* `get_pipeline` - Get details of a specific pipeline in Buildkite
* `list_pipelines` - List all pipelines in a buildkite organization
* `list_builds` - List all builds in a pipeline in Buildkite
* `get_build` - Get a build in Buildkite
* `current_user` - Get the current user
* `user_token_organization` - Get the organization associated with the user token used for this request
* `get_job_logs` - Get the logs of a job in a Buildkite build
* `access_token` - Get the details for the API access token that was used to authenticate the request
* `list_artifacts` - List the artifacts for a Buildkite build
* `get_artifact` - Get an artifact from a Buildkite build

Example of the `get_pipeline` tool in action.

![Get Pipeline Tool](docs/images/get_pipeline.png)

### Production

To ensure the MCP server is run in a secure environment, we recommend running it in a container. 

Pull the pre-built image (recommended):

```bash
docker pull ghcr.io/buildkite/buildkite-mcp-server
```

Or build it yourself using GoReleaser and copy the binary into your path:

```bash
goreleaser build --snapshot --clean
```

# configuration

Create a buildkite api token with read access to pipelines.

## Claude Desktop Configuration

Use this configuration if you want to run the server `buildkite-mcp-server` Docker (recommended):

```json
{
    "mcpServers": {
        "buildkite": {
            "command": "docker",
            "args": [
                "run",
                "-i",
                "--rm",
                "-e",
                "BUILDKITE_API_TOKEN",
                "ghcr.io/buildkite/buildkite-mcp-server",
                "stdio"
            ],
            "env": {
                "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx"
            }
        }
    }
}
```

Configuration if you have `buildkite-mcp-server` installed locally.

```json
{
    "mcpServers": {
        "buildkite": {
            "command": "buildkite-mcp-server",
            "args": [
                "stdio"
            ],
            "env": {
                "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx"
            }
        }
    }
}
```

## Goose Configuration

For Docker with [Goose](https://block.github.io/goose/) (recommended):

```yaml
extensions:
  fetch:
    name: Buildkite
    cmd: docker
    args: ["run", "-i", "--rm", "-e", "BUILDKITE_API_TOKEN", "ghcr.io/buildkite/buildkite-mcp-server", "stdio"]
    enabled: true
    envs: { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx" }
    type: stdio
    timeout: 300
```

Local configuration for Goose:

```yaml
extensions:
  fetch:
    name: Buildkite
    cmd: buildkite-mcp-server
    args: [stdio]
    enabled: true
    envs: { "BUILDKITE_API_TOKEN": "bkua_xxxxxxxx" }
    type: stdio
    timeout: 300
```

## VSCode Configuration

[VSCode](https://code.visualstudio.com/) supports interactive inputs for variables. To get the API token interactively on MCP startup, put the following in `.vscode/mcp.json`

```json
{
    "inputs": [
        {
            "id": "BUILDKITE_API_TOKEN",
            "type": "promptString",
            "description": "Enter your BuildKite Access Token (https://buildkite.com/user/api-access-tokens)",
            "password": true
        }
    ],
    "servers": {
        "buildkite": {
            "command": "docker",
            "args": [
                "run",
                "-i",
                "--rm",
                "-e",
                "BUILDKITE_API_TOKEN",
                "ghcr.io/buildkite/buildkite-mcp-server",
                "stdio"
            ],
            "env": {
                "BUILDKITE_API_TOKEN": "${input:BUILDKITE_API_TOKEN}"
            }
        }
    }
}
```

# Security

This container image is built using [cgr.dev/chainguard/static](https://images.chainguard.dev/directory/image/static/versions) base image and is configured to run the MCP server as a non-root user.

# Contributing

Notes on building this project are in the [DEVELOPMENT.md](DEVELOPMENT.md)

## Disclaimer

This project is in the early stages of development and is not yet ready for use.

## License

This project is released under MIT license.
