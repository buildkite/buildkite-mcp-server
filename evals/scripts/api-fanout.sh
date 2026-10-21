#!/usr/bin/env bash
#
# api-fanout.sh — how many Buildkite API requests each MCP tool call made in
# one eval run ("fan-out").
#
# Input is the run's request log (<run>.api-requests.jsonl): JSON lines from
# the MCP server's --request-log (source "mcp"), one per API request, tagged
# with the tool name + tool call id that made it.
#
# Usage:
#   api-fanout.sh json <requests.jsonl> [metrics.json]   -> fan-out JSON
#   api-fanout.sh markdown <fanout.json>                  -> markdown tables
#   api-fanout.sh errors-markdown <fanout.json>           -> failed requests only
#
# metrics.json (bk-tool-audit-v2.sh metrics) supplies the number of calls per
# MCP tool from the transcript, so tools whose calls made no API request at
# all still count in the per-call average. Without it, calls = tool calls
# that made at least one request.
#
# Endpoints are grouped by a normalized path: query dropped, ids replaced —
# the segment after a plural collection becomes :<singular> (builds/123 ->
# builds/:build), and any other number / uuid becomes :id. GraphQL requests
# are grouped by operation name. Heuristic, for reading, not exact routing.
#
# Failed requests (HTTP >= 400, or no response: "network error") are also
# grouped by status, then by MCP tool (errors_by_status).
#
set -euo pipefail

command -v jq >/dev/null || { echo "api-fanout: jq is required" >&2; exit 1; }

