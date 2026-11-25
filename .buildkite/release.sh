#!/usr/bin/env bash

#
# This script is used to build a release of the CLI and publish it to multiple registries on Buildkite
#

# NOTE: do not exit on non-zero returns codes
set -euo pipefail

goreleaser_args=("$@")

if [[ -n "${GOOS:-}" ]]; then
    goreleaser_config="$(mktemp "${TMPDIR:-/tmp}/goreleaser.XXXXXX")"
    trap 'rm -f "${goreleaser_config:-}"' EXIT

    echo "--- :goreleaser: Filtering GoReleaser config to GOOS=${GOOS}"
    if ! awk '
        BEGIN {
            in_goos = 0
            replaced = 0
        }
        /^[[:space:]]+goos:[[:space:]]*$/ {
            print
            print "      - " ENVIRON["GOOS"]
            in_goos = 1
            replaced = 1
            next
        }
        in_goos && /^[[:space:]]+- / {
            next
        }
        in_goos {
            in_goos = 0
        }
        {
            print
        }
        END {
            if (!replaced) {
                exit 1
            }
        }
    ' .goreleaser.yaml > "${goreleaser_config}"; then
        echo "Failed to filter GoReleaser config for GOOS=${GOOS}"
        exit 1
    fi

    goreleaser_args+=("--config" "${goreleaser_config}")
fi

# check if DOCKERHUB_USER and DOCKERHUB_PASSWORD are set if not skip docker login
if [[ -z "${DOCKERHUB_USER:-}" || -z "${DOCKERHUB_PASSWORD:-}" ]]; then
    echo "Skipping Docker login as DOCKERHUB_USER or DOCKERHUB_PASSWORD is not set"
else
    echo "--- :key: :docker: Login to Docker hub using ko"
    echo "${DOCKERHUB_PASSWORD}" | ko login index.docker.io --username "${DOCKERHUB_USER}" --password-stdin
    if [[ $? -ne 0 ]]; then
        echo "Docker login failed"
        exit 1
    fi
fi

if [[ -n "${EXCHANGE_FUNCTION_ARN:-}" ]]; then
    : "${BUILDKITE_TAG:?GitHub publishing requires a tagged Buildkite build}"
    echo "--- :key: :github: Requesting GitHub release token from Exchange"
    GITHUB_TOKEN=$(
        response=$(mktemp) || exit 1
        trap 'rm -f "$response"' EXIT
        function_error=$(
            buildkite-agent oidc request-token --audience exchange --claim organization_id --claim pipeline_id |
                jq -Rs '{token: rtrimstr("\n"), github_organization: "buildkite"}' |
                AWS_PAGER="" aws lambda invoke --function-name "$EXCHANGE_FUNCTION_ARN" \
                    --payload fileb:///dev/stdin --query FunctionError --output text "$response"
        ) || exit 1
        [[ "$function_error" == None ]] || exit 1
        jq -er 'select(.error == null) | .token | strings | select(length > 0)' "$response"
    ) || { echo "Exchange did not issue a GitHub release token" >&2; exit 1; }
    printf '%s\n' "$GITHUB_TOKEN" | buildkite-agent redactor add
    export GITHUB_TOKEN
fi

if [[ -z "${GHCR_USER:-}" ]]; then
    echo "Skipping GHCR login as GHCR_USER is not set"
else
    echo "--- :key: :github: Login to GHCR using ko"
    printf '%s\n' "${GHCR_TOKEN:?GHCR login requires GHCR_TOKEN}" | ko login ghcr.io --username "$GHCR_USER" --password-stdin
    if [[ $? -ne 0 ]]; then
        echo "GitHub login failed"
        exit 1
    fi
fi

echo "--- :goreleaser: Building release with GoReleaser"

if ! goreleaser "${goreleaser_args[@]}"; then
    echo "Failed to build a release"
    exit 1
fi
