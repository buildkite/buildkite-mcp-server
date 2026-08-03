#!/bin/bash
#
# cursor.sh — CI wrapper for the Cursor CLI (cursor-agent), the `agent: cursor`
# analogue of claude.sh. Same contract: <prompt_file> [KEY=VALUE ...] [flags],
# streams the run to stdout via the parser, and emits CURSOR_SESSION_ID /
# CURSOR_TRANSCRIPT / CURSOR_RESULT_FILE pointers for the caller (babystand.sh).
#
# Differences from claude.sh, all forced by the cursor-agent CLI surface:
#   - Auth is CURSOR_API_KEY. There is no Buildkite Hosted Models path: the
#     hosted proxy fronts the Anthropic API only, and cursor-agent talks to
#     Cursor's own backend.
#   - No --mcp-config flag: cursor-agent discovers .cursor/mcp.json in the
#     project (cwd). We generate it from the harness mcp_in_ci.json with the
#     entry's BUILDKITE_API_TOKEN value substituted in (cursor's ${VAR}
#     interpolation in mcp.json env is undocumented, so don't rely on it).
#   - No --append-system-prompt: prompts/system.md is prepended to the user
#     prompt instead. Fidelity caveat: it lands in the user turn, not the
#     system turn.
#   - No on-disk transcript in a documented location: the raw stream-json
#     capture IS the transcript (and it carries no token usage — metrics are
#     partial for cursor runs; see bk-tool-audit-v2.sh).

set -euo pipefail

# Resolve the harness location from this script's own path, so the parser, MCP
# config and system prompt keep resolving even when the caller has cd'd into a
# separate git checkout (the subject under test) as the working directory.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

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

command -v cursor-agent >/dev/null || {
    echo "Error: cursor-agent not found on PATH (install: curl -fsS https://cursor.com/install | bash)"
    exit 1
}

# Cursor authenticates with its own API key (see header). babystand.sh skips
# cursor entries when this is unset, so this is a backstop for direct callers.
: "${CURSOR_API_KEY:?cursor.sh requires CURSOR_API_KEY — map the secret in .buildkite/pipeline.evals.yml}"

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

# Load the system prompt — prepended below (no --append-system-prompt in
# cursor-agent; see header).
SYSTEM_PROMPT=$(cat "$ROOT_DIR/prompts/system.md") || {
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
# prompt, while everything else (flags such as --output-format, --model, ...)
# is forwarded verbatim to the cursor-agent CLI.
CURSOR_ARGS=()
for arg in "$@"; do
    if [[ "$arg" =~ ^([A-Za-z0-9_]+)=(.*)$ ]]; then
        key="${BASH_REMATCH[1]}"
        value="${BASH_REMATCH[2]}"
        echo "Replacing {{.$key}} with: $value"
        prompt_content="${prompt_content//\{\{.$key\}\}/$value}"
    else
        CURSOR_ARGS+=("$arg")
    fi
done

prompt_content="$SYSTEM_PROMPT

$prompt_content"

# Generate the project MCP config in cwd (the eval-repo clone) from the
# harness config, substituting the token value (see header). 600: it now
# holds a credential.
mkdir -p .cursor
jq --arg tok "${BUILDKITE_API_TOKEN:-}" \
    '.mcpServers |= with_entries(.value.env.BUILDKITE_API_TOKEN = $tok)' \
    "$ROOT_DIR/mcp_in_ci.json" > .cursor/mcp.json
chmod 600 .cursor/mcp.json

echo "--- :robot_face: Starting Cursor agent"

# Tee the raw stream-json output so we can recover the session id after the
# run, while still piping it through the parser for human-readable rendering.
# NOTE: the parser requires --output-format=stream-json; the caller is
# responsible for passing it (babystand.sh does).
# Permission posture matches claude.sh's bypassPermissions: --force allows all
# commands, --approve-mcps skips the MCP server approval prompt (headless runs
# would otherwise hang or silently drop the server). Containment comes from
# the container sandbox + --read-only MCP server + read-only API token, not
# from CLI permissions (see evals/README.md).
RAW_LOG="$(mktemp)"
cursor-agent -p "$prompt_content" \
    --force \
    --approve-mcps \
    ${CURSOR_ARGS[@]+"${CURSOR_ARGS[@]}"} \
    | tee "$RAW_LOG" \
    | node "$ROOT_DIR/dist/parser" -

# Emit machine-readable pointers for the caller. The raw stream-json capture
# stands in for the transcript (cursor-agent keeps no documented on-disk one).
SESSION_ID=$(jq -r 'select(.type == "system" and .subtype == "init") | .session_id' "$RAW_LOG" | head -n1)
echo "CURSOR_SESSION_ID=$SESSION_ID"
echo "CURSOR_TRANSCRIPT=$RAW_LOG"

# Extract the run's final assistant text (the stream-json "result" event) into
# a file so the caller can surface just the final output (e.g. as an annotation).
RESULT_FILE="$(mktemp)"
jq -r 'select(.type == "result") | .result // empty' "$RAW_LOG" > "$RESULT_FILE" 2>/dev/null || true
echo "CURSOR_RESULT_FILE=$RESULT_FILE"
