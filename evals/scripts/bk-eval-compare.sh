#!/usr/bin/env bash
#
# bk-eval-compare.sh — compare one eval matrix entry's run against a baseline,
# and publish the comparison as a per-entry Buildkite annotation.
#
# It is invoked per entry by babystand.sh with the current run's bundle files and
# the entry's optional compare_base / compare_target overrides.
#
# A "comparison source" is either:
#   * a Buildkite build   — its uploaded run bundle (runs/<id>/*) is downloaded
#   * a local path        — a runs/<id>/ dir (its first run is picked) or a
#                           runs/<id>/<id>-<datetime> run prefix (that exact
#                           run). Relative paths resolve against the harness
#                           (evals/) root, NOT the cwd — babystand.sh runs this
#                           from inside the per-invocation eval-repo clone,
#                           while bundles live under <harness>/runs/.
# Each resolves to a bundle dir or run prefix holding <run>.eval-final.md,
# <run>.metrics.json, <run>.klaren.md (and .tools.txt / .transcript.jsonl).
#
# ENV (set by babystand.sh):
#   ENTRY_ID        (required) matrix entry id; namespaces artifacts + annotation
#   RUN_KEY         <id>-<datetime> of the current run (labeling only)
#   RUN_DIR         ./runs/<id> for the current run
#   CUR_EVAL/CUR_METRICS/CUR_TOOLS/CUR_KLAREN   current run's bundle files (target default)
#   COMPARE_BASE    "" (default: last successful `main` build for ENTRY_ID),
#                   or build:<n> / <n> / <url> / local:<path> / <path>
#   COMPARE_TARGET  "" (default: this run), or same forms as COMPARE_BASE
# Buildkite context (org/pipeline/token/build url) comes from the standard env.
#
# Comparisons of metrics are deterministic (jq); the free-form Eval Summary and
# Klaren sections are compared semantically via claude (through claude.sh in CI).
#
# NEVER exits non-zero: a comparison failure must not fail an eval that completed.

set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"

ORG="${BUILDKITE_ORGANIZATION_SLUG:-}"
PIPELINE="${BUILDKITE_PIPELINE_SLUG:-}"
# This script queries the org hosting the eval build, which is a different
# org from the one the agent's MCP server watches (BUILDKITE_API_TOKEN).
# Buildkite API tokens are single-org, so a dedicated token is required;
# fall back to BUILDKITE_API_TOKEN for single-org (local/fork) setups.
TOKEN="${EVAL_COMPARE_BUILDKITE_API_TOKEN:-${BUILDKITE_API_TOKEN:-}}"
CUR_BUILD_NUM="${BUILDKITE_BUILD_NUMBER:-}"
[[ -z "$CUR_BUILD_NUM" && -n "${BUILDKITE_BUILD_URL:-}" ]] && CUR_BUILD_NUM="${BUILDKITE_BUILD_URL##*/}"

api="https://api.buildkite.com/v2/organizations/$ORG/pipelines/$PIPELINE/builds"

