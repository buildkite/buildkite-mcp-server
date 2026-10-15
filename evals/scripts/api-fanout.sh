#!/usr/bin/env bash
#
# api-fanout.sh — how many Buildkite API requests each MCP tool call and each
# bk CLI command made in one eval run ("fan-out").
#
# Input is the run's request log (<run>.api-requests.jsonl): JSON lines from
#   - the MCP server's --request-log (source "mcp"): one per API request,
#     tagged with the tool name + tool call id that made it
#   - bk-request-log-shim.mjs (source "bk"): one per API request, tagged with
#     the bk invocation (argv), plus one "invocation" line per bk run
#
# Usage:
#   api-fanout.sh json <requests.jsonl> [metrics.json]   -> fan-out JSON
#   api-fanout.sh markdown <fanout.json>                  -> markdown tables
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
# grouped by status, then by MCP tool / bk command (errors_by_status).
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
  def bk_command:
    (.argv // []) | map(tostring)
    | (until(length == 0 or (.[0] | startswith("-") | not); .[1:])) as $rest
    | ([ $rest[0:2][] ] | until(length == 0 or all(test("^[a-z][a-z-]*$")); .[0:-1])) as $words
    | "bk" + ($words | map(" " + .) | join(""));
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
    jq -n --argjson all "$lines" --argjson m "$metrics_json" "$FANOUT_JQ"'
      ($all | map(select(.source == "mcp"))) as $mcp
      | ($all | map(select(.source == "bk" and .type == "request"))) as $bkreq
      | ($all | map(select(.source == "bk" and .type == "invocation"))) as $bkinv
      # Calls per MCP tool from the transcript: "N mcp__<server>__<tool>".
      | (($m.tool_calls_by_name // [])
          | map(capture("^(?<n>[0-9]+) mcp__[^_]+(?:_[^_]+)*?__(?<tool>.+)$")?)
          | group_by(.tool) | map({key: .[0].tool, value: (map(.n | tonumber) | add)}) | from_entries) as $calls
      | ($mcp | map(select((.tool // "") != ""))) as $attributed
      | {
          total_requests: ($mcp + $bkreq | length),
          errors: ($mcp + $bkreq | map(select((.error // null) != null or (.status // 0) >= 400)) | length),
          # Failed requests grouped by HTTP status ("network error" when no
          # response came back), then by the MCP tool / bk command that made
          # them: {"429": {"get_build": 2}, "504": {"bk build view": 1}}.
          errors_by_status: (
            ($mcp | map(. + {who: (if (.tool // "") == "" then "(no tool call)" else .tool end)}))
            + ($bkreq | map(. + {who: bk_command}))
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
          },
          bk_cli: {
            requests: ($bkreq | length),
            invocations: ($bkinv | length),
            commands: ([($bkinv | map(bk_command)), ($bkreq | map(bk_command))] | add | unique
              | map(. as $cmd
                  | ($bkinv | map(select(bk_command == $cmd))) as $inv
                  | ($bkreq | map(select(bk_command == $cmd))) as $reqs
                  | ($reqs | group_by(.invocation) | map(length)) as $per_inv
                  | {command: $cmd, invocations: ($inv | length),
                     failed_invocations: ($inv | map(select((.exit_code // 0) != 0)) | length),
                     requests: ($reqs | length),
                     avg_per_invocation: (if ($inv | length) > 0 then (($reqs | length) / ($inv | length) | r2) else null end),
                     max_per_invocation: ($per_inv | max // 0),
                     endpoints: ($reqs | top_endpoints)})
              | sort_by(-.requests, .command))
          }
        }'
}

cmd_markdown() {
    local fanout="$1"
    jq -r '
      def cell: if . == null then "–" else tostring end;
      def eps: (.endpoints // [])[0:3] | map("`\(.)`") | join("<br>");
      "**Buildkite API requests: \(.total_requests)** (MCP server: \(.mcp.requests), bk CLI: \(.bk_cli.requests); 4xx/5xx or failed: \(.errors))",
      "",
      (if ((.errors_by_status // {}) | length) > 0 then
        "**API errors by status** (status → MCP tool / bk command → failed requests):",
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
      (if (.bk_cli.commands | length) > 0 then
        "| bk command | Runs | API requests | Avg / run | Max / run | Top endpoints |",
        "|---|---:|---:|---:|---:|---|",
        (.bk_cli.commands[] | "| `\(.command)` | \(.invocations)\(if .failed_invocations > 0 then " (\(.failed_invocations) failed)" else "" end) | \(.requests) | \(.avg_per_invocation | cell) | \(.max_per_invocation) | \(eps) |"),
        ""
       else empty end),
      (if (.mcp.tools | length) == 0 and (.bk_cli.commands | length) == 0 then "_No MCP tool calls or bk commands recorded._" else empty end)
    ' "$fanout"
}

case "${1:-}" in
    json)     cmd_json "${2:?api-fanout.sh json <requests.jsonl> [metrics.json]}" "${3:-}" ;;
    markdown) cmd_markdown "${2:?api-fanout.sh markdown <fanout.json>}" ;;
    *) sed -n '2,25p' "$0"; exit 2 ;;
esac
