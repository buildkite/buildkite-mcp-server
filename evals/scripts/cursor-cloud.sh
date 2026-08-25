#!/bin/bash
#
# cursor-cloud.sh — run one eval prompt on a Cursor CLOUD agent (the hosted
# harness behind the Cursor 3 "Glass" app's Agents window), the `agent:
# cursor-cloud` analogue of claude.sh/cursor.sh. Same caller contract:
# <prompt_file> [flags], stream the run to stdout, and emit
# CURSOR_CLOUD_SESSION_ID / CURSOR_CLOUD_TRANSCRIPT / CURSOR_CLOUD_RESULT_FILE
# pointers for babystand.sh.
#
# Unlike claude.sh/cursor.sh this is NOT a CI sandbox wrapper: the agent
# executes in Cursor's cloud VM, so there is exactly ONE code path for CI and
# local runs, no permission posture to pick (host containment is irrelevant —
# nothing runs here), and no CLI to install. Everything is Cloud Agents API v1
# (https://cursor.com/docs/cloud-agent/api/endpoints) over curl:
#   launch  POST /v1/agents        prompt + repo@scenario-branch + inline MCP
#   watch   GET  .../runs/{id}/stream   SSE: assistant/tool_call/result events
#   collect GET  /v1/agents/{id}/usage  token usage (the cursor CLI never had this)
#   cleanup DELETE /v1/agents/{id}      the one-shot agent (and its env) deleted
#
# The SSE capture (converted to one JSON object per line) IS the transcript —
# there is no client-side session file. bk-tool-audit-v2.sh reads it with
# --agent cursor-cloud.
#
# MCP: the launch request carries an inline mcpServers definition from
# mcp_cursor_cloud.json (override: CURSOR_CLOUD_MCP_CONFIG). The default
# self-clones and builds the server source at MCP_SRC_REPO@MCP_SRC_SHA inside
# the VM — see that file's _comment for the how and the caveats. MCP_SRC_SHA
# is the commit under review: CI sets it explicitly (pipeline.evals.yml maps
# MCP_SRC_SHA=${BUILDKITE_COMMIT} at upload time); otherwise it falls back to
# an ambient BUILDKITE_COMMIT, then to the harness repo's HEAD locally. This
# preserves mcp_version: source semantics; note that LOCAL UNCOMMITTED
# CHANGES ARE NOT TESTED, unlike local claude/cursor runs which use the
# locally built binary.
#
# Prompt fidelity caveat (same as cursor.sh): no system-prompt channel, so
# prompts/system.md is prepended to the user prompt.
#
# One-time account prerequisites (failures are loud API errors, not silent):
#   - CURSOR_API_KEY: a Cursor Dashboard API key (same secret as `agent: cursor`)
#   - the Cursor GitHub integration must be granted access to the eval repo
#     (the cloud VM clones it via Cursor's GitHub App, not our GITHUB_TOKEN)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(dirname "$SCRIPT_DIR")"

API="${CURSOR_API_URL:-https://api.cursor.com}"

command -v jq >/dev/null || { echo "cursor-cloud: jq is required" >&2; exit 1; }
: "${CURSOR_API_KEY:?cursor-cloud.sh requires CURSOR_API_KEY — map the secret in .buildkite/pipeline.evals.yml}"
: "${BUILDKITE_API_TOKEN:?cursor-cloud.sh requires BUILDKITE_API_TOKEN (the entry token) for the MCP server}"

usage() {
    echo "Usage: $0 <prompt_file> [--repo <github_url>] [--starting-ref <branch>] [--model <cursor_model_id>] [--name <display_name>]" >&2
    exit 1
}

