# buildkite-mcp-server

[![Build status](https://badge.buildkite.com/79fefd75bc7f1898fb35249f7ebd8541a99beef6776e7da1b4.svg?branch=main)](https://buildkite.com/buildkite/buildkite-mcp-server)

> **[Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) server exposing Buildkite data (pipelines, builds, jobs, tests) to AI tooling and editors.**

Full documentation is available at [buildkite.com/docs/apis/mcp-server](https://buildkite.com/docs/apis/mcp-server).

---

## Retrying builds with limited permissions

The `retries` toolset supports investigating failures and retrying jobs without
broader build-write access. It includes `list_builds`, `get_build`,
`wait_for_build`, `list_jobs`, `get_job`, `search_logs`, `tail_logs`, `read_logs`,
`retry_job`, and `retry_failed_jobs`. It requires `read_builds`,
`read_build_logs`, and `retry_builds` scopes, not `write_builds`.

```bash
buildkite-mcp-server stdio --enabled-toolsets retries
```

`retry_failed_jobs` retries only infrastructure failures; use its `dry_run`
option to inspect the proposed retries without executing them. `retry_job`
retries a specific job. Both are mutating tools and are excluded by
`--read-only`. The toolset does not expose create, rebuild, cancel, or unblock
operations. Existing retry tools remain available in `builds`; selecting
`builds` or `all` still requires `write_builds` for the other mutations.

The Buildkite API must have the `RetryBuildsScope` feature enabled for the token
owner to accept `retry_builds`; existing `write_builds` tokens remain accepted.
Hosted MCP OAuth can select this toolset using `/mcp/x/retries` once the hosted
server adopts a release containing it. Selecting toolsets through request
headers does not change an existing token's OAuth scopes.

For library consumers, `WithDisabledToolsets` and the HTTP factory's disabled
toolsets exclude all members of those toolsets, even through overlapping
groups. For example, disabling `logs` also removes log tools from `retries`;
disabling `retries` removes its inspection and retry tools from other groups.

---

## Configuring GitHub Actions pipelines

`create_pipeline` and `update_pipeline` accept GitHub `provider_settings`
and `branch_configuration`. Omit provider keys to use API defaults on create
or leave them unchanged on update. Explicit `false` is preserved. Set
`filter_condition` or `pull_request_branch_filter_configuration` to `null`
to clear them; an empty string is sent as an empty string. Set
`branch_configuration: null` on update to clear an existing branch filter.
On create, an omitted, null or empty branch filter uses the API's null default.
Full `get_pipeline` and `list_pipelines` responses preserve the supported
GitHub settings. go-buildkite response strings do not distinguish null from
empty, so do not reconstruct partial updates from readback.

Follow the [manual GitHub Actions setup](https://buildkite.com/docs/pipelines/migration/run-github-actions-workflows#add-a-github-actions-workflow-to-a-pipeline-configure-an-equivalent-pipeline-manually).
Keep branch-wide `skip_queued_branch_builds` and `cancel_running_branch_builds`
false to avoid skipping or cancelling sibling workflow builds. Use
`create_webhook: false`: provider settings do not provision a dedicated GitHub
Actions trigger or its webhook, and the existing webhook tools create
traditional repository webhooks. Server-side feature availability and token
permissions still apply; accepting settings does not prove workflow execution.

On create, supply nonblank `configuration` **or** an approved
`pipeline_template_uuid`, not both. On update, omit `pipeline_template_uuid`
to retain the assignment, supply a UUID to assign a template without
`configuration`, or send `null` to remove it. Removal can include replacement
`configuration`; the API enforces configuration requirements and organization
template policy. Full pipeline responses retain assigned UUIDs; go-buildkite
omits the field when the response assignment is null or absent.

Use `get_pipeline_github_webhook_processing`,
`enable_pipeline_github_webhook_processing`, and
`disable_pipeline_github_webhook_processing` to inspect or change traditional
GitHub webhook processing. These use GET/PUT/DELETE `github-webhooks`, not the
POST `webhook` creation endpoint. They do not delete repository webhooks or
change dedicated GitHub Actions triggers. Responses preserve `enabled` and
nullable `disabled_at` and `disabled_by` metadata; `disabled_by` is a user name.
All three require Full Access, a GitHub or GitHub Enterprise pipeline, and
organization enrollment in expanded webhook triggers. GET requires
`read_pipelines`; enable/disable require `write_pipelines` and are excluded in
read-only mode. A 404 can mean the controls are unavailable, not that the
pipeline is missing. API errors are returned without treating failure as a
disabled state.

These contracts are covered with local synthetic HTTP fixtures. Hosted
availability, permissions, persistence, and workflow execution require separate
verification; local success is not proof of deployed support. Dedicated GitHub
Actions trigger management is not included.

---

## Comparing builds

The read-only `compare_builds` tool in the `investigations` toolset answers questions such as “What changed since this build last worked on main?” Supply `org_slug`, `pipeline_slug`, and the target `build_number`. It selects the most recently created earlier build that is currently passed on the same pipeline and exact branch. It does not require that the baseline had already passed when the target started. Supply `baseline_build_number` to compare against a specific build in that pipeline instead, including a failed build or one on another branch.

The response identifies the baseline and selection rule, counts outcomes across all jobs, and returns up to 100 job comparisons, prioritizing newly failing, recovered, and still-failing steps. Matching uses step keys, job type, matrix values, and parallel index/total. When both jobs lack keys, it falls back to the exact nonblank name plus type, group key, matrix values, and parallel index/total, only when that combination is unique in each build. Matched pairs expose `match_method: "step_key"` or `"name_fallback"`; fallback matches carry a warning that they are heuristic. Unnamed unkeyed jobs and duplicate identities remain unmatched. Explicit keys never fall back to names, even when a key was added, removed, or changed between builds. Added/removed means a job identity is present in only one build, so renaming unkeyed jobs or changing matrix values or parallelism can also produce added/removed entries. Retried attempts are excluded; final-attempt states and retry counts remain visible.

Execution times and deltas cover final attempts only. Scheduling time is `scheduled_at` to `started_at`, not dependency or manual waiting. These are not build wall-clock comparisons or total retry costs. Missing or inconsistent timestamps omit the corresponding timing. Unfinished builds are explicitly identified as changing snapshots.

Any change in `soft_failed` is reported as `state_changed`: transitions between soft and hard failures, even when both jobs have state `failed`, and between passed and soft-failed jobs, which are never reported as `newly_failing` or `recovered` because a soft failure does not block the build. A passed baseline build can contain soft-failed jobs.

By default, up to three newly blocking failures include their last 20 log entries: `newly_failing` jobs first, then `state_changed` jobs that are now hard failures, such as a job that soft-failed in the baseline. Logs are bounded to 8 KiB of log content each. Set `include_logs: false` to omit logs. Log errors do not discard the comparison, except HTTP 401 authentication errors, which propagate through the server's reauthentication path. The tool requires `read_builds` and `read_build_logs` scopes. Use `get_build_failure_summary` or `tail_logs` to investigate further; a shared failing step does not establish a shared root cause or make a retry safe.

Baseline discovery searches at most 500 candidates. If none is found, the response says no comparison was performed and asks for an explicit baseline. Job inventories are limited to 1,000 jobs per build; larger inventories return an error instead of misleading partial added/removed results. Output omissions are reported separately from the complete outcome counts.

---

## Inspecting Test Engine workflows

The read-only `list_test_workflows` and `get_test_workflow` tools in the `tests`
toolset expose a suite's configured monitors, filters, alarm/recovery actions,
and links. Both require `read_suites` and suite access. Supply `org_slug` and
`test_suite_slug` (a slug or UUID) to list workflows:

```json
{"org_slug": "acme", "test_suite_slug": "backend"}
```

To inspect one, call `get_test_workflow` with the same organization and suite
and an item's `id` as `workflow_id`.

Use the configured labels with `list_tests` to find marked tests; labels alone
do not rank flakiness. Workflows describe configuration, not proof an action
ran or event history. Both tools always replace webhook endpoints with
`[REDACTED]`, regardless of the caller's API permissions.

---

## Organization usage

The read-only `get_active_users_usage` tool in the `usage` toolset reports
distinct active users across an organization and any billing children, matching
the Usage page. It requires `read_organizations` and the `usage-breakdowns`
feature, which is disabled by default; a disabled feature returns 404. The tool
is available with `--read-only` but excluded by `--minimum-access`.
Invalid dates return 400, and query failures return 503 rather than zero usage.

Supply `org_slug` (slug or UUID), `from`, and `to`. Dates are inclusive UTC `YYYY-MM-DD`:
`from` cannot be earlier than today minus three calendar months, and `to` must
be between `from` and today. `interval` accepts `day` (default) or `month`.
Month buckets are dated on the calendar first, but only events within the exact
requested dates contribute. The complete series is returned without pagination.
The original `/usage` summary endpoint and its custom-range cap are unchanged.

The response preserves zero-count buckets, cumulative counts, the whole-range
distinct-user count, and the Usage-page average monthly active users. Do not sum
daily or child-organization counts to derive distinct users. The monthly average
can differ from whole-range distinct users. `scope` is `organization` without
billing children, otherwise `organization_and_billing_children`. Reports are
cached for five minutes and retain `generated_at`, the UTC calculation time,
not an ingestion watermark. Late events can revise results on refresh. These
are usage reports, not invoice reconciliation or per-user cost attribution.

`get_concurrent_agents_usage` reports recorded concurrent-agent usage with the
same dates, intervals, scope, cache, and permissions. `agent_type` accepts
`self_hosted` (default) or `hosted`; `percentile` accepts integers 1–100
(default 95).

The percentile is discrete across the zero-filled **daily sums of each
organization's peak**. It is not an intraday percentile such as P80, simultaneous
cross-organization concurrency, or plan-specific billable usage. With
`interval: "month"`, each bar is the maximum daily sum within that month, but
the percentile still uses the underlying daily values, not the monthly bars.
Use `interval: "day"` to retrieve the inputs for reproducing the statistic.
MCP returns the server's report without recalculating it or paginating it.

`list_concurrent_agents_usage_by_organization` returns the organization table
breakdown in UUID order, using the same concurrency filters and date bounds.
Each organization has a whole-range summary and zero-filled interval buckets;
each bucket's discrete percentile uses daily peaks within its clipped dates,
so a daily bucket's percentile equals its maximum. These percentiles are
nonadditive; use `get_concurrent_agents_usage` for the aggregate report.
Pagination is forward-only: `per_page` accepts 1–100 (default 30); pass the
opaque `after` value from the returned `links.next` URL unchanged. Body links
always include `self` and `next`, which is null on the final page. Late events
can change reports between pages; there is no cross-page snapshot guarantee.

`get_hosted_agents_usage` reports hosted compute in vCPU-minutes with the same
dates, intervals, scope, cache, and permissions. Resource maps always include
`hosted_agents_linux_amd64_vcpu_minutes`, `hosted_agents_linux_arm64_vcpu_minutes`,
`hosted_agents_macos_m4_vcpu_minutes`, and `hosted_agents_windows_amd64_vcpu_minutes`,
including zero values. The server combines CPU-weighted instance shapes per
resource before flooring and excludes unknown shapes from weighted metrics.
Period totals and interval buckets round independently, matching the Usage
table and chart; summing rounded daily buckets can differ from the summary.
MCP preserves these values without recalculation. This complete series is not
paginated; pipeline detail is a separate report.

`get_test_executions_usage` reports test executions with the same dates,
intervals, scope, cache, and permissions. It preserves zero-filled buckets,
additive cumulative execution counts, total executions, active suites, and
the server's average executions. The average is the floor of total executions
divided by the requested day or month bucket count, including zero buckets.
Execution counts are not the distinct managed-test billing metric. This
complete series is not paginated; suite detail is a separate report.

---

## Library Usage

The exported Go API of this module should be considered unstable, and subject to breaking changes as we evolve this project.

---

## Security

To ensure the MCP server is run in a secure environment, we recommend running it in a container.

This image is built from [cgr.dev/chainguard/static](https://images.chainguard.dev/directory/image/static/versions) and runs as an unprivileged user.

### Passing identity headers through HTTP mode

Self-hosted HTTP deployments can forward selected headers from each inbound MCP request to the Buildkite API:

```bash
BUILDKITE_API_TOKEN=bkua_xxx \
  buildkite-mcp-server http \
  --passthrough-http-header X-User-Identity
```

Repeat `--passthrough-http-header` to allow more than one header, or set a comma-separated `BUILDKITE_PASSTHROUGH_HTTP_HEADERS` value. Only explicitly allowed headers are forwarded, and only to the origin configured by `BUILDKITE_BASE_URL`. They are removed from requests redirected elsewhere.

To authenticate each MCP request with its own Buildkite API token, allow `Authorization` and omit the process-wide token:

```bash
BUILDKITE_PASSTHROUGH_HTTP_HEADERS=Authorization \
  buildkite-mcp-server http
```

In this mode every `/mcp` request must contain exactly one non-empty `Authorization` header. Missing credentials return HTTP 401; the server never falls back to a shared API token. The reverse proxy in front of the MCP server is responsible for authenticating callers and setting or validating any forwarded identity headers.

Header passthrough is not available in stdio mode. Before serving job logs, the server verifies that the current caller can access the job log. This check is performed for every log-tool request, including when the log data is already cached.

---

## Contributing

Development guidelines are in [`DEVELOPMENT.md`](DEVELOPMENT.md).

---

## License

MIT © Buildkite

SPDX-License-Identifier: MIT
