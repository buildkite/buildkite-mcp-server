#!/usr/bin/env bash
# Read-only preflight for an existing build with accessible Scheduler metrics.
set -euo pipefail

BUILD_URL="${1:-}"
if [[ ! "$BUILD_URL" =~ ^https://buildkite\.com/([^/]+)/([^/]+)/builds/([0-9]+)$ ]]; then
    echo "Usage: $0 https://buildkite.com/<org>/<pipeline>/builds/<number>" >&2
    exit 1
fi
ORG_SLUG="${BASH_REMATCH[1]}"
PIPELINE_SLUG="${BASH_REMATCH[2]}"
BUILD_NUMBER="${BASH_REMATCH[3]}"
: "${BUILDKITE_API_TOKEN:?Set BUILDKITE_API_TOKEN with read_builds and read_test_pool access}"

api_get() {
    curl --fail --silent --show-error --max-time 30 \
        -H "Authorization: Bearer ${BUILDKITE_API_TOKEN}" "$1"
}

base="https://api.buildkite.com/v2/organizations/$ORG_SLUG"
build=$(api_get "$base/pipelines/$PIPELINE_SLUG/builds/$BUILD_NUMBER")
build_id=$(jq -er '.id' <<<"$build")
pipeline_id=$(jq -er '.pipeline.id' <<<"$build")
pools=$(api_get "$base/test-scheduler/pools?pipeline_id=$pipeline_id&build_id=$build_id&per_page=100")
jq -e '.items | length > 0' <<<"$pools" >/dev/null || {
    echo "No accessible, unexpired pools returned for $BUILD_URL; choose another build." >&2
    exit 1
}
while IFS= read -r pool_id; do
    api_get "$base/test-scheduler/pools/$pool_id/metrics?build_id=$build_id" \
        | jq -e --arg id "$pool_id" '.pool.id == $id and (.observed_at | type == "string")' >/dev/null
done < <(jq -r '.items[].id' <<<"$pools")

if [[ -n "${SCENARIO_VARS_FILE:-}" ]]; then
    printf 'BUILD_URL=%s\n' "$BUILD_URL" >> "$SCENARIO_VARS_FILE"
fi
echo "Scheduler metrics are readable for $(jq '.items | length' <<<"$pools") returned pools: $BUILD_URL"
