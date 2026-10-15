#!/bin/bash
set -euo pipefail

# Bash 5.2+ enables patsub_replacement by default: an unquoted '&' in a
# ${var//pat/repl} replacement expands to the matched pattern, which would
# corrupt scenario values like '?a=1&b=2' in render_prompt (and eat
# backslashes). Quoting the replacement instead is not portable — bash 3.2
# (macOS /bin/bash) keeps such quotes literally — so disable the option
# (guarded: it doesn't exist before 5.2).
shopt -u patsub_replacement 2>/dev/null || true

# Resolve the harness directory so sibling scripts (and the harness copies of
# evals.yaml / prompts) keep resolving after we cd into a separate git checkout
# (the subject under test) below.
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

# The eval matrix. Each entry is one run: agent + model + prompt + scenario +
# mcp_version (+ optional compare base/target). Override with EVALS_CONFIG.
EVALS_CONFIG="${EVALS_CONFIG:-$ROOT_DIR/evals.yaml}"

command -v jq >/dev/null || { echo "babystand: jq is required" >&2; exit 1; }
command -v yq >/dev/null || { echo "babystand: yq is required to parse $EVALS_CONFIG" >&2; exit 1; }
[[ -f "$EVALS_CONFIG" ]] || { echo "babystand: config not found: $EVALS_CONFIG" >&2; exit 1; }
# Canonicalize: a relative EVALS_CONFIG is validated above in the CALLER's cwd,
# but only read (yq) after the cd into the eval-repo clone below, where it
# would resolve to a different file or none at all.
EVALS_CONFIG="$(cd "$(dirname "$EVALS_CONFIG")" && pwd)/$(basename "$EVALS_CONFIG")"

# Both are required (no default), like LOCAL_BYPASS_PERMISSION below; fail with
# a clear message instead of set -u's bare "unbound variable".
: "${LOCAL_CI:?must be set to true or false — see evals/README.md}"
: "${DEBUG_PERMISSIONS:?must be set to true or false — see evals/README.md}"

if [[ "${LOCAL_CI}" == "true" ]]; then
  WAIT_STATUS_STRING="(perform local CI)"
else
  WAIT_STATUS_STRING="(patiently check for cloud CI failure status before proceeding with anything. DO NOT VIOLATE THIS)"
fi

if [[ "${DEBUG_PERMISSIONS}" == "true" ]]; then
  DEBUG_STRING=" If you do NOT have permissions to perform something, flag it loudly and BAIL!"
else
  DEBUG_STRING=""
fi

# Permission posture.
# CI: claude.sh runs with --permission-mode bypassPermissions and no allowlist
# — tool CHOICE (MCP tools vs raw curl/gh against the API) is part of what the
# eval measures, and the agent is contained by the container sandbox, the
# server's --read-only mode, and the read-only API token.
# Local: the same posture is opt-in via LOCAL_BYPASS_PERMISSION=true, because
# it gives the agent unrestricted Bash on the host. With it false, a restricted
# allowlist applies, which must include the MCP server's tools (headless
# denials are silent) — the server name is derived from mcp.json, overridable
# via MCP_SERVER_NAME, and missing both is a loud failure.
# LOCAL_BYPASS_PERMISSION has NO default, deliberately: every caller (CI sets
# it in the docker plugin env) must make the choice consciously.
: "${LOCAL_BYPASS_PERMISSION:?must be set to true or false — see evals/README.md}"
if [[ "${RUN_IN_CI:-false}" != "true" && "$LOCAL_BYPASS_PERMISSION" != "true" ]]; then
    MCP_SERVER_NAME="${MCP_SERVER_NAME:-$(jq -r '.mcpServers | keys | first // empty' "$ROOT_DIR/mcp.json")}"
    if [[ -z "$MCP_SERVER_NAME" ]]; then
        echo "ERROR: MCP_SERVER_NAME is unset and could not be derived from $ROOT_DIR/mcp.json" >&2
        exit 1
    fi
fi

# SECURITY: this script must NEVER hold DD_API_KEY. It runs the evaluated
# agent (possibly with bypassed permissions), and on Linux the key would stay
# readable in /proc/<pid>/environ for the whole run even after `unset` —
# environ is the exec-time snapshot, and any same-UID process the agent
# spawns (or leaves behind) can read it. Datadog publishing is therefore
# CI-only and fully out-of-band: entries record their identity/outcome as
# <run>.dd.json in the bundle, and the key only ever exists in the separate
# dd-publish pipeline step. Local runs are for debugging prompts/scenarios
# and never publish. The unset below is defense in depth for an operator who
# exported the key anyway, not a substitute: children stop inheriting it,
# but this process's environ still exposes it.
if [[ -n "${DD_API_KEY:-}" ]]; then
    echo "WARNING: DD_API_KEY is set in babystand.sh's environment; the agent under test" >&2
    echo "WARNING: may be able to recover it (/proc/<pid>/environ survives unset). Do not" >&2
    echo "WARNING: export it for eval runs — Datadog publishing is CI-only." >&2
    unset DD_API_KEY
fi

# One timestamp for the whole build; entries are disambiguated by ENTRY_ID.
DATETIME=$(date +%Y-%m-%d-%H%M%S)

# Both modes clone a fresh working copy of the EVAL repo — the repo holding the
# scenario base branches, which is NOT the repo this script ships in — for the
# eval (and the agent) to operate on.
REPO_SLUG="${EVAL_REPO_SLUG:-nethsix/buildkite-mcp-server}"
WORKDIR="$HOME/eval-repo-$DATETIME"

if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
  # In CI the container is built via COPY (mount-checkout=false), so nothing is
  # a git checkout. gh already authenticates from GITHUB_TOKEN in the env;
  # `gh auth setup-git` wires that into git so clone/push work too. (Do NOT
  # `gh auth login --with-token` here: it errors when GITHUB_TOKEN is set.)
  # The MCP server under test was built into the image from this repo's source
  # (see evals/Dockerfile).
  gh auth setup-git
  git clone "https://github.com/${REPO_SLUG}.git" "$WORKDIR"
else
  # Locally, clone/push use the caller's ambient git credentials, and the MCP
  # server under test is the binary the caller built at this repo's root
  # (`make build`) — copied into the clone so mcp.json's ./buildkite-mcp-server
  # command resolves against the clone (claude's working directory).
  HARNESS_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
  if [[ ! -x "$HARNESS_ROOT/buildkite-mcp-server" ]]; then
    echo "ERROR: $HARNESS_ROOT/buildkite-mcp-server not found; run 'make build' first." >&2
    exit 1
  fi
  git clone "https://github.com/${REPO_SLUG}.git" "$WORKDIR"
  cp "$HARNESS_ROOT/buildkite-mcp-server" "$WORKDIR/"
fi
cd "$WORKDIR"

# The MCP server binary mcp-mode entries talk to, and the commit it was built
# from — recorded per entry in <run>.run-info.json. CI builds it into the image
# from the commit under review (MCP_SRC_SHA=${BUILDKITE_COMMIT}); locally it is
# the caller's `make build` of the harness checkout (uncommitted changes
# included, so the SHA there is a hint, not a guarantee).
if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
  MCP_SERVER_BIN="buildkite-mcp-server"
  MCP_SOURCE_SHA="${MCP_SRC_SHA:-}"
else
  MCP_SERVER_BIN="$WORKDIR/buildkite-mcp-server"
  MCP_SOURCE_SHA="$(git -C "$HARNESS_ROOT" rev-parse HEAD 2>/dev/null || true)"
fi

# The org/pipeline the agent watches for the scenario branches' builds. This is
# the EVAL repo's pipeline (see EVAL_REPO_SLUG above), NOT the ambient
# BUILDKITE_ORGANIZATION_SLUG/BUILDKITE_PIPELINE_SLUG of the build running this
# script — those refer to the pipeline hosting the eval, which never builds
# scenario branches.
ORG_SLUG="${EVAL_ORG_SLUG:-anothertest}"
PIPELINE_SLUG="${EVAL_PIPELINE_SLUG:-buildkite-mcp-server}"

# Run bundles live under <harness>/runs/<id>/ — NOT the clone, which is
# recreated per invocation — so local runs accumulate a comparison store across
# invocations. Gitignored. This is the canonical comparison store; in CI we
# ALSO upload the same files as build artifacts named runs/<id>/* (see below).
RUNS_ROOT="$ROOT_DIR/runs"

echo "*** Initial Env Vars"
echo "***"
echo "*** LOCAL_CI: $LOCAL_CI"
echo "*** DEBUG_PERMISSIONS: $DEBUG_PERMISSIONS"
echo "*** RUN_IN_CI: ${RUN_IN_CI:-false}"
echo "*** LOCAL_BYPASS_PERMISSION: $LOCAL_BYPASS_PERMISSION"
echo "*** EVALS_CONFIG: $EVALS_CONFIG"
echo "*** DATETIME: $DATETIME"
echo "*** ORG_SLUG: $ORG_SLUG"
echo "*** PIPELINE_SLUG: $PIPELINE_SLUG"
echo "***"

