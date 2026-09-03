#!/usr/bin/env bash
#
# dd-metrics.sh — publish one eval entry's run metrics to Datadog.
#
# Reads the bk-tool-audit-v2.sh metrics JSON plus run metadata from the
# environment and submits gauge series to the Datadog metrics v2 API
# (POST https://api.<site>/api/v2/series). Deliberately BEST-EFFORT:
# dd-publish.sh tolerates per-entry failures so a Datadog outage never fails
# a build, and a missing DD_API_KEY is a loud SKIP (exit 0) so local runs and
# forks without Datadog wiring keep working unchanged.
#
# SECURITY: this script must run strictly OUTSIDE the eval-agent run — the
# agent has unrestricted Bash, and a key present anywhere during the run
# stays readable to same-UID processes via /proc/<pid>/environ even after
# `unset`. Only the CI dd-publish step drives it (via dd-publish.sh, from
# the run-bundle artifacts, key scoped to that step). Local eval runs never
# publish; use DD_DRY_RUN=true to exercise this script by hand.
#
# Env contract (dd-publish.sh provides these):
#   DD_API_KEY              Datadog API key. Unset/empty = skip (exit 0).
#   DD_SITE                 Datadog site (default: datadoghq.com).
#   DD_METRIC_PREFIX        Metric namespace (default: mcp_eval).
#   DD_TIMESTAMP            optional point timestamp (epoch secs, the run's
#                           end). Default: now. Clamped to Datadog's ingest
#                           window (points older than ~1h are dropped
#                           server-side, which would lose early entries of a
#                           long matrix when publishing happens post-build).
#   ENTRY_ID                evals.yaml entry id            -> tag entry:
#   PROMPT_NAME             prompt template name           -> tag prompt:
#   AGENT                   claude | cursor | cursor-cloud -> tag agent:
#   MODEL                   model id ("default" if unset)  -> tag model:
#   GOAL_ACHIEVED           true | false | unknown         -> tag goal:
#   EVAL_DURATION_SECONDS   agent wall-clock measured by run_entry
#   METRICS_FILE            path to the metrics JSON. May be empty or hold
#                           null fields (cursor runs emit no token usage);
#                           null metrics are simply not submitted, so gaps in
#                           Datadog mean "not measured", never zero.
#   BUILDKITE_BUILD_NUMBER  optional                       -> tag buildkite_build:
#
# Submitted series (all gauges; the point timestamp is DD_TIMESTAMP — the
# run's end — falling back to submission time; that carries the "datetime"
# dimension):
#   <prefix>.run                        always 1 — count runs / slice by tags
#   <prefix>.goal_achieved              1/0; omitted when goal is unknown
#   <prefix>.duration_seconds           agent wall-clock
#   <prefix>.tool_time_seconds          wall-clock inside tool calls (claude only)
#   <prefix>.wait_seconds               wall-clock inside WAITING tool calls
#                                       (wait_for_build / Bash sleep; claude only)
#   <prefix>.tool_calls.total
#   <prefix>.tool_calls.buildkite_mcp   calls with the mcp__ prefix
#   <prefix>.tokens.input / .output / .cache_read / .cache_write
#
set -euo pipefail

if [[ -z "${DD_API_KEY:-}" ]]; then
    echo "dd-metrics: DD_API_KEY is unset; skipping Datadog publish (see .buildkite/pipeline.evals.yml)." >&2
    exit 0
fi
command -v jq >/dev/null   || { echo "dd-metrics: jq is required" >&2; exit 1; }
command -v curl >/dev/null || { echo "dd-metrics: curl is required" >&2; exit 1; }

DD_SITE="${DD_SITE:-datadoghq.com}"
DD_METRIC_PREFIX="${DD_METRIC_PREFIX:-mcp_eval}"

# Tolerate a missing/empty/corrupt metrics file: identity tags and duration
# still get published, token/tool series are just omitted.
METRICS_JSON="{}"
if [[ -n "${METRICS_FILE:-}" && -s "${METRICS_FILE:-}" ]]; then
    if jq -e . "$METRICS_FILE" >/dev/null 2>&1; then
        METRICS_JSON="$(cat "$METRICS_FILE")"
    else
        echo "dd-metrics: $METRICS_FILE is not valid JSON; publishing identity/duration series only." >&2
    fi
fi

