# Build the Buildkite MCP server from this repo's source, so the eval exercises
# the code under review rather than a published release.
ARG GO_VERSION="1.25"
FROM golang:${GO_VERSION} AS mcp-builder
WORKDIR /src
# Cache module downloads separately from the source.
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY pkg ./pkg
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/buildkite-mcp-server ./cmd/buildkite-mcp-server

FROM ubuntu:22.04

# Expected arguments for tool versions.
ARG NODE_VERSION="24"
ARG GITHUB_CLI_VERSION

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
COPY scripts /workspace/scripts
COPY prompts /workspace/prompts
COPY package*.json /workspace/
COPY tsconfig.json /workspace/
COPY claude.json .claude/settings.local.json
COPY mcp_in_ci.json /workspace/

# Run the build.
RUN npm ci && npm run build
RUN rm scripts/*.ts tsconfig.json

# Give the non-root user ownership of the workspace.
RUN chown -R agent:agent /workspace

# Switch to the non-root user.
USER agent

# Default command
CMD ["/bin/bash"]