# Format a duration in whole seconds as "Nm Ns".
fmt_elapsed() { printf '%dm %ds' $(($1 / 60)) $(($1 % 60)); }

# Render a prompt template: replace {{.KEY}} with VALUE for every KEY=VALUE line
# in the vars file. Line-oriented (values are single-line: urls, branch names).
# When a KEY appears more than once (globals, then scenario.vars, then setup
# output are concatenated), the LAST value wins — so e.g. a setup script can
# override a global such as ORG_SLUG.
#   render_prompt <template_file> <vars_file>   -> rendered prompt on stdout
render_prompt() {
    local template="$1" vars="$2" content key val
    content="$(cat "$template")"
    # Collapse to the last value per key first: the substitution is destructive
    # (the first replacement consumes every {{.KEY}} occurrence), so without this
    # the FIRST value would win instead of the last. awk keeps the last KEY=VALUE
    # line per key; $0 preserves values that themselves contain '='.
    while IFS='=' read -r key val; do
        [[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
        # $val is safe unquoted: patsub_replacement is disabled at the top of
        # this script (quoting it here is not portable to bash 3.2).
        content="${content//\{\{.$key\}\}/$val}"
    done < <(awk -F= '/^[A-Za-z_][A-Za-z0-9_]*=/ { last[$1] = $0 } END { for (k in last) print last[k] }' "$vars")
    printf '%s' "$content"
}

# annotate_markdown <context> <title> <file> <meta> [style] -- collapsible markdown
annotate_markdown() {
    local context="$1" title="$2" file="$3" meta="$4" style="${5:-info}"
    {
        printf '<details><summary>%s' "$title"
        [[ -n "$meta" ]] && printf ' — %s' "$meta"
        printf '</summary>\n\n'
        if [[ -s "$file" ]]; then cat "$file"; else printf '_(no final output captured)_\n'; fi
        printf '\n\n</details>\n'
    } | buildkite-agent annotate --context "$context" --style "$style" --priority 10 \
        || echo "WARNING: failed to annotate '$context'" >&2
}

# annotate_codeblock <context> <title> <file>  -- plain text, collapsed
annotate_codeblock() {
    local context="$1" title="$2" file="$3"
    if [[ ! -s "$file" ]]; then echo "WARNING: nothing to annotate for '$context'" >&2; return 0; fi
    { printf '<details><summary>%s</summary>\n\n```\n' "$title"; cat "$file"; printf '\n```\n\n</details>\n'; } \
        | buildkite-agent annotate --context "$context" --style info --priority 10 \
        || echo "WARNING: failed to annotate '$context'" >&2
}

# harness_version <agent>  -- the agent harness's own version string, as it
# reports it (e.g. "2.1.300 (Claude Code)"); "unknown" when it can't be read.
# cursor-cloud runs Cursor's hosted harness, which reports no version to us.
harness_version() {
    local out=""
    case "$1" in
        claude)       out="$(claude --version 2>/dev/null | head -n1 || true)" ;;
        cursor)       out="$(cursor-agent --version 2>/dev/null | head -n1 || true)" ;;
        cursor-cloud) out="hosted (not reported)" ;;
    esac
    printf '%s' "${out:-unknown}"
}

# prepare_bk_cli <install_dir> <download_url> <org_slug> <token>  -- make the
# Buildkite CLI (bk) ready for a `mode: cli` entry and set the caller's
# BK_CLI_BIN / BK_CLI_VERSION / BK_CLI_ORG (dynamic scoping, like the runners).
#   - download_url set: fetch it into <install_dir> — a .tar.gz/.tgz or .zip
#     release archive (the bk binary is found inside it), or a bare binary.
#   - download_url empty: use the bk already on PATH (CI: the version pinned
#     in evals/Dockerfile).
#   - org: the entry's org_slug, else the token's only organization (GET
#     /v2/organizations), else none — the agent then has to name the org.
# Returns non-zero (the entry fails) when no runnable bk can be produced.
prepare_bk_cli() {
    local dir="$1" url="$2" org="$3" token="$4"
    BK_CLI_BIN="" BK_CLI_VERSION="" BK_CLI_ORG=""
    if [[ -n "$url" ]]; then
        if [[ "$url" != https://* ]]; then
            echo "ERROR: [$ENTRY_ID] mode_params.download_url must be an https:// URL: $url" >&2
            return 1
        fi
        rm -rf "$dir"
        mkdir -p "$dir/bin" "$dir/extract"
        echo "*** [$ENTRY_ID] downloading bk CLI: $url"
        if ! curl -fsSL --retry 3 --max-time 300 -o "$dir/download" "$url"; then
            echo "ERROR: [$ENTRY_ID] failed to download bk CLI from $url" >&2
            return 1
        fi
        case "${url%%\?*}" in
            *.tar.gz|*.tgz) tar -xzf "$dir/download" -C "$dir/extract" || return 1 ;;
            *.zip)
                command -v unzip >/dev/null || { echo "ERROR: [$ENTRY_ID] unzip is required for $url" >&2; return 1; }
                unzip -q "$dir/download" -d "$dir/extract" || return 1 ;;
            *) cp "$dir/download" "$dir/extract/bk" ;;
        esac
        local found; found="$(find "$dir/extract" -type f -name bk | head -n1)"
        if [[ -z "$found" ]]; then
            echo "ERROR: [$ENTRY_ID] no 'bk' binary found in $url" >&2
            return 1
        fi
        mv "$found" "$dir/bin/bk"
        chmod +x "$dir/bin/bk"
        BK_CLI_BIN="$dir/bin/bk"
    else
        BK_CLI_BIN="$(command -v bk || true)"
        if [[ -z "$BK_CLI_BIN" ]]; then
            echo "ERROR: [$ENTRY_ID] mode 'cli' needs mode_params.download_url, or a bk binary on PATH" >&2
            return 1
        fi
    fi
    # `bk --version` prints "bk version X" with no network update check
    # (unlike `bk version`). Running it also proves the binary fits this
    # platform — a linux tarball on a macOS run fails here, loudly.
    local raw
    if ! raw="$("$BK_CLI_BIN" --version 2>&1 | head -n1)" || [[ -z "$raw" ]]; then
        echo "ERROR: [$ENTRY_ID] $BK_CLI_BIN --version failed: $raw" >&2
        return 1
    fi
    BK_CLI_VERSION="${raw#bk version }"
    BK_CLI_ORG="$org"
    if [[ -z "$BK_CLI_ORG" ]]; then
        BK_CLI_ORG="$(curl -fsS --max-time 30 -H "Authorization: Bearer $token" \
            "https://api.buildkite.com/v2/organizations" 2>/dev/null \
            | jq -r 'if type == "array" and length == 1 then .[0].slug else empty end' 2>/dev/null || true)"
        if [[ -z "$BK_CLI_ORG" ]]; then
            echo "WARNING: [$ENTRY_ID] could not work out the token's single organization; set mode_params.org_slug. bk runs with no default organization." >&2
        fi
    fi
    echo "*** [$ENTRY_ID] bk CLI: $BK_CLI_BIN (version $BK_CLI_VERSION, org ${BK_CLI_ORG:-none})"
}

# install_bk_shim <shim_dir> <real_bk> <request_log>  -- write a `bk` wrapper
# into <shim_dir> (put first on the agent's PATH) that runs the real bk through
# bk-request-log-shim.mjs, which logs each Buildkite API request bk makes to
# <request_log>. bk's output and exit code are passed through unchanged.
# Needs node; returns non-zero (caller warns, bk runs unlogged) without it.
install_bk_shim() {
    local dir="$1" real="$2" log="$3" node_bin
    node_bin="$(command -v node || true)"
    if [[ -z "$node_bin" ]]; then
        echo "WARNING: [$ENTRY_ID] node not found; bk API requests will not be logged." >&2
        return 1
    fi
    mkdir -p "$dir"
    {
        echo '#!/usr/bin/env bash'
        echo '# Eval harness wrapper: runs the real bk and logs its Buildkite API requests.'
        printf 'BK_SHIM_REAL_BK=%q BK_SHIM_REQUEST_LOG=%q exec %q %q "$@"\n' \
            "$real" "$log" "$node_bin" "$SCRIPT_DIR/bk-request-log-shim.mjs"
    } > "$dir/bk"
    chmod +x "$dir/bk"
}

# run_agent <agent> <rendered_prompt_file> <model> <log_file> -- dispatch to
# the per-agent runner. Every runner shares one contract: stream the run to the
# build log (via tee) and set the caller's SESSION_ID / TRANSCRIPT /
# EVAL_RESULT_FILE variables (bash dynamic scoping: the caller declares them
# `local` before calling). Unknown agents were already skipped in run_entry.
run_agent() {
    local agent="$1"; shift
    case "$agent" in
        claude)       run_claude "$@" ;;
        cursor)       run_cursor "$@" ;;
        cursor-cloud) run_cursor_cloud "$@" ;;
    esac
}