[[ $# -ge 1 ]] || usage
PROMPT_FILE="$1"; shift
[[ -f "$PROMPT_FILE" ]] || { echo "cursor-cloud: prompt file not found: $PROMPT_FILE" >&2; exit 1; }

REPO_URL="" STARTING_REF="" MODEL="" AGENT_NAME=""
while [[ $# -gt 0 ]]; do
    case "$1" in
        --repo)         REPO_URL="${2:?--repo needs a URL}"; shift 2 ;;
        --starting-ref) STARTING_REF="${2:?--starting-ref needs a ref}"; shift 2 ;;
        --model)        MODEL="${2:?--model needs a Cursor model id}"; shift 2 ;;
        --name)         AGENT_NAME="${2:?--name needs a value}"; shift 2 ;;
        *) echo "cursor-cloud: unknown argument: $1" >&2; usage ;;
    esac
done
[[ -n "$REPO_URL" ]] || { echo "cursor-cloud: --repo is required (the eval repo the agent works on)" >&2; exit 1; }

# Wall-clock cap for the whole run. Red->green waits on real CI builds, so the
# default is generous; on expiry the run is cancelled (best-effort) and the
# entry fails rather than hanging the matrix forever.
TIMEOUT_MINS="${CURSOR_CLOUD_TIMEOUT_MINS:-90}"

# --- MCP server definition (inline in the launch request) --------------------
MCP_CONFIG="${CURSOR_CLOUD_MCP_CONFIG:-$ROOT_DIR/mcp_cursor_cloud.json}"
[[ -f "$MCP_CONFIG" ]] || { echo "cursor-cloud: MCP config not found: $MCP_CONFIG" >&2; exit 1; }

# The source revision the VM builds the MCP server from (see header). Locally,
# resolve HEAD of the repo this harness ships in.
MCP_SRC_REPO="${MCP_SRC_REPO:-buildkite/buildkite-mcp-server}"
if [[ -z "${MCP_SRC_SHA:-}" ]]; then
    MCP_SRC_SHA="${BUILDKITE_COMMIT:-$(git -C "$ROOT_DIR/.." rev-parse HEAD 2>/dev/null || true)}"
fi
if [[ -z "$MCP_SRC_SHA" ]]; then
    echo "cursor-cloud: cannot resolve MCP_SRC_SHA (no BUILDKITE_COMMIT, not a git checkout); set MCP_SRC_SHA explicitly" >&2
    exit 1
fi

MCP_SERVERS="$(jq -c \
    --arg tok  "$BUILDKITE_API_TOKEN" \
    --arg repo "$MCP_SRC_REPO" \
    --arg sha  "$MCP_SRC_SHA" '
    .mcpServers
    | walk(if type == "string"
           then gsub("\\$\\{BUILDKITE_API_TOKEN\\}"; $tok)
                | gsub("\\$\\{MCP_SRC_REPO\\}"; $repo)
                | gsub("\\$\\{MCP_SRC_SHA\\}"; $sha)
           else . end)' "$MCP_CONFIG")"

# --- Compose the prompt -------------------------------------------------------
# system.md is prepended (no system-prompt channel; same fidelity caveat as
# cursor.sh — it lands in the user turn).
# The Buildkite-plugin preamble is cursor-cloud-only environment hygiene, not
# task guidance: the org's Cursor Buildkite plugin plants a hosted 'Buildkite'
# MCP server that is unauthenticated (needsAuth) in cloud VMs and cannot be
# excluded per-run (no API field; the plugin is shared org-wide). Without the
# preamble the agent probes it, concludes "Buildkite MCP requires auth", and
# writes off MCP entirely (observed on the first live run). Telling it which
# server works corrects that contamination; whether it then PREFERS the MCP
# tools over raw curl remains unprompted — that choice is part of what the
# eval measures.
PROMPT_TEXT="$(cat "$ROOT_DIR/prompts/system.md")

Environment note: the MCP server named 'Buildkite' (from a Cursor plugin) and the 'buildkite-cli' plugin skill are NOT authenticated in this environment and will not work — ignore them. The MCP server named 'buildkite-mcp-server' is configured and authenticated.

$(cat "$PROMPT_FILE")"

