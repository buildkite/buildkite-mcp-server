# Debugging Buildkite Build Failures with Logs

This guide explains how to effectively use the Buildkite MCP server's log tools to debug build failures.

## Tool Availability

This guide covers the full server. Only follow steps for tools advertised in your current session; never call an unavailable tool. Toolset settings and `--minimum-access` can exclude tools described below.

In minimum-access mode, `get_build_failure_summary`, `compare_builds`, `tail_logs`, `search_logs`, and `read_logs` are unavailable. Skip the log workflow below. Instead, use available build and job inspection tools (`get_build`, `list_jobs`, `get_job`) to inspect states and exit statuses, and explain that log-based diagnosis is unavailable in this mode. Supply the organization slug yourself; `user_token_organization` is also unavailable.

## Table of Contents
- [Tools Overview](#tools-overview)
- [Debugging Workflow](#debugging-workflow)
  - [Soft Failures](#soft-failures)
- [Optimizing LLM Usage](#optimizing-llm-usage)
- [Common Error Patterns](#common-error-patterns)

## Tools Overview

The server provides a composite investigation tool and lower-level log tools:

### 1. get_build_failure_summary - Start Here
**Best first step for diagnosing a build failure** — combines build state and job-state counts, failed and canceled jobs, promised failures from still-running jobs, structured Job Errors, error/warning annotations, and failed Test Engine tests in one call, without fetching logs by default. Blocking failures fill `max_jobs` first, then canceled jobs, then soft-failed jobs (`soft_failed: true`). Jobs that never ran (`broken`, `waiting_failed`, `blocked_failed`, `unblocked_failed`) fill the remaining `max_jobs` slots unless `include_never_ran_jobs` is false.

Start with the default errors-first response. Each problem job's `job_errors` distinguishes `found`, `none_recorded`, and `unavailable`; missing capture never means success and does not automatically fetch logs. Records preserve grouped findings in tool-specific `context`. If `job_errors.content_truncated` is true, read `job_errors.url` for omitted records on the first page; `next_url` continues after that page. Use `tail_logs` for jobs with missing or insufficient evidence. Set `include_job_errors: false` to skip capture lookups; this does not enable logs.

Set `include_logs: true` to include bounded log tails (50 lines per eligible job by default, max 200); `log_tail` adjusts the limit but does not enable logs. Use the lower-level tools only when the summary identifies an area that needs deeper inspection. You can reduce output with `max_jobs`, `max_annotations`, `max_test_runs`, and `max_failed_tests`, or disable optional sections with `include_annotations` and `include_failed_tests`. Failed tests default to at most 50 per build, and failure details (`failure_reason`) are scanned from at most 5 Test Engine runs, choosing the runs that hold the most returned failed tests first. A test whose suite cannot be read from its URL falls back to the build's runs in listed order, and a `warnings` entry says when the cap left candidate runs unscanned.

Failed tests are listed per job: each terminal failed or timed-out job carries `failed_tests` (only tests whose every execution within that job failed) and `failed_tests_status`, one of `found`, `none_recorded`, `ingestion_pending`, or `unavailable`. An empty or absent `failed_tests` list does **not** mean the job's tests passed; follow the job's `failed_tests_hint` and inspect `job_errors` before fetching logs if needed. An entry with `failure_detail_status: "not_retrieved"` had no execution fetched for it. When it carries both `test_suite_slug` and `run_id`, call `get_failed_executions` with them for the detail, paging past the first 100 executions if needed. When `run_id` is absent, the suite lists several runs for this build or none; use `get_build_test_engine_runs` to list them and query each.

### 2. tail_logs - Focused Follow-up
Shows the last N entries for one job when the composite summary needs more log context.

Defaults to 10 lines if `tail` is omitted or zero.
Use `tail: 50-100` for an initial failure check when you want more than the default.

### 3. search_logs - For Specific Issues
**Most powerful tool** for finding specific error patterns with context.

**Key Parameters:**
- `pattern` (required): Regex pattern (case-insensitive by default)
- `context`: Lines before/after each match (0-20 recommended)
- `before_context` / `after_context`: Asymmetric context
- `case_sensitive`: Enable case-sensitive matching
- `invert_match`: Return entries that do not match the regex
- `reverse`: Search backwards from end
- `seek_start`: Start search from this row number (0-based)
- `limit`: Max matches to return (set this to avoid excessive output)

Recommended starting values: use `context: 3`, `limit: 10-20`, and leave boolean options false unless you need them. If `limit` is omitted, search can return every match.

### 4. read_logs - For Sequential Reading
**Use when you need to read a specific section** of logs in order, using a row number found via search_logs.

Always set `limit` — logs can be very large.

## Debugging Workflow

### Step 0: Get the Failure Summary
Call `get_build_failure_summary` with the organization slug, pipeline slug, and build number. In most cases this is enough to identify the root cause without additional calls.

If `jobs_truncated` is true, the summary does not cover every problem job. Omitted jobs may still have captured errors. If complete coverage is needed, increase `max_jobs` only if the server permits it; `job_limit` reports the applied limit. If the limit still excludes jobs, use `list_jobs` to identify them. Do not repeatedly request a larger summary beyond that limit or assume the omitted jobs lack errors. Use structured errors through available tools first; when they cannot provide enough evidence, use focused logs for those jobs and explain the coverage limit.

Interpret its jobs as follows:
- **`failed`**, **`timed_out`** and **`expired`** jobs without `soft_failed: true` are blocking failures (an `expired` job never reached an agent). These are the root cause candidates, start here
- **`soft_failed: true`** jobs (see [Soft Failures](#soft-failures)) ran and failed, but the step's `soft_fail` setting allowed that exit status, so they did not block the build
- **`broken`** jobs never ran because the pipeline configuration excluded them (`if`/`branches` did not match, `parallelism: 0`, or `skip`). They are never caused by a failed job, never fail the build, and are normal in passing builds
- **`waiting_failed` / `blocked_failed` / `unblocked_failed`** jobs never ran because a job they depend on failed. They are consequences of that upstream failure, not root causes, and have no logs — investigate the blocking failure instead
- **`running` with a non-zero `promised_exit_status`** has declared an early failure but may still produce more logs, artifacts, or test results before it finishes

Read the job errors, annotations, and test results before fetching logs. If that evidence explains the failure, stop; the steps below are optional follow-up, not a checklist to run for every job. When logs are needed, take the blocking failed job's `id` as the `job_id` for the lower-level log tools.

### Soft Failures
A soft-failed job is a real, unsuccessful execution whose exit status the pipeline author explicitly allowed through `soft_fail`. Rules for handling them:

- **Identify them by `soft_failed: true`**, not by step names (a "security audit" step is not automatically soft-failing) or by a non-zero `exit_status` alone.
- **`soft_fail` in the pipeline configuration may allow only particular exit statuses** (for example `soft_fail: [{exit_status: 1}]`). The same step exiting with a different status has `soft_failed: false` and is a blocking failure. The job's `soft_failed` field is authoritative, so you do not need the pipeline configuration to tell the two apart.
- **Describe them as non-blocking, allowed failures** — not as passing or successful tests, and not as proof the problem is harmless.
- **Deprioritize them** when explaining a red build: do not read their logs, retry them, or propose fixes unless the user asks about them or evidence connects them to the reported problem.
- **Non-blocking does not mean irrelevant**: a soft-failed step still ran, can still be a dependency of later steps, and still contributes to build duration.
- `build.job_state_counts` tallies jobs by state only, so a `failed` count can include soft-failed jobs.
- `retry_failed_jobs` skips soft-failed jobs, and `compare_builds` reports a change in `soft_failed` as `state_changed`, never `newly_failing` or `recovered`.

### Step 1: Focused Log Follow-up, Only If Needed
When structured evidence is missing, unavailable, or insufficient, use `tail_logs` with `tail: 50-100` for that job. A tail may contain cleanup rather than the cause; do not fetch tails for every job by default or continue if the evidence already answers the question.

### Step 2: Error Hunting
If the cause is still unclear, use `search_logs` with a pattern from the available evidence and a bounded `limit`. When there is no more specific clue, common patterns include:
- `error|failed|exception`
- `fatal|panic|abort`
- `timeout|cancelled`
- `permission denied|access denied`

### Step 3: Context Investigation
When you find errors, increase `context: 5-10` to see surrounding lines. Use `before_context` and `after_context` for asymmetric context (e.g. more lines after a match than before).

### Step 4: Deep Dive
Use `read_logs` with the `rn` row number from a `search_logs` result as `seek` to read the section around a specific error.

## Log Entry Format

Log entries are returned as JSON objects:
```json
{"ts": 1696168225123, "c": "Test failed: assertion error", "rn": 42}
```
- `ts`: Timestamp in Unix milliseconds
- `c`: Log content (ANSI codes stripped)
- `rn`: Row number (0-based) — use this as `seek` in `read_logs` or `seek_start` in `search_logs`

## Optimizing LLM Usage

### Token Efficiency
- **Always set `limit`** on `search_logs` and `read_logs` to avoid excessive output
- Start with low limits (`limit: 10-20`) and refine based on findings
- Use `invert_match: true` only with a narrow pattern and a `limit`; it returns entries that do not match the regex
- Use `reverse: true` with `seek_start` to search backwards from a known failure point

### Context Guidelines
- Use `context: 3-5` for general investigation
- Use `context: 10-20` when you need to understand complex error flows
- Limit context to avoid token waste on unrelated log entries

**Approximate token cost per call:**
- `tail_logs` (50 lines): ~800-1200 tokens
- `search_logs` (20 matches, context 3): ~1000-2000 tokens
- `read_logs` (100 lines): ~1500-2500 tokens

## Common Error Patterns

**Build/compile failures:**
```
"pattern": "build failed|compilation error|linking error"
```

**Test failures:**
```
"pattern": "test.*failed|assertion.*failed|expected.*but got"
```

**Infrastructure issues:**
```
"pattern": "network.*error|timeout|connection.*refused|dns.*error"
```

**Permission/security:**
```
"pattern": "permission denied|access denied|unauthorized|forbidden"
```

## Cache Management

- Completed builds are cached permanently
- Running builds use a 30s TTL by default
- Use `force_refresh: true` only when you need the absolute latest data from a running build
- Set `cache_ttl` to adjust the TTL for running build investigations