# run_claude <rendered_prompt_file> <model> <log_file>  -- runs the agent,
# streaming its output to the build log (via tee), and sets the caller's
# SESSION_ID / TRANSCRIPT / EVAL_RESULT_FILE variables (bash dynamic scoping: the
# caller declares them `local` before calling). $ENTRY_ID is also read from the
# caller's scope to namespace the parser annotations.
# Returns non-zero when the agent pipeline fails. The pipeline must be checked
# explicitly: errexit is dead in here (run_entry is invoked on the left of `||`,
# which disables errexit throughout the call tree), and a function's return
# status is otherwise just its LAST command's — a dead agent would fall through
# the pointer extraction and read as success.
run_claude() {
    local prompt_file="$1" model="$2" log="$3"
    local args=(
        --output-format stream-json --verbose --model "$model"
    )
    # Permission posture (see the block near the top): CI adds nothing —
    # claude.sh bypasses; local either bypasses (opt-in) or gets an allowlist
    # that must include the MCP server's tools (cli mode: the bk CLI instead).
    if [[ "${RUN_IN_CI:-false}" != "true" ]]; then
        if [[ "$LOCAL_BYPASS_PERMISSION" == "true" ]]; then
            args+=(--permission-mode bypassPermissions)
        elif [[ "${EVAL_MODE:-mcp}" == "cli" ]]; then
            args+=(--allowedTools "Edit" "Bash(go:*)" "Bash(make:*)" "Bash(git:*)" "Bash(bk:*)")
        else
            args+=(--allowedTools "Edit" "Bash(go:*)" "Bash(make:*)" "Bash(git:*)" "mcp__${MCP_SERVER_NAME}")
        fi
    fi
    if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
        # Sandboxed CI execution via claude.sh (owns mcp_in_ci.json, the system
        # prompt, and the parser). The prompt is already rendered, so pass it as a
        # plain prompt file with no KEY=VALUE substitution args. Output streams to
        # the build log; the CLAUDE_* pointers land in $log for recovery below.
        if ! ANNOTATION_CONTEXT_PREFIX="eval-$ENTRY_ID" \
            "$SCRIPT_DIR/claude.sh" "$prompt_file" "${args[@]}" | tee "$log"; then
            return 1
        fi
        SESSION_ID=$(sed -n 's/^CLAUDE_SESSION_ID=//p' "$log" | tail -n1)
        TRANSCRIPT=$(sed -n 's/^CLAUDE_TRANSCRIPT=//p' "$log" | tail -n1)
        EVAL_RESULT_FILE=$(sed -n 's/^CLAUDE_RESULT_FILE=//p' "$log" | tail -n1)
        RAW_STREAM=$(sed -n 's/^CLAUDE_RAW_LOG=//p' "$log" | tail -n1)
    else
        # Local execution: run claude directly against the harness mcp.json (cwd
        # is the eval-repo clone, so a relative path would not resolve).
        # cli mode: an empty MCP config, made --strict so MCP servers from the
        # caller's own Claude settings (e.g. a Buildkite one) can't leak in.
        local mcp_args=(--mcp-config "$ROOT_DIR/mcp.json")
        [[ "${EVAL_MODE:-mcp}" == "cli" ]] && mcp_args=(--mcp-config "$ROOT_DIR/mcp_none.json" --strict-mcp-config)
        if ! claude -p "$(cat "$prompt_file")" "${mcp_args[@]}" "${args[@]}" | tee "$log"; then
            return 1
        fi
        SESSION_ID=$(jq -r 'select(.type == "system" and .subtype == "init") | .session_id' "$log" | head -n1)
        TRANSCRIPT="$HOME/.claude/projects/$(pwd | sed -e 's/[\/.]/-/g')/$SESSION_ID.jsonl"
        # Locally the tee'd log is the raw stream-json itself.
        RAW_STREAM="$log"
        EVAL_RESULT_FILE="$(mktemp)"
        jq -r 'select(.type == "result") | .result // empty' "$log" > "$EVAL_RESULT_FILE" 2>/dev/null || true
    fi
}

# run_cursor <rendered_prompt_file> <model> <log_file> -- the Cursor CLI
# (cursor-agent) analogue of run_claude, same contract. Cursor-specific
# differences (details in scripts/cursor.sh):
#   - auth is CURSOR_API_KEY (no Buildkite Hosted Models path); entries are
#     skipped in run_entry when it is unset
#   - no --mcp-config flag: a .cursor/mcp.json is generated in the working
#     copy (cwd), referencing the entry token as ${env:BUILDKITE_API_TOKEN}
#     (no secret on disk; cursor resolves it at server spawn)
#   - no --append-system-prompt: prompts/system.md is prepended to the prompt
#   - no on-disk transcript: the raw stream-json capture IS the transcript,
#     and it carries no token usage (metrics are partial for cursor runs)
#   - permission posture is --force only; the restricted local allowlist
#     (cursor's .cursor/cli.json permissions) is NOT wired up, so restricted
#     local cursor entries are skipped in run_entry
# Like run_claude, returns non-zero when the agent pipeline fails, and the
# pipeline must be checked explicitly: errexit is dead in here (run_entry is
# invoked on the left of `||`), so an unguarded failure would fall through the
# pointer extraction and read as success.
run_cursor() {
    local prompt_file="$1" model="$2" log="$3"
    local args=( --output-format stream-json --model "$model" )
    if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
        # Sandboxed CI execution via cursor.sh (owns the generated
        # .cursor/mcp.json, the prepended system prompt, and the parser).
        if ! ANNOTATION_CONTEXT_PREFIX="eval-$ENTRY_ID" \
            "$SCRIPT_DIR/cursor.sh" "$prompt_file" "${args[@]}" | tee "$log"; then
            return 1
        fi
        SESSION_ID=$(sed -n 's/^CURSOR_SESSION_ID=//p' "$log" | tail -n1)
        TRANSCRIPT=$(sed -n 's/^CURSOR_TRANSCRIPT=//p' "$log" | tail -n1)
        EVAL_RESULT_FILE=$(sed -n 's/^CURSOR_RESULT_FILE=//p' "$log" | tail -n1)
    else
        # Local execution (LOCAL_BYPASS_PERMISSION=true only; run_entry skips
        # otherwise). Generate the project MCP config in the clone (cwd) from
        # the harness mcp.json, making the server command absolute (cwd is the
        # clone, but don't depend on cursor's relative-path resolution). No
        # secret is materialized: the token is referenced as
        # ${env:BUILDKITE_API_TOKEN} (Cursor's documented mcp.json
        # interpolation syntax), resolved at server-spawn time from this
        # process's environment — run_entry exports the entry token on the
        # run_agent call.
        command -v cursor-agent >/dev/null || {
            echo "ERROR: cursor-agent not found on PATH (install: curl -fsS https://cursor.com/install | bash)" >&2
            return 1
        }
        # cli mode: no project MCP config at all (an empty mcp_none.json).
        local mcp_src="$ROOT_DIR/mcp.json"
        [[ "${EVAL_MODE:-mcp}" == "cli" ]] && mcp_src="$ROOT_DIR/mcp_none.json"
        mkdir -p .cursor
        jq --arg cwd "$PWD" \
            '.mcpServers |= with_entries(
               .value.env.BUILDKITE_API_TOKEN = "${env:BUILDKITE_API_TOKEN}"
               | .value.env.BUILDKITE_REQUEST_LOG = "${env:BUILDKITE_REQUEST_LOG}"
               | .value.command |= (if startswith("./") then $cwd + ltrimstr(".") else . end))' \
            "$mcp_src" > .cursor/mcp.json
        # Diff hygiene, not a security control (the file holds no secret):
        # keep the generated config off the scenario branch the agent commits
        # and pushes to (see cursor.sh for the full rationale). The clone is
        # always a git repo here.
        local git_dir; git_dir="$(git rev-parse --git-dir)"
        mkdir -p "$git_dir/info"
        echo ".cursor/" >> "$git_dir/info/exclude"
        if ! cursor-agent -p "$(cat "$ROOT_DIR/prompts/system.md")

$(cat "$prompt_file")" --force --approve-mcps "${args[@]}" | tee "$log"; then
            return 1
        fi
        SESSION_ID=$(jq -r 'select(.type == "system" and .subtype == "init") | .session_id' "$log" | head -n1)
        # cursor-agent's stdout is pure stream-json locally, so the log IS the
        # transcript.
        TRANSCRIPT="$log"
        EVAL_RESULT_FILE="$(mktemp)"
        jq -r 'select(.type == "result") | .result // empty' "$log" > "$EVAL_RESULT_FILE" 2>/dev/null || true
    fi
}