# --- Launch -------------------------------------------------------------------
# workOnCurrentBranch keeps pushes on the startingRef (the scenario branch the
# prompt names) instead of an auto-generated cursor/* branch, and autoCreatePR
# stays off — the eval's green signal is the scenario branch's own build.
# envVars mirrors the claude/cursor runners, whose agent shell also sees
# BUILDKITE_API_TOKEN: reaching for raw curl against the API instead of the
# MCP tools is part of what the eval measures.
PAYLOAD="$(jq -n \
    --arg text  "$PROMPT_TEXT" \
    --arg repo  "$REPO_URL" \
    --arg ref   "$STARTING_REF" \
    --arg model "$MODEL" \
    --arg name  "$AGENT_NAME" \
    --arg tok   "$BUILDKITE_API_TOKEN" \
    --argjson mcp "$MCP_SERVERS" '
    {
      prompt: {text: $text},
      repos: [({url: $repo} + (if $ref != "" then {startingRef: $ref} else {} end))],
      workOnCurrentBranch: true,
      autoCreatePR: false,
      envVars: {BUILDKITE_API_TOKEN: $tok},
      mcpServers: $mcp
    }
    + (if $model != "" then {model: {id: $model}} else {} end)
    + (if $name  != "" then {name: $name} else {} end)')"

api_curl() { curl -sS -H "Authorization: Bearer $CURSOR_API_KEY" "$@"; }

echo "--- :cursor: Launching Cursor cloud agent (repo: $REPO_URL${STARTING_REF:+ @ $STARTING_REF}${MODEL:+, model: $MODEL})"
echo "cursor-cloud: MCP server source: ${MCP_SRC_REPO}@${MCP_SRC_SHA}"

# The launch endpoint is strictly rate-limited (~1/user/min), so a 429 gets one
# gentle retry cycle instead of failing the entry outright.
CREATE_RESP=""
for attempt in 1 2 3; do
    HTTP_CODE=0
    CREATE_RESP="$(api_curl -w '\n%{http_code}' -X POST "$API/v1/agents" \
        -H 'Content-Type: application/json' -d "$PAYLOAD")" || true
    HTTP_CODE="${CREATE_RESP##*$'\n'}"
    CREATE_RESP="${CREATE_RESP%$'\n'*}"
    [[ -n "$HTTP_CODE" ]] || HTTP_CODE="000"   # curl itself failed (network, DNS)
    if [[ "$HTTP_CODE" == "429" ]]; then
        echo "cursor-cloud: launch rate-limited (attempt $attempt/3); sleeping 70s" >&2
        sleep 70
        continue
    fi
    break
done
if [[ "$HTTP_CODE" != 2* ]]; then
    echo "cursor-cloud: launch failed (HTTP $HTTP_CODE): $CREATE_RESP" >&2
    exit 1
fi

# Field names are read defensively: the docs show {agent: {...}, run: {...}},
# but tolerate a flat shape too. Both ids missing is a hard failure.
AGENT_ID="$(jq -r '.agent.id // .id // empty' <<<"$CREATE_RESP")"
RUN_ID="$(jq -r '.run.id // .runs[0].id // empty' <<<"$CREATE_RESP")"
if [[ -z "$AGENT_ID" || -z "$RUN_ID" ]]; then
    echo "cursor-cloud: could not extract agent/run id from launch response: $CREATE_RESP" >&2
    exit 1
fi
echo "cursor-cloud: agent $AGENT_ID run $RUN_ID"

# On any exit before a terminal status, cancel the run so an aborted eval does
# not leave a cloud agent burning through the repo unattended. Then delete the
# one-shot agent regardless of outcome: agents are durable and their envVars
# (the entry's BUILDKITE_API_TOKEN) persist until the agent is deleted, so
# leaving it would hand the token to anyone who can resume the agent and
# accumulate one agent per eval. Everything the harness needs (transcript,
# result, usage) is already collected before exit. CURSOR_CLOUD_KEEP_AGENT=1
# skips the delete for postmortems in the Cursor dashboard — remember the
# token custody trade-off. All best-effort.
FINISHED=""
cleanup() {
    if [[ -z "$FINISHED" ]]; then
        api_curl -X POST "$API/v1/agents/$AGENT_ID/runs/$RUN_ID/cancel" >/dev/null 2>&1 || true
    fi
    if [[ "${CURSOR_CLOUD_KEEP_AGENT:-}" != "1" ]]; then
        api_curl -X DELETE "$API/v1/agents/$AGENT_ID" >/dev/null 2>&1 || true
    fi
}
trap cleanup EXIT

