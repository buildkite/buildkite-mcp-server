#!/usr/bin/env bash
#
# yilong.sh — the default (cheap) evaluator: grade each eval run with Jev,
# TypeSafe's System One model (https://docs.typesafe.ai), called directly.
#
# For every run whose evaluator is yilong, babystand.sh leaves a
# <run>.judge.json in the run bundle (goal, the scenario branch's diff and
# final build state). This script combines that, the bundle's eval-final.md
# and transcript.jsonl, and the prompt/scenario reference answer (expected/<prompt>/<scenario>/root_cause.md
# and fix.md, read from this checkout — never part of the agent's container)
# into evidence records, then asks Jev one Score question per rubric entry in
# prompts/rubric.json, each against only the records that question names:
#   conclusion     bollocks | sorta | yeah-baby
#   investigation  homer-simpson | scooby-doo | sherlock
#   fix            airshot | duct-tape | sweeet
# The verdict is the level Jev gives the highest probability; its
# probabilities, expected score and confidence are kept alongside. Verdicts
# land in <run>.yilong.json (plus an annotation in CI). A question is "n/a"
# when its required evidence is missing and "undetermined" when the API fails,
# the complete patch is unavailable, or the request exceeds the input limits.
#
# Limits (https://docs.typesafe.ai/models): 64k tokens per request and 32k per
# question, 32 MiB bodies, per-organization rate limits (429/529 are retried
# with backoff). Token-limit failures retry full evidence with 262k long context,
# then use chunked Haiku summaries if long context also rejects it. Summary-based
# grades are marked; complete redacted evidence and summary requests are retained.
# Bodies exceeding 32 MiB are still withheld locally.
#
# SECURITY: MCP_EVAL_FRAMEWORK_JEV_TOKEN must never exist in the eval step,
# where the agent under test has unrestricted Bash. In CI this runs as its own
# pipeline step (see .buildkite/pipeline.evals.yml), from the run-bundle
# artifacts.
#
# Usage:
#   yilong.sh                        CI: download runs/*/*.judge.json artifacts,
#                                    judge them, upload <run>.yilong.json, annotate
#   yilong.sh <run>.judge.json ...   local: judge these bundles, print the summary
#
# Env:
#   MCP_EVAL_FRAMEWORK_JEV_TOKEN  TypeSafe API key. Unset = loud skip (exit 0).
#   YILONG_API_URL        default https://api.typesafe.ai/v1/systemone
#   YILONG_MODEL          default jev-1.14.0 (pinned so verdicts stay comparable
#                         across builds; jev-latest tracks new releases)
#   YILONG_DRY_RUN=true   build and print each request; call nothing (no key needed)
#
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
RUBRIC="$ROOT_DIR/prompts/rubric.json"
API_URL="${YILONG_API_URL:-https://api.typesafe.ai/v1/systemone}"
MODEL="${YILONG_MODEL:-jev-1.14.0}"
DRY_RUN="${YILONG_DRY_RUN:-false}"

command -v jq >/dev/null   || { echo "yilong: jq is required" >&2; exit 1; }
command -v curl >/dev/null || { echo "yilong: curl is required" >&2; exit 1; }
# Secrets pasted with a trailing newline are common; no API key has whitespace.
JEV_TOKEN="$(printf '%s' "${MCP_EVAL_FRAMEWORK_JEV_TOKEN:-}" | tr -d '[:space:]')"
if [[ -z "$JEV_TOKEN" && "$DRY_RUN" != "true" ]]; then
    echo "yilong: MCP_EVAL_FRAMEWORK_JEV_TOKEN is unset; skipping (see .buildkite/pipeline.evals.yml)." >&2
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
    buildkite-agent artifact download "runs/*/*.transcript.jsonl" "$WORK_DIR/" || true
    buildkite-agent artifact download "runs/*/*.diff.patch" "$WORK_DIR/" || true
    JUDGE_FILES=()
    while IFS= read -r f; do JUDGE_FILES+=("$f"); done < <(find "$WORK_DIR" -name '*.judge.json' | sort)
fi