# run_cursor_cloud <rendered_prompt_file> <model> <log_file> -- the Cursor
# CLOUD agent (the hosted harness behind the Cursor app's Agents window),
# same contract as run_claude/run_cursor. One code path for CI and local: the
# agent executes in Cursor's cloud VM either way, so there is no sandbox /
# permission split here and the local clone (cwd) is only the place scenario
# setup pushed the branch from — the VM makes its own clone at that branch.
# cursor-cloud.sh owns the API launch, SSE transcript capture, and usage
# collection; see its header for the cloud-specific caveats (prepended system
# prompt, MCP server built in the VM from MCP_SRC_SHA, etc.).
# Like the other runners, the pipeline must be checked explicitly: errexit is
# dead in here (run_entry sits on the left of `||`).
run_cursor_cloud() {
    local prompt_file="$1" model="$2" log="$3"
    local args=(--repo "https://github.com/${REPO_SLUG}" --name "eval-${ENTRY_ID}-${DATETIME}")
    [[ -n "$model" ]] && args+=(--model "$model")
    # The scenario branch (when this entry's setup created one) becomes the
    # VM's startingRef, so the agent boots directly on the red branch. Read it
    # from the assembled vars file (run_entry's local, via dynamic scoping);
    # last value wins, matching render_prompt. Entries without one (e.g.
    # analyze-build) start on the repo's default branch.
    local ref=""
    if [[ -n "${VARS_FILE:-}" && -f "${VARS_FILE:-}" ]]; then
        ref="$(awk -F= '$1 == "SCENARIO_BRANCH" { v = substr($0, index($0, "=") + 1) } END { print v }' "$VARS_FILE")"
    fi
    [[ -n "$ref" ]] && args+=(--starting-ref "$ref")
    # Per-entry agent retention: kill_agent_on_complete (run_entry's local,
    # via dynamic scoping; validated there, default true) maps onto
    # cursor-cloud.sh's CURSOR_CLOUD_KEEP_AGENT for THIS invocation only.
    # false = keep the agent (and the entry token in its env) for dashboard
    # postmortems. An ambient CURSOR_CLOUD_KEEP_AGENT=1 still keeps it too —
    # the entry field only ever adds retention, never forces deletion over it.
    local runner_env=()
    [[ "${KILL_AGENT_ON_COMPLETE:-true}" == "false" ]] && runner_env=(CURSOR_CLOUD_KEEP_AGENT=1)
    if ! env ${runner_env[@]+"${runner_env[@]}"} \
        "$SCRIPT_DIR/cursor-cloud.sh" "$prompt_file" "${args[@]}" | tee "$log"; then
        return 1
    fi
    SESSION_ID=$(sed -n 's/^CURSOR_CLOUD_SESSION_ID=//p' "$log" | tail -n1)
    TRANSCRIPT=$(sed -n 's/^CURSOR_CLOUD_TRANSCRIPT=//p' "$log" | tail -n1)
    EVAL_RESULT_FILE=$(sed -n 's/^CURSOR_CLOUD_RESULT_FILE=//p' "$log" | tail -n1)
}