# Point timestamp: prefer the recorded run end (CI publishes after the whole
# matrix finishes). Clamp into Datadog's accepted window — points older than
# ~1h are silently dropped, and a clamped-but-present point beats a lost one.
NOW="$(date +%s)"
TS="${DD_TIMESTAMP:-$NOW}"
[[ "$TS" =~ ^[0-9]+$ ]] || TS="$NOW"
MIN_TS=$(( NOW - 3300 ))
if (( TS < MIN_TS )); then
    echo "dd-metrics: DD_TIMESTAMP $TS is outside the Datadog ingest window; clamping to $MIN_TS." >&2
    TS="$MIN_TS"
fi
(( TS > NOW )) && TS="$NOW"

PAYLOAD="$(jq -n \
    --arg prefix   "$DD_METRIC_PREFIX" \
    --arg entry    "${ENTRY_ID:-unknown}" \
    --arg prompt   "${PROMPT_NAME:-unknown}" \
    --arg agent    "${AGENT:-unknown}" \
    --arg model    "${MODEL:-default}" \
    --arg goal     "${GOAL_ACHIEVED:-unknown}" \
    --arg duration "${EVAL_DURATION_SECONDS:-}" \
    --arg build    "${BUILDKITE_BUILD_NUMBER:-}" \
    --argjson now  "$TS" \
    --argjson m    "$METRICS_JSON" '
    # type 3 = gauge in the v2 series API. A null value emits nothing: absent
    # in Datadog must mean "not measured" (cursor tokens), never zero.
    def gauge($name; $v; $tags):
      if $v == null then empty
      else {metric: "\($prefix).\($name)", type: 3,
            points: [{timestamp: $now, value: ($v | tonumber)}], tags: $tags}
      end;
    ([ "entry:\($entry)", "prompt:\($prompt)", "agent:\($agent)",
       "model:\($model)", "goal:\($goal)" ]
     + (if $build != "" then ["buildkite_build:\($build)"] else [] end)) as $tags
    | ($m.tokens // {}) as $tok
    # claude reports the cache-write split (5m/1h); cursor-cloud a single
    # cache_write. Publish one combined series so dashboards compare agents.
    | (($tok.cache_write_5m // 0) + ($tok.cache_write_1h // 0) + ($tok.cache_write // 0)) as $cw
    | {series: [
        gauge("run"; 1; $tags),
        (if $goal == "true" then gauge("goal_achieved"; 1; $tags)
         elif $goal == "false" then gauge("goal_achieved"; 0; $tags)
         else empty end),
        gauge("duration_seconds"; (if $duration == "" then null else $duration end); $tags),
        gauge("tool_time_seconds"; ($m.tool_time_seconds // null); $tags),
        gauge("wait_seconds"; (($m.wait // {}) | .seconds // null); $tags),
        gauge("tool_calls.total"; ($m.tool_calls_total // null); $tags),
        gauge("tool_calls.buildkite_mcp"; ($m.tool_calls_mcp // null); $tags),
        gauge("tokens.input"; ($tok.input // null); $tags),
        gauge("tokens.output"; ($tok.output // null); $tags),
        gauge("tokens.cache_read"; ($tok.cache_read // null); $tags),
        gauge("tokens.cache_write"; (if ($m.tokens // null) == null then null else $cw end); $tags)
      ]}
')"

# Local testing: print the payload instead of submitting it.
if [[ "${DD_DRY_RUN:-false}" == "true" ]]; then
    jq . <<<"$PAYLOAD"
    exit 0
fi

RESP="$(mktemp)"
trap 'rm -f "$RESP"' EXIT
HTTP_CODE="$(curl -sS --max-time 30 -o "$RESP" -w '%{http_code}' -X POST \
    "https://api.${DD_SITE}/api/v2/series" \
    -H "DD-API-KEY: ${DD_API_KEY}" \
    -H "Content-Type: application/json" \
    --data "$PAYLOAD")" || { echo "dd-metrics: request to api.${DD_SITE} failed" >&2; exit 1; }

if [[ "$HTTP_CODE" != 2* ]]; then
    echo "dd-metrics: Datadog API returned HTTP $HTTP_CODE: $(head -c 500 "$RESP")" >&2
    exit 1
fi
echo "dd-metrics: published $(jq '.series | length' <<<"$PAYLOAD") series to ${DD_METRIC_PREFIX}.* (entry: ${ENTRY_ID:-unknown}, goal: ${GOAL_ACHIEVED:-unknown})"
