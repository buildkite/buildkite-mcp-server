---
name: release-notes
description: Draft and update GitHub release notes for this repository. Use when preparing, reviewing, or revising a versioned buildkite-mcp-server release. Do not use for changelog files or ordinary pull request descriptions.
---

# Release notes

Produce accurate, maintainer-reviewed GitHub release notes that follow the style
of recent curated releases in this repository.

## Gather evidence

1. Confirm the target version and inspect its current GitHub release, if one
   exists.
2. Identify the comparison base from the release series and verify the full
   comparison range. Do not rely only on locally fetched tags.
3. Inspect a recent curated release, such as `v1.19.0`, for layout and level of
   detail. Treat it as a style reference, not a fixed template.
4. Collect every pull request in the comparison range. Read the pull request
   descriptions and changed files for user-facing changes. Inspect source at the
   target tag when needed to verify tool names, arguments, defaults, limits, and
   behavior.

Use `gh` for GitHub data when available. Do not infer technical details from pull
request titles alone.

## Draft the release

Start with a short paragraph describing the most important user outcomes. Then
use the following sections when they have relevant content:

- `## What's Changed`, with concise categories such as Features, Agent Guidance,
  Development and Evals, and Dependencies.
- `## Tool Changes`, describing new tools and material changes to existing tools
  in practical terms.
- `## New Contributors`, when applicable.
- A `**Full Changelog**` comparison link as the final line.

For each pull request in `What's Changed`, preserve its title, author attribution,
and URL. Ensure every pull request in the comparison range appears exactly once
in those categorized lists. The narrative `Tool Changes` section may discuss the
same work without repeating attribution.

Prefer concise, factual language. Focus on externally observable behavior and
agent outcomes. Exclude implementation detail unless it explains a limit,
compatibility constraint, or operational risk. Do not use em dashes.

## Review and update

Always show the complete Markdown draft and stop. Wait for a new user response
that explicitly approves that exact body before changing GitHub, even when the
initial request asks to draft and update the release in one step. Approval to
draft notes or general permission to update a release is not approval of the
generated body. If the draft changes, present the complete revised Markdown and
wait for approval again. After approval:

1. Write the approved Markdown to a temporary notes file.
2. Update the specified existing release using `gh release edit` with
   `--notes-file`.
3. Read the release back from GitHub and verify the complete body and URL.
4. Remove the temporary file and confirm it did not leave worktree changes.

Do not publish a draft release, create a tag, or create a new release unless the
user explicitly requests that separate action.
