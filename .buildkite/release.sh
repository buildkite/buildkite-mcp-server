#!/bin/env bash

#
# This script is used to build a release of the CLI and publish it to multiple registries on Buildkite
#

# NOTE: do not exit on non-zero returns codes
set -uo pipefail

export GORELEASER_KEY=""
GORELEASER_KEY=$(buildkite-agent secret get goreleaser_key)

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

# check if GITHUB_USER is set
if [[ -z "${GITHUB_USER:-}" ]]; then
    echo "Skipping GHCR login as GITHUB_USER is not set"
else
    echo "--- :key: :github: Login to GHCR using ko"
    echo "$GITHUB_TOKEN" | ko login ghcr.io --username "$GITHUB_USER" --password-stdin
    if [[ $? -ne 0 ]]; then
        echo "GitHub login failed"
        exit 1
    fi
fi

echo "--- :goreleaser: Building release with GoReleaser"

if [[ $? -ne 0 ]]; then
    echo "Failed to retrieve GoReleaser Pro key"
    exit 1
fi

# Build DXT package
echo "Building DXT package..."
make dxt

if ! goreleaser "$@"; then
    echo "Failed to build a release"
    exit 1
fi