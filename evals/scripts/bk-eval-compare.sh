#!/usr/bin/env bash
#
# bk-eval-compare.sh — compare the CURRENT eval build against the last
# successful build on `main`, and publish the comparison as a Buildkite
# annotation on the current build.
#
# It compares the same signals babystand.sh already surfaces:
#   * Eval Summary  (eval-final)  — Goal (achieved?) and Plan Steps (side-by-side)
#   * Metrics       (eval-metrics)— input/output tokens, cache usage, tool calls,
#                                    duration, cost (side-by-side with deltas)
#   * Klaren report (klaren-final)— weighted score + issue diff (gone/new/remaining)
#
# HOW THE BASELINE IS OBTAINED
#   The current build's raw comparison inputs are uploaded as build artifacts
#   under `eval-compare/`. To compare, we look up the most recent PASSED build on
#   `main` via the Buildkite REST API and download ITS `eval-compare/` artifacts.
#   (So the very first main build after this lands has nothing to baseline
#   against; every run after that does. This is reported gracefully.)
#
# DETERMINISTIC vs SEMANTIC
#   Metrics are numeric, so they are diffed deterministically with jq.
#   Eval Summary + Klaren are free-form prose, so the semantic comparison
#   (goal achievement, plan-step diff, weighted Klaren score, issue diff) is done
#   by `claude` — reusing claude.sh in CI exactly like babystand.sh does for
#   klaren, so Buildkite Hosted Models auth + the parser are handled for us.
#
# USAGE — append to the END of scripts/babystand.sh (best-effort; guarded):
#
#     # --- Compare against last passing main build (best-effort) --------------
#     EVAL_RESULT_FILE="${EVAL_RESULT_FILE:-}" \
#     AUDIT_METRICS_FILE="$AUDIT_METRICS_FILE" \
#     AUDIT_TOOLS_FILE="$AUDIT_TOOLS_FILE" \
#     KLAREN_RESULT_FILE="${KLAREN_RESULT_FILE:-}" \
#       "$SCRIPT_DIR/bk-eval-compare.sh" || echo "WARNING: eval comparison failed" >&2
#
#   The four files are the same ones babystand.sh already produced; they are read
#   from the environment. Everything else (org, pipeline, token, build url,
#   RUN_IN_CI) comes from the standard Buildkite job environment.
#
# This script NEVER exits non-zero: a comparison failure must not fail an eval
# that already completed.

