#!/usr/bin/env bash
# Create fresh buildkite/buildkite Scheduler data from a Buildkite scenario setup.
set -euo pipefail

: "${BUILDKITE_API_TOKEN:?Set a buildkite-org token with read_builds and read_test_pool}"
: "${BUILDKITE_BUILD_NUMBER:?Run from a Buildkite eval build}"
: "${BUILDKITE_PIPELINE_SLUG:?Run from a Buildkite eval build}"
: "${ENTRY_ID:?Scenario entry ID is required}"
: "${DATETIME:?Scenario timestamp is required}"
: "${SCENARIO_VARS_FILE:?Scenario vars output file is required}"
[[ "${BUILDKITE_ORGANIZATION_SLUG:-}" == "buildkite" ]] || {
    echo "Scheduler setup must run in the buildkite organization." >&2
    exit 1
}

api_get() {
    curl --fail --silent --show-error --max-time 30 \
        -H "Authorization: Bearer $BUILDKITE_API_TOKEN" "$1"
}

base=https://api.buildkite.com/v2/organizations/buildkite
commit=$(api_get "$base/pipelines/buildkite/builds?branch=main&per_page=1" \
    | jq -er '.[0].commit | select(test("^[0-9a-f]{40}$"))')
step_key="scheduler-${ENTRY_ID}-${DATETIME}"
# Trigger steps use the agent's existing authorization, not a write API token.
# No dependency on the running eval job: setup must be able to read the build
# before that job finishes. These existing pipeline flags disable deployment,
# merged-PR actions, schema publication and experiments, and force Scheduler.
jq -n --arg key "$step_key" --arg commit "$commit" \
    '{steps: [{key: $key, label: "Scheduler scenario build", trigger: "buildkite",
      async: true, depends_on: [], build: {branch: "main", commit: $commit,
        message: ("Scheduler eval setup " + $key), env: {
          SCHEDULED_MAIN_BUILD: "true", PREFLIGHT: "true",
          RSPEC_DYNAMIC_PARALLELISM: "true", RSPEC_STATIC_SPLIT: "false",
          EXPERIMENT_RUNNING: "false"}}}]}' \
    | buildkite-agent pipeline upload

parent="$base/pipelines/$BUILDKITE_PIPELINE_SLUG/builds/$BUILDKITE_BUILD_NUMBER"
deadline=$((SECONDS + 1800))
build_number=""
while (( SECONDS < deadline )); do
    job=$(api_get "$parent" | jq -c --arg key "$step_key" '.jobs[] | select(.step_key == $key)')
    if [[ -n "$job" ]]; then
        build_number=$(jq -r '.triggered_build.number // empty' <<<"$job")
        [[ -z "$build_number" ]] || break
        if [[ "$(jq -r '.state' <<<"$job")" == "failed" ]]; then
            echo "Scheduler trigger step failed: $step_key" >&2
            exit 1
        fi
    fi
    sleep 10
done

while [[ -n "$build_number" ]] && (( SECONDS < deadline )); do
    build=$(api_get "$base/pipelines/buildkite/builds/$build_number")
    build_id=$(jq -er '.id' <<<"$build")
    pipeline_id=$(jq -er '.pipeline.id' <<<"$build")
    pools=$(api_get "$base/test-scheduler/pools?pipeline_id=$pipeline_id&build_id=$build_id&per_page=100")
    if jq -e '.items | length > 0' <<<"$pools" >/dev/null; then
        ready=true
        while IFS= read -r pool_id; do
            metrics=$(api_get "$base/test-scheduler/pools/$pool_id/metrics?build_id=$build_id")
            if ! jq -e --arg id "$pool_id" '.pool.id == $id and (.observed_at | type == "string")' <<<"$metrics" >/dev/null; then
                ready=false
                break
            fi
        done < <(jq -r '.items[].id' <<<"$pools")
        if [[ "$ready" == "true" ]]; then
            build_url=$(jq -er '.web_url' <<<"$build")
            printf 'BUILD_URL=%s\n' "$build_url" >> "$SCENARIO_VARS_FILE"
            echo "Fresh Scheduler build ready for review: $build_url"
            exit 0
        fi
    fi
    case "$(jq -r '.state' <<<"$build")" in
        passed|failed|canceled|not_run)
            echo "Fresh build $build_number finished without readable Scheduler metrics." >&2
            exit 1
            ;;
    esac
    sleep 10
done
echo "Timed out waiting for Scheduler build/metrics (trigger: $step_key, build: ${build_number:-unknown})." >&2
exit 1
