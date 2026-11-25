#!/usr/bin/env bash
# Find a recent buildkite/buildkite build with readable Scheduler metrics.
set -euo pipefail

: "${BUILDKITE_API_TOKEN:?Set a buildkite-org token with read_builds and read_test_pool}"
: "${SCENARIO_VARS_FILE:?Scenario vars output file is required}"

# Keep response bodies out of diagnostics. Missing/expired data skips a candidate;
# authentication, permission, transport and server errors stop discovery.
api_get() {
    response=$(curl --silent --show-error --max-time 30 --write-out '\n%{http_code}' \
        -H "Authorization: Bearer $BUILDKITE_API_TOKEN" "$1") || exit 1
    status=${response##*$'\n'}
    response=${response%$'\n'*}
    case "$status" in
        200) return 0 ;;
        404|410) return 1 ;;
        *) echo "Scheduler discovery request failed (HTTP $status)." >&2; exit 1 ;;
    esac
}

base=https://api.buildkite.com/v2/organizations/buildkite
since=$(jq -nr 'now - 30 * 86400 | strftime("%Y-%m-%dT%H:%M:%SZ")')
api_get "$base/pipelines/buildkite/builds?branch=main&created_from=$since&per_page=100" || {
    echo "Could not list buildkite/buildkite builds." >&2
    exit 1
}
builds=$response
while IFS= read -r build; do
    build_id=$(jq -er '.id' <<<"$build")
    pipeline_id=$(jq -er '.pipeline.id' <<<"$build")
    if ! api_get "$base/test-scheduler/pools?pipeline_id=$pipeline_id&build_id=$build_id&per_page=100"; then
        continue
    fi
    pools=$response
    jq -e '.items | length > 0' <<<"$pools" >/dev/null || continue

    ready=true
    while IFS= read -r pool_id; do
        if ! api_get "$base/test-scheduler/pools/$pool_id/metrics?build_id=$build_id"; then
            ready=false
            break
        fi
        if ! jq -e --arg id "$pool_id" '.pool.id == $id and (.observed_at | type == "string")' <<<"$response" >/dev/null; then
            ready=false
            break
        fi
    done < <(jq -r '.items[].id' <<<"$pools")
    if [[ "$ready" == "true" ]]; then
        build_url=$(jq -er '.web_url' <<<"$build")
        printf 'BUILD_URL=%s\n' "$build_url" >> "$SCENARIO_VARS_FILE"
        echo "Recent Scheduler build ready for review: $build_url"
        exit 0
    fi
done < <(jq -c '.[]' <<<"$builds")

echo "No readable Scheduler build found among the latest 100 main builds from the last 30 days." >&2
exit 1