# --- Transcript capture -------------------------------------------------------
RAW_LOG="$(mktemp)"

# The agent-created record gives the audit's metrics a model/ids source; the
# same wrapper shape ({type, data}) as the SSE events below.
jq -c --arg model "${MODEL:-default}" \
    '{type: "agent_meta", data: {agentId: (.agent.id // .id), runId: (.run.id // .runs[0].id), model: $model, created: .}}' \
    <<<"$CREATE_RESP" >> "$RAW_LOG"

# SSE frames -> one JSON object per line: {"type": "<event>", "data": <data>}.
# Multi-line data: fields are concatenated per the SSE spec; comment lines
# (":heartbeat") and CR line-endings are handled. Event names are sanitized
# because they are interpolated into JSON syntax verbatim. `id:` fields are
# written to $1 (latest wins, per the SSE last-event-id rules) so the stream
# loop can resume with a Last-Event-ID header instead of replaying history.
sse_to_jsonl() {
    awk -v IDFILE="$1" '
    { sub(/\r$/, "") }
    function emit() {
        if (d != "") { printf "{\"type\":\"%s\",\"data\":%s}\n", ev, d; fflush() }
        ev = "message"; d = ""
    }
    /^event: ?/ { ev = $0; sub(/^event: ?/, "", ev); gsub(/[^A-Za-z0-9_-]/, "", ev); next }
    /^data: ?/  { line = $0; sub(/^data: ?/, "", line); d = d line; next }
    /^id: ?/    { lid = $0; sub(/^id: ?/, "", lid)
                  if (lid != "") { print lid > IDFILE; close(IDFILE) }
                  next }
    /^:/        { next }
    /^$/        { emit(); next }
    END         { emit() }'
}

