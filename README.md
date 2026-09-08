# buildkite-mcp-server

[![Build status](https://badge.buildkite.com/79fefd75bc7f1898fb35249f7ebd8541a99beef6776e7da1b4.svg?branch=main)](https://buildkite.com/buildkite/buildkite-mcp-server)

> **[Model Context Protocol (MCP)](https://modelcontextprotocol.io/introduction) server exposing Buildkite data (pipelines, builds, jobs, tests) to AI tooling and editors.**

Full documentation is available at [buildkite.com/docs/apis/mcp-server](https://buildkite.com/docs/apis/mcp-server).

---

## Comparing builds

The read-only `compare_builds` tool in the `investigations` toolset answers questions such as “What changed since this build last worked on main?” Supply `org_slug`, `pipeline_slug`, and the target `build_number`. It selects the most recently created earlier build that is currently passed on the same pipeline and exact branch. It does not require that the baseline had already passed when the target started. Supply `baseline_build_number` to compare against a specific build in that pipeline instead, including a failed build or one on another branch.

The response identifies the baseline and selection rule, counts outcomes across all jobs, and returns up to 100 job comparisons, prioritizing newly failing, recovered, and still-failing steps. Matching uses step keys, job type, matrix values, and parallel index/total. When both jobs lack keys, it falls back to the exact nonblank name plus type, group key, matrix values, and parallel index/total, only when that combination is unique in each build. Matched pairs expose `match_method: "step_key"` or `"name_fallback"`; fallback matches carry a warning that they are heuristic. Unnamed unkeyed jobs and duplicate identities remain unmatched. Explicit keys never fall back to names, even when a key was added, removed, or changed between builds. Added/removed means a job identity is present in only one build, so renaming unkeyed jobs or changing matrix values or parallelism can also produce added/removed entries. Retried attempts are excluded; final-attempt states and retry counts remain visible.

Execution times and deltas cover final attempts only. Scheduling time is `scheduled_at` to `started_at`, not dependency or manual waiting. These are not build wall-clock comparisons or total retry costs. Missing or inconsistent timestamps omit the corresponding timing. Unfinished builds are explicitly identified as changing snapshots.

By default, up to three newly failing jobs include their last 20 log entries, bounded to 8 KiB of log content each. Set `include_logs: false` to omit logs. Log errors do not discard the comparison. The tool requires `read_builds` and `read_build_logs` scopes. Use `get_build_failure_summary` or `tail_logs` to investigate further; a shared failing step does not establish a shared root cause or make a retry safe.

Baseline discovery searches at most 500 candidates. If none is found, the response says no comparison was performed and asks for an explicit baseline. Job inventories are limited to 1,000 jobs per build; larger inventories return an error instead of misleading partial added/removed results. Output omissions are reported separately from the complete outcome counts.

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
