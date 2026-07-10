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
git checkout main && git pull && git checkout -b "bork-$DATETIME"

# Bork the test
sed -i '' -e 's/to.ke.n"}}/to.kee.n"}}/' internal/commands/headers_test.go

git add internal/commands/headers_test.go && git commit -m "Bork this and PR" && git push -u origin HEAD

PR_URL=$(gh pr create --title "Borked PR $DATETIME" --body "Just bork it!" | tail -1)
LLM_PROMPT="/goal make the CI for PR ($PR_URL) from red $WAIT_STATUS_STRING to green. Do NOT read/reverse-engineer babystand.sh.$DEBUG_STRING"

# Echo env vars
echo "*** Initial Env Vars"
echo "***"
echo "*** LOCAL_CI: $LOCAL_CI"
echo "*** DEBUG_PERMISSIONS: $DEBUG_PERMISSIONS"
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


LOG="/tmp/babystand-$DATETIME.jsonl"
claude -p "$LLM_PROMPT" \
    --output-format stream-json --verbose \
    --allowedTools "Edit" "Bash(go:*)" "Bash(make:*)" "Bash(git:*)" "mcp__bk_bkbk_ro" \
    | tee "$LOG"

SESSION_ID=$(jq -r 'select(.type == "system" and .subtype == "init") | .session_id' "$LOG")
TRANSCRIPT=~/.claude/projects/$(pwd | sed -e 's/[\/.]/-/g')/$SESSION_ID.jsonl

/Users/developer/Downloads/bk-tool-audit-v2.sh "$TRANSCRIPT"
