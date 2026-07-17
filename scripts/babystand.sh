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

# Create the scenario branch from the deliberately-broken base branch and push it.
# We push the BRANCH only and deliberately do NOT open a PR: the branch push
# triggers its own Quality Checks build (build_branches), which is the red->green
# CI signal the agent fixes. Opening a PR would fan pull_request events into the
# mcp-eval trigger and re-loop. The scenario branch name is prefixed so the eval
# step is skipped on its builds (see .buildkite/pipeline.yml `branches:` guard).
SCENARIO_BRANCH="mcp-eval-scenario-$DATETIME"
git fetch && git checkout -b "$SCENARIO_BRANCH" origin/test-broken-and-failed-jobs
git push -u origin HEAD

ORG_SLUG="${BUILDKITE_ORGANIZATION_SLUG:-anothertest}"
PIPELINE_SLUG="${BUILDKITE_PIPELINE_SLUG:-buildkite-mcp-server}"
LLM_PROMPT="/goal make the CI for git branch '$SCENARIO_BRANCH' (Buildkite org '$ORG_SLUG', pipeline '$PIPELINE_SLUG') from red $WAIT_STATUS_STRING to green. Push your fixes to that same branch and confirm its build passes. Do NOT read/reverse-engineer babystand.sh.$DEBUG_STRING"

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
echo "*** SCENARIO_BRANCH: $SCENARIO_BRANCH"
echo "*** WAIT_STATUS_STRING: $WAIT_STATUS_STRING"
echo "*** DEBUG_STRING: $DEBUG_STRING"
echo "***"
echo "----------------------"
echo "***"
echo "*** LLM_PROMPT: $LLM_PROMPT"
echo "***"

LOG="/tmp/babystand-$DATETIME.log"

# Format a duration in whole seconds as "Nm Ns".
fmt_elapsed() { printf '%dm %ds' $(($1 / 60)) $(($1 % 60)); }

# Claude args shared by both execution modes.
CLAUDE_TOOL_ARGS=(
    --output-format stream-json --verbose
    --allowedTools "Edit" "Bash(go:*)" "Bash(make:*)" "Bash(git:*)" "mcp__bk_bkbk_ro"
)

EVAL_START=$(date +%s)
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
    EVAL_RESULT_FILE=$(sed -n 's/^CLAUDE_RESULT_FILE=//p' "$LOG" | tail -n1)
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
EVAL_ELAPSED=$(fmt_elapsed $(( $(date +%s) - EVAL_START )))
echo "*** EVAL ELAPSED (red-to-green): $EVAL_ELAPSED"

echo "*** SESSION_ID: $SESSION_ID"
echo "*** TRANSCRIPT: $TRANSCRIPT"

# Upload the eval session log (transcript) as a build artifact. Best-effort and
# CI-only (buildkite-agent is unavailable locally); never fail the build.
if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
    if [[ -s "$TRANSCRIPT" ]]; then
        buildkite-agent artifact upload "$TRANSCRIPT" \
            || echo "WARNING: failed to upload session transcript artifact" >&2
    else
        echo "WARNING: session transcript not found or empty: $TRANSCRIPT" >&2
    fi
fi

AUDIT_TOOLS_FILE="$(mktemp)"
AUDIT_METRICS_FILE="$(mktemp)"
echo "*** Session Details ***"
"$SCRIPT_DIR/bk-tool-audit-v2.sh" --all "$TRANSCRIPT" | tee "$AUDIT_TOOLS_FILE"
echo "*** Session Metrics ***"
"$SCRIPT_DIR/bk-tool-audit-v2.sh" metrics "$TRANSCRIPT" | tee "$AUDIT_METRICS_FILE"

# --- Review the eval session with the klaren prompt -------------------------
# Best-effort, post-run analysis: a failure here must NOT fail the eval, which
# has already completed. klaren.md requires the session log path, so append it
# to the prompt. Use the harness copy of the prompt (alongside this script), not
# the checked-out subject-under-test copy.
echo "--- :female-detective: Reviewing the session (klaren)"
KLAREN_LOG="/tmp/klaren-$DATETIME.log"
KLAREN_PROMPT_FILE="$(mktemp)"
{
    cat "$SCRIPT_DIR/../prompts/klaren.md"
    echo
    echo "The LLM session log file to analyze is: $TRANSCRIPT"
} > "$KLAREN_PROMPT_FILE"

