#!/usr/bin/env bash
#
# yilong.sh — the default (cheap) evaluator: grade each eval run with Jev.
#
# For every run whose evaluator is yilong, babystand.sh leaves a
# <run>.judge.json in the run bundle (goal, the scenario branch's diff and
# final build state). This script condenses that, the bundle's eval-final.md
# and tools.txt, and the entry's reference answer (expected/<id>/root_cause.md
# and fix.md, read from this checkout — never part of the agent's container)
# into small evidence records and asks Jev (jev_owner_route) one
# multiple-choice question per rubric entry in prompts/yilong.json:
#   conclusion     bollocks | sorta | yeah-baby
#   investigation  homer-simpson | scooby-doo | sherlock
#   fix            airshot | duct-tape | sweeet
# Verdicts land in <run>.yilong.json (plus an annotation in CI). A question is
# "n/a" when its evidence is missing (e.g. no expected/<id>/ answer) and
# "undetermined" when Jev abstains (it does below 0.8 confidence), is
# unavailable, or errors — never a guess.
#
# Jev's limits shape everything here: a ~8,000-byte vendor request (task,
# candidates and evidence, double-JSON-escaped), no full transcripts, and a
# per-key rate limit of 2 burst / 1 every 6s. So evidence is condensed and
# trimmed to fit, a long final report is first digested by a cheap Claude
# model, and calls are paced.
#
# SECURITY: MCP_EVAL_JEV_KEY must never exist in the eval step, where the agent
# under test has unrestricted Bash. In CI this runs as its own pipeline step
# (see .buildkite/pipeline.evals.yml), from the run-bundle artifacts.
#
# Usage:
#   yilong.sh                        CI: download runs/*/*.judge.json artifacts,
#                                    judge them, upload <run>.yilong.json, annotate
#   yilong.sh <run>.judge.json ...   local: judge these bundles, print the summary
#
# Env:
#   MCP_EVAL_JEV_KEY      Jev bearer token. Unset = loud skip (exit 0).
#   MCP_EVAL_JEV_URL      default https://jev-mcp-bk.fly.dev/mcp
#   YILONG_DRY_RUN=true   build and print each Jev request; call nothing (no key needed)
#   YILONG_DIGEST_OVER    final reports longer than this many bytes are digested (default 2500)
#   YILONG_DIGEST_MODEL   default claude-haiku-4-5. Auth: ANTHROPIC_API_KEY
#                         (+ optional ANTHROPIC_BASE_URL), else Buildkite Hosted
#                         Models via the job's agent endpoint/token (CI). With
#                         neither, long reports are truncated instead.
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
RUBRIC="$ROOT_DIR/prompts/yilong.json"
JEV_URL="${MCP_EVAL_JEV_URL:-https://jev-mcp-bk.fly.dev/mcp}"
DRY_RUN="${YILONG_DRY_RUN:-false}"
DIGEST_OVER="${YILONG_DIGEST_OVER:-2500}"
DIGEST_MODEL="${YILONG_DIGEST_MODEL:-claude-haiku-4-5}"
# Jev rejects vendor requests over 8,000 bytes; leave room for template drift.
BUDGET=7600

command -v jq >/dev/null   || { echo "yilong: jq is required" >&2; exit 1; }
command -v curl >/dev/null || { echo "yilong: curl is required" >&2; exit 1; }
if [[ -z "${MCP_EVAL_JEV_KEY:-}" && "$DRY_RUN" != "true" ]]; then
    echo "yilong: MCP_EVAL_JEV_KEY is unset; skipping (see .buildkite/pipeline.evals.yml)." >&2
    exit 0
fi

WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

