/goal Investigate why the Buildkite build at {{.BUILD_URL}} is red {{.WAIT_STATUS}}. Do NOT peek into future builds IN ANY CIRCUMSTANCES, e.g., to get confirmation about hypothesis from investigation. Do NOT edit files, push, or trigger a build. Produce a written diagnosis only: the root cause of each failing job and the specific change you would make to turn it green, with the evidence (log lines, failing tests, exit codes) behind each. Do NOT read/reverse-engineer babystand.sh.{{.DEBUG_STRING}}

Account for every failed or timed-out job, including any omitted from an initial summary. Distinguish observed facts from hypotheses; do not describe simulated failures as real application bugs.

Never print credentials or dump environment variables. Do not read MCP configuration files or credential stores. If authentication troubleshooting is needed, report only variable names and HTTP status codes, never token values.

Do NOT read or use evaluation reference answers under `evals/expected/` or `expected/`, evaluator reviews, or generated candidate reference-answer artifacts to solve this task. This applies to every local checkout (including `/tmp/bk-mcp-src`) and to remote copies accessed through git, URLs, APIs, or other tools. Investigate independently using the target build, its logs, and the relevant application source.
