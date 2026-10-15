#!/usr/bin/env bash
#
# bk-tool-audit.sh — audit the MCP tool calls + parameters recorded in a
# Claude Code session transcript. Useful for comparing which tools/params
# get used before vs. after a code change.
#
# Transcripts live in:
#   ~/.claude/projects/<encoded-cwd>/<sessionId>.jsonl
# where <encoded-cwd> is the project path with every "/" replaced by "-".
#
# Usage:
#   ./bk-tool-audit.sh                       # newest session for current project
#   ./bk-tool-audit.sh <session.jsonl>       # a specific transcript
#   ./bk-tool-audit.sh list                  # list this project's sessions (newest first)
#   ./bk-tool-audit.sh diff <a.jsonl> <b.jsonl>   # diff tool/param calls between two sessions
#   ./bk-tool-audit.sh metrics [session.jsonl]    # token totals, cost, tool counts, bk CLI counts, duration
#   ./bk-tool-audit.sh struggles [session.jsonl]  # failed tool calls by type + struggle patterns (JSON)
#   ./bk-tool-audit.sh struggles-markdown <struggles.json>   # render it
#   ./bk-tool-audit.sh model-retries <raw-stream.jsonl>      # model API retries by status (JSON; claude only)
#   ./bk-tool-audit.sh model-retries-markdown <retries.json> # render it
#
# Options (before the positional args):
#   --all        Include all tools, not just mcp__buildkite-mcp-server__*
#   --names      Print only "<count> <tool>" summary instead of full params
#   --results    Also show each call's tool_result (matched by tool_use_id)
#   --project P  Use project dir for path P instead of the current directory
#   --agent A    Transcript dialect: 'claude' (default; Claude Code session
#                jsonl / stream-json — tool calls are tool_use content blocks),
#                'cursor' (cursor-agent stream-json capture — tool calls are
#                standalone tool_call started/completed events, and NO token
#                usage is emitted, so `metrics` reports tokens/cost as null),
#                or 'cursor-cloud' (cursor-cloud.sh's SSE capture — each line
#                is {type, data}; tool calls are tool_call events keyed by
#                data.callId, and an appended `usage` record carries token
#                totals from the Cloud Agents API, so `metrics` reports real
#                tokens — but cost stays null: Cursor pricing is not modeled).
#
# Pricing for `metrics` (USD per million tokens) is overridable via env vars;
# defaults are Claude Opus 4.8 rates:
#   BK_PRICE_IN=5  BK_PRICE_OUT=25  BK_PRICE_CACHE_5M=6.25
#   BK_PRICE_CACHE_1H=10  BK_PRICE_CACHE_READ=0.5
#
set -euo pipefail

# Pricing (USD per 1M tokens) — override via env. Defaults: Claude Opus 4.8.
BK_PRICE_IN="${BK_PRICE_IN:-5}"
BK_PRICE_OUT="${BK_PRICE_OUT:-25}"
BK_PRICE_CACHE_5M="${BK_PRICE_CACHE_5M:-6.25}"
BK_PRICE_CACHE_1H="${BK_PRICE_CACHE_1H:-10}"
BK_PRICE_CACHE_READ="${BK_PRICE_CACHE_READ:-0.5}"

TOOL_FILTER=''
NAMES_ONLY=0
WITH_RESULTS=0
PROJECT_PATH="$PWD"
AGENT="claude"

# --- parse options ---------------------------------------------------------
while [[ "${1:-}" == -* ]]; do
  case "$1" in
    --all)     TOOL_FILTER='true'; shift ;;
    --names)   NAMES_ONLY=1; shift ;;
    --results) WITH_RESULTS=1; shift ;;
    --project) PROJECT_PATH="${2:?--project needs a path}"; shift 2 ;;
    --agent)   AGENT="${2:?--agent needs claude, cursor or cursor-cloud}"; shift 2 ;;
    -h|--help) sed -n '2,44p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done
case "$AGENT" in claude|cursor|cursor-cloud) ;; *) echo "unsupported --agent: $AGENT" >&2; exit 2 ;; esac

# Default tool filter (without --all): claude MCP tool names carry the
# mcp__<server>__ prefix; cursor's tool_call event keys are per-tool camelCase
# names (readToolCall, mcpToolCall, ...) with no comparable server prefix, so
# no default narrowing is possible there — everything is included. cursor-cloud
# tool names come straight from the Cloud Agents API stream, whose MCP-tool
# naming is unverified against a live run, so it gets no narrowing either.
if [[ -z "$TOOL_FILTER" ]]; then
  if [[ "$AGENT" == "cursor" || "$AGENT" == "cursor-cloud" ]]; then TOOL_FILTER='true'
  else TOOL_FILTER='startswith("mcp__buildkite-mcp-server__")'; fi
fi

command -v jq >/dev/null || { echo "jq is required" >&2; exit 1; }

# Encode a directory path the way Claude Code names its project dir.
encode_dir() {
  local abs; abs="$(cd "$1" 2>/dev/null && pwd || echo "$1")"
  printf '%s' "$abs" | sed 's:/:-:g'
}

