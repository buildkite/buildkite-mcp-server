#!/usr/bin/env bash
#
# dd-publish.sh — publish every entry's recorded run metrics to Datadog.
#
# SECURITY: this is the ONLY process that may hold DD_API_KEY, and it must
# run strictly OUTSIDE the eval-agent run. The evaluated agent has
# unrestricted Bash, and on Linux a key present anywhere in the run —
# even `unset` after capture — stays readable to same-UID processes via
# /proc/<pid>/environ (the exec-time snapshot). So babystand.sh never
# touches the key in either mode; it records each entry's identity +
# outcome as runs/<id>/<run-key>.dd.json (next to <run-key>.metrics.json)
# and this script submits them via dd-metrics.sh afterwards.
#
# Modes:
#   CI (no argument):    the dd-publish pipeline step — downloads the
#                        run-bundle artifacts the eval step uploaded, with
#                        the key scoped to that step alone.
#   Local (<runs-dir>):  run manually AFTER babystand.sh has exited, in a
#                        fresh shell:
#                            DD_API_KEY=... ./dd-publish.sh evals/runs
#                        Scans the directory instead of downloading, and
#                        publishes ONLY the latest run (the newest
#                        babystand DATETIME stamp): the runs tree is
#                        persistent, and republishing older bundles would
#                        clamp their timestamps into Datadog's ~1h ingest
#                        window, making stale results look recent.
#
# Best-effort like dd-metrics.sh: no DD_API_KEY -> loud skip (exit 0); no
# .dd.json files -> loud skip (exit 0 in CI; the eval step may have died
# before any entry finished); a failed entry logs a warning and the script
# exits 1 at the end (the CI step is soft_fail, so the build stays green).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
RUNS_DIR="${1:-}"

if [[ -z "${DD_API_KEY:-}" ]]; then
    echo "dd-publish: DD_API_KEY is unset; skipping Datadog publish (see .buildkite/pipeline.evals.yml)." >&2
    exit 0
fi
command -v jq >/dev/null || { echo "dd-publish: jq is required" >&2; exit 1; }

NAME_GLOB="*.dd.json"
if [[ -n "$RUNS_DIR" ]]; then
    # Local mode: publish only the LATEST run from the persistent runs tree.
    # Bundle files are <entry-id>-<YYYY-MM-DD-HHMMSS>.dd.json and the stamp
    # is shared by every entry of one babystand invocation, so the newest
    # stamp (the format sorts chronologically) selects exactly the
    # just-completed run.
    [[ -d "$RUNS_DIR" ]] || { echo "dd-publish: runs directory not found: $RUNS_DIR" >&2; exit 1; }
    WORK_DIR="$RUNS_DIR"
    LATEST="$(find "$RUNS_DIR" -name '*.dd.json' \
        | sed -nE 's/.*-([0-9]{4}-[0-9]{2}-[0-9]{2}-[0-9]{6})\.dd\.json$/\1/p' \
        | sort | tail -n1)"
    [[ -n "$LATEST" ]] || { echo "dd-publish: no run bundles (*.dd.json) under $RUNS_DIR; nothing to publish." >&2; exit 1; }
    NAME_GLOB="*-${LATEST}.dd.json"
    echo "dd-publish: publishing latest run ($LATEST) from $RUNS_DIR"
else
    # CI mode: pull the run-bundle artifacts the eval step uploaded.
    command -v buildkite-agent >/dev/null || { echo "dd-publish: buildkite-agent is required (or pass a runs directory)" >&2; exit 1; }
    WORK_DIR="$(mktemp -d)"
    trap 'rm -rf "$WORK_DIR"' EXIT
    # Metadata drives the loop; metrics files can be missing for entries whose
    # audit failed (dd-metrics.sh then publishes identity/duration series
    # only), hence the separate best-effort download.
    if ! buildkite-agent artifact download "runs/*/*.dd.json" "$WORK_DIR/"; then
        echo "dd-publish: no run metadata artifacts found; nothing to publish." >&2
        exit 0
    fi
    buildkite-agent artifact download "runs/*/*.metrics.json" "$WORK_DIR/" \
        || echo "dd-publish: no metrics artifacts; publishing identity/duration series only." >&2
fi

PUBLISHED=0 FAILED=0
while IFS= read -r META; do
    # An empty or corrupt metadata file would publish every series with
    # entry:unknown tags — noise in Datadog. Skip it loudly instead.
    if ! jq -e . "$META" >/dev/null 2>&1; then
        echo "WARNING: dd-publish: $META is empty or not valid JSON; skipping this entry." >&2
        FAILED=$(( FAILED + 1 ))
        continue
    fi
    ENTRY="$(jq -r '.entry // "unknown"' "$META")"
    echo "--- :datadog: [$ENTRY] publishing run metrics"
    if ENTRY_ID="$ENTRY" \
        PROMPT_NAME="$(jq -r '.prompt // "unknown"' "$META")" \
        AGENT="$(jq -r '.agent // "unknown"' "$META")" \
        MODEL="$(jq -r '.model // "default"' "$META")" \
        GOAL_ACHIEVED="$(jq -r '.goal // "unknown"' "$META")" \
        EVAL_DURATION_SECONDS="$(jq -r '.duration_seconds // ""' "$META")" \
        DD_TIMESTAMP="$(jq -r '.end_ts // ""' "$META")" \
        METRICS_FILE="${META%.dd.json}.metrics.json" \
        "$SCRIPT_DIR/dd-metrics.sh"; then
        PUBLISHED=$(( PUBLISHED + 1 ))
    else
        echo "WARNING: dd-publish: publish failed for '$ENTRY' ($META)" >&2
        FAILED=$(( FAILED + 1 ))
    fi
done < <(find "$WORK_DIR" -name "$NAME_GLOB" | sort)
echo "dd-publish: $PUBLISHED entry(ies) published, $FAILED failed."
[[ "$FAILED" -eq 0 ]]