# Deliberately NOT `set -e`: this is post-run, best-effort analysis.
set -uo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# Everything below is wrapped so any unexpected error still lets us `exit 0`.
main() {
  command -v jq >/dev/null || { echo "bk-eval-compare: jq required, skipping" >&2; return 0; }

  # --- Inputs (current build) ---------------------------------------------
  local CUR_EVAL="${EVAL_RESULT_FILE:-}"
  local CUR_METRICS="${AUDIT_METRICS_FILE:-}"
  local CUR_TOOLS="${AUDIT_TOOLS_FILE:-}"
  local CUR_KLAREN="${KLAREN_RESULT_FILE:-}"

  local ORG="${BUILDKITE_ORGANIZATION_SLUG:-}"
  local PIPELINE="${BUILDKITE_PIPELINE_SLUG:-}"
  # This script queries the org hosting the eval build, which is a different
  # org from the one the agent's MCP server watches (BUILDKITE_API_TOKEN).
  # Buildkite API tokens are single-org, so a dedicated token is required;
  # fall back to BUILDKITE_API_TOKEN for single-org (local/fork) setups.
  local TOKEN="${EVAL_COMPARE_BUILDKITE_API_TOKEN:-${BUILDKITE_API_TOKEN:-}}"
  local BUILD_URL="${BUILDKITE_BUILD_URL:-}"
  local CUR_NUM="${BUILDKITE_BUILD_NUMBER:-}"
  # BUILDKITE_BUILD_NUMBER isn't forwarded into the eval container, but the URL
  # (which ends in the build number) is — fall back to parsing it.
  [[ -z "$CUR_NUM" && -n "$BUILD_URL" ]] && CUR_NUM="${BUILD_URL##*/}"

  local IN_CI="false"
  [[ "${RUN_IN_CI:-false}" == "true" ]] && command -v buildkite-agent >/dev/null 2>&1 && IN_CI="true"

  if [[ "$IN_CI" != "true" ]]; then
    echo "bk-eval-compare: not running in CI (no buildkite-agent); nothing to compare." >&2
    return 0
  fi
  if [[ -z "$ORG" || -z "$PIPELINE" || -z "$TOKEN" ]]; then
    echo "bk-eval-compare: missing ORG/PIPELINE/EVAL_COMPARE_BUILDKITE_API_TOKEN; skipping." >&2
    return 0
  fi

  echo "--- :scales: Comparing eval against last passing main build"

  # --- Publish current inputs as artifacts (baseline for future runs) ------
  local STAGE; STAGE="$(mktemp -d)"
  mkdir -p "$STAGE/eval-compare"
  [[ -s "$CUR_EVAL"    ]] && cp "$CUR_EVAL"    "$STAGE/eval-compare/eval-final.md"    2>/dev/null || true
  [[ -s "$CUR_METRICS" ]] && cp "$CUR_METRICS" "$STAGE/eval-compare/eval-metrics.json" 2>/dev/null || true
  [[ -s "$CUR_TOOLS"   ]] && cp "$CUR_TOOLS"   "$STAGE/eval-compare/eval-tools.txt"    2>/dev/null || true
  [[ -s "$CUR_KLAREN"  ]] && cp "$CUR_KLAREN"  "$STAGE/eval-compare/klaren-final.md"   2>/dev/null || true
  ( cd "$STAGE" && buildkite-agent artifact upload "eval-compare/*" ) \
    || echo "bk-eval-compare: WARNING failed to upload comparison artifacts" >&2

  # --- Find the last PASSED build on main (excluding this one) --------------
  local api="https://api.buildkite.com/v2/organizations/$ORG/pipelines/$PIPELINE/builds"
  local builds
  builds="$(curl -fsS -H "Authorization: Bearer $TOKEN" \
      "$api?branch=main&state=passed&per_page=10" 2>/dev/null)" || builds=""

  local BASE_ID BASE_NUM BASE_WEB
  BASE_ID="$(jq -r --arg cur "$CUR_NUM" \
      'map(select((.number|tostring) != $cur)) | .[0].id // empty' <<<"${builds:-[]}" 2>/dev/null)"
  BASE_NUM="$(jq -r --arg cur "$CUR_NUM" \
      'map(select((.number|tostring) != $cur)) | .[0].number // empty' <<<"${builds:-[]}" 2>/dev/null)"
  BASE_WEB="$(jq -r --arg cur "$CUR_NUM" \
      'map(select((.number|tostring) != $cur)) | .[0].web_url // empty' <<<"${builds:-[]}" 2>/dev/null)"

  if [[ -z "$BASE_ID" ]]; then
    annotate "eval-compare" ":scales: Eval — comparison vs last main" \
      "_No prior passing \`main\` build found to compare against._"
    return 0
  fi
  echo "bk-eval-compare: baseline = build #$BASE_NUM ($BASE_ID)"

  # --- Download baseline artifacts -----------------------------------------
  local BASE_DIR; BASE_DIR="$(mktemp -d)"
  buildkite-agent artifact download "eval-compare/*" "$BASE_DIR" --build "$BASE_ID" \
    2>/dev/null || true
  local B_EVAL="$BASE_DIR/eval-compare/eval-final.md"
  local B_METRICS="$BASE_DIR/eval-compare/eval-metrics.json"
  local B_KLAREN="$BASE_DIR/eval-compare/klaren-final.md"

  local base_link="build [#$BASE_NUM](${BASE_WEB:-#})"
  if [[ ! -s "$B_METRICS" && ! -s "$B_EVAL" && ! -s "$B_KLAREN" ]]; then
    annotate "eval-compare" ":scales: Eval — comparison vs last main" \
      "_Baseline $base_link predates comparison artifacts — nothing to compare yet. Future runs will compare against builds produced after this change._"
    return 0
  fi

  # --- Build the report ----------------------------------------------------
  local REPORT; REPORT="$(mktemp)"
  {
    echo "Baseline: last passing **main** $base_link"
    echo
    # ---- Metrics (deterministic) ----
    echo "## :bar_chart: Metrics"
    echo
    if [[ -s "$B_METRICS" && -s "$CUR_METRICS" ]]; then
      metrics_table "$B_METRICS" "$CUR_METRICS" "$BASE_NUM" "$CUR_NUM"
      echo
      echo "<details><summary>Per-tool call counts</summary>"
      echo
      tool_table "$B_METRICS" "$CUR_METRICS"
      echo
      echo "</details>"
    else
      echo "_Metrics unavailable (missing baseline or current metrics)._"
    fi
    echo
    # ---- Eval Summary + Klaren (semantic, via claude) ----
    local SEM; SEM="$(semantic_compare "$B_EVAL" "$CUR_EVAL" "$B_KLAREN" "$CUR_KLAREN")"
    if [[ -n "$SEM" ]]; then
      echo "$SEM"
    else
      echo "## :robot_face: Eval Summary & :female-detective: Klaren"
      echo
      echo "_Semantic comparison unavailable._"
    fi
  } > "$REPORT"

  annotate_file "eval-compare" ":scales: Eval — comparison vs last main" "$REPORT"
  echo "--- :scales: Comparison published (eval-compare annotation)"
}

