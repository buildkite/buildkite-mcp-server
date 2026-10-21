## What is this?

There is a [pipeline](https://buildkite.com/buildkite/buildkite-mcp-server-evals-framework) that kicks-off a specified LLM agent backed by a specified model to exercise a specified buildkite-mcp-server against preset scenarios to evaluate the performance of the buildkite-mcp-server.

## What is the output?

A set of artifacts, and annotations in the Buildkite build showing:
* LLM agent metrics
  * Tool calls, input/output tokens, input/output cache tokens, etc.
* Quantitative evaluation report
  * List of things order by criticality that the LLM agent could have done better, due to harness loop, tool call choices, etc.
* Comparison reports
  * LLM agent metrics
  * High-level steps taken by LLM agent to complete scenarios
* Datadog dashboard
  * Charts the LLM agent metrics (see above) over runs

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

## Evaluator selection

Each `evals.yaml` entry selects `evaluator: yilong | klaren` (default `yilong`).
Set `EVALUATOR=klaren` when creating a build to override all entries.

* **Yilong** calls Jev, TypeSafe's System One model, directly at
  `https://api.typesafe.ai/v1/systemone` (`YILONG_MODEL`, default `jev-1.14.0`).
  It uses the same `prompts/rubric.json` and prompt/scenario reference paths as
  Klaren. There is no second rubric file or automatic grade adjustment.
* **Klaren** produces the detailed review, rubric grades and candidate references
  described below. Use it on demand; comparison annotations compare its reports
  only when both runs have one.

Yilong sends one request per category, with these input fields:

| Category | Input sent to Jev |
| --- | --- |
| Conclusion | Reviewed root-cause reference; agent's final report |
| Investigation | Rendered task; reviewed root-cause reference; tool calls paired with full captured tool outputs; final report |
| Fix | Reviewed root cause (when available) and fix reference; complete pushed patch, or final report for diagnosis-only tasks; final build state when available |

A **tool output** is the response to one tool call (for example, returned build
logs). The **trace** combines calls and their captured outputs; it is not the
whole conversation or private reasoning. An **input field** is one named piece
of evidence in the request. Jev's **result** contains the grading probabilities,
confidence and expected score, saved in `<run>.yilong.json`. The annotation and
Datadog score use the most probable grade without changing it, even when grades
contradict one another. Known token patterns are redacted before sending.

Ties select the lower tied grade. Responses must contain exactly one numeric
probability per rubric level, each in [0,1], summing to 1 within 0.000001.
Invalid distributions are reported as **undetermined**, with an explanation in
the annotation and no numeric category score or Datadog category metric.

Yilong first sends complete evidence. TypeSafe documents a 32 MiB body limit and
default contexts of 64k tokens per request and 32k per question
([limits](https://docs.typesafe.ai/models), [errors](https://docs.typesafe.ai/api)).
Only HTTP 400 `detail.error_type: max_tokens_exceeded` triggers these fallbacks:

1. Retry the **identical full request** with
   `TypeSafe-Danger-Zone-Long-Context: true` (262k tokens per request and question).
2. If long context also rejects it for tokens, ask **Haiku 4.5** to summarize
   oversized observed evidence (`trace`, final report, or patch/proposed fix).
   Send the summarized request to Jev in long-context mode. Jev still assigns
   the grades; Haiku has no tools and does not grade or generate reference answers.

Summarization processes every part of an oversized field in ordered, UTF-8-safe
chunks of at most 48,000 bytes. Shorter fields, the task, reviewed references,
rubric and final build state stay unchanged. Chunk summaries are reduced again
if needed (at most six passes, requiring size reduction each time). Instructions
preserve decisive evidence, contradictions, failed attempts, verification gaps,
and weakened/deleted tests, but summaries are still **lossy**. The annotation
marks each summary-based verdict with an asterisk (for example, **Sherlock**\*)
and an explanatory footnote. Each question in `<run>.yilong.json`
records `evidence_mode: full | summarized`, `context_mode: default | long`, and
Haiku model/call/token usage when summarization succeeds. Redaction is reapplied
to generated summaries.

On a token-limit failure, the complete redacted request is saved as
`<run>.yilong-<category>.evidence.json`; a successful summary is saved as
`<run>.yilong-<category>.summary.json`. CI uploads these beside the verdicts and
links them from the annotation. Locally they remain beside the input bundle.
Bodies over 32 MiB are still withheld locally, without summarization. Unusable,
unfinished or failed summaries, unchanged oversized goals/references, and final
Jev rejections produce **undetermined**, with a reason and no numeric category
score. There is no silent head/tail truncation or repeated summarization loop
after the final Jev request.

429s and transient failures are retried with backoff. Missing references or
required evidence mean **n/a**; an unavailable patch for an implemented fix is
**undetermined**, never replaced by the agent's claims. An empty captured patch
is sent as evidence that no changes were pushed.

CI runs Yilong in a separate step with `MCP_EVAL_FRAMEWORK_JEV_TOKEN`, never in
the evaluated agent's container. Haiku uses the existing Buildkite Hosted Models
connection (`BUILDKITE_AGENT_ENDPOINT` and `BUILDKITE_AGENT_ACCESS_TOKEN`) through
the Anthropic Messages API; no extra secret or Claude CLI is needed. The fallback
requires Python 3 (standard library only), available on hosted Ubuntu agents.
Outside CI, set `ANTHROPIC_BASE_URL` and `ANTHROPIC_API_KEY` to enable Haiku;
missing credentials leave oversized categories undetermined.
Yilong downloads `<run>.judge.json`, final reports,
transcripts and complete `.diff.patch` artifacts, then uploads verdicts and
annotates the build. Datadog publishing follows it and handles either evaluator.
Locally, run `./evals/scripts/yilong.sh evals/runs/<id>/<run>.judge.json` with the
token in your environment; `YILONG_DRY_RUN=true` prints requests without calling
the API or requiring a key. Old bundles lacking prompt/scenario metadata must
be regenerated rather than falling back to entry-ID references.

## Klaren rubric and reference answers

Klaren keeps its detailed session review and also grades three outcomes using
`prompts/rubric.json`, the canonical grading instructions and level definitions
for evaluators. The review prompt does not duplicate those definitions:

| Category | Score 0 | Score 0.5 | Score 1 |
| --- | --- | --- | --- |
| Correctness of conclusion | Bollocks | Sorta | Yeah baby |
| Quality of investigation | Homer Simpson | Scooby-Doo | Sherlock |
| Correctness of fix | Airshot | Duct tape | Sweeet |

Sherlock requires a correct diagnosis **and** a rigorous investigation backed by
actual tool results. A completely incorrect conclusion, or a correct conclusion
reached through guessing, fabrication or fundamental misinterpretation, is Homer
Simpson. Meaningful investigation with a partly correct conclusion or material
verification gaps is Scooby-Doo. Hiding a failure by skipping a valid test or
weakening assertions is Airshot; a genuine but partial or fragile fix is Duct
tape. A minimal, robust fix across the relevant conditions is Sweeet.

Reviewer grades and reasons are retained without automatic cross-category
adjustments, including contradictory grades. Missing reviewed references or
required evidence mean **n/a**, not zero;
malformed reviewer output means **undetermined**, not a successful grade.

Reference answers are keyed by **prompt and scenario**, not the matrix entry ID,
agent or model. Set a stable `scenario.id` in each matrix entry:

```yaml
prompt: red-to-green-comment-only
scenario:
  id: buildkite-204234
  vars:
    BUILD_URL: https://buildkite.com/buildkite/buildkite/builds/204234
```

This uses `expected/red-to-green-comment-only/buildkite-204234/root_cause.md`
for conclusion and investigation, and `fix.md` in the same directory for fix.
Agent/model variants with the same prompt and scenario share those answers.
Use a different scenario ID when the target or expected outcome changes; do not
include generated branch names or run timestamps in it. No `scenario.id` means
no reference lookup and n/a grades, but detailed review and candidate generation
still run. There is no fallback to an entry-ID answer key.

Locally the reviewer uses the harness's files. CI fetches them from the exact
`MCP_SRC_SHA` commit **after the evaluated agent returns**; they are never
baked into the eval image. If the files cannot
be loaded, the affected categories remain n/a. The initial reference for
`red-to-green-comment-only` comes from the diagnosis in eval build 395.

The run bundle adds:

* `<run>.diff.patch` — complete pushed diff for code-changing scenarios, linked
  from the verdict annotation. Klaren reads the file rather than a truncated
  inline excerpt. If capture fails or klaren does not confirm full review, fix
  is undetermined and publishes no numeric score. Diagnosis-only runs continue
  to grade the proposed fix from the report without requiring a diff.
* `<run>.klaren.json` — validated verdicts and derived numeric scores.
* `<run>.klaren-verdicts.md` — summary annotation with reasons and candidate links.
* `<run>.klaren-candidate-root_cause.md` and `<run>.klaren-candidate-fix.md` —
  evidence-backed **candidate** reference answers, when the reviewer has enough
  evidence. These never replace the reviewed references automatically and cannot
  be used to grade the same run that generated them.

To use a candidate as a reference answer, verify its evidence and scenario applicability,
then copy the reviewed content into `evals/expected/<prompt>/<scenario.id>/root_cause.md` or
`fix.md` and commit it. An unsupported candidate is omitted, not invented.
When candidates exist, the verdict annotation and log include download links,
the destination paths, and instructions to review, correct or discard candidates,
submit the approved answers in a PR, and rerun using the commit containing them.
If `scenario.id` is missing, the instructions explain how to configure it first.

CI publishes `mcp_eval.klaren.{conclusion,investigation,fix}` and
`mcp_eval.klaren.undetermined`, tagged `evaluator:klaren`, alongside normal run
metrics. n/a and undetermined categories publish no score. Both
[MCP Evals](https://app.datadoghq.com/dashboard/vsr-kw9-upf/) and
[MCP Evals by Build](https://app.datadoghq.com/dashboard/rx7-bme-f27/) show the
three categories with the level markers above. Yilong publishes matching
`mcp_eval.yilong.*` metrics separately; selecting Klaren retains its full workflow.

Run the focused checks with
`python3 -B -m unittest discover -s evals/scripts -p 'test_*.py' -v`.

## Struggles

`bk-tool-audit-v2.sh struggles` reads the transcript for failed tool calls and the patterns around them — signs the agent was struggling:

* Failed calls grouped by type, then tool: `exit N` (shell exit code), `timeout`, `interrupted`, `blocked` (permission / harness refusal), `MCP tool error`, `tool error`. Shell calls are labelled by their first real program (`Bash: git`, `Bash: go`).
* "Likely harmless" failures — exit 1 with no output from `grep`/`rg`/`test`/`diff`/... (no match / false) — are counted separately and left out of the patterns.
* Patterns: longest run of failed calls in a row, the same call (tool + exact input) failing 2+ times, and a failed call followed straight away by the same tool (and whether that retry worked). Example error lines per type.
* Supported for `claude` (tool_result `is_error`, `Exit code N`) and `cursor` (`failure` results, shell `exitCode` / `aborted`, MCP `isError`), both checked against real transcripts. **Not implemented for `cursor-cloud` yet**: the run logs a WARNING and the annotation says so (no numbers, so nothing reads as zero failures).
* Output: the `eval-struggles-<id>` annotation (and `<run>.struggles.md`), `struggles` in `<run>.metrics.json`, comparison rows (failed calls, longest streak, calls failing 2+ times), Datadog `tool_errors.failed` / `.likely_harmless` / `.longest_streak` / `.repeated`, and klaren's prompt.

### Model API retries

Separate from the agent's own struggles: `bk-tool-audit-v2.sh model-retries` counts the agent harness retrying its call to the **model API** — Claude Code's `{"type":"system","subtype":"api_retry", error_status, retry_delay_ms, ...}` stream events, e.g. a 504 or 429 from the model gateway (Buildkite Hosted Models in CI). These are not tool calls (and not Buildkite API requests), so the struggles report does not see them, but they add wall-clock time. Grouped by status with the time spent waiting; read from claude.sh's raw stream (`CLAUDE_RAW_LOG`). Claude only — other agents show "not available". Shown at the bottom of the `eval-struggles-<id>` annotation (count in its title), as `model_api_retries` in `<run>.metrics.json`, a "Model API retries" comparison row, and Datadog `model_api.retries` / `.retry_wait_seconds`.

## Permission posture

In CI the LLM agent runs with `--permission-mode bypassPermissions` (claude) / `--force --approve-mcps` (cursor) and NO tool allowlist. (cursor-cloud has no posture to pick — the agent runs unrestricted inside Cursor's own VM sandbox, never on our host; see "Supported agents".) This is deliberate: real users rarely restrict tools, so tool CHOICE is part of what the eval measures — an agent that reaches for `curl` against the Buildkite API instead of the MCP tools is signal that the tools aren't compelling. Check the `eval-tools` annotation on each build to see what was actually called.

Scenario prompts prohibit reading reference answers, evaluator reviews and
candidate-answer artifacts, whether local or remote. This is an instruction to
the evaluated agent, not filesystem or network isolation: Cursor Cloud's source
checkout still contains the public references. The restriction is deliberately
not in the shared system prompt, since the reviewer needs those references.

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
      credentials able to push to the eval repo. The MCP_EVAL_FRAMEWORK_BUILDKITE_ORG_XXXX_API_TOKEN
      is optional and only required if you are asking the agent to perform evaluation on a build
      that does not belong to buildkite; it holds the API token of the organization, e.g., XXXX, which owns the buiild.
      ```bash
      BUILDKITE_API_TOKEN=... \
      MCP_EVAL_FRAMEWORK_BUILDKITE_ORG_XXXX_API_TOKEN=... \
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
  * yilong.sh
    * Jev evaluator — see "Evaluator selection" above. CI runs it with no
      arguments; locally, pass the `<run>.judge.json` paths to evaluate.
  * dd-metrics.sh
    * Publish one entry's run metrics to Datadog (metrics v2 series API) as
      `mcp_eval.*` gauges — run count, goal_achieved (1/0), duration /
      tool-time / wait seconds, tool call counts (total + `mcp__`-prefixed),
      and token usage (input / output / cache read / cache write), plus separate
      klaren/yilong rubric scores — tagged `entry:`, `prompt:`, `agent:`, `model:`, `goal:`,
      `buildkite_build:`, `evaluator:`
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
      hold `DD_API_KEY` — the `dd-publish` pipeline step. Downloads each
      entry's `runs/<id>/<run-key>.dd.json` (identity + outcome, written by
      babystand.sh) plus `.metrics.json` and `.klaren.json` run-bundle artifacts, then drives
      dd-metrics.sh once per entry; the key is scoped to that step via
      `secrets`
    * Datadog publishing is CI-only. Local runs are for debugging prompts
      and scenarios: they record the same `dd.json` in the bundle but never
      publish (and babystand.sh warns + unsets if `DD_API_KEY` is exported
      into a run). To test the payload by hand, use
      `DD_DRY_RUN=true dd-metrics.sh`
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