# --- Jev ---------------------------------------------------------------------
# jev_call <request_file> [long_context=false] -> API response or {"error": ...}
jev_call() {
    local req="$1" resp="$WORK_DIR/jev-resp.json" hdrs="$WORK_DIR/jev-headers.txt" code attempt wait
    local context_headers=() timeout=60
    if [[ "${2:-false}" == true ]]; then
        context_headers=(-H "TypeSafe-Danger-Zone-Long-Context: true")
        timeout=180
    fi
    if (( $(wc -c < "$req") > 33554432 )); then
        jq -n '{error: "Complete evidence exceeds the TypeSafe 32 MiB request-body limit; grade withheld, evidence not truncated"}'
        return
    fi
    for attempt in 1 2 3 4; do
        code="$(curl -sS --max-time "$timeout" -o "$resp" -D "$hdrs" -w '%{http_code}' -X POST "$API_URL" \
            -H "Authorization: Bearer $JEV_TOKEN" \
            -H "Content-Type: application/json" \
            -H "Accept: application/json" \
            ${context_headers[@]+"${context_headers[@]}"} \
            --data @"$req")" || code="000"
        case "$code" in
            200)
                if jq -e '.answers' "$resp" >/dev/null 2>&1; then jq -c . "$resp"
                else jq -n '{error: "unparseable response"}'; fi
                return ;;
            000|408|429|5*)
                # Capacity and transient failures are not verdicts: back off
                # (honouring retry-after) and retry.
                if (( attempt < 4 )); then
                    wait="$(sed -n 's/^retry-after: *\([0-9][0-9]*\).*/\1/Ip' "$hdrs" 2>/dev/null | head -n1)"
                    [[ "$wait" =~ ^[0-9]+$ && "$wait" -le 60 ]] || wait=$(( 2 ** attempt ))
                    echo "yilong: Jev returned ${code/#000/no response}; retrying in ${wait}s" >&2
                    sleep "$wait"; continue
                fi ;;
            401)
                echo "WARNING: yilong: TypeSafe rejected MCP_EVAL_FRAMEWORK_JEV_TOKEN (401): it is not a valid API key for $API_URL." >&2
                jq -n '{error: "HTTP 401: TypeSafe rejected MCP_EVAL_FRAMEWORK_JEV_TOKEN"}'; return ;;
            400|413)
                local token_limit=false
                if [[ "$code" == 400 ]] && jq -e '.detail.error_type == "max_tokens_exceeded"' "$resp" >/dev/null 2>&1; then
                    token_limit=true
                fi
                jq -n --arg code "$code" --arg body "$(head -c 500 "$resp")" --argjson token_limit "$token_limit" \
                    '{error: "HTTP \($code): complete evidence rejected; grade withheld, evidence not truncated. \($body)", token_limit: $token_limit}'
                return ;;
        esac
        # Error bodies describe the request, never the key; keep a short excerpt.
        jq -n --arg c "$code" --arg b "$(head -c 300 "$resp" 2>/dev/null | tr '\n' ' ')" '{error: "HTTP \($c): \($b)"}'
        return
    done
}

# --- Evidence ------------------------------------------------------------------
# Records yilong can offer Jev; prompts/rubric.json picks per question.
#   goal                 the rendered eval prompt
#   expected_root_cause  expected/<prompt>/<scenario>/root_cause.md
#   expected             reviewed root cause + fix (needs fix.md)
#   conclusion           the agent's final report
#   trace                tool calls paired with full captured results, in call order
#   fix                  the pushed diff, else the report's proposed fix
#   outcome              the scenario branch's final build state
# Redaction is defense in depth: bundles should hold no secrets, but agent
# output is untrusted and leaves Buildkite.
JQ_LIB='
def redact:
  gsub("(?<p>bkua_|bkct_|bkagent_|ghp_|gho_|ghs_|ghu_|github_pat_|sk-ant-|xox[abpr]-)[A-Za-z0-9_-]{8,}"; "\(.p)[REDACTED]")
  | gsub("AKIA[0-9A-Z]{16}"; "[REDACTED]")
  | gsub("(?<p>[Bb]earer +)[A-Za-z0-9._~+/=-]{16,}"; "\(.p)[REDACTED]");
'

