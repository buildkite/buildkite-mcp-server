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
* In 'evals/prompts/' folder:
  * klaren.md
    * Review LLM agent session log and complain loudly about what could've been better
* In 'evals/scripts/' folder:
  * babystand.sh
    * This script actually can be run locally as well for testing/debugging
      * `LOCAL_CI`: If false, then prompt specifically instructs LLM agent not to cheat by running local CI to uncover issues, but instead wait for CI to be red before attempting to turn it green
      * `DEBUG_PERMISSIONS`: If true, then prompt specifically instructs LLM agent to fail instead of trying to bypass. This is useful when you're trying to setup the env to run scenarios.
      * `LOCAL_BYPASS_PERMISSION`: See "Permission posture" above. Required, no default.
    * Running locally: from this repo's root, with `./buildkite-mcp-server` built (`make build`) and your git credentials able to push to the eval repo:
      ```bash
      BUILDKITE_API_TOKEN=... \
      LOCAL_CI=false \
      DEBUG_PERMISSIONS=false \
      LOCAL_BYPASS_PERMISSION=false \
      ./evals/scripts/babystand.sh
      ```
      The script clones the eval repo (`EVAL_REPO_SLUG`) into `~/eval-repo-<timestamp>`, copies your built server binary into it, and runs the agent there — the scenario branches live in the eval repo, not this one.
    * Setup various scenarios 
    * Calls LLM agent with prompts to evaluate scenarios
    * Calls klaren.md prompt to review LLM agent performance
  * bk-eval-compare.sh
    * Compare current eval result with previous 
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
      * Github to access scenarios (read/write) in external repo
      * Buildkite for bk org to retrieve the last successful build (read) to compare
      * Buildkite for anothertest org where the external repo scenario pipelines (read) are (to monitor scenarios from red-to-green)
