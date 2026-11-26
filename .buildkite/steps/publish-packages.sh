#!/usr/bin/env bash

#
# Copy the released container image from GHCR to Buildkite Packages,
# authenticating with an OIDC token issued to this job.
#

set -euo pipefail

: "${BUILDKITE_TAG:?This step must run on a tag build}"

ORGANIZATION="${PACKAGES_ORGANIZATION:-buildkite}"
REGISTRY="${PACKAGES_REGISTRY:-buildkite-mcp-server}"
IMAGE="buildkite-mcp-server"
VERSION="${BUILDKITE_TAG#v}"

CRANE_VERSION="v0.22.1"
crane() {
    go run "github.com/google/go-containerregistry/cmd/crane@${CRANE_VERSION}" "$@"
}

source_image="ghcr.io/buildkite/${IMAGE}:${VERSION}"
target_repo="packages.buildkite.com/${ORGANIZATION}/${REGISTRY}/${IMAGE}"

echo "--- :key: Logging in to packages.buildkite.com/${ORGANIZATION}/${REGISTRY}"
buildkite-agent oidc request-token \
    --audience "https://packages.buildkite.com/${ORGANIZATION}/${REGISTRY}" \
    --lifetime 300 |
    crane auth login packages.buildkite.com --username buildkite --password-stdin

echo "--- :package: Copying ${source_image} to ${target_repo}:${VERSION}"
crane copy "${source_image}" "${target_repo}:${VERSION}"
crane tag "${target_repo}:${VERSION}" latest