# run_entry <entry_json> -- execute one matrix entry end to end. Best-effort: a
# failure in one entry logs and returns without aborting the rest of the matrix.
run_entry() {
    local entry="$1"
    local ENTRY_ID AGENT MODEL PROMPT_NAME MCP_VERSION SETUP COMPARE_BASE COMPARE_TARGET TOKEN_ENV KILL_AGENT_ON_COMPLETE
    local MODE MODE_PARAMS
    ENTRY_ID=$(jq -r '.id'                    <<<"$entry")
    AGENT=$(jq -r '.agent // "claude"'        <<<"$entry")
    # How the agent reaches Buildkite: 'mcp' (the MCP server under test) or
    # 'cli' (the bk CLI, no MCP server). mode_params is a per-mode hash.
    MODE=$(jq -r '.mode // "mcp"'             <<<"$entry")
    MODE_PARAMS=$(jq -c '.mode_params // {}'  <<<"$entry")
    MODEL=$(jq -r '.model // ""'              <<<"$entry")
    # Model ids are per-agent namespaces (cursor rejects claude-* ids; see
    # `cursor-agent --list-models`), so the default is agent-aware.
    # cursor-cloud stays empty on purpose: the launch request omits the model
    # field entirely and the Cursor account's configured default applies (ids
    # discoverable via GET /v1/models) — pin one in the entry for stable
    # cross-build comparisons.
    if [[ -z "$MODEL" ]]; then
        case "$AGENT" in
            cursor)       MODEL="composer" ;;
            cursor-cloud) ;;
            *)            MODEL="claude-opus-4-8" ;;
        esac
    fi
    PROMPT_NAME=$(jq -r '.prompt'             <<<"$entry")
    MCP_VERSION=$(jq -r '.mcp_version // "source"' <<<"$entry")
    SETUP=$(jq -r '.scenario.setup // ""'     <<<"$entry")
    COMPARE_BASE=$(jq -r '.compare_base // ""'   <<<"$entry")
    COMPARE_TARGET=$(jq -r '.compare_target // ""' <<<"$entry")
    TOKEN_ENV=$(jq -r '.token_env // ""'      <<<"$entry")
    # jq's `//` treats false as absent, so an explicit `false` must be read
    # with has() rather than defaulted over.
    KILL_AGENT_ON_COMPLETE=$(jq -r 'if has("kill_agent_on_complete") then (.kill_agent_on_complete | tostring) else "true" end' <<<"$entry")

    echo "+++ :robot_face: Eval entry: $ENTRY_ID"

    case "$AGENT" in
        claude) ;;
        cursor)
            # Cursor preflight: skip (loudly, like token_env below) rather than
            # fail mid-run — the failure modes are all environmental.
            if [[ -z "${CURSOR_API_KEY:-}" ]]; then
                echo "WARNING: entry '$ENTRY_ID' uses agent 'cursor' but CURSOR_API_KEY is unset (see .buildkite/pipeline.evals.yml); skipping." >&2
                return 0
            fi
            if [[ "${RUN_IN_CI:-false}" != "true" && "$LOCAL_BYPASS_PERMISSION" != "true" ]]; then
                echo "WARNING: entry '$ENTRY_ID': local cursor runs require LOCAL_BYPASS_PERMISSION=true (cursor-agent's restricted allowlist — .cursor/cli.json permissions — is not wired up); skipping." >&2
                return 0
            fi
            ;;
        cursor-cloud)
            # Same auth as cursor, but NO LOCAL_BYPASS_PERMISSION gate: the
            # agent executes in Cursor's cloud VM, never on this host, so the
            # local permission posture is irrelevant. Containment comes from
            # Cursor's sandbox + the --read-only MCP server built in the VM +
            # the read-only entry token (see cursor-cloud.sh).
            if [[ -z "${CURSOR_API_KEY:-}" ]]; then
                echo "WARNING: entry '$ENTRY_ID' uses agent 'cursor-cloud' but CURSOR_API_KEY is unset (see .buildkite/pipeline.evals.yml); skipping." >&2
                return 0
            fi
            if [[ "$KILL_AGENT_ON_COMPLETE" != "true" && "$KILL_AGENT_ON_COMPLETE" != "false" ]]; then
                echo "WARNING: entry '$ENTRY_ID' has invalid kill_agent_on_complete '$KILL_AGENT_ON_COMPLETE' (want true or false); skipping." >&2
                return 0
            fi
            ;;
        *)
            echo "WARNING: entry '$ENTRY_ID' uses agent '$AGENT' which is not implemented yet; skipping." >&2
            return 0
            ;;
    esac
    # --- Mode (mcp | cli) + mode_params --------------------------------------
    # Unknown modes / params are config mistakes: skip loudly, like a bad
    # token_env, rather than silently running the wrong setup.
    if [[ "$(jq -r 'type' <<<"$MODE_PARAMS")" != "object" ]]; then
        echo "WARNING: entry '$ENTRY_ID' has mode_params that is not a hash; skipping." >&2
        return 0
    fi
    local KNOWN_PARAMS UNKNOWN_PARAMS
    case "$MODE" in
        mcp) KNOWN_PARAMS='[]' ;;
        cli)
            KNOWN_PARAMS='["download_url","org_slug"]'
            # cursor-cloud runs in Cursor's VM: the bk install below happens
            # on this host and would never reach it.
            if [[ "$AGENT" == "cursor-cloud" ]]; then
                echo "WARNING: entry '$ENTRY_ID': mode 'cli' is not supported for agent 'cursor-cloud' yet; skipping." >&2
                return 0
            fi
            ;;
        *)
            echo "WARNING: entry '$ENTRY_ID' has unknown mode '$MODE' (want mcp or cli); skipping." >&2
            return 0
            ;;
    esac
    UNKNOWN_PARAMS="$(jq -r --argjson known "$KNOWN_PARAMS" 'keys - $known | join(", ")' <<<"$MODE_PARAMS")"
    if [[ -n "$UNKNOWN_PARAMS" ]]; then
        echo "WARNING: entry '$ENTRY_ID' has unknown mode_params for mode '$MODE': $UNKNOWN_PARAMS; skipping." >&2
        return 0
    fi
    if [[ "$MODE" == "cli" && -n "$MCP_VERSION" && "$MCP_VERSION" != "source" ]]; then
        echo "NOTICE: entry '$ENTRY_ID' sets mcp_version '$MCP_VERSION', which mode 'cli' ignores (no MCP server)." >&2
    elif [[ -n "$MCP_VERSION" && "$MCP_VERSION" != "source" ]]; then
        echo "NOTICE: entry '$ENTRY_ID' requests mcp_version '$MCP_VERSION', which is not yet wired into the image build (follow-up); running the in-image 'source' binary." >&2
    fi

    # --- Per-entry API token (token_env) -------------------------------------
    # Scenarios can live in ANY Buildkite org, and API tokens are single-org, so
    # an entry may name (token_env) the env var holding the token for ITS org
    # (convention: MCP_EVAL_FRAMEWORK_<ORG>_ORG_BUILDKITE_API_TOKEN, matching the
    # secret names in .buildkite/pipeline.evals.yml). The value is handed to the
    # agent's MCP server as BUILDKITE_API_TOKEN — the server's fixed interface
    # (see mcp.json / mcp_in_ci.json) — for this entry's run only. Without
    # token_env the ambient BUILDKITE_API_TOKEN is used, as before.
    local ENTRY_TOKEN="${BUILDKITE_API_TOKEN:-}"
    if [[ -n "$TOKEN_ENV" ]]; then
        if [[ ! "$TOKEN_ENV" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]]; then
            echo "WARNING: entry '$ENTRY_ID' has an invalid token_env name '$TOKEN_ENV'; skipping." >&2
            return 0
        fi
        ENTRY_TOKEN="${!TOKEN_ENV:-}"
        if [[ -z "$ENTRY_TOKEN" ]]; then
            echo "WARNING: entry '$ENTRY_ID' requires \$${TOKEN_ENV} (token_env) which is unset or empty; skipping." >&2
            return 0
        fi
    fi

    local RUN_KEY="${ENTRY_ID}-${DATETIME}"
    local RUN_DIR="$RUNS_ROOT/$ENTRY_ID"
    mkdir -p "$RUN_DIR"
    local PREFIX="$RUN_DIR/$RUN_KEY"

    # --- Mode setup + run info (versions) -----------------------------------
    # cli: install/locate bk BEFORE scenario setup, so a bad download_url
    # fails the entry before it pushes anything. The install dir sits outside
    # the eval-repo clone so the agent can't commit the binary.
    local BK_CLI_BIN="" BK_CLI_VERSION="" BK_CLI_ORG=""   # set by prepare_bk_cli
    local MCP_SERVER_VERSION=""
    if [[ "$MODE" == "cli" ]]; then
        echo "--- :terminal: [$ENTRY_ID] bk CLI setup"
        if ! prepare_bk_cli "$HOME/.eval-bk-cli/$RUN_KEY" \
                "$(jq -r '.download_url // ""' <<<"$MODE_PARAMS")" \
                "$(jq -r '.org_slug // ""' <<<"$MODE_PARAMS")" \
                "$ENTRY_TOKEN"; then
            echo "ERROR: [$ENTRY_ID] bk CLI setup failed; entry not run." >&2
            return 1
        fi
    else
        # kong's --version prints the build's version ("dev" for source builds,
        # hence MCP_SOURCE_SHA alongside it).
        MCP_SERVER_VERSION="$("$MCP_SERVER_BIN" --version 2>/dev/null | head -n1 || true)"
    fi
    # API request log (fan-out): one JSON line per Buildkite API request, from
    # the MCP server (--request-log via BUILDKITE_REQUEST_LOG in the MCP
    # configs; tagged with the tool call) and from bk (a logging wrapper first
    # on PATH; tagged with the bk command). In mcp mode bk is wrapped too when
    # it is on PATH, so an MCP run that shells out to bk is still counted.
    # cursor-cloud runs in Cursor's VM, where neither log can be collected.
    local REQUEST_LOG="" BK_SHIM_DIR=""
    if [[ "$AGENT" != "cursor-cloud" ]]; then
        REQUEST_LOG="$PREFIX.api-requests.jsonl"
        : > "$REQUEST_LOG"
        local BK_REAL="$BK_CLI_BIN"
        [[ "$MODE" == "mcp" ]] && BK_REAL="$(command -v bk || true)"
        if [[ -n "$BK_REAL" ]]; then
            BK_SHIM_DIR="$HOME/.eval-bk-cli/$RUN_KEY/shim"
            install_bk_shim "$BK_SHIM_DIR" "$BK_REAL" "$REQUEST_LOG" || BK_SHIM_DIR=""
        fi
    fi
    local HARNESS_VERSION; HARNESS_VERSION="$(harness_version "$AGENT")"
    # One record of what this run used — written to the bundle as
    # <run>.run-info.json, merged into <run>.metrics.json (so the metrics
    # annotation, the comparison and the artifacts all carry it), and
    # summarized in the annotation titles.
    local RUN_INFO
    RUN_INFO="$(jq -n \
        --arg mode "$MODE" --argjson params "$MODE_PARAMS" \
        --arg agent "$AGENT" --arg harness "$HARNESS_VERSION" --arg model "${MODEL:-default}" \
        --arg bk_bin "$BK_CLI_BIN" --arg bk_ver "$BK_CLI_VERSION" --arg bk_org "$BK_CLI_ORG" \
        --arg mcp_req "$MCP_VERSION" --arg mcp_ver "$MCP_SERVER_VERSION" --arg mcp_sha "$MCP_SOURCE_SHA" \
        --arg req_log "$REQUEST_LOG" --arg bk_shim "$BK_SHIM_DIR" \
        '{mode: $mode, mode_params: $params,
          api_request_log: {mcp_server: ($req_log != "" and $mode == "mcp"), bk_cli: ($bk_shim != "")},
          harness: {agent: $agent, version: $harness, model: $model},
          bk_cli: (if $mode == "cli" then
                     {version: $bk_ver, path: $bk_bin, download_url: ($params.download_url // null),
                      org_slug: (if $bk_org == "" then null else $bk_org end)}
                   else null end),
          mcp_server: (if $mode == "mcp" then
                         {requested: $mcp_req, version: $mcp_ver,
                          source_sha: (if $mcp_sha == "" then null else $mcp_sha end)}
                       else null end)}')"
    local RUN_LABEL="mode: $MODE · $AGENT $HARNESS_VERSION"
    [[ "$MODE" == "cli" ]] && RUN_LABEL+=" · bk $BK_CLI_VERSION"
    echo "*** [$ENTRY_ID] run info: $(jq -c . <<<"$RUN_INFO")"

    # --- Scenario setup -----------------------------------------------------
    # setup bash runs in the eval-repo clone (cwd); it may push branches, etc.
    # It exposes values to the prompt by appending KEY=VALUE lines to
    # $SCENARIO_VARS_FILE.
    local SCENARIO_VARS_FILE; SCENARIO_VARS_FILE="$(mktemp)"
    if [[ -n "$SETUP" ]]; then
        echo "--- :gear: [$ENTRY_ID] scenario setup"
        # -euo pipefail: the setup is a fresh child shell, which does NOT
        # inherit this script's errexit — without it, a multi-line setup whose
        # last command succeeds (e.g. the vars-file echo) would mask an earlier
        # failed fetch/checkout/push and exit 0.
        if ! env ENTRY_ID="$ENTRY_ID" DATETIME="$DATETIME" ORG_SLUG="$ORG_SLUG" \
                 PIPELINE_SLUG="$PIPELINE_SLUG" SCENARIO_VARS_FILE="$SCENARIO_VARS_FILE" \
                 bash -euo pipefail -c "$SETUP"; then
            # A failure, not a skip: setup does real work (fetch/checkout/push),
            # so a transient or auth failure must fail the entry, not green it.
            echo "ERROR: [$ENTRY_ID] scenario setup failed; entry not run." >&2
            return 1
        fi
    fi

    # --- Assemble substitution vars: globals + scenario.vars + setup vars ---
    local VARS_FILE; VARS_FILE="$(mktemp)"
    {
        printf 'ENTRY_ID=%s\n' "$ENTRY_ID"
        printf 'DATETIME=%s\n' "$DATETIME"
        printf 'ORG_SLUG=%s\n' "$ORG_SLUG"
        printf 'PIPELINE_SLUG=%s\n' "$PIPELINE_SLUG"
        printf 'WAIT_STATUS=%s\n' "$WAIT_STATUS_STRING"
        printf 'DEBUG_STRING=%s\n' "$DEBUG_STRING"
        jq -r '.scenario.vars // {} | to_entries[] | "\(.key)=\(.value)"' <<<"$entry"
        cat "$SCENARIO_VARS_FILE"
    } > "$VARS_FILE"

    # --- Render the prompt --------------------------------------------------
    local TEMPLATE="$ROOT_DIR/prompts/$PROMPT_NAME.md"
    if [[ ! -f "$TEMPLATE" ]]; then
        echo "WARNING: prompt template not found for '$ENTRY_ID': $TEMPLATE; skipping." >&2
        return 0
    fi
    local RENDERED; RENDERED="$(mktemp)"
    render_prompt "$TEMPLATE" "$VARS_FILE" > "$RENDERED"
    # the agent would silently receive a literal placeholder. Surface it.
    local UNRESOLVED
    UNRESOLVED="$(grep -oE '\{\{\.[A-Za-z_][A-Za-z0-9_]*\}\}' "$RENDERED" | sort -u | tr '\n' ' ' || true)"
    if [[ -n "${UNRESOLVED// }" ]]; then
        echo "WARNING: [$ENTRY_ID] prompt has unresolved placeholder(s): ${UNRESOLVED% }" >&2
    fi
    # Per-mode environment note (prompts/modes/<mode>.md), appended to the
    # user prompt for every agent. An MCP server announces itself through its
    # tool list; a CLI does not, so cli mode tells the agent bk is there.
    local MODE_NOTE="$ROOT_DIR/prompts/modes/$MODE.md"
    if [[ -f "$MODE_NOTE" ]]; then
        local MODE_VARS; MODE_VARS="$(mktemp)"
        {
            printf 'BK_CLI_VERSION=%s\n' "$BK_CLI_VERSION"
            if [[ -n "$BK_CLI_ORG" ]]; then
                printf 'BK_CLI_ORG_NOTE=Its default organization is `%s` (set through `BUILDKITE_ORGANIZATION_SLUG`).\n' "$BK_CLI_ORG"
            else
                printf 'BK_CLI_ORG_NOTE=No default organization is set.\n'
            fi
        } > "$MODE_VARS"
        printf '\n\n' >> "$RENDERED"
        render_prompt "$MODE_NOTE" "$MODE_VARS" >> "$RENDERED"
    fi
    echo "--- :scroll: [$ENTRY_ID] rendered prompt"
    cat "$RENDERED"; echo

    # --- Run the agent ------------------------------------------------------
    local LOG="/tmp/babystand-$RUN_KEY.log"
    local SESSION_ID="" TRANSCRIPT="" EVAL_RESULT_FILE=""   # set by the runner (dynamic scope)
    local RAW_STREAM=""   # claude only: raw stream-json, for model API retries
    local EVAL_START; EVAL_START=$(date +%s)
    # Agent env per mode. mcp: as before (ambient org slug untouched). cli: the
    # entry token reaches bk through BUILDKITE_API_TOKEN (bk reads it before
    # any stored credential), the org slug is the entry's — never the ambient
    # one, which in CI names the org HOSTING the eval — and bk telemetry is
    # off so eval runs don't count as real CLI usage. EVAL_MODE tells the
    # runners (and claude.sh / cursor.sh) to leave the MCP server out.
    local AGENT_PATH="$PATH" AGENT_ORG="${BUILDKITE_ORGANIZATION_SLUG:-}"
    if [[ "$MODE" == "cli" ]]; then
        AGENT_PATH="$(dirname "$BK_CLI_BIN"):$PATH"
        AGENT_ORG="$BK_CLI_ORG"
    fi
    # The logging bk wrapper goes in front of the real bk.
    [[ -n "$BK_SHIM_DIR" ]] && AGENT_PATH="$BK_SHIM_DIR:$AGENT_PATH"
    if ! BUILDKITE_API_TOKEN="$ENTRY_TOKEN" EVAL_MODE="$MODE" PATH="$AGENT_PATH" \
            BUILDKITE_ORGANIZATION_SLUG="$AGENT_ORG" BK_TELEMETRY=false \
            BUILDKITE_REQUEST_LOG="$REQUEST_LOG" \
            run_agent "$AGENT" "$RENDERED" "$MODEL" "$LOG"; then
        echo "ERROR: [$ENTRY_ID] agent run failed; skipping audit/bundle/compare for this entry (log: $LOG)" >&2
        return 1
    fi
    local EVAL_ELAPSED_SECS=$(( $(date +%s) - EVAL_START ))
    local EVAL_ELAPSED; EVAL_ELAPSED=$(fmt_elapsed "$EVAL_ELAPSED_SECS")
    echo "*** [$ENTRY_ID] EVAL ELAPSED: $EVAL_ELAPSED  SESSION_ID: $SESSION_ID"
    echo "*** [$ENTRY_ID] TRANSCRIPT: $TRANSCRIPT"

    # --- Audit: tools + metrics --------------------------------------------
    local AUDIT_TOOLS_FILE AUDIT_METRICS_FILE
    AUDIT_TOOLS_FILE="$(mktemp)"; AUDIT_METRICS_FILE="$(mktemp)"
    # --agent switches the transcript dialect: cursor's stream-json records
    # tool calls as standalone tool_call events, not Claude-style tool_use
    # content blocks, and emits no token usage (metrics come back partial).
    echo "--- :hammer_and_wrench: [$ENTRY_ID] tool calls"
    "$SCRIPT_DIR/bk-tool-audit-v2.sh" --agent "$AGENT" --all "$TRANSCRIPT" | tee "$AUDIT_TOOLS_FILE" || true
    echo "--- :bar_chart: [$ENTRY_ID] session metrics"
    "$SCRIPT_DIR/bk-tool-audit-v2.sh" --agent "$AGENT" metrics "$TRANSCRIPT" | tee "$AUDIT_METRICS_FILE" || true
    # Attach the run info (mode, harness / bk CLI / MCP server versions). An
    # empty or broken metrics file still gets a record holding just run_info.
    local MERGED_METRICS; MERGED_METRICS="$(mktemp)"
    if jq -e 'type == "object"' "$AUDIT_METRICS_FILE" >/dev/null 2>&1; then
        jq --argjson ri "$RUN_INFO" '. + {run_info: $ri}' "$AUDIT_METRICS_FILE" > "$MERGED_METRICS"
    else
        jq -n --argjson ri "$RUN_INFO" '{run_info: $ri}' > "$MERGED_METRICS"
    fi
    mv "$MERGED_METRICS" "$AUDIT_METRICS_FILE"

    # --- API fan-out (requests per MCP tool call / per bk command) ----------
    # From the request log; merged into metrics.json as api_fanout (so the
    # comparison and Datadog see it) and rendered as markdown for the
    # eval-fanout annotation and klaren.
    local FANOUT_JSON="" FANOUT_MD=""
    if [[ -n "$REQUEST_LOG" ]]; then
        echo "--- :satellite_antenna: [$ENTRY_ID] API fan-out"
        FANOUT_JSON="$(mktemp)"; FANOUT_MD="$(mktemp)"
        if "$SCRIPT_DIR/api-fanout.sh" json "$REQUEST_LOG" "$AUDIT_METRICS_FILE" > "$FANOUT_JSON" \
                && "$SCRIPT_DIR/api-fanout.sh" markdown "$FANOUT_JSON" > "$FANOUT_MD"; then
            cat "$FANOUT_MD"
            MERGED_METRICS="$(mktemp)"
            jq --slurpfile f "$FANOUT_JSON" '. + {api_fanout: $f[0]}' "$AUDIT_METRICS_FILE" > "$MERGED_METRICS" \
                && mv "$MERGED_METRICS" "$AUDIT_METRICS_FILE"
        else
            echo "WARNING: [$ENTRY_ID] API fan-out summary failed (request log: $REQUEST_LOG)" >&2
            FANOUT_JSON="" FANOUT_MD=""
        fi
    fi

    # --- Struggles (failed tool calls + patterns) ---------------------------
    # From the transcript: failures by type and tool, likely-harmless ones
    # (grep with no match), failure streaks, repeated failing calls, and
    # retries. Merged into metrics.json as `struggles`; rendered for the
    # eval-struggles annotation and klaren. cursor-cloud is not implemented
    # yet — the audit script warns and the annotation says so.
    echo "--- :face_with_spiral_eyes: [$ENTRY_ID] struggles"
    local STRUGGLES_JSON STRUGGLES_MD
    STRUGGLES_JSON="$(mktemp)"; STRUGGLES_MD="$(mktemp)"
    if "$SCRIPT_DIR/bk-tool-audit-v2.sh" --agent "$AGENT" struggles "$TRANSCRIPT" > "$STRUGGLES_JSON" \
            && "$SCRIPT_DIR/bk-tool-audit-v2.sh" struggles-markdown "$STRUGGLES_JSON" > "$STRUGGLES_MD"; then
        cat "$STRUGGLES_MD"
        MERGED_METRICS="$(mktemp)"
        jq --slurpfile s "$STRUGGLES_JSON" '. + {struggles: $s[0]}' "$AUDIT_METRICS_FILE" > "$MERGED_METRICS" \
            && mv "$MERGED_METRICS" "$AUDIT_METRICS_FILE"
    else
        echo "WARNING: [$ENTRY_ID] struggles summary failed (transcript: $TRANSCRIPT)" >&2
        : > "$STRUGGLES_MD"
    fi

    # Model API retries: the harness retrying its call to the model API (e.g.
    # 504 / 429 from the model gateway) — infrastructure, not the agent. From
    # the raw stream (claude only). Merged into metrics.json as
    # model_api_retries and shown under the struggles annotation.
    local RETRIES_JSON; RETRIES_JSON="$(mktemp)"
    if "$SCRIPT_DIR/bk-tool-audit-v2.sh" --agent "$AGENT" model-retries "$RAW_STREAM" > "$RETRIES_JSON"; then
        { echo; "$SCRIPT_DIR/bk-tool-audit-v2.sh" model-retries-markdown "$RETRIES_JSON"; } >> "$STRUGGLES_MD"
        "$SCRIPT_DIR/bk-tool-audit-v2.sh" model-retries-markdown "$RETRIES_JSON"
        MERGED_METRICS="$(mktemp)"
        jq --slurpfile r "$RETRIES_JSON" '. + {model_api_retries: $r[0]}' "$AUDIT_METRICS_FILE" > "$MERGED_METRICS" \
            && mv "$MERGED_METRICS" "$AUDIT_METRICS_FILE"
    else
        echo "WARNING: [$ENTRY_ID] model API retry summary failed (raw stream: ${RAW_STREAM:-none})" >&2
        RETRIES_JSON=""
    fi

    # --- Goal outcome ---------------------------------------------------------
    # Scenario-shaped: entries whose setup pushed a scenario branch
    # (SCENARIO_BRANCH in the vars file; last value wins, matching
    # render_prompt) achieved their goal when that branch's LATEST build in the
    # eval org is green — the red->green contract. Entries without a branch
    # (e.g. analyze-build) have no machine-checkable goal and stay "unknown";
    # so does an API failure or a branch with no builds, so a Datadog gap
    # never miscounts as a miss.
    local GOAL_ACHIEVED="unknown" SCENARIO_BRANCH_VAL="" FINAL_BUILD_STATE=""
    SCENARIO_BRANCH_VAL="$(awk -F= '$1 == "SCENARIO_BRANCH" { v = substr($0, index($0, "=") + 1) } END { print v }' "$VARS_FILE")"
    if [[ -n "$SCENARIO_BRANCH_VAL" ]]; then
        FINAL_BUILD_STATE="$(curl -fsS --max-time 30 \
            -H "Authorization: Bearer $ENTRY_TOKEN" \
            "https://api.buildkite.com/v2/organizations/$ORG_SLUG/pipelines/$PIPELINE_SLUG/builds?branch=$SCENARIO_BRANCH_VAL&per_page=1" \
            | jq -r '.[0].state // "none"' || echo "unknown")"
        case "$FINAL_BUILD_STATE" in
            passed)       GOAL_ACHIEVED="true" ;;
            unknown|none) GOAL_ACHIEVED="unknown" ;;
            *)            GOAL_ACHIEVED="false" ;;
        esac
        echo "*** [$ENTRY_ID] scenario branch '$SCENARIO_BRANCH_VAL' latest build state: $FINAL_BUILD_STATE (goal_achieved: $GOAL_ACHIEVED)"
    fi

    # --- Datadog run metadata ------------------------------------------------
    # SECURITY: DD_API_KEY must never exist in this process or any of its
    # children while the evaluated agent runs (see the header note). This
    # section only RECORDS the entry's identity + outcome as <run>.dd.json in
    # the bundle; CI's dd-publish pipeline step submits it later from a
    # separate process. Local runs never publish — they are for debugging
    # prompts/scenarios. Klaren's own runtime is deliberately excluded:
    # EVAL_DURATION_SECONDS measures the agent under test, not the judge.
    echo "--- :datadog: [$ENTRY_ID] datadog metrics"
    # NOTE: $end_ts, not $end — `end` is a jq reserved keyword and jq 1.6
    # (this container's Ubuntu jq) rejects it as a variable name; a compile
    # error here would leave a 0-byte dd.json and the entry would publish as
    # entry:unknown. Fail loudly instead of trusting errexit: run_entry is
    # called in a conditional context, where set -e is suspended.
    if ! jq -n \
        --arg entry    "$ENTRY_ID" \
        --arg prompt   "$PROMPT_NAME" \
        --arg agent    "$AGENT" \
        --arg model    "${MODEL:-default}" \
        --arg goal     "$GOAL_ACHIEVED" \
        --arg duration "$EVAL_ELAPSED_SECS" \
        --argjson end_ts "$(date +%s)" \
        --arg mode     "$MODE" \
        --arg harness  "$HARNESS_VERSION" \
        --arg bk_cli   "$BK_CLI_VERSION" \
        --arg mcp_ver  "$MCP_SERVER_VERSION" \
        '{entry: $entry, prompt: $prompt, agent: $agent, model: $model,
          goal: $goal, duration_seconds: $duration, end_ts: $end_ts,
          mode: $mode, harness_version: $harness, bk_cli_version: $bk_cli,
          mcp_server_version: $mcp_ver}' \
        > "$PREFIX.dd.json"; then
        echo "WARNING: [$ENTRY_ID] failed to write $PREFIX.dd.json; Datadog publish will skip this entry" >&2
    fi
    echo "*** [$ENTRY_ID] run metadata recorded: $PREFIX.dd.json (published by CI's dd-publish step; local runs never publish)"

    # --- Klaren review (best-effort) ---------------------------------------
    # Klaren always runs on claude regardless of the entry's agent: it is the
    # judge, not the subject under test, and holding the reviewer fixed keeps
    # reviews comparable across agents. It only reads the transcript file, so
    # cursor's stream-json capture works as input too.
    # Use the harness copy of the prompt, not the checked-out subject-under-test
    # copy.
    echo "--- :female-detective: [$ENTRY_ID] klaren review"
    local KLAREN_LOG="/tmp/klaren-$RUN_KEY.log"
    local KLAREN_PROMPT_FILE KLAREN_RESULT_FILE=""
    KLAREN_PROMPT_FILE="$(mktemp)"
    {
        cat "$ROOT_DIR/prompts/klaren.md"
        echo
        echo "The LLM session log file to analyze is: $TRANSCRIPT"
        echo
        if [[ "$MODE" == "cli" ]]; then
            echo "Run context: the agent had NO Buildkite MCP server; it reached Buildkite through the Buildkite CLI \`bk\` (version $BK_CLI_VERSION) via Bash. Judge the bk commands, flags and output as the tools under review."
        else
            echo "Run context: the agent reached Buildkite through the Buildkite MCP server (mode: mcp)."
        fi
        if [[ -s "$FANOUT_MD" ]]; then
            echo
            echo "Buildkite API requests behind each tool call / bk command (fan-out). Use it to quantify waste. The raw request log, one JSON line per API request tagged with the tool call or bk command that made it, is: $REQUEST_LOG"
            echo
            cat "$FANOUT_MD"
        fi
        if [[ -s "$STRUGGLES_MD" ]]; then
            echo
            echo "Failed tool calls and struggle patterns, from the session log (\"likely harmless\" = e.g. grep with no match). Use them as leads, and check each in the log:"
            echo
            cat "$STRUGGLES_MD"
        fi
    } > "$KLAREN_PROMPT_FILE"
    # klaren reads the transcript and inspects the repo; it needs read/search
    # tools. Same permission posture as the eval run above: CI bypasses via
    # claude.sh; local bypasses only when LOCAL_BYPASS_PERMISSION=true, else an
    # allowlist (no MCP entry — klaren analyzes the transcript file, not
    # Buildkite).
    local KLAREN_ARGS=( --output-format stream-json --verbose )
    if [[ "${RUN_IN_CI:-false}" != "true" ]]; then
        if [[ "$LOCAL_BYPASS_PERMISSION" == "true" ]]; then
            KLAREN_ARGS+=(--permission-mode bypassPermissions)
        else
            KLAREN_ARGS+=(--allowedTools "Read" "Grep" "Glob" "Bash")
        fi
    fi
    local KLAREN_START; KLAREN_START=$(date +%s)
    if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
        if ! ANNOTATION_CONTEXT_PREFIX="klaren-$ENTRY_ID" \
            "$SCRIPT_DIR/claude.sh" "$KLAREN_PROMPT_FILE" "${KLAREN_ARGS[@]}" | tee "$KLAREN_LOG"; then
            echo "WARNING: klaren review failed for '$ENTRY_ID'" >&2
        fi
        KLAREN_RESULT_FILE=$(sed -n 's/^CLAUDE_RESULT_FILE=//p' "$KLAREN_LOG" | tail -n1)
    else
        if ! claude -p "$(cat "$KLAREN_PROMPT_FILE")" --mcp-config "$ROOT_DIR/mcp.json" "${KLAREN_ARGS[@]}" | tee "$KLAREN_LOG"; then
            echo "WARNING: klaren review failed for '$ENTRY_ID'" >&2
        fi
        KLAREN_RESULT_FILE="$(mktemp)"
        jq -r 'select(.type == "result") | .result // empty' "$KLAREN_LOG" > "$KLAREN_RESULT_FILE" 2>/dev/null || true
    fi
    local KLAREN_ELAPSED; KLAREN_ELAPSED=$(fmt_elapsed $(( $(date +%s) - KLAREN_START )))
    echo "*** [$ENTRY_ID] KLAREN ELAPSED: $KLAREN_ELAPSED"

    # --- Write the run bundle to <harness>/runs/<id>/<id>-<datetime>.<ext> ---
    [[ -s "$EVAL_RESULT_FILE"   ]] && cp "$EVAL_RESULT_FILE"   "$PREFIX.eval-final.md"     || : > "$PREFIX.eval-final.md"
    [[ -s "$AUDIT_METRICS_FILE" ]] && cp "$AUDIT_METRICS_FILE" "$PREFIX.metrics.json"      || : > "$PREFIX.metrics.json"
    [[ -s "$AUDIT_TOOLS_FILE"   ]] && cp "$AUDIT_TOOLS_FILE"   "$PREFIX.tools.txt"         || : > "$PREFIX.tools.txt"
    [[ -s "$KLAREN_RESULT_FILE" ]] && cp "$KLAREN_RESULT_FILE" "$PREFIX.klaren.md"         || : > "$PREFIX.klaren.md"
    [[ -s "$TRANSCRIPT"         ]] && cp "$TRANSCRIPT"         "$PREFIX.transcript.jsonl"  || true
    jq . <<<"$RUN_INFO" > "$PREFIX.run-info.json"
    if [[ -s "$FANOUT_MD" ]]; then
        cp "$FANOUT_MD" "$PREFIX.api-fanout.md"
    elif [[ -z "$REQUEST_LOG" ]]; then
        echo "WARNING: [$ENTRY_ID] API fan-out is not implemented for agent '$AGENT' yet (no request log)." >&2
        printf '> ⚠️ **API fan-out not implemented for agent `%s` yet** — its MCP server and any bk calls run in its own VM, where the request log cannot be collected.\n' "$AGENT" > "$PREFIX.api-fanout.md"
    fi
    [[ -s "$STRUGGLES_MD" ]] && cp "$STRUGGLES_MD" "$PREFIX.struggles.md"
    echo "*** [$ENTRY_ID] run bundle: $PREFIX.*"

    # --- CI: upload the bundle as build artifacts (dual store) + annotate ---
    if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
        # runs/ lives under the harness root, not cwd (the clone); upload from
        # there so artifact names keep the runs/<id>/... form that
        # bk-eval-compare.sh downloads.
        (cd "$ROOT_DIR" && buildkite-agent artifact upload "runs/$ENTRY_ID/$RUN_KEY.*") \
            || echo "WARNING: failed to upload run bundle for '$ENTRY_ID'" >&2

        annotate_markdown "eval-final-$ENTRY_ID"  ":robot_face: [$ENTRY_ID] Eval — final output" \
            "$PREFIX.eval-final.md" "⏱️ Elapsed: ${EVAL_ELAPSED} · ${RUN_LABEL}" "success"
        annotate_codeblock "eval-metrics-$ENTRY_ID" ":bar_chart: [$ENTRY_ID] Eval — session metrics (${RUN_LABEL})" "$PREFIX.metrics.json"
        annotate_codeblock "eval-tools-$ENTRY_ID"   ":hammer_and_wrench: [$ENTRY_ID] Eval — tool calls (${RUN_LABEL})" "$PREFIX.tools.txt"
        if [[ -s "$PREFIX.api-fanout.md" && -n "$FANOUT_JSON" ]]; then
            annotate_markdown "eval-fanout-$ENTRY_ID" ":satellite_antenna: [$ENTRY_ID] Eval — API fan-out" \
                "$PREFIX.api-fanout.md" "$(jq -r '"\(.total_requests) API requests"' "$FANOUT_JSON") · ${RUN_LABEL}"
        elif [[ -s "$PREFIX.api-fanout.md" ]]; then
            annotate_markdown "eval-fanout-$ENTRY_ID" ":satellite_antenna: [$ENTRY_ID] Eval — API fan-out" \
                "$PREFIX.api-fanout.md" "not implemented for $AGENT" "warning"
        fi
        if [[ -s "$PREFIX.struggles.md" ]]; then
            local STRUGGLES_META STRUGGLES_STYLE="info"
            STRUGGLES_META="$(jq -r 'if .implemented then "\(.failed_calls) failed of \(.tool_calls) tool calls · longest streak \(.longest_failure_streak)" else "not implemented for this agent" end' "$STRUGGLES_JSON")"
            jq -e '.implemented | not' "$STRUGGLES_JSON" >/dev/null 2>&1 && STRUGGLES_STYLE="warning"
            if [[ -n "$RETRIES_JSON" ]] && jq -e '(.retries // 0) > 0' "$RETRIES_JSON" >/dev/null 2>&1; then
                STRUGGLES_META+="$(jq -r '" · \(.retries) model API retries"' "$RETRIES_JSON")"
            fi
            annotate_markdown "eval-struggles-$ENTRY_ID" ":face_with_spiral_eyes: [$ENTRY_ID] Eval — struggles" \
                "$PREFIX.struggles.md" "${STRUGGLES_META} · ${RUN_LABEL}" "$STRUGGLES_STYLE"
        fi
        annotate_markdown "klaren-final-$ENTRY_ID"   ":female-detective: [$ENTRY_ID] Klaren — session review" \
            "$PREFIX.klaren.md" "⏱️ Elapsed: ${KLAREN_ELAPSED}"
    fi

    # --- Compare against the baseline (best-effort) -------------------------
    echo "--- :scales: [$ENTRY_ID] comparison"
    ENTRY_ID="$ENTRY_ID" \
    RUN_KEY="$RUN_KEY" \
    RUN_DIR="$RUN_DIR" \
    CUR_EVAL="$PREFIX.eval-final.md" \
    CUR_METRICS="$PREFIX.metrics.json" \
    CUR_TOOLS="$PREFIX.tools.txt" \
    CUR_KLAREN="$PREFIX.klaren.md" \
    COMPARE_BASE="$COMPARE_BASE" \
    COMPARE_TARGET="$COMPARE_TARGET" \
        "$SCRIPT_DIR/bk-eval-compare.sh" \
        || echo "WARNING: comparison failed for '$ENTRY_ID'" >&2
}