PROJECT_DIR="$HOME/.claude/projects/$(encode_dir "$PROJECT_PATH")"

newest_session() {
  ls -t "$PROJECT_DIR"/*.jsonl 2>/dev/null | head -1
}

# Emit one compact JSON object per tool call in a transcript.
# claude: tool_use content blocks inside assistant messages.
# cursor: standalone {"type":"tool_call","subtype":"started",...} events whose
#   .tool_call object has exactly one key naming the tool KIND (readToolCall,
#   shellToolCall, ...), with the call's arguments under that key's .args.
#   MCP invocations are wrapped: mcpToolCall's real identity lives at
#   .args.toolName + .args.serverIdentifier and the real tool arguments nest
#   at .args.args (observed schema — eval build 98; newer runs also emit the
#   Cloud-style {server, toolName, arguments} field names, so both spellings
#   are accepted). Normalize those to claude-style mcp__<server>__<tool> so
#   cross-agent tool tables line up; getMcpToolsToolCall (schema fetch) stays
#   as-is — it is a distinct tool-discovery step worth seeing in audits.
#   function.name is a defensive fallback for the OpenAI-style shape other
#   cursor versions may emit.
#   CAVEAT (same as cursor-cloud below): the FIRST event for a call may be an
#   args-less placeholder, with args only arriving on a later event for the
#   same call_id. Reading only "started" events therefore loses MCP identity
#   ("4 mcpToolCall" instead of per-tool names). cursor_calls collapses ALL
#   tool_call events by call_id — first occurrence keeps its stream position,
#   but an args-less stored event is upgraded to a later sibling that has
#   args. Events with no call_id pass through unmerged (one call each).
CURSOR_TOOL_NAME_JQ='
  def cursor_tool_name($t):
    ($t.value.args // {}) as $a
    | if $t.key == "mcpToolCall" and (($a.toolName // $a.name) != null) then
        "mcp__\($a.serverIdentifier // $a.providerIdentifier // $a.server // "mcp")__\($a.toolName // $a.name)"
      elif (($a.function? | type) == "object") and ($a.function.name != null) then
        $a.function.name
      else $t.key end;
  def cursor_calls:
    reduce (.[] | select(.type=="tool_call" and (.tool_call|type=="object"))) as $e (
      {seen: {}, out: []};
      (($e.call_id // "") | tostring) as $id
      | if $id == "" then
          (if $e.subtype == "started" then .out += [$e] else . end)
        elif .seen[$id] == null then .seen[$id] = (.out|length) | .out += [$e]
        elif ((.out[.seen[$id]].tool_call | to_entries[0].value.args // null) == null)
             and (($e.tool_call | to_entries[0].value.args // null) != null)
        then .out[.seen[$id]] = $e
        else . end)
    | .out;'
# cursor-cloud: cursor-cloud.sh captures the Cloud Agents SSE stream as
# {"type": "<event>", "data": {...}} lines. A tool call appears at least twice
# (a started/running event with args, a completed event repeating them with the
# result), all sharing data.callId — and the stream may additionally REPLAY
# events if the SSE connection dropped and reconnected mid-run. So calls are
# deduped by callId, keeping the FIRST occurrence's stream position to
# preserve order (group_by would sort by id and scramble the timeline) — BUT
# NOT its payload: live runs show the first event for a callId is often an
# args-less "running" placeholder, with args arriving only on a later event.
# Keeping the bare first event blanked every MCP identity ("5 mcp" in
# metrics), so an args-less stored event is upgraded in place to a later
# sibling that has args. Tool names: the docs describe tool_call.name as the
# PUBLIC wrapper name — "mcp" for every MCP invocation — which would collapse
# all MCP calls into one bucket and break per-tool comparisons. When the name
# is such a wrapper, derive the identity from the args into claude-style
# mcp__<server>__<tool>; non-wrapper names pass through as sent. The args
# shape per the Cloud Agents API is {server, toolName, arguments} — the older
# CLI-observed spellings (serverIdentifier, .args nesting) stay as fallbacks.
CURSOR_CLOUD_CALLS_JQ='
  def cloud_tool_events: [.[] | select(type=="object" and .type=="tool_call" and (.data|type=="object")) | .data];
  def dedupe_calls:
    reduce .[] as $e ({seen: {}, out: []};
        (($e.callId // "") | tostring) as $id
        | if $id == "" then .out += [$e]
          elif .seen[$id] == null then .seen[$id] = (.out|length) | .out += [$e]
          elif ((.out[.seen[$id]].args // null) == null) and (($e.args // null) != null)
          then .out[.seen[$id]] = $e
          else . end)
    | .out;
  def cloud_tool_name($d):
    ($d.args // {}) as $a
    | (($d.name // "unknown") | tostring) as $n
    | if ($n == "mcp" or $n == "mcpToolCall" or $n == "mcp_tool_call")
         and (($a.toolName // $a.name // null) != null)
      then "mcp__\($a.server // $a.serverIdentifier // $a.providerIdentifier // "mcp")__\($a.toolName // $a.name)"
      else $n end;'
# bk CLI usage (mode: cli entries — and any mcp run that shells out to bk).
# bk_cli_invocations turns one shell command string into its bk invocations,
# named by up to two subcommand words ("bk build view", "bk api", "bk").
# Heuristic: bk must sit in command position — start of the string, or after
# ; & | ( $( ` or a newline, optionally behind VAR=val / sudo / time / exec /
# command / do / then / else and a path prefix (./bk, /usr/local/bin/bk) — so `echo bk` and
# `grep bk` don't count. bk_cli_summary($per_call) takes one invocation list
# per shell tool call and reports how many calls ran bk, plus a per-command
# count list shaped like tool_calls_by_name.
BK_CLI_JQ='
  def bk_cli_invocations:
    [ scan("(?:^|[;&|(`\\n]|\\$\\()\\s*(?:(?:[A-Za-z_][A-Za-z0-9_]*=\\S*|sudo|time|exec|command|do|then|else)\\s+)*(?:[^\\s;&|()`]*/)?bk(?:\\s+([a-z][a-z-]*))?(?:\\s+([a-z][a-z-]*))?(?=\\s|$|[;&|)`])")
      | "bk" + (map(select(. != null) | " " + .) | join("")) ];
  def bk_cli_summary($per_call):
    { tool_calls_bk_cli: ($per_call | map(select(length > 0)) | length),
      bk_cli_commands_by_name: ($per_call | add // [] | group_by(.) | map("\(length) \(.[0])") | sort | reverse) };'
extract() {
  local f="$1"
  if [[ "$AGENT" == "cursor-cloud" ]]; then
    jq -c -n --argjson names "$NAMES_ONLY" "$CURSOR_CLOUD_CALLS_JQ"'
      [inputs] | cloud_tool_events | dedupe_calls | .[]
      | (.args // {}) as $a
      | cloud_tool_name(.) as $name
      | select($name | '"$TOOL_FILTER"')
      | (if $name != (.name // "unknown") then
           (if ($a.arguments? | type) == "object" then $a.arguments
            elif ($a.args? | type) == "object" then $a.args
            else $a end)
         else $a end) as $input
      | if $names == 1 then {tool: $name}
        else {id: (.callId // null), tool: $name, input: $input} end
    ' "$f"
  elif [[ "$AGENT" == "cursor" ]]; then
    jq -c -n --argjson names "$NAMES_ONLY" "$CURSOR_TOOL_NAME_JQ"'
      [inputs] | cursor_calls | .[]
      | (.tool_call | to_entries[0]) as $t
      | ($t.value.args // {}) as $a
      | cursor_tool_name($t) as $name
      | (if $t.key == "mcpToolCall" then ($a.args // $a.arguments // {}) else $a end) as $input
      | select($name | '"$TOOL_FILTER"')
      | if $names == 1 then {tool: $name}
        else {id: (.call_id // null), tool: $name, input: $input} end
    ' "$f"
  else
    jq -c --argjson names "$NAMES_ONLY" '
      select(.message.content) | .message.content[]?
      | select(.type=="tool_use" and (.name | '"$TOOL_FILTER"'))
      | if $names == 1 then {tool: .name}
        else {id: .id, tool: .name, input: .input} end
    ' "$f"
  fi
}

print_session() {
  local f="$1"
  [[ -f "$f" ]] || { echo "no such transcript: $f" >&2; exit 1; }
  echo "# session: $f"

  if [[ "$NAMES_ONLY" == 1 ]]; then
    extract "$f" | jq -r '.tool' | sort | uniq -c | sort -rn
    return
  fi

  if [[ "$WITH_RESULTS" == 1 ]]; then
    # Map call id -> truncated result text (claude: tool_use_id on tool_result
    # blocks; cursor: call_id on tool_call completed events, result under the
    # tool key's .result — .success on success, the whole object otherwise;
    # cursor-cloud: data.callId on completed tool_call events, result at
    # data.result — same .success-on-success convention).
    local map
    if [[ "$AGENT" == "cursor-cloud" ]]; then
      map="$(jq -cn '
        [ inputs
          | select(type=="object" and .type=="tool_call" and (.data|type=="object"))
          | .data
          | select((.status // "") == "completed" and ((.callId // "") | tostring) != "")
          | {((.callId | tostring)): ((.result.success // .result // "(no result)")
               | tostring | gsub("\n";" ") | .[0:300])} ]
        | add // {}' "$f")"
    elif [[ "$AGENT" == "cursor" ]]; then
      map="$(jq -cn '
        [ inputs
          | select(.type=="tool_call" and .subtype=="completed" and .call_id and (.tool_call|type=="object"))
          | (.tool_call | to_entries[0].value.result // {}) as $r
          | {(.call_id): (($r.success // $r) | tostring | gsub("\n";" ") | .[0:300])} ]
        | add // {}' "$f")"
    else
      map="$(jq -cn '
        [ inputs | (.message.content? // []) | select(type=="array") | .[]
          | select(.type=="tool_result")
          | {(.tool_use_id): ((.content // "")
               | if type=="array" then (map(.text // "") | join("")) else tostring end
               | gsub("\n";" ") | .[0:300])} ]
        | add // {}' "$f")"
    fi
    extract "$f" | jq -c --argjson map "$map" \
      '. + {result: ($map[.id] // "(no result captured)")} | del(.id)'
  else
    extract "$f" | jq -c 'del(.id)'
  fi
}

# Token totals, cost, tool counts, model mix, and duration for one session.
# The claude dialect also reports a waiting-time breakdown (the `wait` key):
# wall-clock spent inside wait_for_build calls and Bash `sleep` polls, computed
# by pairing each tool_use with its tool_result via the per-line timestamps.
# cursor / cursor-cloud streams carry no per-event timestamps, so their
# `wait` / `tool_time_seconds` are null.
# Dedupes usage by message.id (content blocks are split one-per-line, each
# repeating the message-level usage) and reads from a stable snapshot copy to
# avoid racing the live append of the current session.
metrics() {
  local f="$1"
  [[ -f "$f" ]] || { echo "no such transcript: $f" >&2; exit 1; }

  local snap; snap="$(mktemp -t bk-audit.XXXXXX)"
  trap 'rm -f "$snap"' RETURN
  cp "$f" "$snap"

  # cursor-cloud: the SSE capture's assistant events are TEXT DELTAS with no
  # message boundaries, so user/assistant counts would be meaningless and stay
  # null. What IS available beats the cursor CLI: real token totals from the
  # appended `usage` record (Cloud Agents API /usage), duration from the
  # terminal result event, and the model from the agent_meta record ("default"
  # when the entry pinned none — the launch omitted the field and Cursor's
  # account default applied). est_cost_usd stays null: the BK_PRICE_* table is
  # Anthropic pricing and does not model Cursor's.
  if [[ "$AGENT" == "cursor-cloud" ]]; then
    echo "# session: $f" >&2
    jq -nr "$CURSOR_CLOUD_CALLS_JQ$BK_CLI_JQ"'
      [inputs | select(type=="object")] as $all
      | ($all | cloud_tool_events | dedupe_calls) as $tools
      | ($all | map(select(.type=="result")) | last | .data // {}) as $res
      | ($all | map(select(.type=="usage")) | last | .data.totalUsage // null) as $u
      | ($all | map(select(.type=="agent_meta")) | first | .data // {}) as $meta
      | {
          user_messages: null,
          assistant_responses: null,
          duration_min: (if ($res.durationMs // null) != null then ($res.durationMs/60000|floor) else null end),
          duration_seconds: (if ($res.durationMs // null) != null then (($res.durationMs/1000)|round) else null end),
          models: {(($meta.model // "unknown") | tostring): 1},
          tokens: (if $u != null then
                     {input: ($u.inputTokens // 0), output: ($u.outputTokens // 0),
                      cache_write: ($u.cacheWriteTokens // 0), cache_read: ($u.cacheReadTokens // 0)}
                   else null end),
          tool_calls_total: ($tools | length),
          tool_calls_mcp: ($tools | map(cloud_tool_name(.)) | map(select(startswith("mcp__"))) | length),
          tool_calls_by_name: ($tools | map(cloud_tool_name(.)) | group_by(.) | map("\(length) \(.[0])") | sort | reverse),
          # SSE events carry no per-event timestamps, so in-tool / waiting time
          # cannot be reconstructed for cloud runs.
          tool_time_seconds: null,
          wait: null,
          est_cost_usd: null
        }
        + bk_cli_summary($tools | map((.args.command? // "") | if type == "string" then bk_cli_invocations else [] end))
    ' "$snap"
    return
  fi

  # cursor-agent's stream-json emits no token usage and no per-event
  # timestamps, so the cursor dialect reports what IS available — message and
  # tool-call counts, the init event's model, and the result event's
  # duration_ms — with tokens/cost explicitly null (comparisons must not read
  # them as zero-cost runs). Keys match the claude output where the meaning
  # matches, so bk-eval-compare.sh diffs stay line-comparable.
  if [[ "$AGENT" == "cursor" ]]; then
    echo "# session: $f" >&2
    # Tool names use the same normalization as extract() (mcpToolCall ->
    # mcp__<server>__<tool>), so metrics and tool listings agree.
    jq -nr "$CURSOR_TOOL_NAME_JQ$BK_CLI_JQ"'
      [inputs | select(type=="object")] as $all
      | ($all | cursor_calls
              | map((.tool_call | to_entries[0]) as $t | cursor_tool_name($t))) as $tools
      | ($all | cursor_calls
              | map(.tool_call | to_entries[0] | select(.key == "shellToolCall")
                    | (.value.args.command // "") | if type == "string" then bk_cli_invocations else [] end)) as $bk
      | ($all | map(select(.type=="result")) | last) as $res
      | ($all | map(select(.type=="system" and .subtype=="init")) | first) as $init
      | {
          user_messages: ($all | map(select(.type=="user")) | length),
          assistant_responses: ($all | map(select(.type=="assistant")) | length),
          duration_min: (if ($res.duration_ms? // null) != null then ($res.duration_ms/60000|floor) else null end),
          duration_seconds: (if ($res.duration_ms? // null) != null then (($res.duration_ms/1000)|round) else null end),
          models: {(($init.model? // "unknown")): 1},
          tokens: null,
          tool_calls_total: ($tools | length),
          tool_calls_mcp: ($tools | map(select(startswith("mcp__"))) | length),
          tool_calls_by_name: ($tools | group_by(.) | map("\(length) \(.[0])") | sort | reverse),
          # cursor stream-json has no per-event timestamps, so in-tool /
          # waiting time cannot be reconstructed.
          tool_time_seconds: null,
          wait: null,
          est_cost_usd: null
        }
        + bk_cli_summary($bk)
    ' "$snap"
    return
  fi

  local prices
  prices="$(jq -n \
    --arg in "$BK_PRICE_IN" --arg out "$BK_PRICE_OUT" \
    --arg c5 "$BK_PRICE_CACHE_5M" --arg c1h "$BK_PRICE_CACHE_1H" \
    --arg cr "$BK_PRICE_CACHE_READ" \
    '{in: ($in|tonumber), out: ($out|tonumber), c5: ($c5|tonumber),
      c1h: ($c1h|tonumber), cread: ($cr|tonumber)}')"

  echo "# session: $f" >&2
  jq -nr --argjson p "$prices" "$BK_CLI_JQ"'
    def n: . // 0;
    def tsec: sub("\\.[0-9]+";"") | fromdateiso8601;
    [inputs] as $all
    | ($all | map(select(.type=="assistant")) | group_by(.message.id) | map(.[0])) as $resp
    | ($resp | map(.message.usage)) as $u
    | ($all | [ .[] | . as $l | (.message.content? // []) | select(type=="array") | .[]
                | select(.type=="tool_use")
                | {id, name, input, ts: $l.timestamp} ] | unique_by(.id)) as $tools
    | ($all | map(.timestamp) | map(select(type=="string"))
            | map(tsec)) as $secs
    # Wall-clock spent INSIDE each tool call: pair every tool_use with its
    # tool_result (by tool_use_id) and diff the line timestamps. The subset in
    # $waits is time the agent spent WAITING on external state rather than
    # working: wait_for_build calls, plus Bash commands that shell out to
    # `sleep` (the poll-and-sleep loop agents fall back to). Heuristic on
    # purpose — a get_build poll with no sleep is NOT counted as waiting.
    | ($all | [ .[] | . as $l | (.message.content? // []) | select(type=="array") | .[]
                | select(.type=="tool_result" and .tool_use_id != null)
                | {key: .tool_use_id, value: $l.timestamp} ] | from_entries) as $rts
    | ($tools | map(select((.ts|type)=="string" and (($rts[.id] // null)|type)=="string")
                    | {name, input, dur: (($rts[.id]|tsec) - (.ts|tsec))})) as $timed
    | ($timed | map(select((.name | test("^mcp__.*__wait_for_build$"))
                    or (.name == "Bash" and ((.input.command? // "") | test("\\bsleep\\b")))))) as $waits
    # token sums
    | ($u | map(.input_tokens|n)            | add | n) as $tin
    | ($u | map(.output_tokens|n)           | add | n) as $tout
    | ($u | map(.cache_read_input_tokens|n) | add | n) as $tread
    | ($u | map(.cache_creation.ephemeral_5m_input_tokens|n) | add | n) as $c5raw
    | ($u | map(.cache_creation.ephemeral_1h_input_tokens|n) | add | n) as $c1h
    | ($u | map(.cache_creation_input_tokens|n) | add | n) as $ccTotal
    # if the 5m/1h split is absent, attribute all cache-creation to 5m
    | (if ($c5raw + $c1h) == 0 then $ccTotal else $c5raw end) as $c5
    | (($tin*$p.in + $tout*$p.out + $c5*$p.c5 + $c1h*$p.c1h + $tread*$p.cread) / 1000000) as $cost
    | {
        user_messages: ($all|map(select(.type=="user"))|length),
        assistant_responses: ($resp|length),
        duration_min: (if ($secs|length) > 0 then ((($secs|max)-($secs|min))/60|floor) else null end),
        duration_seconds: (if ($secs|length) > 0 then (($secs|max)-($secs|min)) else null end),
        models: ($resp | map(.message.model // "unknown") | group_by(.) | map({(.[0]): length}) | add),
        tokens: {input: $tin, output: $tout, cache_write_5m: $c5, cache_write_1h: $c1h, cache_read: $tread},
        tool_calls_total: ($tools|length),
        tool_calls_mcp: ($tools | map(select(.name | startswith("mcp__"))) | length),
        tool_calls_by_name: ($tools | group_by(.name) | map("\(length) \(.[0].name)") | sort | reverse),
        tool_time_seconds: (($timed | map(.dur) | add) // 0),
        wait: {
          seconds: (($waits | map(.dur) | add) // 0),
          calls: ($waits | length),
          by_tool: (($waits | group_by(.name)
                     | map({key: .[0].name, value: {calls: length, seconds: (map(.dur) | add)}})
                     | from_entries) // {})
        },
        est_cost_usd: (($cost*100|round)/100)
      }
      + bk_cli_summary($tools | map(select(.name == "Bash") | (.input.command? // "") | if type == "string" then bk_cli_invocations else [] end))
  ' "$snap"
}

# --- struggles -------------------------------------------------------------
# Failed tool calls and the patterns around them — signs the agent was
# struggling. Each call is normalized to {label, input, failed, type, text,
# harmless} and summarized by STRUGGLES_JQ:
#   errors_by_type   failure type -> tool label -> count, where type is
#                    "exit N" (shell exit code), "timeout", "interrupted",
#                    "blocked" (permission / harness refusal), "MCP tool
#                    error" or "tool error"; shell calls are labelled by
#                    their first real program, skipping cd / export / plain
#                    VAR=value parts ("Bash: git", "Bash: bk build view")
#   likely harmless  exit 1 with no output from grep/rg/test/diff/... ("no
#                    match" / "false", not a struggle) — counted under
#                    "exit 1 (likely harmless)" and left out of the patterns
#   longest_failure_streak  most failed calls in a row
#   repeated_failures       the same call (tool + exact input) failing 2+ times
#   retries_after_failure   a failed call followed straight away by another
#                           call with the same label; recovered_after_retry =
#                           how many of those retries succeeded
# Dialects: claude (tool_result is_error; "Exit code N" text; toolUseResult
# interrupted) and cursor (completed tool_call events: a `failure` result,
# shell failure.exitCode / aborted / stderr, MCP success.isError) — both
# checked against real transcripts. cursor-cloud is not implemented yet: it
# reports {"implemented": false}.
STRUGGLES_JQ='
  def first_line: tostring | split("\n") | map(select(test("\\S"))) | (.[0] // "") | .[0:160];
  # The first real program in the command, across all lines: skips comment
  # lines and cd/export/VAR= setup (e.g. "cd /tmp\n# note\nsed ..." -> "sed").
  # Always a string, even for a command that is only setup.
  def shell_program:
    ([bk_cli_invocations[]] | first) // (
      split("\n")
      | map(select(test("^\\s*#") | not))
      | map([splits("\\s*(?:&&|\\|\\||;|\\|)\\s*")]) | add // []
      | map(sub("^\\s+"; "") | sub("^(?:[A-Za-z_][A-Za-z0-9_]*=\\S*\\s+)+"; ""))
      | map(select(. != ""
                    and (test("^(?:cd|export|set|source|\\.)(?:\\s|$)") | not)
                    and (test("^[A-Za-z_][A-Za-z0-9_]*=\\S*$") | not)))
      | (.[0] // "") | (split(" ")[0] // "") | sub(".*/"; ""));
  def harmless_programs: ["grep", "egrep", "fgrep", "rg", "test", "[", "[[", "diff", "cmp", "false", "pgrep", "which", "command"];
  def struggle_summary:
    . as $calls
    | ($calls | map(select(.failed))) as $failed
    | ($failed | map(select(.harmless | not))) as $real
    # longest run of real failures (a harmless failure neither extends nor breaks it)
    | (reduce ($calls[] | select((.failed and .harmless) | not)) as $c ({cur: 0, max: 0};
         if $c.failed then .cur += 1 | .max = ([.max, .cur] | max) else .cur = 0 end) | .max) as $streak
    | ([range(0; ($calls | length) - 1) as $i
         | select($calls[$i].failed and ($calls[$i].harmless | not) and $calls[$i + 1].label == $calls[$i].label)
         | $calls[$i + 1].failed | not]) as $retries
    | {
        implemented: true,
        tool_calls: ($calls | length),
        failed_calls: ($failed | length),
        likely_harmless: ($failed | map(select(.harmless)) | length),
        errors_by_type: ($failed
          | map(. + {type: (if .harmless then "exit 1 (likely harmless)" else .type end)})
          | group_by(.type)
          | map({key: .[0].type,
                 value: (group_by(.label) | sort_by(-length, .[0].label) | map({key: .[0].label, value: length}) | from_entries)})
          | from_entries),
        longest_failure_streak: $streak,
        repeated_failures: ($real | group_by([.label, (.input | tojson)]) | map(select(length > 1))
          | map({label: .[0].label, count: length,
                 call: ((.[0].input.command? // (.[0].input | tojson)) | tostring | .[0:160])})
          | sort_by(-.count)),
        retries_after_failure: ($retries | length),
        recovered_after_retry: ($retries | map(select(.)) | length),
        examples: ($real | group_by(.type)
          | map({key: .[0].type, value: (map("\(.label): \(.text | first_line)") | unique | .[0:3])})
          | from_entries)
      };'

struggles() {
  local f="$1"
  [[ -f "$f" ]] || { echo "no such transcript: $f" >&2; exit 1; }
  if [[ "$AGENT" == "cursor-cloud" ]]; then
    echo "WARNING: struggles are not implemented for cursor-cloud yet (its failed-call format is unverified)." >&2
    jq -n '{implemented: false, reason: "not implemented for cursor-cloud yet"}'
    return
  fi
  if [[ "$AGENT" == "cursor" ]]; then
    jq -n "$CURSOR_TOOL_NAME_JQ$BK_CLI_JQ$STRUGGLES_JQ"'
      [inputs | select(type == "object")] as $all
      # Results come on the completed events, keyed by call_id.
      | ($all | map(select(.type == "tool_call" and .subtype == "completed" and (.call_id // "") != ""
                           and (.tool_call | type) == "object"))
              | map({key: (.call_id | tostring), value: (.tool_call | to_entries[0].value.result // {})})
              | from_entries) as $results
      | [ $all | cursor_calls | .[]
          | (.tool_call | to_entries[0]) as $t
          | ($t.value.args // {}) as $a
          | ($results[(.call_id // "") | tostring] // {}) as $r
          | ($r.failure // null) as $fail
          | (($t.key == "mcpToolCall") and (($r.success.isError? // false) == true)) as $mcp_err
          | ($t.key == "shellToolCall") as $shell
          | (if $shell then ($a.command // "" | shell_program | "Bash: " + .)
             else (cursor_tool_name($t) | sub("^mcp__[^_]+(?:_[^_]+)*?__"; "")) end) as $label
          | {label: $label,
             input: (if $t.key == "mcpToolCall" then ($a.args // $a.arguments // {}) else $a end),
             failed: ($fail != null or $mcp_err),
             type: (if $mcp_err then "MCP tool error"
                    elif $fail == null then null
                    elif ($fail.aborted? // false) then "interrupted"
                    elif ($fail.exitCode? // null) != null then "exit \($fail.exitCode)"
                    else "tool error" end),
             text: (if $mcp_err then ($r.success.content // [] | map(.text // "") | join(" "))
                    else ($fail.stderr? // $fail.message? // $fail.error? // ($fail | tojson)) end),
             harmless: ($shell and ($fail.exitCode? // null) == 1
                        and ((($fail.stdout? // "") + ($fail.stderr? // "")) | test("\\S") | not)
                        and (($a.command // "" | shell_program) as $p | harmless_programs | index($p) != null))} ]
      | struggle_summary
    ' "$f"
    return
  fi
  jq -n "$BK_CLI_JQ$STRUGGLES_JQ"'
    [inputs] as $all
    # tool_use blocks in transcript order (deduped by id, first seen wins)
    | (reduce ($all[] | (.message.content? // []) | select(type == "array") | .[] | select(.type == "tool_use")) as $u
         ({seen: {}, out: []}; if .seen[$u.id] then . else .seen[$u.id] = true | .out += [$u] end) | .out) as $uses
    | ($all | [ .[] | . as $l | (.message.content? // []) | select(type == "array") | .[]
                | select(.type == "tool_result" and .tool_use_id != null)
                | {key: .tool_use_id,
                   value: {is_error: (.is_error // false),
                           text: ((.content // "") | if type == "array" then (map(.text // "") | join("")) else tostring end),
                           tur: (if ($l.toolUseResult | type) == "object" then $l.toolUseResult else {} end)}} ]
              | from_entries) as $results
    | [ $uses[]
        | ($results[.id] // {is_error: false, text: "", tur: {}}) as $r
        | (.name == "Bash") as $shell
        | (.input.command? // "" | tostring) as $cmd
        # null (not empty) when there is no exit code, or the call would be dropped
        | ([$r.text | capture("^Exit code (?<n>[0-9]+)")? | .n] | first) as $exit
        | {label: (if $shell then ($cmd | shell_program | "Bash: " + .) else (.name | sub("^mcp__[^_]+(?:_[^_]+)*?__"; "")) end),
           input: .input,
           failed: ($r.is_error or ($r.tur.interrupted? // false)),
           type: (if ($r.tur.interrupted? // false) or ($r.text | test("\\[Request interrupted")) then "interrupted"
                  elif $exit != null then "exit \($exit)"
                  elif ($r.text | test("timed out"; "i")) then "timeout"
                  elif ($r.text | test("not permitted|permission|haven.t granted|requires approval|denied|isolated in the worktree|Refusing to run|blocked"; "i")) then "blocked"
                  elif (.name | startswith("mcp__")) then "MCP tool error"
                  else "tool error" end),
           text: ($r.text | sub("^Exit code [0-9]+\n?"; "")),
           harmless: ($shell and $exit == "1"
                      and ($r.text | sub("^Exit code [0-9]+\n?"; "") | test("\\S") | not)
                      and (($cmd | shell_program) as $p | harmless_programs | index($p) != null))} ]
    | struggle_summary
  ' "$f"
}

# struggles_markdown <struggles.json>  -- render the struggles summary.
struggles_markdown() {
  jq -r '
    if (.implemented // false) | not then
      "> ⚠️ **Struggles report not implemented for this agent yet** (\(.reason // "unknown reason")). Failed tool calls are not counted."
    else
      "**Tool calls: \(.tool_calls), failed: \(.failed_calls)** (likely harmless: \(.likely_harmless), e.g. grep with no match)",
      "",
      (if (.errors_by_type | length) > 0 then
        "**Failed tool calls by type** (type → tool → failed calls):", "", "```",
        (.errors_by_type | to_entries[] | "\(.key):", (.value | to_entries[] | "  \(.key): \(.value)")),
        "```", ""
       else empty end),
      "- Longest run of failed calls in a row: **\(.longest_failure_streak)**",
      "- Failed, then the same tool straight away: **\(.retries_after_failure)** (succeeded on that retry: \(.recovered_after_retry))",
      "- Same call failing more than once: **\(.repeated_failures | length)**",
      (.repeated_failures[0:5][] | "  - \(.count)× `\(.label)`: `\(.call | gsub("`"; "\u0027"))`"),
      (if (.examples | length) > 0 then
        "", "<details><summary>Example errors</summary>", "",
        (.examples | to_entries[] | "**\(.key)**", (.value[] | "- `\(gsub("`"; "\u0027"))`"), ""),
        "</details>"
       else empty end)
    end
  ' "$1"
}

# --- model API retries -----------------------------------------------------
# The agent harness retrying its call to the MODEL API (e.g. a 504 or 429
# from the model gateway) — not a Buildkite request and not a tool call, so
# neither the API fan-out nor the struggles report sees it. Claude Code emits
# one {"type":"system","subtype":"api_retry", attempt, max_retries,
# retry_delay_ms, error_status, error} event per retry in its stream-json
# output (claude.sh's raw log, CLAUDE_RAW_LOG). Grouped by status; a null
# status (no HTTP response) is keyed by the error name. Other agents emit no
# known equivalent yet: {"available": false}.
model_retries() {
  local f="${1:-}"
  if [[ "$AGENT" != "claude" || -z "$f" || ! -f "$f" ]]; then
    jq -n --arg agent "$AGENT" '{available: false, reason: "no model API retry events known for agent \($agent) (or no raw stream captured)"}'
    return
  fi
  # The raw stream may hold non-JSON lines; skip them.
  jq -Rn '
    [inputs | fromjson? | select(type == "object" and .type == "system" and .subtype == "api_retry")] as $r
    | {
        available: true,
        retries: ($r | length),
        wait_seconds: ((($r | map(.retry_delay_ms // 0) | add) // 0) / 1000 | . * 10 | round / 10),
        max_attempt: ($r | map(.attempt // 0) | max // 0),
        max_retries: ($r | map(.max_retries // 0) | max // 0),
        by_status: ($r
          | map(. + {key: (if .error_status != null then (.error_status | tostring) else (.error // "unknown") end)})
          | group_by(.key)
          | map({key: .[0].key,
                 value: {retries: length,
                         wait_seconds: ((map(.retry_delay_ms // 0) | add) / 1000 | . * 10 | round / 10),
                         error: (map(.error // empty) | unique | join(", "))}})
          | from_entries)
      }' "$f"
}

# model_retries_markdown <model-retries.json>  -- render it.
model_retries_markdown() {
  jq -r '
    "**Model API retries** (the agent harness retrying its call to the model API — not the agent, not Buildkite):",
    "",
    if (.available // false) | not then
      "_Not available: \(.reason // "unknown")._"
    elif .retries == 0 then
      "_None._"
    else
      "```",
      (.by_status | to_entries[] | "\(.key): \(.value.retries) retr\(if .value.retries == 1 then "y" else "ies" end) (\(.value.wait_seconds)s waiting\(if .value.error != "" then ", \(.value.error)" else "" end))"),
      "```",
      "",
      "Total: \(.retries) retries, \(.wait_seconds)s waiting; highest attempt \(.max_attempt) of \(.max_retries)."
    end
  ' "$1"
}

# --- dispatch --------------------------------------------------------------
case "${1:-}" in
  model-retries)
    model_retries "${2:-}"
    ;;
  model-retries-markdown)
    model_retries_markdown "${2:?model-retries-markdown needs the model-retries JSON}"
    ;;
  struggles)
    f="${2:-$(newest_session)}"
    [[ -n "$f" ]] || { echo "no sessions in $PROJECT_DIR" >&2; exit 1; }
    struggles "$f"
    ;;
  struggles-markdown)
    struggles_markdown "${2:?struggles-markdown needs the struggles JSON}"
    ;;
  list)
    echo "# project dir: $PROJECT_DIR"
    ls -lt "$PROJECT_DIR"/*.jsonl 2>/dev/null || echo "(no sessions found)"
    ;;
  diff)
    a="${2:?diff needs two transcripts}"; b="${3:?diff needs two transcripts}"
    echo "# diff: $a  <->  $b"
    diff <(extract "$a" | jq -c 'del(.id)') <(extract "$b" | jq -c 'del(.id)') || true
    ;;
  metrics)
    f="${2:-$(newest_session)}"
    [[ -n "$f" ]] || { echo "no sessions in $PROJECT_DIR" >&2; exit 1; }
    metrics "$f"
    ;;
  "")
    f="$(newest_session)"
    [[ -n "$f" ]] || { echo "no sessions in $PROJECT_DIR" >&2; exit 1; }
    print_session "$f"
    ;;
  *)
    print_session "$1"
    ;;
esac