# Compact human-readable progress for the build log; the full event stream is
# in the transcript. Deltas print raw (-j) so assistant text reads as prose.
render_progress() {
    jq --unbuffered -rj '
    if type != "object" then ""
    elif .type == "assistant" then (.data.text // "")
    elif .type == "tool_call" then
        (if (.data.status // "") == "completed" then ""
         else "\n[tool] \(.data.name // "?") \((.data.args // {}) | tostring | .[0:160])\n" end)
    elif .type == "status" then "\n[status] \(.data.status // "?")\n"
    elif .type == "result" then "\n[result] \(.data.status // "?"): \(.data.text // "")\n"
    else "" end' 2>/dev/null || cat >/dev/null
}

# Stream until the run reaches a terminal status. The SSE connection can drop
# mid-run (these runs span CI waits), so stream attempts alternate with status
# polls. Reconnects resume from the last seen SSE id via Last-Event-ID; if the
# server ignores it and replays from the start anyway (first-live-run check),
# tool audits still dedupe by callId, but assistant text may repeat.
echo "--- :cursor: Streaming run (timeout: ${TIMEOUT_MINS}m)"
DEADLINE=$(( $(date +%s) + TIMEOUT_MINS * 60 ))
STATUS="" POLL_RESP=""
LAST_EVENT_ID_FILE="$(mktemp)"
while :; do
    # Each attempt gets the REMAINING wall-clock budget, not a fresh full
    # timeout — a reconnect near the deadline must not extend the advertised
    # cap by another TIMEOUT_MINS.
    REMAINING=$(( DEADLINE - $(date +%s) ))
    if (( REMAINING <= 0 )); then
        echo "cursor-cloud: run exceeded ${TIMEOUT_MINS}m (status: ${STATUS:-unknown}); cancelling" >&2
        break
    fi

    RESUME_ARGS=()
    LAST_EVENT_ID="$(cat "$LAST_EVENT_ID_FILE" 2>/dev/null || true)"
    [[ -n "$LAST_EVENT_ID" ]] && RESUME_ARGS=(-H "Last-Event-ID: $LAST_EVENT_ID")

    api_curl -N --max-time "$REMAINING" ${RESUME_ARGS[@]+"${RESUME_ARGS[@]}"} \
        "$API/v1/agents/$AGENT_ID/runs/$RUN_ID/stream" 2>/dev/null \
        | sse_to_jsonl "$LAST_EVENT_ID_FILE" | tee -a "$RAW_LOG" | render_progress || true
    echo

    # Keep the WHOLE poll response, not just .status: if the stream died
    # before the terminal result event arrived, this poll is the only copy of
    # the run's result text and durationMs (synthesized below).
    POLL_RESP="$(api_curl "$API/v1/agents/$AGENT_ID/runs/$RUN_ID" 2>/dev/null || true)"
    STATUS="$(jq -r '.status // .run.status // empty' <<<"$POLL_RESP" 2>/dev/null || true)"
    case "$STATUS" in
        FINISHED|ERROR|EXPIRED|CANCELLED|CANCELED) break ;;
    esac
    echo "cursor-cloud: stream ended, run still ${STATUS:-unknown}; reconnecting in 15s" >&2
    sleep 15
done

# If the run reached a terminal status but the stream never delivered a
# `result` event (dropped connection, poll already terminal), synthesize one
# from the poll response so RESULT_FILE and the audit's duration/result
# metrics still populate. Marked source: status_poll so audits can tell.
case "$STATUS" in
    FINISHED|ERROR|EXPIRED|CANCELLED|CANCELED)
        if [[ -n "$POLL_RESP" ]] && ! grep -q '^{"type":"result"' "$RAW_LOG"; then
            jq -c '{type: "result", data: {
                    status: (.status // .run.status // "unknown"),
                    text: ((.result // .run.result // .summary // "") | tostring),
                    durationMs: (.durationMs // .run.durationMs // null),
                    source: "status_poll"}}' <<<"$POLL_RESP" >> "$RAW_LOG" 2>/dev/null \
                || echo "WARNING: cursor-cloud: could not synthesize result record from status poll" >&2
        fi
        ;;
esac

# --- Collect ------------------------------------------------------------------
# Token usage: the one metric the cursor CLI could never provide. Appended as a
# transcript record so the audit's `metrics` mode reads a single file.
USAGE="$(api_curl "$API/v1/agents/$AGENT_ID/usage" 2>/dev/null || true)"
if jq -e . >/dev/null 2>&1 <<<"$USAGE"; then
    jq -c '{type: "usage", data: .}' <<<"$USAGE" >> "$RAW_LOG"
else
    echo "WARNING: cursor-cloud: could not fetch usage for agent $AGENT_ID" >&2
fi

# Transcripts feed jq-based audits line by line; drop any line a flaky stream
# left unparseable rather than poisoning every later read.
TRANSCRIPT="$(mktemp)"
jq -cR 'fromjson? | select(. != null)' "$RAW_LOG" > "$TRANSCRIPT"

RESULT_FILE="$(mktemp)"
jq -r 'select(.type == "result") | .data.text // empty' "$TRANSCRIPT" | tail -n 1 > "$RESULT_FILE"

echo "cursor-cloud: run $RUN_ID terminal status: ${STATUS:-unknown}"

# Machine-readable pointers for babystand.sh (its run_cursor_cloud parses these).
echo "CURSOR_CLOUD_SESSION_ID=$AGENT_ID"
echo "CURSOR_CLOUD_TRANSCRIPT=$TRANSCRIPT"
echo "CURSOR_CLOUD_RESULT_FILE=$RESULT_FILE"

if [[ "$STATUS" == "FINISHED" ]]; then
    FINISHED=1   # suppress the cancel in cleanup()
    exit 0
fi
echo "cursor-cloud: run did not finish cleanly (status: ${STATUS:-unknown})" >&2
exit 1