FANOUT_JQ='
  def endpoint:
    if (.api // "") == "graphql" then "POST graphql \(.graphql_operation // "(unnamed)")"
    else
      (.method // "?") as $method
      | (.url // "" | sub("^[a-z]+://[^/]+"; "") | sub("[?#].*$"; "") | split("/")) as $segs
      | [ range(0; $segs | length) as $i
          | $segs[$i] as $s
          | (if $i > 0 then $segs[$i - 1] else "" end) as $prev
          | if ($prev | test("^[a-z_-]+s$")) and ($prev | IN("analytics", "status") | not) and $s != ""
            then ":" + ($prev | rtrimstr("s"))
            elif ($s | test("^[0-9]+$|^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$"))
            then ":id"
            else $s end ]
      | "\($method) \(join("/"))"
    end;
  def top_endpoints: group_by(endpoint) | map("\(length) \(.[0] | endpoint)") | sort_by(split(" ")[0] | tonumber) | reverse;
  def r2: (. * 100 | round) / 100;
'

cmd_json() {
    local log="$1" metrics="${2:-}"
    local metrics_json='{}'
    if [[ -n "$metrics" && -s "$metrics" ]] && jq -e 'type == "object"' "$metrics" >/dev/null 2>&1; then
        metrics_json="$(cat "$metrics")"
    fi
    local lines='[]'
    if [[ -s "$log" ]]; then
        # Skip any partial/corrupt line rather than failing the whole summary.
        lines="$(jq -cn '[inputs | select(type == "object")]' "$log" 2>/dev/null \
            || jq -Rcn '[inputs | fromjson? | select(type == "object")]' "$log")"
    fi
    # The log goes in on stdin: a single command-line argument is capped at
    # 128 KiB, which a long run's log can exceed.
    jq --argjson m "$metrics_json" "$FANOUT_JQ"'
      . as $all
      | ($all | map(select(.source == "mcp" and .method != null))) as $mcp
      | if ($mcp | length) == 0 and ($all | any(.source == "mcp" and .type == "started") | not) then
          {available: false, reason: "MCP request logging did not start (no startup record or requests)"}
        else
      # Calls per MCP tool from the transcript: "N mcp__<server>__<tool>".
        (($m.tool_calls_by_name // [])
          | map(capture("^(?<n>[0-9]+) mcp__[^_]+(?:_[^_]+)*?__(?<tool>.+)$")?)
          | group_by(.tool) | map({key: .[0].tool, value: (map(.n | tonumber) | add)}) | from_entries) as $calls
      | ($mcp | map(select((.tool // "") != ""))) as $attributed
      | {
          available: true,
          total_requests: ($mcp | length),
          errors: ($mcp | map(select((.error // null) != null or (.status // 0) >= 400)) | length),
          # Failed requests grouped by HTTP status ("network error" when no
          # response came back), then by the MCP tool that made them:
          # {"429": {"get_build": 2}, "504": {"list_builds": 2}}.
          errors_by_status: (
            ($mcp | map(. + {who: (if (.tool // "") == "" then "(no tool call)" else .tool end)}))
            | map(select((.error // null) != null or (.status // 0) >= 400)
                  | . + {status_key: (if (.error // null) != null then "network error" else (.status | tostring) end)})
            | group_by(.status_key)
            | map({key: .[0].status_key,
                   value: (group_by(.who) | sort_by(-length, .[0].who) | map({key: .[0].who, value: length}) | from_entries)})
            | from_entries),
          mcp: {
            requests: ($mcp | length),
            unattributed_requests: ($mcp | map(select((.tool // "") == "")) | length),
            tools: ([($attributed | map(.tool)), ($calls | keys)] | add | unique
              | map(. as $t
                  | ($attributed | map(select(.tool == $t))) as $reqs
                  | ($reqs | group_by(.tool_call_id) | map(length)) as $per_call
                  | (($calls[$t] // 0) as $c | if $c > 0 then $c else ($per_call | length) end) as $n
                  | {tool: $t, calls: $n, calls_with_requests: ($per_call | length),
                     requests: ($reqs | length),
                     avg_per_call: (if $n > 0 then (($reqs | length) / $n | r2) else null end),
                     max_per_call: ($per_call | max // 0),
                     endpoints: ($reqs | top_endpoints)})
              | sort_by(-.requests, .tool))
          }
        } end' <<<"$lines"
}

cmd_markdown() {
    local fanout="$1"
    jq -r '
      def cell: if . == null then "–" else tostring end;
      def eps: (.endpoints // [])[0:3] | map("`\(.)`") | join("<br>");
      if .available == false then
        "**API fan-out not available:** \(.reason)."
      else
      "**Buildkite API requests: \(.total_requests)** (MCP server: \(.mcp.requests); 4xx/5xx or failed: \(.errors))",
      "",
      (if ((.errors_by_status // {}) | length) > 0 then
        "**API errors by status** (status → MCP tool → failed requests):",
        "",
        "```",
        (.errors_by_status | to_entries[] | "\(.key):", (.value | to_entries[] | "  \(.key): \(.value)")),
        "```",
        ""
       else empty end),
      (if (.mcp.tools | length) > 0 then
        "| MCP tool | Calls | API requests | Avg / call | Max / call | Top endpoints |",
        "|---|---:|---:|---:|---:|---|",
        (.mcp.tools[] | "| `\(.tool)` | \(.calls) | \(.requests) | \(.avg_per_call | cell) | \(.max_per_call) | \(eps) |"),
        (if .mcp.unattributed_requests > 0 then "", "_\(.mcp.unattributed_requests) MCP server request(s) were not made inside a tool call._" else empty end),
        ""
       else empty end),
      (if (.mcp.tools | length) == 0 then "_No MCP tool calls recorded._" else empty end)
      end
    ' "$fanout"
}

# Errors only (for the struggles annotation): failed API requests by status
# then tool.
cmd_errors_markdown() {
    local fanout="$1"
    jq -r '
      if .available == false then
        "**Buildkite API errors:** _not available: \(.reason)._"
      else
      "**Buildkite API errors** (failed API requests from the MCP server, by status → tool):",
      "",
      (if ((.errors_by_status // {}) | length) > 0 then
        "```",
        (.errors_by_status | to_entries[] | "\(.key):", (.value | to_entries[] | "  \(.key): \(.value)")),
        "```"
       else "_None — every API request got a successful response._" end),
      ""
      end
    ' "$fanout"
}

case "${1:-}" in
    json)     cmd_json "${2:?api-fanout.sh json <requests.jsonl> [metrics.json]}" "${3:-}" ;;
    markdown) cmd_markdown "${2:?api-fanout.sh markdown <fanout.json>}" ;;
    errors-markdown) cmd_errors_markdown "${2:?api-fanout.sh errors-markdown <fanout.json>}" ;;
    *) sed -n '2,27p' "$0"; exit 2 ;;
esac
