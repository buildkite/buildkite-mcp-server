#!/usr/bin/env bash

set -euo pipefail

# Extract the PR number from the commit subject.
# Handles both formats:
#   Squash merge: "Some title (#1234)"
#   Merge commit: "Merge pull request #1234 from ..."
COMMIT_SUBJECT="${BUILDKITE_MESSAGE%%$'\n'*}"
PR_NUMBER=""
if [[ "${COMMIT_SUBJECT}" =~ ^Merge\ pull\ request\ \#([0-9]+)\ from\  ]]; then
  PR_NUMBER="${BASH_REMATCH[1]}"
elif [[ "${COMMIT_SUBJECT}" =~ \(\#([0-9]+)\)$ ]]; then
  PR_NUMBER="${BASH_REMATCH[1]}"
fi

if [ -z "${PR_NUMBER}" ]; then
  echo "Could not extract PR number from commit message, skipping"
  exit 0
fi

echo "--- :memo: Triggering docs-draft-writer for buildkite/buildkite-mcp-server#${PR_NUMBER}"

buildkite-agent pipeline upload <<EOF_PIPELINE
steps:
  - trigger: "docs-draft-writer"
    label: ":memo: Docs draft for buildkite/buildkite-mcp-server#${PR_NUMBER}"
    async: true
    soft_fail: true
    build:
      message: "Docs draft for buildkite/buildkite-mcp-server#${PR_NUMBER}"
      branch: "main"
      env:
        UPSTREAM_REPO: "buildkite/buildkite-mcp-server"
        UPSTREAM_PR_NUMBER: "${PR_NUMBER}"
EOF_PIPELINE
