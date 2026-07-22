# Debugging Buildkite Build Failures with Logs

This guide explains how to effectively use the Buildkite MCP server's log tools to debug build failures.

## Table of Contents
- [Tools Overview](#tools-overview)
- [Debugging Workflow](#debugging-workflow)
- [Optimizing LLM Usage](#optimizing-llm-usage)
- [Common Error Patterns](#common-error-patterns)

## Tools Overview

The server provides these tools for log analysis:

### 1. tail_logs - Start Here for Failures
**Best first step for diagnosing failures** — shows the last N log lines where errors typically appear.

Defaults to 10 lines if `tail` is omitted or zero.
Use `tail: 50-100` for an initial failure check when you want more than the default.

### 2. search_logs - For Specific Issues
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

### 3. read_logs - For Sequential Reading
**Use when you need to read a specific section** of logs in order, using a row number found via search_logs.

Always set `limit` — logs can be very large.

## Debugging Workflow

### Step 0: Identify the Failing Job
Before pulling any logs, call `list_jobs` with `state=failed,broken` to narrow down which jobs need attention.

This returns only the jobs that didn't pass. From the results:
- **`failed`** jobs actually ran and exited non-zero — these are the root cause, start here
- **`broken`** jobs never ran due to a failed dependency or unmet `if` condition — they are usually downstream victims of the `failed` job

Take the `id` of the `failed` job as your `job_id` for log investigation.

### Step 1: Quick Assessment
Use `tail_logs` with `tail: 50-100` to see the most recent output. Most failures surface here.

### Step 2: Error Hunting
Use `search_logs` with common error patterns:
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