# ---------------------------------------------------------------------------
# pick SRC SUFFIX  -> the bundle file for SUFFIX (e.g. metrics.json). SRC is
# either a bundle directory (first *.SUFFIX file in it) or a run prefix like
# .../runs/<id>/<id>-<datetime> (that exact run's file, never a sibling run's).
pick() {
    if [[ -d "$1" ]]; then
        ls "$1"/*."$2" 2>/dev/null | head -n1
    elif [[ -f "$1.$2" ]]; then
        echo "$1.$2"
    fi
}

# resolve_local PATH -> a bundle dir or run prefix for a local source spec, or
# fail if neither exists. Relative paths resolve against the harness (evals/)
# root: babystand.sh invokes this script from inside the per-invocation
# eval-repo clone, while bundles live under <harness>/runs/. A path that is not
# a directory is treated as a run prefix and must own at least one bundle file.
resolve_local() {
    local p="$1"
    [[ "$p" == /* ]] || p="$ROOT_DIR/${p#./}"
    if [[ -d "$p" ]]; then
        echo "$p"
    elif ls "$p".* >/dev/null 2>&1; then
        echo "$p"
    else
        return 1
    fi
}

# build_id_from_number N -> the build UUID (needed for artifact download)
build_id_from_number() {
    [[ -n "$TOKEN" ]] || return 1
    curl -fsS -H "Authorization: Bearer $TOKEN" "$api/$1" 2>/dev/null \
        | jq -r '.id // empty' 2>/dev/null
}

# download_build_bundle BUILD_ID ENTRY_ID -> dir containing the bundle (or fail)
download_build_bundle() {
    local id="$1" eid="$2" d
    command -v buildkite-agent >/dev/null 2>&1 || return 1
    d="$(mktemp -d)"
    buildkite-agent artifact download "runs/$eid/*" "$d" --build "$id" 2>/dev/null || return 1
    [[ -d "$d/runs/$eid" ]] || return 1
    echo "$d/runs/$eid"
}

# resolve_src SPEC ENTRY_ID -> a bundle dir or run prefix for an explicit
# source spec (both forms are understood by pick above). Returns non-zero for
# empty/default sentinels and unresolvable specs so the caller can react.
resolve_src() {
    local spec="$1" eid="$2" ref id
    case "$spec" in
        ''|last-successful-main) return 1 ;;
        local:*) resolve_local "${spec#local:}" ;;
        build:*) ref="${spec#build:}"
                 if [[ "$ref" =~ ^[0-9]+$ ]]; then id="$(build_id_from_number "$ref")"; else id="$ref"; fi
                 [[ -n "$id" ]] && download_build_bundle "$id" "$eid" ;;
        http*://*) ref="${spec##*/}"; id="$(build_id_from_number "$ref")"
                   [[ -n "$id" ]] && download_build_bundle "$id" "$eid" ;;
        /*|./*|../*) resolve_local "$spec" ;;
        *) if [[ "$spec" =~ ^[0-9]+$ ]]; then
               id="$(build_id_from_number "$spec")"; [[ -n "$id" ]] && download_build_bundle "$id" "$eid"
           else resolve_local "$spec"; fi ;;
    esac
}

# metrics_table BASE_JSON CUR_JSON BASE_LABEL CUR_LABEL  -> markdown table
metrics_table() {
    jq -rn --slurpfile b "$1" --slurpfile c "$2" --arg bl "$3" --arg cl "$4" '
        ($b[0] // {}) as $B | ($c[0] // {}) as $C |
        def num(x): (x // 0);
        def rnd(x): ((x*100|round)/100);
        def sign(x): (rnd(x)) as $y | (if $y > 0 then "+\($y)" elif $y < 0 then "\($y)" else "0" end);
        def r(lbl; bv; cv): "| \(lbl) | \(num(bv)) | \(num(cv)) | \(sign(num(cv)-num(bv))) |";
        [
          "| Metric | Base (\($bl)) | Target (\($cl)) | Δ |",
          "|---|---:|---:|---:|",
          r("Input tokens";        $B.tokens.input;          $C.tokens.input),
          r("Output tokens";       $B.tokens.output;         $C.tokens.output),
          r("Cache write (5m)";    $B.tokens.cache_write_5m; $C.tokens.cache_write_5m),
          r("Cache write (1h)";    $B.tokens.cache_write_1h; $C.tokens.cache_write_1h),
          r("Cache read";          $B.tokens.cache_read;     $C.tokens.cache_read),
          r("Tool calls (total)";  $B.tool_calls_total;      $C.tool_calls_total),
          r("Assistant responses"; $B.assistant_responses;   $C.assistant_responses),
          r("User messages";       $B.user_messages;         $C.user_messages),
          r("Duration (min)";      $B.duration_min;          $C.duration_min),
          r("Est. cost (USD)";     $B.est_cost_usd;          $C.est_cost_usd)
        ] | .[]'
}

# tool_table BASE_JSON CUR_JSON  -> per-tool call-count comparison table
tool_table() {
    jq -rn --slurpfile b "$1" --slurpfile c "$2" '
        def tomap(a): ((a // [])
          | map(capture("^(?<n>[0-9]+) (?<name>.+)$"))
          | map({(.name): (.n|tonumber)}) | add // {});
        (tomap($b[0].tool_calls_by_name)) as $B |
        (tomap($c[0].tool_calls_by_name)) as $C |
        (($B|keys) + ($C|keys) | unique) as $names |
        (["| Tool | Base | Target | Δ |", "|---|---:|---:|---:|"]
         + ($names | map(. as $n
             | (($B[$n]) // 0) as $bv | (($C[$n]) // 0) as $cv
             | "| `\($n)` | \($bv) | \($cv) | \(if ($cv-$bv)>0 then "+\($cv-$bv)" else "\($cv-$bv)" end) |")))
        | .[]'
}

# _sect FILE -- print a file, or a placeholder if empty/missing.
_sect() { if [[ -s "$1" ]]; then cat "$1"; else echo "_(not available)_"; fi; }

# semantic_compare BASE_EVAL CUR_EVAL BASE_KLAREN CUR_KLAREN -> markdown on stdout
semantic_compare() {
    local b_eval="$1" c_eval="$2" b_klaren="$3" c_klaren="$4"
    [[ -s "$c_eval" || -s "$c_klaren" ]] || return 0

    local PROMPT; PROMPT="$(mktemp)"
    {
        cat <<'PROMPT_HEADER'
You are comparing two runs of an automated eval (a "base" and a "target").
Everything you need is inline below — DO NOT use any tools; just analyze the text.

Output ONLY GitHub-flavored markdown (no preamble). Use exactly these sections:

## :robot_face: Eval Summary
### Goal
- State each run's goal and whether it ACHIEVED it (✅/❌ + one-line reason), base vs target.
### Plan Steps
- SIDE-BY-SIDE table: Step | Base | Target.
- Then bullet the notable DIFFERENCES (steps added, dropped, reordered, or done differently).

## :female-detective: Klaren
### Score
Compute a weighted score for EACH run identically:
  - Points per issue by severity: Critical=5, High=4, Medium=3, Low=2.
  - Frequency multiplier = occurrences of the issue in the run (min 1).
  - Issue contribution = severity_points × frequency_multiplier.
  - Final score = sum of contributions. (Higher = worse.)
Show a breakdown table per run: Issue | Severity | Points | Frequency (×) | Contribution.
Then give each run's Final score and the delta (note if target improved, i.e. lower).
If severity/frequency is not explicit, infer the most reasonable value and note the assumption.
### Issues diff
Categorize issues across the two reports (match by substance, not wording):
  - **Resolved (gone):** in base but not target.
  - **New:** in target but not base.
  - **Remaining:** in both (note any severity/frequency change).

Keep it tight and skimmable.
PROMPT_HEADER
        echo; echo "=== BASE — EVAL SUMMARY ===";  _sect "$b_eval"
        echo; echo "=== TARGET — EVAL SUMMARY ==="; _sect "$c_eval"
        echo; echo "=== BASE — KLAREN REPORT ===";  _sect "$b_klaren"
        echo; echo "=== TARGET — KLAREN REPORT ==="; _sect "$c_klaren"
    } > "$PROMPT"

    local LOG; LOG="$(mktemp)"; local RESULT=""
    if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
        if ANNOTATION_CONTEXT_PREFIX="compare-${ENTRY_ID:-}" \
             "$SCRIPT_DIR/claude.sh" "$PROMPT" --output-format stream-json --verbose --allowedTools "Read" >"$LOG" 2>/dev/null; then
            local rf; rf="$(sed -n 's/^CLAUDE_RESULT_FILE=//p' "$LOG" | tail -n1)"
            [[ -s "$rf" ]] && RESULT="$(cat "$rf")"
        fi
    else
        if claude -p "$(cat "$PROMPT")" --output-format stream-json --verbose --allowedTools "Read" >"$LOG" 2>/dev/null; then
            RESULT="$(jq -r 'select(.type=="result") | .result // empty' "$LOG" 2>/dev/null)"
        fi
    fi
    printf '%s' "$RESULT"
}

# annotate_file CONTEXT TITLE FILE
annotate_file() {
    local ctx="$1" title="$2" file="$3"
    command -v buildkite-agent >/dev/null 2>&1 || { echo "--- comparison ($ctx):"; cat "$file"; return 0; }
    {
        printf '<details><summary>%s</summary>\n\n' "$title"
        if [[ -s "$file" ]]; then cat "$file"; else printf '_(empty comparison)_\n'; fi
        printf '\n\n</details>\n'
    } | buildkite-agent annotate --context "$ctx" --style info --priority 10 \
        || echo "bk-eval-compare: WARNING failed to annotate '$ctx'" >&2
}

# ---------------------------------------------------------------------------
main() {
    command -v jq >/dev/null || { echo "bk-eval-compare: jq required, skipping" >&2; return 0; }
    local ENTRY_ID="${ENTRY_ID:-}"
    [[ -n "$ENTRY_ID" ]] || { echo "bk-eval-compare: ENTRY_ID required, skipping" >&2; return 0; }
    local ctx="eval-compare-$ENTRY_ID"
    local title=":scales: [$ENTRY_ID] Eval — comparison"

    # --- Resolve TARGET ------------------------------------------------------
    local TGT_EVAL TGT_METRICS TGT_KLAREN TGT_LABEL
    if [[ -n "${COMPARE_TARGET:-}" ]]; then
        local td; td="$(resolve_src "$COMPARE_TARGET" "$ENTRY_ID")"
        if [[ -z "$td" ]]; then
            annotate_note "$ctx" "$title" "_Could not resolve compare_target \`$COMPARE_TARGET\` for \`$ENTRY_ID\`._"; return 0
        fi
        TGT_EVAL="$(pick "$td" eval-final.md)"; TGT_METRICS="$(pick "$td" metrics.json)"; TGT_KLAREN="$(pick "$td" klaren.md)"
        TGT_LABEL="$COMPARE_TARGET"
    else
        TGT_EVAL="${CUR_EVAL:-}"; TGT_METRICS="${CUR_METRICS:-}"; TGT_KLAREN="${CUR_KLAREN:-}"
        TGT_LABEL="${RUN_KEY:-this run}"
    fi

    # --- Resolve BASE --------------------------------------------------------
    local BASE_EVAL="" BASE_METRICS="" BASE_KLAREN="" BASE_LABEL="" bd=""
    if [[ -n "${COMPARE_BASE:-}" ]]; then
        bd="$(resolve_src "$COMPARE_BASE" "$ENTRY_ID")"
        BASE_LABEL="$COMPARE_BASE"
        if [[ -z "$bd" ]]; then
            annotate_note "$ctx" "$title" "_Could not resolve compare_base \`$COMPARE_BASE\` for \`$ENTRY_ID\`._"; return 0
        fi
    else
        # Default: last successful `main` build for this entry id.
        [[ -n "$ORG" && -n "$PIPELINE" && -n "$TOKEN" ]] || {
            annotate_note "$ctx" "$title" "_No baseline: org/pipeline/token unavailable (not in CI?)._"; return 0; }
        local builds base_id base_num base_web
        builds="$(curl -fsS -H "Authorization: Bearer $TOKEN" "$api?branch=main&state=passed&per_page=10" 2>/dev/null || echo '[]')"
        base_id="$(jq -r --arg c "$CUR_BUILD_NUM" 'map(select((.number|tostring)!=$c)) | .[0].id // empty'      <<<"${builds:-[]}")"
        base_num="$(jq -r --arg c "$CUR_BUILD_NUM" 'map(select((.number|tostring)!=$c)) | .[0].number // empty' <<<"${builds:-[]}")"
        base_web="$(jq -r --arg c "$CUR_BUILD_NUM" 'map(select((.number|tostring)!=$c)) | .[0].web_url // empty' <<<"${builds:-[]}")"
        if [[ -z "$base_id" ]]; then
            annotate_note "$ctx" "$title" "_No prior passing \`main\` build to compare \`$ENTRY_ID\` against._"; return 0
        fi
        BASE_LABEL="main #$base_num"
        bd="$(download_build_bundle "$base_id" "$ENTRY_ID")"
        if [[ -z "$bd" || ! -d "$bd" ]]; then
            annotate_note "$ctx" "$title" "_Baseline build [#$base_num](${base_web:-#}) has no \`runs/$ENTRY_ID\` bundle yet (predates this entry) — nothing to compare._"; return 0
        fi
    fi
    [[ -n "$BASE_EVAL"    ]] || BASE_EVAL="$(pick "$bd" eval-final.md)"
    [[ -n "$BASE_METRICS" ]] || BASE_METRICS="$(pick "$bd" metrics.json)"
    [[ -n "$BASE_KLAREN"  ]] || BASE_KLAREN="$(pick "$bd" klaren.md)"

    # --- Build the report ----------------------------------------------------
    local REPORT; REPORT="$(mktemp)"
    {
        echo "Comparing **target** \`$TGT_LABEL\` against **base** \`$BASE_LABEL\` for entry \`$ENTRY_ID\`."
        echo
        echo "## :bar_chart: Metrics"
        echo
        if [[ -s "$BASE_METRICS" && -s "$TGT_METRICS" ]]; then
            metrics_table "$BASE_METRICS" "$TGT_METRICS" "$BASE_LABEL" "$TGT_LABEL"
            echo; echo "<details><summary>Per-tool call counts</summary>"; echo
            tool_table "$BASE_METRICS" "$TGT_METRICS"
            echo; echo "</details>"
        else
            echo "_Metrics unavailable (missing base or target metrics)._"
        fi
        echo
        local SEM; SEM="$(semantic_compare "$BASE_EVAL" "$TGT_EVAL" "$BASE_KLAREN" "$TGT_KLAREN")"
        if [[ -n "$SEM" ]]; then echo "$SEM"; else
            echo "## :robot_face: Eval Summary & :female-detective: Klaren"; echo; echo "_Semantic comparison unavailable._"
        fi
    } > "$REPORT"

    annotate_file "$ctx" "$title" "$REPORT"
    echo "--- :scales: [$ENTRY_ID] comparison published"
}

# annotate_note CONTEXT TITLE MARKDOWN -- short collapsible note (or stdout locally)
annotate_note() {
    local ctx="$1" title="$2" body="$3"
    command -v buildkite-agent >/dev/null 2>&1 || { echo "--- comparison ($ctx): $body"; return 0; }
    printf '<details><summary>%s</summary>\n\n%s\n\n</details>\n' "$title" "$body" \
        | buildkite-agent annotate --context "$ctx" --style info --priority 10 \
        || echo "bk-eval-compare: WARNING failed to annotate '$ctx'" >&2
}

main "$@" || echo "bk-eval-compare: WARNING comparison aborted early" >&2
exit 0
