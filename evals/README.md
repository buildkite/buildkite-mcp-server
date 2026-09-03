## What is this?

Under certain conditions, e.g., branch being pushed/created in repo, PR created, etc., this pipeline will run, kicking-off a specified LLM agent backed by a specified model to exercise a specified buildkite-mcp-server against preset scenarios to evaluate the performance of the buildkite-mcp-server.

## What is the output?

A set of artifacts, and annotations in the Buildkite build showing:
* LLM agent metrics
  * Tool calls, input/output tokens, input/output cache tokens, etc.
* Quantitative evaluation report
  * List of things order by criticality that the LLM agent could have done better, due to harness loop, tool call choices, etc.
* Comparison reports
  * LLM agent metrics
  * High-level steps taken by LLM agent to complete scenarios

## Supported agents

`evals.yaml` entries choose the agent per scenario (`agent:` key):

* `claude` — Claude Code CLI, run via `scripts/claude.sh` in CI. Auth comes from Buildkite Hosted Models (`ANTHROPIC_BASE_URL`/`ANTHROPIC_API_KEY` derived from the agent endpoint), the MCP server is passed with `--mcp-config`, and the session transcript under `~/.claude/projects/` feeds the audit and klaren steps.
* `cursor` — Cursor CLI (`cursor-agent`), run via `scripts/cursor.sh` in CI. Differences, all forced by the CLI surface:
  * Auth is `CURSOR_API_KEY` (Cursor's own backend; the Hosted Models Anthropic proxy can't front it). Map the secret in `.buildkite/pipeline.evals.yml`; entries are skipped loudly while it's unset.
  * `model` must be a Cursor model id (`composer-2.5`, `sonnet-4.5`, `gpt-5.2`, ... — `cursor-agent --list-models` or `GET https://api.cursor.com/v1/models`; ids rotate as Cursor ships new models, aliases like `composer` track the latest).
  * No `--mcp-config` flag: a `.cursor/mcp.json` is generated inside the throwaway eval-repo clone, referencing the entry token as `${env:BUILDKITE_API_TOKEN}` (Cursor's documented interpolation syntax — no secret is written to disk; the generated file is also kept off the scenario branch via `.git/info/exclude` as diff hygiene), and servers are auto-approved with `--approve-mcps`.
  * No `--append-system-prompt`: `prompts/system.md` is prepended to the user prompt (it lands in the user turn, not the system turn — a fidelity caveat when comparing against claude runs).
  * No on-disk transcript: the raw `stream-json` capture stands in for it. That stream records tool calls as `tool_call` started/completed events and carries NO token usage, so `bk-tool-audit-v2.sh --agent cursor` reports tokens/cost as `null` — compare cursor runs on tool calls, duration, and the klaren review instead.
  * Locally, cursor entries require `LOCAL_BYPASS_PERMISSION=true` (`--force`); the restricted allowlist mode below is claude-only for now (cursor's `.cursor/cli.json` permissions are not wired up).

* `cursor-cloud` — Cursor CLOUD agents, the hosted harness behind the Cursor 3 ("Glass") app's Agents window — i.e. the client Origin users actually run, always at Cursor's latest release (PB-2959). Driven entirely over the [Cloud Agents API](https://cursor.com/docs/cloud-agent/api/endpoints) by `scripts/cursor-cloud.sh`; there is ONE code path for CI and local runs because the agent executes in Cursor's cloud VM either way:
  * Launch: `POST /v1/agents` with the rendered prompt (`prompts/system.md` prepended — same fidelity caveat as `cursor`), the eval repo at the scenario branch as `startingRef`, `workOnCurrentBranch: true` (fixes land on the scenario branch itself, not an auto `cursor/*` branch), and the entry token as a session-scoped encrypted `envVar`.
  * MCP: an inline `mcpServers` definition from `mcp_cursor_cloud.json` whose stdio command **clones and builds the MCP server inside the VM at the exact commit under review** (`MCP_SRC_SHA` — set explicitly in CI via the `MCP_SRC_SHA=${BUILDKITE_COMMIT}` mapping in `.buildkite/pipeline.evals.yml`, defaulting to the harness repo's HEAD locally; the repo via `MCP_SRC_REPO`, or the whole definition via `CURSOR_CLOUD_MCP_CONFIG`). This preserves `mcp_version: source` in the cloud — with two caveats: local **uncommitted** changes are not what's tested (unlike local claude/cursor runs, which use the locally built binary), and the VM must offer git+go and GitHub egress (first-live-run check; the fallback is a `.cursor/environment.json` install step on the scenario base branch — see `mcp_cursor_cloud.json`'s `_comment`). The server is deliberately named `buildkite-mcp-server`, NOT `buildkite`: the Cursor org's shared Buildkite **plugin** plants a hosted `Buildkite` MCP server in every cloud VM that sits unauthenticated (`needsAuth`) there, the API offers no per-run plugin exclusion, and a colliding name made the first live run see only the dead plugin server (zero MCP calls). `cursor-cloud.sh` prepends an environment note telling the agent the plugin server/skill are dead and which server works — hygiene only; whether the agent then prefers MCP over raw `curl` stays unprompted.
  * Transcript: the SSE run stream (`GET .../runs/{id}/stream`), captured as one `{type, data}` JSON line per event — `tool_call` events carry args and results. `bk-tool-audit-v2.sh --agent cursor-cloud` reads it; token totals come from `GET /v1/agents/{id}/usage` (appended as a `usage` record), so **tokens are real** — better than the CLI — while cost and message counts stay null (Cursor pricing isn't modeled; the stream is text deltas without message boundaries).
  * Auth & access: same `CURSOR_API_KEY` secret as `cursor` (entries skip loudly without it), plus a one-time grant of the **Cursor GitHub integration** on the eval repo (the VM clones via Cursor's GitHub App, not our `GITHUB_TOKEN`).
  * Containment: Cursor's VM sandbox + the `--read-only` MCP server built in the VM + the read-only entry token. Nothing executes on the host/container running babystand.sh, so `LOCAL_BYPASS_PERMISSION` does not gate cursor-cloud entries. Note the token custody shift: the entry's Buildkite API token is handed to Cursor's cloud (encrypted at rest, deleted with the agent) — keep eval tokens read-only and single-purpose. The one-shot agent is **deleted when the run ends** (any outcome) precisely so that token doesn't outlive the eval; set `kill_agent_on_complete: false` on the entry (or `CURSOR_CLOUD_KEEP_AGENT=1` locally) to keep it for postmortems in the Cursor dashboard, accepting the retained token until you delete the agent manually.
  * Ops notes: the launch endpoint is strictly rate-limited (~1/user/min; the script retries a 429 once per attempt cycle), runs are capped at `CURSOR_CLOUD_TIMEOUT_MINS` (default 90 — reconnects get the *remaining* budget) and cancelled best-effort on abort/timeout, and dropped SSE connections reconnect with `Last-Event-ID` resume plus a status-poll fallback that synthesizes the terminal `result` record if the stream missed it (if the server ignores the resume header and replays events, tool audits stay correct — calls dedupe by `callId`).

The klaren reviewer always runs on claude regardless of the entry's agent — it's the judge, not the subject under test, and a fixed reviewer keeps reviews comparable across agents.

Cursor's Slack bot (`@Cursor`) and BugBot are further Cursor entry points, but they're interactive/review-oriented and a poor fit for a scripted matrix.

## Permission posture

In CI the LLM agent runs with `--permission-mode bypassPermissions` (claude) / `--force --approve-mcps` (cursor) and NO tool allowlist. (cursor-cloud has no posture to pick — the agent runs unrestricted inside Cursor's own VM sandbox, never on our host; see "Supported agents".) This is deliberate: real users rarely restrict tools, so tool CHOICE is part of what the eval measures — an agent that reaches for `curl` against the Buildkite API instead of the MCP tools is signal that the tools aren't compelling. Check the `eval-tools` annotation on each build to see what was actually called.

Containment does not come from permissions; it comes from layers the agent cannot talk its way around:
* The docker container sandbox (throwaway, non-root)
* The MCP server's `--read-only` mode (write tools are never registered, see `mcp_in_ci.json`/`mcp.json`)
* The read-only Buildkite API token

Locally there is no container sandbox, so the posture is a conscious choice via `LOCAL_BYPASS_PERMISSION` (required, deliberately NO default — the script fails loudly and points here):
* `false`: restricted mode — the agent gets `Edit`, `go`/`make`/`git` Bash, and the MCP server's tools (server name derived from `mcp.json`, override with `MCP_SERVER_NAME`). Safe on a host machine, but NOT comparable to CI runs since the agent's tool choice is constrained. Claude-only: cursor entries are skipped in this mode (see "Supported agents").
* `true`: CI parity — `bypassPermissions` / `--force`, i.e. the agent has unrestricted Bash ON YOUR MACHINE. Use with care.

## Setup

### Code

All new code are mostly in `evals/` folder

* Add pipeline (.buildkite/pipeline.evals.yml) which run evals
* evals.yaml
  * The eval matrix: each entry pins an agent, model, prompt template, scenario
    (setup bash + vars), MCP version, and optional comparison base/target.
    babystand.sh executes every entry in order. Override the file with
    `EVALS_CONFIG`. The per-field semantics are documented at the top of the file.
  * Scenarios can live in ANY Buildkite org (tokens are single-org), so an entry
    may set `token_env` to the name of the env var holding its org's token —
    convention `MCP_EVAL_FRAMEWORK_<ORG>_ORG_BUILDKITE_API_TOKEN`, identical to
    the secret names in .buildkite/pipeline.evals.yml. babystand.sh hands the
    value to that entry's MCP server as `BUILDKITE_API_TOKEN` (the server's
    fixed interface); entries whose named var is unset are skipped loudly
* In 'evals/prompts/' folder:
  * klaren.md
    * Review LLM agent session log and complain loudly about what could've been better
  * Other `<name>.md` files are prompt templates referenced by evals.yaml entries,
    rendered per entry with `{{.KEY}}` substitution (globals + scenario vars +
    setup vars)
* In 'evals/scripts/' folder:
  * babystand.sh
    * Drives the evals.yaml matrix: per entry it runs the scenario setup, renders
      the prompt, runs the LLM agent, audits the session, runs the klaren review,
      writes a run bundle to `evals/runs/<id>/<id>-<datetime>.<ext>` (eval-final,
      metrics, tools, klaren, transcript; gitignored; also uploaded as build
      artifacts in CI), publishes per-entry annotations (`eval-final-<id>`,
      `eval-metrics-<id>`, ...), and compares against a baseline via
      bk-eval-compare.sh
    * This script actually can be run locally as well for testing/debugging
      * `LOCAL_CI`: If false, then prompt specifically instructs LLM agent not to cheat by running local CI to uncover issues, but instead wait for CI to be red before attempting to turn it green
      * `DEBUG_PERMISSIONS`: If true, then prompt specifically instructs LLM agent to fail instead of trying to bypass. This is useful when you're trying to setup the env to run scenarios.
      * `LOCAL_BYPASS_PERMISSION`: See "Permission posture" above. Required, no default.
    * Running locally: from this repo's root, with `./buildkite-mcp-server` built
      (`make build`), `jq` and `yq` (mikefarah) installed, and your git
      credentials able to push to the eval repo:
      ```bash
      BUILDKITE_API_TOKEN=... \
      LOCAL_CI=false \
      DEBUG_PERMISSIONS=false \
      LOCAL_BYPASS_PERMISSION=false \
      ./evals/scripts/babystand.sh
      ```
  * bk-eval-compare.sh
    * Compare one matrix entry's run against a baseline. Base/target are each a
      Buildkite build (its uploaded `runs/<id>/*` artifacts) or a local
      `evals/runs` path; the default base is the entry's bundle from the last
      successful `main` build, the default target is the current run
  * bk-tool-audit-v2.sh
    * Retrieve stats about tool calls, input/output token/cache usage from LLM session logs
    * For claude runs it also breaks execution time down: `duration_seconds`
      (wall-clock), `tool_time_seconds` (inside tool calls), and `wait`
      (inside *waiting* tool calls — `wait_for_build` plus Bash commands that
      `sleep`-poll), computed by pairing each `tool_use` with its
      `tool_result` via the transcript's per-line timestamps. cursor /
      cursor-cloud streams carry no per-event timestamps, so `wait` /
      `tool_time_seconds` are null there (their result event still reports
      total duration)
  * dd-metrics.sh
    * Publish one entry's run metrics to Datadog (metrics v2 series API) as
      `mcp_eval.*` gauges — run count, goal_achieved (1/0), duration /
      tool-time / wait seconds, tool call counts (total + `mcp__`-prefixed),
      and token usage (input / output / cache read / cache write) — tagged
      `entry:`, `prompt:`, `agent:`, `model:`, `goal:`, `buildkite_build:`
    * Best-effort by design: skips loudly when `DD_API_KEY` is unset (local
      runs, forks), and a publish failure never fails the entry. `DD_SITE`
      picks the Datadog site, `DD_METRIC_PREFIX` the namespace (default
      `mcp_eval`), `DD_TIMESTAMP` the point timestamp (the run's end; clamped
      to Datadog's ~1h ingest window), `DD_DRY_RUN=true` prints the payload
      instead of submitting
    * SECURITY: this never runs while the eval agent does — in either mode.
      The agent under test has unrestricted Bash, and a key present anywhere
      in the run (even captured-then-`unset`) stays readable to same-UID
      processes via `/proc/<pid>/environ`. babystand.sh only records
      metadata; dd-publish.sh (below) is the single place the key exists
    * Goal detection (in babystand.sh): entries whose setup pushed a
      `SCENARIO_BRANCH` count as achieved when that branch's latest build in
      the eval org is `passed`; entries without one (e.g. analyze-build)
      report `unknown` — tagged, but no `goal_achieved` series, so gaps in
      Datadog never miscount as misses
  * dd-publish.sh
    * The single Datadog publish entry point, and the only process that may
      hold `DD_API_KEY`. Reads each entry's `runs/<id>/<run-key>.dd.json`
      (identity + outcome, written by babystand.sh) plus `.metrics.json`,
      then drives dd-metrics.sh once per entry
    * CI mode (no argument): the `dd-publish` pipeline step — downloads the
      run-bundle artifacts; the key is scoped to that step via `secrets`
    * Local mode (`dd-publish.sh <runs-dir>`): run it AFTER babystand.sh has
      exited, in a fresh shell — never export `DD_API_KEY` into the eval run
      itself:
      ```bash
      DD_API_KEY=... ./evals/scripts/dd-publish.sh evals/runs
      ```
      (Points keep each run's recorded end time, clamped to Datadog's ~1h
      ingest window — publish soon after the run or old points snap to the
      window edge)
    * CI wiring: the `MCP_EVAL_FRAMEWORK_DATADOG_API_KEY` secret must exist in
      the cluster (see .buildkite/pipeline.evals.yml) — create it before that
      mapping ships, or the step fails at secret resolution
  * parser.ts
    * Parse LLM agent assistant/user convo into bk annotation
  * Dockerfile
    * Given an buildkite mcp version, build that buildkite mcp version into a docker image (not implemented yet, currently it just builds the buildkite mcp based on current code
  * mcp_in_ci.json
    * The mcp config used when `babystand.sh` is running in ci
  * mcp.json
    * The mcp config used when `babystand.sh` is running locally
  * mcp_cursor_cloud.json
    * The inline `mcpServers` definition sent in Cursor Cloud Agents launch
      requests (`agent: cursor-cloud`) — self-clones and builds the server in
      Cursor's VM at the commit under review; see its `_comment`
  * package.json/package-lock.json
    * npm packages required
  * tsconfig.json
    * Typescript config

### On buildkite.com

* Pipeline is set at - https://buildkite.com/buildkite/buildkite-mcp-server-evals-framework
  * This pipeline is set to:
    * Upload .buildkite/pipeline.evals.yml
    * Use buildkite-mcp-evals cluster (https://buildkite.com/organizations/buildkite/clusters/1295e59e-8fa9-4525-b873-f3a0ce2efe45/queues)
    * Secrets for
      * Github to access scenarios (read/write) in external repo, e.g., where scnearios are, so we can access/create branches and push, etc.
      * Buildkite for bk org to retrieve the last successful build (read) to compare
      * Buildkite for orgs where the scenario pipelines (read) are (to monitor scenarios from red-to-green)
