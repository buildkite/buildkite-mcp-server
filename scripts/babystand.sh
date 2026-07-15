#!/bin/bash
set -euo pipefail

# Resolve the harness directory so sibling scripts keep resolving after we cd
# into a separate git checkout (the subject under test) below.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ "${LOCAL_CI}" == "true" ]]; then
  WAIT_STATUS_STRING="(perform local CI)"
else
  WAIT_STATUS_STRING="(patiently check for cloud CI failure status before proceeding with anything. DO NOT VIOLATE THIS)"
fi

if [[ "${DEBUG_PERMISSIONS}" == "true" ]]; then
  DEBUG_STRING=" If you do NOT have permissions to perform something, flag it loudly and BAIL!"
else
  DEBUG_STRING=""
fi

DATETIME=$(date +%Y-%m-%d-%H%M%S)

if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
  # The CI agent runs in a container built via COPY (mount-checkout=false), so the
  # working directory is not a git checkout. Clone a fresh working copy for the
  # eval (and the agent) to operate on. gh already authenticates from GITHUB_TOKEN
  # in the env; `gh auth setup-git` wires that into git so clone/push work too.
  # (Do NOT `gh auth login --with-token` here: it errors when GITHUB_TOKEN is set.)
  gh auth setup-git

  REPO_SLUG="${EVAL_REPO_SLUG:-nethsix/buildkite-mcp-server}"
  WORKDIR="$HOME/eval-repo-$DATETIME"
  git clone "https://github.com/${REPO_SLUG}.git" "$WORKDIR"
  cd "$WORKDIR"
fi

git fetch && git checkout -b "bork-branch-$DATETIME" origin/test-broken-and-failed-jobs
git push -u origin HEAD
PR_URL=$(gh pr create --title "test-broken-and-failed jobs PR $DATETIME" --body "test-broken-and-failed jobs" | tail -1)
LLM_PROMPT="/goal make the CI for PR ($PR_URL) from red $WAIT_STATUS_STRING to green. Do NOT read/reverse-engineer babystand.sh.$DEBUG_STRING"

# Echo env vars
echo "*** Initial Env Vars"
echo "***"
echo "*** LOCAL_CI: $LOCAL_CI"
echo "*** DEBUG_PERMISSIONS: $DEBUG_PERMISSIONS"
echo "*** RUN_IN_CI: ${RUN_IN_CI:-false}"
echo "***"
echo "----------------------"
echo "***"
echo "*** Derived Env Vars"
echo "***"
echo "*** PR_URL: $PR_URL"
echo "*** WAIT_STATUS_STRING: $WAIT_STATUS_STRING"
echo "*** DEBUG_STRING: $DEBUG_STRING"
echo "***"
echo "----------------------"
echo "***"
echo "*** LLM_PROMPT: $LLM_PROMPT"
echo "***"

LOG="/tmp/babystand-$DATETIME.log"

# Claude args shared by both execution modes.
CLAUDE_TOOL_ARGS=(
    --output-format stream-json --verbose
    --allowedTools "Edit" "Bash(go:*)" "Bash(make:*)" "Bash(git:*)" "mcp__bk_bkbk_ro"
)

if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
    # Sandboxed CI execution: run inside the container via claude.sh, which owns
    # the mcp_in_ci.json config, appends the system prompt, and pipes through the
    # parser. It prints CLAUDE_SESSION_ID / CLAUDE_TRANSCRIPT pointers on stdout.
    GOAL_FILE="$(mktemp)"
    echo "$LLM_PROMPT" > "$GOAL_FILE"
    "$SCRIPT_DIR/claude.sh" "$GOAL_FILE" "${CLAUDE_TOOL_ARGS[@]}" | tee "$LOG"

    # Recover the session transcript location from claude.sh's emitted pointers
    # (resolved in the agent's user context, so the $HOME is correct).
    SESSION_ID=$(sed -n 's/^CLAUDE_SESSION_ID=//p' "$LOG" | tail -n1)
    TRANSCRIPT=$(sed -n 's/^CLAUDE_TRANSCRIPT=//p' "$LOG" | tail -n1)
else
    # Local execution: run claude directly against mcp.json, as before.
    claude -p "$LLM_PROMPT" \
        --mcp-config mcp.json \
        "${CLAUDE_TOOL_ARGS[@]}" \
        | tee "$LOG"

    # The raw stream-json is in $LOG, so derive the session id / transcript here.
    SESSION_ID=$(jq -r 'select(.type == "system" and .subtype == "init") | .session_id' "$LOG" | head -n1)
    TRANSCRIPT=~/.claude/projects/$(pwd | sed -e 's/[\/.]/-/g')/$SESSION_ID.jsonl
fi

echo "*** SESSION_ID: $SESSION_ID"
echo "*** TRANSCRIPT: $TRANSCRIPT"

echo "*** Session Metrics ***"
"$SCRIPT_DIR/bk-tool-audit-v2.sh" metrics "$TRANSCRIPT"
echo "*** Session Details ***"
"$SCRIPT_DIR/bk-tool-audit-v2.sh" "$TRANSCRIPT"
