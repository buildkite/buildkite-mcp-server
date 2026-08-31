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
#   ./bk-tool-audit.sh metrics [session.jsonl]    # token totals, cost, tool counts, duration
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
    jq -nr "$CURSOR_CLOUD_CALLS_JQ"'
      [inputs | select(type=="object")] as $all
      | ($all | cloud_tool_events | dedupe_calls) as $tools
      | ($all | map(select(.type=="result")) | last | .data // {}) as $res
      | ($all | map(select(.type=="usage")) | last | .data.totalUsage // null) as $u
      | ($all | map(select(.type=="agent_meta")) | first | .data // {}) as $meta
      | {
          user_messages: null,
          assistant_responses: null,
          duration_min: (if ($res.durationMs // null) != null then ($res.durationMs/60000|floor) else null end),
          models: {(($meta.model // "unknown") | tostring): 1},
          tokens: (if $u != null then
                     {input: ($u.inputTokens // 0), output: ($u.outputTokens // 0),
                      cache_write: ($u.cacheWriteTokens // 0), cache_read: ($u.cacheReadTokens // 0)}
                   else null end),
          tool_calls_total: ($tools | length),
          tool_calls_by_name: ($tools | map(cloud_tool_name(.)) | group_by(.) | map("\(length) \(.[0])") | sort | reverse),
          est_cost_usd: null
        }
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
    jq -nr "$CURSOR_TOOL_NAME_JQ"'
      [inputs | select(type=="object")] as $all
      | ($all | cursor_calls
              | map((.tool_call | to_entries[0]) as $t | cursor_tool_name($t))) as $tools
      | ($all | map(select(.type=="result")) | last) as $res
      | ($all | map(select(.type=="system" and .subtype=="init")) | first) as $init
      | {
          user_messages: ($all | map(select(.type=="user")) | length),
          assistant_responses: ($all | map(select(.type=="assistant")) | length),
          duration_min: (if ($res.duration_ms? // null) != null then ($res.duration_ms/60000|floor) else null end),
          models: {(($init.model? // "unknown")): 1},
          tokens: null,
          tool_calls_total: ($tools | length),
          tool_calls_by_name: ($tools | group_by(.) | map("\(length) \(.[0])") | sort | reverse),
          est_cost_usd: null
        }
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
  jq -nr --argjson p "$prices" '
    def n: . // 0;
    [inputs] as $all
    | ($all | map(select(.type=="assistant")) | group_by(.message.id) | map(.[0])) as $resp
    | ($resp | map(.message.usage)) as $u
    | ($all | [ .[] | (.message.content? // []) | select(type=="array") | .[]
                | select(.type=="tool_use") | {id, name} ] | unique_by(.id)) as $tools
    | ($all | map(.timestamp) | map(select(type=="string"))
            | map(sub("\\.[0-9]+";"") | fromdateiso8601)) as $secs
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
        models: ($resp | map(.message.model // "unknown") | group_by(.) | map({(.[0]): length}) | add),
        tokens: {input: $tin, output: $tout, cache_write_5m: $c5, cache_write_1h: $c1h, cache_read: $tread},
        tool_calls_total: ($tools|length),
        tool_calls_by_name: ($tools | group_by(.name) | map("\(length) \(.[0].name)") | sort | reverse),
        est_cost_usd: (($cost*100|round)/100)
      }
  ' "$snap"
}

# --- dispatch --------------------------------------------------------------
case "${1:-}" in
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