# trace <transcript.jsonl> <agent> -> calls with full captured results (empty if none)
trace() {
    [[ -s "$1" ]] || return 0
    local calls
    calls="$("$SCRIPT_DIR/bk-tool-audit-v2.sh" --all --results --full-results --agent "$2" "$1")" || return 1
    jq -rs '
        if any(.[]; (.result // "") | IN("", "{}", "(no result)", "(no result captured)") | not)
        then "Tool calls with full captured results, in call order (not completion order). Missing evidence is not proof of a claim:\n"
             + (map(tojson) | join("\n"))
        else empty end
    ' <<<"$(printf '%s\n' "$calls" | grep '^{' || true)"
}

# --- Judge one run -------------------------------------------------------------
# judge <run>.judge.json -> writes <run>.yilong.json; prints the summary markdown
judge() {
    local judge_file="$1" prefix="${1%.judge.json}"
    local out="$prefix.yilong.json" report="$prefix.eval-final.md"
    local conclusion_file="$WORK_DIR/conclusion.txt" trace_file="$WORK_DIR/trace.txt"
    : > "$conclusion_file"
    [[ -s "$report" ]] && cp "$report" "$conclusion_file"
    if ! trace "$prefix.transcript.jsonl" "$(jq -r '.agent // "claude"' "$judge_file")" > "$trace_file"; then
        echo "WARNING: yilong: could not read tool results from $prefix.transcript.jsonl; investigation will be n/a" >&2
        : > "$trace_file"
    fi
    local prompt scenario reference_path=""
    prompt="$(jq -r '.prompt // ""' "$judge_file")"
    scenario="$(jq -r '.scenario // ""' "$judge_file")"
    for component in "$prompt" "$scenario"; do
        [[ -z "$component" ]] && continue
        if [[ ! "$component" =~ ^[A-Za-z0-9._-]+$ || "$component" == . || "$component" == .. ]]; then
            echo "yilong: prompt and scenario must be single path components" >&2; return 1
        fi
    done
    [[ -z "$prompt" || -z "$scenario" ]] || reference_path="expected/$prompt/$scenario"
    local expected_dir="$ROOT_DIR/$reference_path"
    local root_cause_file="$WORK_DIR/root_cause.md" fix_file="$WORK_DIR/fix.md"
    : > "$root_cause_file"; : > "$fix_file"
    if [[ -n "$reference_path" ]]; then
        [[ ! -f "$expected_dir/root_cause.md" ]] || cp "$expected_dir/root_cause.md" "$root_cause_file"
        [[ ! -f "$expected_dir/fix.md" ]] || cp "$expected_dir/fix.md" "$fix_file"
    fi
    local diff_status diff_file="$WORK_DIR/diff.patch"
    diff_status="$(jq -r '.diff_status // "unavailable"' "$judge_file")"
    : > "$diff_file"
    case "$diff_status" in
        available)
            if [[ -f "$prefix.diff.patch" && -r "$prefix.diff.patch" ]]; then
                cp "$prefix.diff.patch" "$diff_file"
            else diff_status="unavailable"; fi ;;
        not_applicable|unavailable) ;;
        *) diff_status="unavailable" ;;
    esac

    # All evidence records, keyed by id; absent when there is nothing to say.
    local records="$WORK_DIR/records.json"
    jq -n --arg diff_status "$diff_status" "$JQ_LIB"' ($judge[0]) as $j
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
            fix: (if $diff_status == "available" then "Complete pushed change (git diff; an empty patch means no changes):\n\($diff)"
                  elif $diff_status == "not_applicable" and $c then "Diagnosis-only task. The fix as proposed in the agent'"'"'s report:\n\($c)"
                  else null end),
            outcome: (if ($j.final_build_state // "") != "" then
                        "Latest build of the scenario branch after the agent finished: \($j.final_build_state)" else null end)
          }
        | with_entries(select((.value // "") | tostring | test("\\S")) | .value |= redact)
    ' --slurpfile judge "$judge_file" --rawfile conclusion "$conclusion_file" --rawfile trace "$trace_file" \
      --rawfile root_cause "$root_cause_file" --rawfile fix "$fix_file" --rawfile diff "$diff_file" > "$records" || return 1

    local questions=() q
    while IFS= read -r q; do questions+=("$q"); done < <(jq -c '.questions[]' "$RUBRIC")
    local results="$WORK_DIR/results.jsonl"; : > "$results"
    for q in "${questions[@]}"; do
        local key missing req="$WORK_DIR/request.json" jev
        local evidence_mode=full context_mode=default summary_info=null evidence_artifact="" summary_artifact=""
        key="$(jq -r '.key' <<<"$q")"
        missing="$(jq -r --slurpfile r "$records" '[.requires[] | select($r[0][.] == null)] | join(", ")' <<<"$q")"
        if [[ -z "$reference_path" ]]; then
            jq -c '{key, title, verdict: "n/a", reason: "Missing prompt or scenario.id: configure both and add reviewed prompt/scenario references"}' <<<"$q" >> "$results"
            continue
        fi
        if [[ "$key" == fix && "$diff_status" == unavailable && "$missing" == fix ]]; then
            jq -c '{key, title, verdict: "undetermined", reason: "Complete pushed diff unavailable; fix grade withheld, not inferred from the agent report"}' <<<"$q" >> "$results"
            continue
        fi
        if [[ -n "$missing" ]]; then
            local reason
            reason="$(jq -r --slurpfile r "$records" --arg dir "evals/$reference_path" '
              [.requires[] | select($r[0][.] == null) |
                if . == "expected_root_cause" then "No reference answer available: add " + $dir + "/root_cause.md"
                elif . == "expected" then "No reference fix available: add " + $dir + "/fix.md"
                elif . == "trace" then "No captured tool results available in transcript.jsonl"
                else "Missing evidence: " + . end] | join("; ")
            ' <<<"$q")"
            echo "yilong: [$key] n/a — $reason; skipping Jev" >&2
            jq -c --arg reason "$reason" '{key, title, verdict: "n/a", reason: $reason}' <<<"$q" >> "$results"
            continue
        fi
        # One request per question, so each sees only its own records. Levels go
        # in order as a Score rubric: index 0 is the worst verdict.
        jq --slurpfile r "$records" --arg model "$MODEL" '
            {model: $model,
             state: ([.evidence[] as $id | select($r[0][$id] != null) | {($id): $r[0][$id]}] | add),
             questions: {(.key): {type: "score", instructions, criteria: [.levels[].description]}}}
        ' <<<"$q" > "$req" || return 1

        if [[ "$DRY_RUN" == "true" ]]; then
            echo "--- [$key] Jev request ($(wc -c < "$req") bytes)" >&2
            jq . "$req" >&2
            jev='{"error": "dry run"}'
        else
            echo "yilong: [$key] asking Jev ($(wc -c < "$req") bytes)" >&2
            jev="$(jev_call "$req")"
            if [[ "$(jq -r '.token_limit // false' <<<"$jev")" == true ]]; then
                echo "yilong: [$key] token limit exceeded; retrying complete evidence with 262k long context" >&2
                context_mode=long
                evidence_artifact="${prefix##*/}.yilong-$key.evidence.json"
                cp "$req" "$prefix.yilong-$key.evidence.json" || return 1
                jev="$(jev_call "$req" true)"
                if [[ "$(jq -r '.token_limit // false' <<<"$jev")" == true ]]; then
                    echo "yilong: [$key] long-context token limit exceeded; summarizing observed evidence with Haiku" >&2
                    local summary_request="$WORK_DIR/summarized.json"
                    if command -v python3 >/dev/null && summary_info="$(python3 "$SCRIPT_DIR/yilong-summarize.py" "$req" "$summary_request")"; then
                        # Reapply the same redaction to generated text before it
                        # leaves the judge or becomes a downloadable artifact.
                        jq "$JQ_LIB"'walk(if type == "string" then redact else . end)' "$summary_request" > "$prefix.yilong-$key.summary.json" || return 1
                        summary_artifact="${prefix##*/}.yilong-$key.summary.json"
                        evidence_mode=summarized
                        jev="$(jev_call "$prefix.yilong-$key.summary.json" true)"
                    else
                        # No partial summary, empty response, or unfinished
                        # completion is allowed to produce a numeric grade.
                        local summary_error
                        if ! jq -e 'type == "object" or . == null' <<<"$summary_info" >/dev/null 2>&1; then
                            summary_info='{"error":"Haiku summarizer failed without a valid response"}'
                        fi
                        summary_error="$(jq -r '.error // "python3 is required for Haiku summarization"' <<<"$summary_info")"
                        jev="$(jq -n --arg reason "$summary_error" '{error: "Long-context token limit exceeded; Haiku summarization failed: \($reason); grade withheld"}')"
                    fi
                fi
            fi
        fi
        # Verdict = the most probable level (first on a tie). Jev's own score
        # is the probability-weighted level; normalised to 0-1 as
        # expected_score so it lines up with the verdict scores.
        jq -c --argjson jev "$jev" --arg evidence_mode "$evidence_mode" --arg context_mode "$context_mode" \
          --argjson summarization "$summary_info" --arg evidence_artifact "$evidence_artifact" --arg summary_artifact "$summary_artifact" '
            .levels as $lv
            | (try $jev.answers[.key] catch null) as $a
            | (try $a.probabilities catch null) as $p
            # Require every level, numeric probabilities in [0,1], and a total
            # of 1 (allowing floating-point rounding, not normalising bad data).
            | (try (
                ($p | type) == "object"
                and ($p | keys) == ([range(0; $lv | length) | tostring] | sort)
                and all($p[]; type == "number" and . >= 0 and . <= 1)
                and (($p | add) - 1 | fabs) <= 0.000001
              ) catch false) as $valid
            | (if $valid then
                ($p | map(.) | max) as $max
                | first(range(0; $lv | length) | select($p[tostring] == $max))
              else null end) as $i
            | {key, title,
               verdict: (if $i != null then $lv[$i].id else "undetermined" end),
               label: (if $i != null then $lv[$i].label else null end),
               score: (if $i != null then $lv[$i].score else null end),
               expected_score: (if $valid and ($a.score | type) == "number" then ($a.score / (($lv | length) - 1) * 100 | round / 100) else null end),
               confidence: (if $valid then $a.confidence else null end),
               probabilities: (if $valid then ([$lv | to_entries[] | {(.value.id): $p[.key | tostring]}] | add) else null end),
               reason: ((if $evidence_mode == "summarized" then "Based on summarized evidence (Haiku; lossy). " else "" end)
                 + ($jev.error // (if $valid then "" else "Invalid Jev probability distribution: expected one numeric probability per level in [0,1], summing to 1 (tolerance 0.000001); no score published." end))),
               evidence_mode: $evidence_mode, context_mode: $context_mode,
               summarization: $summarization,
               evidence_artifact: $evidence_artifact, summary_artifact: $summary_artifact,
               model: ($jev.model // null),
               usage: ($jev.usage // null)}
        ' <<<"$q" >> "$results"
    done

    jq -s --slurpfile judge "$judge_file" '
        {entry: $judge[0].entry, run_key: $judge[0].run_key, evaluator: "yilong", questions: .}' "$results" > "$out"

    # Summary markdown (annotation in CI, stdout locally).
    jq -r --arg artifacts "runs/$(jq -r '.entry' "$judge_file")/" '
        def pct: if . == null then "–" else (. * 100 | round | tostring) + "%" end;
        "<details open><summary>🧑🏻‍⚖️ [\(.entry)] yilong — verdicts</summary>\n",
        (if any(.questions[]; .verdict == "n/a") then
           "**Some categories were not evaluated (n/a).** See the Note column for missing references or evidence. No score is published for those categories.\n"
         else empty end),
        (if any(.questions[]; .evidence_mode == "summarized") then
           "**Some verdicts are based on summarized evidence (Haiku; lossy), not the full evidence.** Those categories are marked in the Note column.\n"
         else empty end),
        "| Question | Verdict | Jev probabilities | Confidence | Note |",
        "| --- | --- | --- | --- | --- |",
        (.questions[] |
          "| \(.title) | \(if .label then "**\(.label)**" else "_\(.verdict)_" end)"
          + (if .evidence_mode == "summarized" then "\\*" else "" end) + " | "
          + (if .probabilities then ([.probabilities | to_entries[] | "\(.key) \(.value | pct)"] | join(" · ")) else "–" end)
          + " | \(.confidence | pct) | \((.reason // "") | gsub("[\\r\\n|]"; " ")) |"),
        "",
        (if any(.questions[]; .evidence_mode == "summarized") then
           "\\* Jev evaluated Haiku-summarized evidence (lossy), not the complete evidence.\n"
         else empty end),
        (.questions[] | select((.evidence_artifact // "") != "") |
          "\(.title): [Complete redacted request](artifact://\($artifacts)\(.evidence_artifact))"
          + (if (.summary_artifact // "") != "" then " · [Summarized request](artifact://\($artifacts)\(.summary_artifact))" else "" end)),
        "Verdict = the level Jev finds most probable, without cross-category adjustments. Undetermined means evaluation failed or the complete patch could not be supplied, including input-limit failures; n/a means required references or evidence are missing (see Note).",
        "</details>"
    ' "$out"
    if [[ "$diff_status" == available ]]; then
        printf '\n[Complete pushed diff](artifact://runs/%s/%s.diff.patch)\n' \
            "$(jq -r '.entry' "$judge_file")" "${prefix##*/}"
    fi
}

FAILED=0
for judge_file in "${JUDGE_FILES[@]}"; do
    ENTRY="$(jq -r '.entry // "unknown"' "$judge_file" 2>/dev/null || echo unknown)"
    echo "--- 🧑🏻‍⚖️ [$ENTRY] yilong"
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
        for evidence in "${judge_file%.judge.json}".yilong-*.json; do
            [[ -f "$evidence" ]] || continue
            (cd "$WORK_DIR" && buildkite-agent artifact upload "${evidence#"$WORK_DIR/"}") \
                || echo "WARNING: yilong: failed to upload evidence for '$ENTRY'" >&2
        done
        buildkite-agent annotate --context "yilong-$ENTRY" --style info --priority 10 < "$SUMMARY" \
            || echo "WARNING: yilong: failed to annotate '$ENTRY'" >&2
    else
        echo "yilong: verdicts written to ${judge_file%.judge.json}.yilong.json"
    fi
done

[[ "$FAILED" -eq 0 ]]
