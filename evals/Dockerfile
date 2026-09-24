# Build the Buildkite MCP server from this repo's source, so the eval exercises
# the code under review rather than a published release.
ARG GO_VERSION="1.26"
FROM golang:${GO_VERSION} AS mcp-builder
WORKDIR /src
# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY pkg ./pkg
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/buildkite-mcp-server ./cmd/buildkite-mcp-server

FROM ubuntu:26.04

# Expected arguments for tool versions.
ARG NODE_VERSION="24"
ARG GITHUB_CLI_VERSION
ARG YQ_VERSION="v4.47.1"

# Install system dependencies.
RUN apt-get update && apt-get install -y \
    curl \
    git \
    ca-certificates \
    gnupg \
    jq \
    && rm -rf /var/lib/apt/lists/*

# Install Node.js.
RUN curl -fsSL https://deb.nodesource.com/setup_${NODE_VERSION}.x | bash - \
    && apt-get install -y nodejs \
    && rm -rf /var/lib/apt/lists/*

# Install yq (mikefarah) so babystand.sh can parse the eval matrix (evals.yaml).
# Pinned like the other tools above: 'latest' would make image builds
# unreproducible and is a supply-chain risk.
RUN curl -fsSL https://github.com/mikefarah/yq/releases/download/${YQ_VERSION}/yq_linux_amd64 \
    -o /usr/local/bin/yq && chmod +x /usr/local/bin/yq

# Install the GitHub CLI.
RUN curl -fsSL https://github.com/cli/cli/releases/download/v${GITHUB_CLI_VERSION}/gh_${GITHUB_CLI_VERSION}_linux_amd64.tar.gz | tar -xz -C /tmp
RUN cp /tmp/gh_${GITHUB_CLI_VERSION}_linux_amd64/bin/gh /usr/local/bin/

# Install the Buildkite MCP server, built from source in the mcp-builder stage.
COPY --from=mcp-builder /out/buildkite-mcp-server /usr/local/bin/buildkite-mcp-server

# Install Claude Code.
RUN npm install -g @anthropic-ai/claude-code

# Create a non-root user to run the agent in isolation.
RUN useradd -m -u 1000 -s /bin/bash agent

# Create the workspace.
RUN mkdir -p /workspace
WORKDIR /workspace

# Copy in the necessary files.
COPY evals/scripts /workspace/scripts
COPY evals/prompts /workspace/prompts
COPY evals/package*.json /workspace/
COPY evals/tsconfig.json /workspace/
COPY evals/mcp_in_ci.json /workspace/
COPY evals/mcp_cursor_cloud.json /workspace/
COPY evals/evals.yaml /workspace/

# Run the build.
RUN npm ci && npm run build
RUN rm scripts/*.ts tsconfig.json

# Give the non-root user ownership of the workspace.
RUN chown -R agent:agent /workspace

# Switch to the non-root user.
USER agent

# Install the Cursor CLI (cursor-agent) for evals.yaml entries with
# `agent: cursor`. Installed as the agent user because the installer targets
# $HOME/.local. NOTE: unlike the pinned tools above, Cursor's installer has no
# documented version pin — an unreproducibility/supply-chain trade-off we
# accept for now; revisit if Cursor publishes pinned release artifacts.
RUN curl -fsS https://cursor.com/install | bash
ENV PATH="/home/agent/.local/bin:${PATH}"

# Default command
CMD ["/bin/bash"]