# klaren reads the transcript and inspects the repo; it needs read/search tools.
KLAREN_TOOL_ARGS=(
    --output-format stream-json --verbose
    --allowedTools "Read" "Grep" "Glob" "Bash"
)

KLAREN_RESULT_FILE=""
KLAREN_START=$(date +%s)
if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
    if ! "$SCRIPT_DIR/claude.sh" "$KLAREN_PROMPT_FILE" "${KLAREN_TOOL_ARGS[@]}" | tee "$KLAREN_LOG"; then
        echo "WARNING: klaren review failed" >&2
    fi
    KLAREN_RESULT_FILE=$(sed -n 's/^CLAUDE_RESULT_FILE=//p' "$KLAREN_LOG" | tail -n1)
    buildkite-agent artifact upload "$KLAREN_LOG" || echo "WARNING: failed to upload klaren artifact" >&2
else
    if ! claude -p "$(cat "$KLAREN_PROMPT_FILE")" \
        --mcp-config mcp.json \
        "${KLAREN_TOOL_ARGS[@]}" \
        | tee "$KLAREN_LOG"; then
        echo "WARNING: klaren review failed" >&2
    fi
fi
KLAREN_ELAPSED=$(fmt_elapsed $(( $(date +%s) - KLAREN_START )))
echo "*** KLAREN ELAPSED: $KLAREN_ELAPSED"

echo "*** KLAREN_LOG: $KLAREN_LOG"

# --- Curated summary annotations --------------------------------------------
# In addition to the per-message annotations from the parser (which stay on
# unless ANNOTATE_MESSAGES=false), surface a few high-signal summaries at the top
# of the build (priority 10 > the parser's 5). CI-only: buildkite-agent annotate
# is unavailable locally. All best-effort; never fail the build.
if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
    # annotate_markdown <context> <title> <file> <meta> [style]
    # content is already markdown; collapsed by default (like the code-block
    # annotations). The always-visible summary shows the title + <meta> (e.g.
    # elapsed time); the markdown body is inside the collapsed <details>.
    annotate_markdown() {
        local context="$1" title="$2" file="$3" meta="$4" style="${5:-info}"
        {
            printf '<details><summary>%s' "$title"
            [[ -n "$meta" ]] && printf ' — %s' "$meta"
            printf '</summary>\n\n'
            if [[ -s "$file" ]]; then
                cat "$file"
            else
                printf '_(no final output captured)_\n'
            fi
            printf '\n\n</details>\n'
        } | buildkite-agent annotate --context "$context" --style "$style" --priority 10 \
            || echo "WARNING: failed to annotate '$context'" >&2
    }

    # annotate_codeblock <context> <title> <file>  -- content is plain text; collapse it
    annotate_codeblock() {
        local context="$1" title="$2" file="$3"
        if [[ ! -s "$file" ]]; then
            echo "WARNING: nothing to annotate for '$context'" >&2
            return 0
        fi
        { printf '<details><summary>%s</summary>\n\n```\n' "$title"; cat "$file"; printf '\n```\n\n</details>\n'; } \
            | buildkite-agent annotate --context "$context" --style info --priority 10 \
            || echo "WARNING: failed to annotate '$context'" >&2
    }

    annotate_markdown "eval-final"    ":robot_face: Eval — final output"      "${EVAL_RESULT_FILE:-}" "⏱️ Red-to-green elapsed: ${EVAL_ELAPSED}" "success"
    annotate_codeblock "eval-metrics" ":bar_chart: Eval — session metrics"    "$AUDIT_METRICS_FILE"
    annotate_codeblock "eval-tools"   ":hammer_and_wrench: Eval — tool calls" "$AUDIT_TOOLS_FILE"
    annotate_markdown "klaren-final"  ":female-detective: Klaren — session review" "${KLAREN_RESULT_FILE:-}" "⏱️ Klaren elapsed: ${KLAREN_ELAPSED}"
fi
