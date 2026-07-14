#!/bin/bash

set -euo pipefail

# Run as non-root user if currently root
if [ "$(id -u)" -eq 0 ]; then
    echo "Running as root, switching to non-root user..."

    # Create a non-root user if it doesn't exist
    if ! id -u agent >/dev/null 2>&1; then
        useradd -m -s /bin/bash agent
    fi

    # Give agent ownership of the current directory
    chown -R agent:agent "$(pwd)"

    # Switch to agent and re-run this script
    exec su agent -c "$0 $*"
fi

# Set up Buildkite Hosted Models
export ANTHROPIC_BASE_URL="$BUILDKITE_AGENT_ENDPOINT/ai/anthropic"
export ANTHROPIC_API_KEY="$BUILDKITE_AGENT_ACCESS_TOKEN"

# Configure GitHub authentication using gh CLI if GITHUB_TOKEN is available
if [ -n "$GITHUB_TOKEN" ]; then
    echo "Configuring GitHub authentication with gh CLI..."
    echo "$GITHUB_TOKEN" | gh auth login --with-token || {
        echo "Warning: Failed to authenticate with gh CLI, falling back to git token authentication"
    }

    # Verify gh authentication and setup git integration
    if gh auth status >/dev/null 2>&1; then
        echo "Successfully authenticated with GitHub via gh CLI"
        gh auth setup-git || {
            echo "Warning: Failed to setup git integration with gh CLI"
        }
    else
        echo "Warning: gh CLI authentication verification failed"
    fi
fi

# Parse arguments
if [ $# -lt 1 ]; then
    echo "Usage: $0 <prompt_file> [KEY=VALUE ...]"
    echo
    echo "Arguments:"
    echo "  prompt_file    Path to the prompt markdown file"
    echo "  KEY=VALUE      Optional key-value pairs for token replacement"
    echo
    echo "Example:"
    echo "  $0 prompts/user.md BuildURL=https://example.com/build/123"
    exit 1
fi

PROMPT_FILE="$1"
shift

# Verify prompt file exists
if [ ! -f "$PROMPT_FILE" ]; then
    echo "Error: Prompt file not found: $PROMPT_FILE"
    exit 1
fi

# Load a system prompt to append
SYSTEM_PROMPT=$(cat "prompts/system.md") || {
    echo "Failed to read system.md file"
    exit 1
}

# Read prompt content
prompt_content=$(cat "$PROMPT_FILE") || {
    echo "Failed to read prompt file: $PROMPT_FILE"
    exit 1
}

echo "--- :scroll: Processing prompt: $PROMPT_FILE"

# Split the remaining arguments: KEY=VALUE pairs substitute {{.KEY}} in the
# prompt, while everything else (flags such as --output-format, --verbose,
# --allowedTools, ...) is forwarded verbatim to the claude CLI.
CLAUDE_ARGS=()
for arg in "$@"; do
    if [[ "$arg" =~ ^([A-Za-z0-9_]+)=(.*)$ ]]; then
        key="${BASH_REMATCH[1]}"
        value="${BASH_REMATCH[2]}"
        echo "Replacing {{.$key}} with: $value"
        prompt_content="${prompt_content//\{\{.$key\}\}/$value}"
    else
        CLAUDE_ARGS+=("$arg")
    fi
done

echo "--- :robot_face: Starting Claude agent"

# Tee the raw stream-json output so we can recover the session id after the run,
# while still piping it through the parser for human-readable rendering.
# NOTE: the parser requires --output-format=stream-json --verbose; the caller is
# responsible for passing those (babystand.sh does).
RAW_LOG="$(mktemp)"
echo "$prompt_content" | claude \
    --mcp-config mcp_in_ci.json \
    --strict-mcp-config \
    -p \
    --permission-mode bypassPermissions \
    --append-system-prompt "$SYSTEM_PROMPT" \
    ${CLAUDE_ARGS[@]+"${CLAUDE_ARGS[@]}"} \
    | tee "$RAW_LOG" \
    | node dist/parser -

# Emit machine-readable pointers to the Claude session transcript for the caller.
# Resolved here (in the agent's user context) so $HOME matches where Claude wrote it.
SESSION_ID=$(jq -r 'select(.type == "system" and .subtype == "init") | .session_id' "$RAW_LOG" | head -n1)
TRANSCRIPT="$HOME/.claude/projects/$(pwd | sed -e 's/[\/.]/-/g')/$SESSION_ID.jsonl"
echo "CLAUDE_SESSION_ID=$SESSION_ID"
echo "CLAUDE_TRANSCRIPT=$TRANSCRIPT"