CI_MODE=false
if [[ $# -gt 0 ]]; then
    JUDGE_FILES=("$@")
else
    command -v buildkite-agent >/dev/null || { echo "yilong: pass <run>.judge.json paths, or run in CI" >&2; exit 1; }
    CI_MODE=true
    if ! buildkite-agent artifact download "runs/*/*.judge.json" "$WORK_DIR/"; then
        echo "yilong: no judge.json artifacts (no entry used evaluator: yilong); nothing to judge." >&2
        exit 0
    fi
    buildkite-agent artifact download "runs/*/*.eval-final.md" "$WORK_DIR/" || true
    buildkite-agent artifact download "runs/*/*.tools.txt" "$WORK_DIR/" || true
    JUDGE_FILES=()
    while IFS= read -r f; do JUDGE_FILES+=("$f"); done < <(find "$WORK_DIR" -name '*.judge.json' | sort)
fi

# --- Final-report digest -----------------------------------------------------
# A long report would crowd every other record out of Jev's budget. Ask a
# cheap model for a faithful, judgement-free digest; fall back to the raw
# report (truncated later) when no model is reachable.
DIGEST_URL="" DIGEST_KEY=""
if [[ -n "${ANTHROPIC_API_KEY:-}" ]]; then
    DIGEST_URL="${ANTHROPIC_BASE_URL:-https://api.anthropic.com}/v1/messages"; DIGEST_KEY="$ANTHROPIC_API_KEY"
elif [[ -n "${BUILDKITE_AGENT_ENDPOINT:-}" && -n "${BUILDKITE_AGENT_ACCESS_TOKEN:-}" ]]; then
    DIGEST_URL="$BUILDKITE_AGENT_ENDPOINT/ai/anthropic/v1/messages"; DIGEST_KEY="$BUILDKITE_AGENT_ACCESS_TOKEN"
fi

# digest <report_file> -> digest on stdout; non-zero on failure
digest() {
    [[ -n "$DIGEST_URL" ]] || return 1
    local req="$WORK_DIR/digest-req.json" resp="$WORK_DIR/digest-resp.json" code
    jq -n --arg model "$DIGEST_MODEL" --rawfile report "$1" '{
        model: $model, max_tokens: 1000,
        messages: [{role: "user", content: ("Below is the final report an AI agent wrote after working on a CI build. Condense it to at most 1,800 characters of plain text. For each issue: the root cause the agent concluded, the key evidence it cited (quote log lines, test names and exit codes verbatim), and the fix it made or proposed. Keep the agent'"'"'s claims, hedges and uncertainty exactly as stated. Do not add, correct, or judge anything. No preamble.\n\n<report>\n" + $report + "\n</report>")}]
    }' > "$req"
    code="$(curl -sS --max-time 120 -o "$resp" -w '%{http_code}' -X POST "$DIGEST_URL" \
        -H "x-api-key: $DIGEST_KEY" -H "anthropic-version: 2023-06-01" \
        -H "content-type: application/json" --data @"$req")" || return 1
    if [[ "$code" != 2* ]]; then
        echo "yilong: digest model returned HTTP $code: $(head -c 300 "$resp")" >&2
        return 1
    fi
    jq -er '[.content[]? | select(.type == "text") | .text] | join("") | select(length > 0)' "$resp"
}

# --- Jev ---------------------------------------------------------------------
# Last call time lives in a file: jev_call runs in a command substitution.
LAST_CALL_FILE="$WORK_DIR/jev-last-call"
# jev_call <input_file> -> Jev's structured result on stdout, or {"error": ...}
jev_call() {
    local input="$1" req="$WORK_DIR/jev-req.json" resp="$WORK_DIR/jev-resp.json" code attempt wait
    jq '{jsonrpc: "2.0", id: 1, method: "tools/call", params: {name: "jev_owner_route", arguments: .}}' "$input" > "$req"
    for attempt in 1 2 3; do
        # Pace calls to the per-key token bucket (1 token per 6s).
        if [[ -f "$LAST_CALL_FILE" ]]; then
            wait=$(( $(cat "$LAST_CALL_FILE") + 7 - $(date +%s) ))
            (( wait > 0 )) && sleep "$wait"
        fi
        date +%s > "$LAST_CALL_FILE"
        if ! code="$(curl -sS --max-time 30 -o "$resp" -w '%{http_code}' -X POST "$JEV_URL" \
            -H "Authorization: Bearer $MCP_EVAL_JEV_KEY" \
            -H "Content-Type: application/json" \
            -H "Accept: application/json, text/event-stream" \
            --data @"$req")"; then
            jq -n '{error: "request failed"}'; return
        fi
        if [[ "$code" != 200 ]]; then
            jq -n --arg c "$code" '{error: "HTTP \($c)"}'; return
        fi
        if ! jq -e '.result.structuredContent' "$resp" >/dev/null 2>&1; then
            jq -c '{error: (.error.message // .result.content[0].text // "no structured result")}' "$resp" 2>/dev/null \
                || jq -n '{error: "unparseable response"}'
            return
        fi
        # Capacity limits are not verdicts: wait and retry them. Abstentions
        # and rejected input are final (Jev's guidance: never shop for answers).
        if jq -e '.result.structuredContent.reason | IN("rate_limited", "busy")' "$resp" >/dev/null && (( attempt < 3 )); then
            sleep 12; continue
        fi
        jq -c '.result.structuredContent' "$resp"; return
    done
}

# --- Evidence ------------------------------------------------------------------
# Records yilong can offer Jev; prompts/yilong.json picks per question.
#   goal                 the rendered eval prompt
#   expected_root_cause  expected/<id>/root_cause.md
#   expected             expected/<id>/ root cause + fix (needs fix.md)
#   conclusion           the agent's final report (or its digest)
#   trace                the agent's tool calls, in order, condensed
#   fix                  the pushed diff, else the report's proposed fix
#   outcome              the scenario branch's final build state
# Redaction is defense in depth: bundles should hold no secrets, but agent
# output is untrusted and Jev stores what it is sent.
JQ_LIB='
def redact:
  gsub("(?<p>bkua_|bkct_|bkagent_|ghp_|gho_|ghs_|ghu_|github_pat_|sk-ant-|xox[abpr]-)[A-Za-z0-9_-]{8,}"; "\(.p)[REDACTED]")
  | gsub("AKIA[0-9A-Z]{16}"; "[REDACTED]")
  | gsub("(?<p>[Bb]earer +)[A-Za-z0-9._~+/=-]{16,}"; "\(.p)[REDACTED]");
# Keep the head and tail: reports end with their bottom line, traces with the
# final checks.
def cut($n):
  if length <= $n then .
  else (($n / 2) | floor) as $h | .[:$h] + "\n[…truncated…]\n" + .[length - ($n - $h):] end;
'

# trace <tools.txt> -> condensed tool-call list on stdout (empty if no audit)
trace() {
    [[ -s "$1" ]] || return 0
    local calls; calls="$(grep '^{' "$1" || true)"
    if [[ -z "$calls" ]]; then echo "The agent made no tool calls."; return; fi
    jq -rs "$JQ_LIB"'
        def short: tostring | gsub("\\s+"; " ") | if length > 80 then .[:77] + "..." else . end;
        map((.tool // "?" | sub("^mcp__.*?__"; "")) + " "
            + ((.input // {}) | if type == "object" then
                  del(.org_slug, .pipeline_slug, .telemetry, .description)
                  | to_entries | map("\(.key)=\(.value | short)") | join(" ")
                else short end)
            | .[:200])
        | reduce .[] as $l ([]; if (.[-1].l // null) == $l then .[-1].n += 1 else . + [{l: $l, n: 1}] end)
        | "Tool calls in order (\(map(.n) | add) total):\n"
          + (map(.l + (if .n > 1 then " (×\(.n))" else "" end)) | join("\n"))
    ' <<<"$calls"
}

# --- Judge one run -------------------------------------------------------------
# judge <run>.judge.json -> writes <run>.yilong.json; prints the summary markdown
judge() {
    local judge_file="$1" prefix="${1%.judge.json}"
    local out="$prefix.yilong.json" report="$prefix.eval-final.md"
    local conclusion_file="$WORK_DIR/conclusion.txt" conclusion_source="none"
    : > "$conclusion_file"
    if [[ -s "$report" ]]; then
        conclusion_source="the agent's final report"
        if (( $(wc -c < "$report") > DIGEST_OVER )); then
            if digest "$report" > "$conclusion_file"; then
                conclusion_source="digest of the final report by $DIGEST_MODEL ($(wc -c < "$report") → $(wc -c < "$conclusion_file") bytes)"
            else
                echo "yilong: digest unavailable; truncating the final report instead" >&2
                cp "$report" "$conclusion_file"
                conclusion_source="final report, truncated to fit"
            fi
        else
            cp "$report" "$conclusion_file"
        fi
    fi
    local trace_file="$WORK_DIR/trace.txt"
    trace "$prefix.tools.txt" > "$trace_file"
    local expected_dir; expected_dir="$ROOT_DIR/expected/$(jq -r '.entry' "$judge_file")"
    local root_cause_file="$WORK_DIR/root_cause.md" fix_file="$WORK_DIR/fix.md"
    : > "$root_cause_file"; : > "$fix_file"
    [[ -f "$expected_dir/root_cause.md" ]] && cp "$expected_dir/root_cause.md" "$root_cause_file"
    [[ -f "$expected_dir/fix.md" ]] && cp "$expected_dir/fix.md" "$fix_file"

    # All evidence records, keyed by id; absent when there is nothing to say.
    local records="$WORK_DIR/records.json"
    jq -n "$JQ_LIB"' ($judge[0]) as $j
        | (if $conclusion == "" then null else $conclusion end) as $c
        | {root_cause: $root_cause, fix: $fix} as $e
        | {
            goal: $j.goal,
            expected_root_cause: $e.root_cause,
            expected: (if ($e.fix | test("\\S")) then
                         (if ($e.root_cause | test("\\S")) then "Root cause: \($e.root_cause)\n" else "" end)
                         + "Correct fix: \($e.fix)" else null end),
            conclusion: $c,
            trace: $trace,
            fix: (if ($j.diff // "") != "" then "Change the agent pushed to the scenario branch (git diff):\n\($j.diff)"
                  elif $c then "No code was pushed. The fix as proposed in the agent'"'"'s report:\n\($c)"
                  else null end),
            outcome: (if ($j.final_build_state // "") != "" then
                        "Latest build of the scenario branch after the agent finished: \($j.final_build_state)" else null end)
          }
        | with_entries(select((.value // "") | tostring | test("\\S")) | .value |= (redact | cut(4000)))
    ' --slurpfile judge "$judge_file" --rawfile conclusion "$conclusion_file" --rawfile trace "$trace_file" \
      --rawfile root_cause "$root_cause_file" --rawfile fix "$fix_file" > "$records" || return 1

    local questions=() q
    while IFS= read -r q; do questions+=("$q"); done < <(jq -c '.questions[]' "$RUBRIC")
    local results="$WORK_DIR/results.jsonl"; : > "$results"
    for q in "${questions[@]}"; do
        local key missing input="$WORK_DIR/input.json" est jev
        key="$(jq -r '.key' <<<"$q")"
        missing="$(jq -r --slurpfile r "$records" '[.requires[] | select($r[0][.] == null)] | join(", ")' <<<"$q")"
        if [[ -n "$missing" ]]; then
            jq -c --arg m "$missing" '{key, title, verdict: "n/a", reason: "missing evidence: \($m)"}' <<<"$q" >> "$results"
            continue
        fi
        # Build the Jev input and shrink the longest record until the
        # estimated vendor request fits. The estimate mirrors jev-mcp's
        # createEvaluationRequest (src/evaluate.ts): the input JSON is embedded
        # as an escaped string, plus fixed instructions and one criterion per
        # candidate and evidence record.
        jq --slurpfile r "$records" --argjson budget "$BUDGET" "$JQ_LIB"'
            def est:
              ({task, evidence, candidates} | tojson | tojson | utf8bytelength)
              + 1009 + 5 * (.evidence | length)
              + ([.candidates[] | "Candidate \(.id): \(.description)"] | join("") | tojson | utf8bytelength)
              + ([.evidence[] | "Evidence \(.id)"] | join("") | utf8bytelength);
            {task, candidates: [.levels[] | {id, description}],
             evidence: [.evidence[] as $id | select($r[0][$id] != null) | {id: $id, text: $r[0][$id]}]}
            | until(est <= $budget or ([.evidence[].text | length] | max) <= 300;
                (est - $budget + 64) as $excess
                | ([.evidence[].text | length] | to_entries | max_by(.value).key) as $i
                | .evidence[$i].text |= cut([length - $excess, 300] | max))
            | {task, evidence, candidates, _est: est}
        ' <<<"$q" > "$input" || return 1
        est="$(jq '._est' "$input")"
        jq -c 'del(._est)' "$input" > "$input.tmp" && mv "$input.tmp" "$input"

        if [[ "$DRY_RUN" == "true" ]]; then
            echo "--- [$key] Jev request (estimated vendor request: $est bytes)" >&2
            jq . "$input" >&2
            jev='{"error": "dry run"}'
        elif (( est > BUDGET )); then
            jev="$(jq -n --arg e "$est" '{error: "evidence still too large after trimming (\($e) bytes)"}')"
        else
            echo "yilong: [$key] asking Jev ($est bytes)" >&2
            jev="$(jev_call "$input")"
        fi
        # Map Jev's c0/c1/... candidate keys back to level ids. A verdict only
        # when Jev committed (status ok); otherwise undetermined, keeping the
        # probabilities for the record.
        jq -c --argjson jev "$jev" --argjson est "$est" '
            .levels as $lv
            | ($jev.probabilities.decision // null) as $p
            | ($lv | map(select(.id == $jev.candidateId)) | first) as $hit
            | {key, title,
               verdict: (if $jev.status == "ok" and $hit then $hit.id else "undetermined" end),
               label: ($hit.label // null),
               score: (if $jev.status == "ok" and $hit then $hit.score else null end),
               expected_score: (if $p then
                   ([$lv | to_entries[] | ($p["c\(.key)"] // 0) * .value.score] | add) as $num
                   | ([$lv | to_entries[] | $p["c\(.key)"] // 0] | add) as $den
                   | if $den > 0 then ($num / $den * 100 | round / 100) else null end
                 else null end),
               status: ($jev.status // "error"),
               reason: ($jev.reason // $jev.error),
               probabilities: (if $p then
                   ([$lv | to_entries[] | {(.value.id): $p["c\(.key)"]}] | add) + {no_match: $p.no_match}
                 else null end),
               confidence: ($jev.confidence // null),
               request_id: ($jev.requestId // null),
               request_bytes: $est}
        ' <<<"$q" >> "$results"
    done

    jq -s --slurpfile judge "$judge_file" --arg source "$conclusion_source" '
        {entry: $judge[0].entry, run_key: $judge[0].run_key, evaluator: "yilong",
         conclusion_source: $source, questions: .}' "$results" > "$out"

    # Summary markdown (annotation in CI, stdout locally).
    jq -r '
        def pct: if . == null then "–" else (. * 100 | round | tostring) + "%" end;
        "<details open><summary>:judge: [\(.entry)] yilong — verdicts</summary>\n",
        "| Question | Verdict | Jev probabilities | Note |",
        "| --- | --- | --- | --- |",
        (.questions[] |
          "| \(.title) | \(if .label then "**\(.label)**" else "_\(.verdict)_" end) | "
          + (if .probabilities then ([.probabilities | to_entries[] | "\(.key) \(.value | pct)"] | join(" · ")) else "–" end)
          + " | \(if .status == "ok" then "" else (.reason // "") end) |"),
        "",
        "_Conclusion evidence: \(.conclusion_source)._ Undetermined means Jev abstained or was unavailable; n/a means the entry lacks the evidence (add its reference answer under `evals/expected/<id>/`).",
        "</details>"
    ' "$out"
}

FAILED=0
for judge_file in "${JUDGE_FILES[@]}"; do
    ENTRY="$(jq -r '.entry // "unknown"' "$judge_file" 2>/dev/null || echo unknown)"
    echo "--- :judge: [$ENTRY] yilong"
    SUMMARY="$WORK_DIR/summary.md"
    if ! judge "$judge_file" > "$SUMMARY"; then
        echo "WARNING: yilong: failed to judge $judge_file" >&2
        FAILED=$(( FAILED + 1 ))
        continue
    fi
    cat "$SUMMARY"
    if [[ "$CI_MODE" == "true" ]]; then
        # Upload under the same runs/<id>/ name the rest of the bundle uses.
        REL="${judge_file#"$WORK_DIR/"}"
        (cd "$WORK_DIR" && buildkite-agent artifact upload "${REL%.judge.json}.yilong.json") \
            || echo "WARNING: yilong: failed to upload verdicts for '$ENTRY'" >&2
        buildkite-agent annotate --context "yilong-$ENTRY" --style info --priority 10 < "$SUMMARY" \
            || echo "WARNING: yilong: failed to annotate '$ENTRY'" >&2
    else
        echo "yilong: verdicts written to ${judge_file%.judge.json}.yilong.json"
    fi
done

[[ "$FAILED" -eq 0 ]]