# --- Drive the matrix -------------------------------------------------------
MATRIX_JSON="$(yq -o=json '.matrix' "$EVALS_CONFIG")"
ENTRY_COUNT="$(jq 'length' <<<"$MATRIX_JSON")"
echo "*** Matrix entries: $ENTRY_COUNT"
[[ "$ENTRY_COUNT" -gt 0 ]] || { echo "babystand: matrix is empty; nothing to run." >&2; exit 0; }

# --- Validate entry ids before running anything -----------------------------
# ids namespace run artifacts (runs/<id>/) and per-entry annotations
# (eval-final-<id>, ...), and key each entry to its counterpart in a prior build
# for comparison. A missing id deserializes to the literal string "null"; a
# duplicate id makes two entries clobber the same bundle and annotations. Both
# silently corrupt results, so fail loudly here rather than mid-run.
ALL_IDS="$(jq -r '.[].id // ""' <<<"$MATRIX_JSON")"
# Missing id → jq null → coalesced to "" above; empty string id also counts.
# Detected via jq rather than a grep empty-alternation, which BSD grep rejects.
MISSING_COUNT="$(jq '[.[] | select((.id // "") == "")] | length' <<<"$MATRIX_JSON")"
if [[ "$MISSING_COUNT" -gt 0 ]]; then
    echo "babystand: every matrix entry needs a non-empty 'id' ($MISSING_COUNT missing)." >&2
    exit 1
