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
  * `model` must be a Cursor model id (`composer-1`, `sonnet-4.5`, `gpt-5.2`, ... — `cursor-agent --list-models`).
  * No `--mcp-config` flag: a `.cursor/mcp.json` is generated inside the throwaway eval-repo clone, referencing the entry token as `${env:BUILDKITE_API_TOKEN}` (Cursor's documented interpolation syntax — no secret is written to disk; the generated file is also kept off the scenario branch via `.git/info/exclude` as diff hygiene), and servers are auto-approved with `--approve-mcps`.
  * No `--append-system-prompt`: `prompts/system.md` is prepended to the user prompt (it lands in the user turn, not the system turn — a fidelity caveat when comparing against claude runs).
  * No on-disk transcript: the raw `stream-json` capture stands in for it. That stream records tool calls as `tool_call` started/completed events and carries NO token usage, so `bk-tool-audit-v2.sh --agent cursor` reports tokens/cost as `null` — compare cursor runs on tool calls, duration, and the klaren review instead.
  * Locally, cursor entries require `LOCAL_BYPASS_PERMISSION=true` (`--force`); the restricted allowlist mode below is claude-only for now (cursor's `.cursor/cli.json` permissions are not wired up).

The klaren reviewer always runs on claude regardless of the entry's agent — it's the judge, not the subject under test, and a fixed reviewer keeps reviews comparable across agents.

### Cursor cloud agents (not implemented — options)

Besides the CLI, Cursor exposes hosted "cloud agents" that could run scenarios without us managing the container/CLI at all, via its REST API (Bearer/Basic auth with a Cursor Dashboard API key):

* `POST https://api.cursor.com/v1/agents` with `prompt.text`, `repos[]` (GitHub URL + `startingRef`), and optional `model.id` — the agent clones the repo, works, and pushes branches/PRs itself.
* Poll `GET /v1/agents/{agentId}/runs/{runId}` for `status` (`RUNNING`/`FINISHED`/`ERROR`/...), or stream `GET .../stream` (SSE) for real-time tool calls and assistant text — that stream could feed the same parser/annotation pipeline.
* Terminal runs return `result` (final reply), `durationMs`, and `git.branches` (pushed branches / PR URLs); `GET /v1/agents/{agentId}/usage` returns token usage (which the CLI's stream-json lacks).

Fit with this harness: a `run_cursor_cloud()` would replace the clone + CLI invocation with create-agent → poll/stream → collect results. Three impedance mismatches to solve first: (1) MCP — cloud agents run in Cursor's environment and cannot spawn our locally-built `--read-only` stdio binary, so the scenario repo would need Buildkite's hosted HTTP MCP server configured in its `.cursor/mcp.json`, which changes what's under test from "this PR's source build" to a deployed server; (2) scenario setup (the branch push) still has to happen from CI before launching the agent; (3) containment shifts from our throwaway container to Cursor's sandbox plus the repo/token permissions granted to it. Cursor's Slack bot (`@Cursor`) and BugBot are further entry points, but they're interactive/review-oriented and a poor fit for a scripted matrix.

## Permission posture

In CI the LLM agent runs with `--permission-mode bypassPermissions` (claude) / `--force --approve-mcps` (cursor) and NO tool allowlist. This is deliberate: real users rarely restrict tools, so tool CHOICE is part of what the eval measures — an agent that reaches for `curl` against the Buildkite API instead of the MCP tools is signal that the tools aren't compelling. Check the `eval-tools` annotation on each build to see what was actually called.

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
  * parser.ts
    * Parse LLM agent assistant/user convo into bk annotation
  * Dockerfile
    * Given an buildkite mcp version, build that buildkite mcp version into a docker image (not implemented yet, currently it just builds the buildkite mcp based on current code
  * mcp_in_ci.json
    * The mcp config used when `babystand.sh` is running in ci
  * mcp.json
    * The mcp config used when `babystand.sh` is running locally
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
