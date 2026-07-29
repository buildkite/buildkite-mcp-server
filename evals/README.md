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

## Permission posture

In CI the LLM agent runs with `--permission-mode bypassPermissions` and NO tool allowlist. This is deliberate: real users rarely restrict tools, so tool CHOICE is part of what the eval measures — an agent that reaches for `curl` against the Buildkite API instead of the MCP tools is signal that the tools aren't compelling. Check the `eval-tools` annotation on each build to see what was actually called.

Containment does not come from permissions; it comes from layers the agent cannot talk its way around:
* The docker container sandbox (throwaway, non-root)
* The MCP server's `--read-only` mode (write tools are never registered, see `mcp_in_ci.json`/`mcp.json`)
* The read-only Buildkite API token

Locally there is no container sandbox, so the posture is a conscious choice via `LOCAL_BYPASS_PERMISSION` (required, deliberately NO default — the script fails loudly and points here):
* `false`: restricted mode — the agent gets `Edit`, `go`/`make`/`git` Bash, and the MCP server's tools (server name derived from `mcp.json`, override with `MCP_SERVER_NAME`). Safe on a host machine, but NOT comparable to CI runs since the agent's tool choice is constrained.
* `true`: CI parity — `bypassPermissions`, i.e. the agent has unrestricted Bash ON YOUR MACHINE. Use with care.

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
