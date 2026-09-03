#!/usr/bin/env bash
#
# dd-publish.sh — CI post-step: publish every entry's run metrics to Datadog.
#
# Runs OUTSIDE the eval agent's container (the dd-publish step in
# .buildkite/pipeline.evals.yml): the evaluated agent runs with unrestricted
# Bash, so DD_API_KEY must never enter its environment. babystand.sh records
# each entry's identity + outcome as runs/<id>/<run-key>.dd.json (next to
# <run-key>.metrics.json) in the run-bundle artifacts; this script downloads
# those and drives dd-metrics.sh once per entry, with the key scoped to this
# step alone.
#
# Best-effort like dd-metrics.sh: no DD_API_KEY -> loud skip (exit 0); no
# .dd.json artifacts (eval step died before any entry finished) -> loud skip
# (exit 0); a failed entry logs a warning and the script exits 1 at the end
# (the step is soft_fail, so the build stays green either way).
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

if [[ -z "${DD_API_KEY:-}" ]]; then
    echo "dd-publish: DD_API_KEY is unset; skipping Datadog publish (see .buildkite/pipeline.evals.yml)." >&2
    exit 0
fi
command -v jq >/dev/null              || { echo "dd-publish: jq is required" >&2; exit 1; }
command -v buildkite-agent >/dev/null || { echo "dd-publish: buildkite-agent is required" >&2; exit 1; }

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

# Metadata drives the loop; metrics files can be missing for entries whose
# audit failed (dd-metrics.sh then publishes identity/duration series only),
# hence the separate best-effort download.
if ! buildkite-agent artifact download "runs/*/*.dd.json" "$WORK_DIR/"; then
    echo "dd-publish: no run metadata artifacts found; nothing to publish." >&2
    exit 0
fi
buildkite-agent artifact download "runs/*/*.metrics.json" "$WORK_DIR/" \
    || echo "dd-publish: no metrics artifacts; publishing identity/duration series only." >&2

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
done < <(find "$WORK_DIR" -name '*.dd.json' | sort)

echo "dd-publish: $PUBLISHED entry(ies) published, $FAILED failed."
[[ "$FAILED" -eq 0 ]]