fi
DUP_IDS="$(printf '%s\n' "$ALL_IDS" | sort | uniq -d | tr '\n' ' ' || true)"
if [[ -n "${DUP_IDS// }" ]]; then
    echo "babystand: duplicate matrix entry id(s): ${DUP_IDS% }" >&2
    echo "  ids must be unique — they namespace runs/<id>/ and eval-<id> annotations." >&2
    exit 1
fi
BAD_IDS="$(printf '%s\n' "$ALL_IDS" | grep -vxE '[A-Za-z0-9._-]+' | tr '\n' ' ' || true)"
if [[ -n "${BAD_IDS// }" ]]; then
    echo "babystand: invalid matrix entry id(s): ${BAD_IDS% }" >&2
    echo "  ids may contain only letters, digits, dot, underscore and hyphen (they appear in paths and annotation names)." >&2
    exit 1
fi
# "." and ".." satisfy the charset above but are path components: runs/.. is
# the harness root, so a bundle would land outside the gitignored runs/ tree.
DOT_IDS="$(printf '%s\n' "$ALL_IDS" | grep -Fx -e . -e .. | tr '\n' ' ' || true)"
if [[ -n "${DOT_IDS// }" ]]; then
    echo "babystand: matrix entry id(s) '.' and '..' are not allowed (runs/<id> must stay inside runs/)." >&2
    exit 1
fi

FAILED_ENTRIES=0
for i in $(seq 0 $((ENTRY_COUNT - 1))); do
    entry="$(jq -c ".[$i]" <<<"$MATRIX_JSON")"
    # Never let one entry abort the rest of the matrix — but count failures so
    # the build still goes red at the end.
    run_entry "$entry" || { FAILED_ENTRIES=$((FAILED_ENTRIES + 1)); echo "WARNING: entry index $i failed" >&2; }
done

if [[ "$FAILED_ENTRIES" -gt 0 ]]; then
    echo "babystand: $FAILED_ENTRIES of $ENTRY_COUNT matrix entries failed" >&2
    exit 1
fi
