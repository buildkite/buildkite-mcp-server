#!/bin/bash
set -euo pipefail

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
    echo "$LLM_PROMPT" > prompts/goal.md
    ./scripts/claude.sh prompts/goal.md "${CLAUDE_TOOL_ARGS[@]}" | tee "$LOG"

    # Recover the session transcript location from claude.sh's emitted pointers
    # (resolved in the agent's user context, so the $HOME is correct).
    SESSION_ID=$(sed -n 's/^CLAUDE_SESSION_ID=//p' "$LOG" | tail -n1)
    TRANSCRIPT=$(sed -n 's/^CLAUDE_TRANSCRIPT=//p' "$LOG" | tail -n1)
else
    # Local execution: run claude directly against mcp_local.json, as before.
    claude -p "$LLM_PROMPT" \
        --mcp-config mcp_local.json \
        "${CLAUDE_TOOL_ARGS[@]}" \
        | tee "$LOG"

    # The raw stream-json is in $LOG, so derive the session id / transcript here.
    SESSION_ID=$(jq -r 'select(.type == "system" and .subtype == "init") | .session_id' "$LOG" | head -n1)
    TRANSCRIPT=~/.claude/projects/$(pwd | sed -e 's/[\/.]/-/g')/$SESSION_ID.jsonl
fi

echo "*** SESSION_ID: $SESSION_ID"
echo "*** TRANSCRIPT: $TRANSCRIPT"

./scripts/bk-tool-audit-v2.sh "$TRANSCRIPT"