# ---------------------------------------------------------------------------
# metrics_table BASE_JSON CUR_JSON BASE_NUM CUR_NUM  -> markdown table on stdout
metrics_table() {
  jq -rn \
    --slurpfile b "$1" --slurpfile c "$2" \
    --arg bn "$3" --arg cn "$4" '
    ($b[0] // {}) as $B | ($c[0] // {}) as $C |
    def num(x): (x // 0);
    def rnd(x): ((x*100|round)/100);
    def sign(x): (rnd(x)) as $y | (if $y > 0 then "+\($y)" elif $y < 0 then "\($y)" else "0" end);
    def r(lbl; bv; cv): "| \(lbl) | \(num(bv)) | \(num(cv)) | \(sign(num(cv)-num(bv))) |";
    [
      "| Metric | Baseline #\($bn) | Current #\($cn) | Δ |",
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
  jq -rn \
    --slurpfile b "$1" --slurpfile c "$2" '
    def tomap(a): ((a // [])
      | map(capture("^(?<n>[0-9]+) (?<name>.+)$"))
      | map({(.name): (.n|tonumber)}) | add // {});
    (tomap($b[0].tool_calls_by_name)) as $B |
    (tomap($c[0].tool_calls_by_name)) as $C |
    (($B|keys) + ($C|keys) | unique) as $names |
    (["| Tool | Baseline | Current | Δ |", "|---|---:|---:|---:|"]
     + ($names | map(. as $n
         | (($B[$n]) // 0) as $bv | (($C[$n]) // 0) as $cv
         | "| `\($n)` | \($bv) | \($cv) | \(if ($cv-$bv)>0 then "+\($cv-$bv)" else "\($cv-$bv)" end) |")))
    | .[]'
}

# _sect FILE -- print a file's contents, or a placeholder if empty/missing.
_sect() { if [[ -s "$1" ]]; then cat "$1"; else echo "_(not available)_"; fi; }

# ---------------------------------------------------------------------------
# semantic_compare BASE_EVAL CUR_EVAL BASE_KLAREN CUR_KLAREN -> markdown on stdout
semantic_compare() {
  local b_eval="$1" c_eval="$2" b_klaren="$3" c_klaren="$4"
  # Nothing meaningful to compare semantically.
  [[ -s "$c_eval" || -s "$c_klaren" ]] || return 0

  local PROMPT; PROMPT="$(mktemp)"
  {
    cat <<'PROMPT_HEADER'
You are comparing two runs of an automated CI-fixing eval. Everything you need is
provided inline below — DO NOT use any tools; just analyze the given text.

Output ONLY a GitHub-flavored-markdown report (no preamble, no code fences around
the whole thing). Use exactly these top-level sections and headings:

## :robot_face: Eval Summary
### Goal
- State the goal of each run and whether it ACHIEVED the goal (✅/❌ with a one-line reason), baseline vs current.
### Plan Steps
- Present a SIDE-BY-SIDE comparison of the plan/steps each run took, as a markdown table with columns: Step | Baseline | Current.
- Below the table, bullet the notable DIFFERENCES (steps added, dropped, reordered, or done differently).

## :female-detective: Klaren
### Score
Compute a weighted score for EACH run using this rubric, identically:
  - Points per issue by severity: Critical = 5, High = 4, Medium = 3, Low = 2.
  - Frequency multiplier = how many times the issue manifested in the run (occurrences), minimum 1.
  - Issue contribution = severity_points × frequency_multiplier.
  - Final score = sum of all issue contributions. (Higher score = worse session.)
Show a breakdown table for each run: Issue | Severity | Points | Frequency (×) | Contribution.
Then state the Final score for baseline and current and the delta (and whether current improved, i.e. lower).
If a severity or frequency is not explicit in the report, infer the most reasonable value and note the assumption.
### Issues diff
Categorize issues across the two reports:
  - **Resolved (gone):** in baseline but not current.
  - **New:** in current but not baseline.
  - **Remaining:** present in both (note any change in severity/frequency).
Match issues by their substance, not exact wording.

Keep it tight and skimmable.
PROMPT_HEADER
    echo
    echo "=== BASELINE — EVAL SUMMARY (eval-final) ==="
    _sect "$b_eval"
    echo
    echo "=== CURRENT — EVAL SUMMARY (eval-final) ==="
    _sect "$c_eval"
    echo
    echo "=== BASELINE — KLAREN REPORT (klaren-final) ==="
    _sect "$b_klaren"
    echo
    echo "=== CURRENT — KLAREN REPORT (klaren-final) ==="
    _sect "$c_klaren"
  } > "$PROMPT"

  local LOG; LOG="$(mktemp)"
  local RESULT=""
  if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
    if ANNOTATION_CONTEXT_PREFIX=compare \
         "$SCRIPT_DIR/claude.sh" "$PROMPT" \
         --output-format stream-json --verbose --allowedTools "Read" \
         >"$LOG" 2>/dev/null; then
      local rf; rf="$(sed -n 's/^CLAUDE_RESULT_FILE=//p' "$LOG" | tail -n1)"
      [[ -s "$rf" ]] && RESULT="$(cat "$rf")"
    fi
  else
    if claude -p "$(cat "$PROMPT")" \
         --output-format stream-json --verbose --allowedTools "Read" \
         >"$LOG" 2>/dev/null; then
      RESULT="$(jq -r 'select(.type=="result") | .result // empty' "$LOG" 2>/dev/null)"
    fi
  fi
  printf '%s' "$RESULT"
}

# ---------------------------------------------------------------------------
# annotate CONTEXT TITLE MARKDOWN_STRING   -- collapsible markdown annotation
annotate() {
  local ctx="$1" title="$2" body="$3"
  { printf '<details><summary>%s</summary>\n\n%s\n\n</details>\n' "$title" "$body"; } \
    | buildkite-agent annotate --context "$ctx" --style info --priority 10 \
    || echo "bk-eval-compare: WARNING failed to annotate '$ctx'" >&2
}

# annotate_file CONTEXT TITLE FILE
annotate_file() {
  local ctx="$1" title="$2" file="$3"
  {
    printf '<details><summary>%s</summary>\n\n' "$title"
    if [[ -s "$file" ]]; then cat "$file"; else printf '_(empty comparison)_\n'; fi
    printf '\n\n</details>\n'
  } | buildkite-agent annotate --context "$ctx" --style info --priority 10 \
    || echo "bk-eval-compare: WARNING failed to annotate '$ctx'" >&2
}

main "$@" || echo "bk-eval-compare: WARNING comparison aborted early" >&2
exit 0
