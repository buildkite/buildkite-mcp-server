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

TOOL_FILTER='startswith("mcp__buildkite-mcp-server__")'
NAMES_ONLY=0
WITH_RESULTS=0
PROJECT_PATH="$PWD"

# --- parse options ---------------------------------------------------------
while [[ "${1:-}" == -* ]]; do
  case "$1" in
    --all)     TOOL_FILTER='true'; shift ;;
    --names)   NAMES_ONLY=1; shift ;;
    --results) WITH_RESULTS=1; shift ;;
    --project) PROJECT_PATH="${2:?--project needs a path}"; shift 2 ;;
    -h|--help) sed -n '2,34p' "$0"; exit 0 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
done

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

# Emit one compact JSON object per tool_use in a transcript.
extract() {
  local f="$1"
  jq -c --argjson names "$NAMES_ONLY" '
    select(.message.content) | .message.content[]?
    | select(.type=="tool_use" and (.name | '"$TOOL_FILTER"'))
    | if $names == 1 then {tool: .name}
      else {id: .id, tool: .name, input: .input} end
  ' "$f"
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
    # Map tool_use_id -> truncated result text.
    local map; map="$(jq -cn '
      [ inputs | (.message.content? // []) | select(type=="array") | .[]
        | select(.type=="tool_result")
        | {(.tool_use_id): ((.content // "")
             | if type=="array" then (map(.text // "") | join("")) else tostring end
             | gsub("\n";" ") | .[0:300])} ]
      | add // {}' "$f")"
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
