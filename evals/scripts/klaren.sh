#!/usr/bin/env bash
# Run the detailed reviewer, export rubric verdicts and candidate answer keys.
# Usage: ENTRY_ID=id AGENT=claude klaren.sh transcript goal final-report diff prefix
# CI loads checked-in references from MCP_SRC_SHA only AFTER the evaluated run;
# expected/ remains excluded from the agent image. Local runs use expected/.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ROOT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
TRANSCRIPT="${1:?transcript required}" GOAL="${2:?goal required}"
REPORT="${3:?final report required}" DIFF="${4:?diff file required}"
PREFIX="${5:?output prefix required}"
: "${ENTRY_ID:?entry id required}"
[[ -s "$TRANSCRIPT" && -s "$GOAL" ]] || { echo "klaren: session log and goal are required" >&2; exit 1; }
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

# Never use the scenario checkout's answer key or newly generated candidates.
# CI's image has no expected/; read the pinned harness commit, not latest main.
for name in root_cause fix; do
    : > "$WORK_DIR/$name.md"
    if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
        if [[ "${MCP_SRC_SHA:-}" =~ ^[0-9a-f]{40}$ ]]; then
            code="$(curl -sS --max-time 30 -o "$WORK_DIR/$name.download" -w '%{http_code}' \
                "https://raw.githubusercontent.com/buildkite/buildkite-mcp-server/$MCP_SRC_SHA/evals/expected/$ENTRY_ID/$name.md")" || code=000
            if [[ "$code" == 200 ]]; then
                cp "$WORK_DIR/$name.download" "$WORK_DIR/$name.md"
            elif [[ "$code" != 404 ]]; then
                echo "WARNING: klaren: could not load pinned $name reference (HTTP $code); affected grades will be n/a" >&2
            fi
        else
            echo "WARNING: klaren: MCP_SRC_SHA is missing or invalid; affected grades will be n/a" >&2
        fi
    elif [[ -f "$ROOT_DIR/expected/$ENTRY_ID/$name.md" ]]; then
        cp "$ROOT_DIR/expected/$ENTRY_ID/$name.md" "$WORK_DIR/$name.md"
    fi
done

# A transcript with invocations but no captured results cannot earn a grade
# for investigation, regardless of what the final report claims.
HAS_RESULTS=false
if "$SCRIPT_DIR/bk-tool-audit-v2.sh" --all --results --agent "${AGENT:-claude}" "$TRANSCRIPT" \
    | jq -sR -e 'split("\n") | map(select(startswith("{")) | fromjson)
        | any(.[]; (.result // "") | IN("", "{}", "(no result)", "(no result captured)") | not)' >/dev/null; then
    HAS_RESULTS=true
fi

jq -n --rawfile goal "$GOAL" --rawfile conclusion "$REPORT" --rawfile diff "$DIFF" \
    --rawfile root "$WORK_DIR/root_cause.md" --rawfile fix "$WORK_DIR/fix.md" \
    --arg state "${FINAL_BUILD_STATE:-}" --argjson results "$HAS_RESULTS" \
    '{goal: $goal, conclusion: $conclusion, diff: $diff, outcome: $state,
      expected_root_cause: $root, expected_fix: $fix, captured_tool_results: $results}' > "$WORK_DIR/evidence.json"
{
    cat "$ROOT_DIR/prompts/klaren.md"
    printf '\nThe LLM session log file to analyze is: %s\n' "$TRANSCRIPT"
    printf '\nCanonical rubric (verdict IDs, criteria and required evidence):\n'
    cat "$ROOT_DIR/prompts/rubric.json"
    printf '\nHarness evidence and reviewed references (empty strings mean unavailable):\n'
    cat "$WORK_DIR/evidence.json"
} > "$WORK_DIR/prompt.md"

FAILED=0
ARGS=(--output-format stream-json --verbose)
if [[ "${RUN_IN_CI:-false}" == "true" ]]; then
    if ! ANNOTATION_CONTEXT_PREFIX="klaren-$ENTRY_ID" \
        "$SCRIPT_DIR/claude.sh" "$WORK_DIR/prompt.md" "${ARGS[@]}" | tee "$WORK_DIR/review.log"; then
        FAILED=1
    fi
    RESULT="$(sed -n 's/^CLAUDE_RESULT_FILE=//p' "$WORK_DIR/review.log" | tail -n1)"
    if [[ -s "$RESULT" ]]; then cp "$RESULT" "$PREFIX.klaren.md"; else : > "$PREFIX.klaren.md"; fi
else
    if [[ "${LOCAL_BYPASS_PERMISSION:?must be true or false}" == true ]]; then
        ARGS+=(--permission-mode bypassPermissions)
    else
        ARGS+=(--allowedTools Read Grep Glob Bash)
    fi
    if ! claude -p "$(cat "$WORK_DIR/prompt.md")" --mcp-config "$ROOT_DIR/mcp.json" "${ARGS[@]}" \
        | tee "$WORK_DIR/review.log"; then
        FAILED=1
    fi
    jq -r 'select(.type == "result") | .result // empty' "$WORK_DIR/review.log" > "$PREFIX.klaren.md" || FAILED=1
fi

# Only the explicitly delimited block is machine-readable. Never execute or
# trust numeric scores/file paths from the model. Malformed output is a review
# failure, not three zero grades. Keep the prose review for diagnosis.
awk '
    {sub(/\r$/, "")}
    /^<!-- klaren-result -->$/ {inside=1; count++; next}
    /^<!-- \/klaren-result -->$/ {inside=0; next}
    inside && !/^```(json)?$/ {print}
    END {if (count != 1 || inside) exit 1}
' "$PREFIX.klaren.md" > "$WORK_DIR/result.json" || FAILED=1
if ! jq -e -s --slurpfile rubric "$ROOT_DIR/prompts/rubric.json" '
    length == 1 and (.[0] as $r |
      ($r.questions | type == "object") and
      ($r.questions | keys == ["conclusion", "fix", "investigation"]) and
      all($rubric[0].questions[]; . as $q | $r.questions[$q.key] as $a |
          ($a.reason | type == "string" and test("\\S")) and
          (([$q.levels[].id] + ["n/a", "undetermined"]) | index($a.verdict) != null)) and
      ($r.reference_answers | type == "object") and
      all(["root_cause", "fix"][]; $r.reference_answers[.] | . == null or type == "string"))
' "$WORK_DIR/result.json" >/dev/null 2>&1; then
    FAILED=1
fi
if (( FAILED )); then
    echo "WARNING: klaren: reviewer failed or returned invalid rubric JSON; verdicts will be undetermined where evidence exists" >&2
    echo '{"questions":{},"reference_answers":{}}' > "$WORK_DIR/result.json"
fi

jq -n --slurpfile raw "$WORK_DIR/result.json" --slurpfile rubric "$ROOT_DIR/prompts/rubric.json" \
    --slurpfile evidence "$WORK_DIR/evidence.json" --arg entry "$ENTRY_ID" --arg run "${PREFIX##*/}" '
    $evidence[0] as $e | $raw[0] as $r
    | {entry: $entry, run_key: $run, evaluator: "klaren",
       reference_answers_status: "candidate — human review required",
       reference_answers: $r.reference_answers,
       questions: [$rubric[0].questions[] | . as $q
         | (if .key == "fix" then
              if ($e.expected_fix | test("\\S") | not) then "No reference fix available: add evals/expected/\($entry)/fix.md"
              elif (($e.diff + $e.conclusion) | test("\\S") | not) then "Missing evidence: fix" else null end
            elif ($e.expected_root_cause | test("\\S") | not) then "No reference answer available: add evals/expected/\($entry)/root_cause.md"
            elif ($e.conclusion | test("\\S") | not) then "Missing evidence: conclusion"
            elif .key == "investigation" and ($e.captured_tool_results | not) then "No captured tool results available in transcript.jsonl"
            else null end) as $missing
         | ($r.questions[.key] // {verdict:"undetermined", reason:"Reviewer failed or returned invalid rubric JSON"}) as $a
         | (if $missing then "n/a"
            elif .key == "investigation" and $a.verdict == "sherlock" and $r.questions.conclusion.verdict != "yeah-baby" then "scooby-doo"
            else $a.verdict end) as $v
         | ([.levels[] | select(.id == $v)][0] // {}) as $level
         | {key, title, verdict: $v, label: ($level.label // null), score: ($level.score // null),
            reason: (if $missing then $missing
                     elif $v != $a.verdict then "Sherlock requires a correct conclusion (Yeah baby); capped at Scooby-Doo. " + $a.reason
                     else $a.reason end)}]}
' > "$PREFIX.klaren.json"

for name in root_cause fix; do
    candidate="$PREFIX.klaren-candidate-$name.md"
    rm -f "$candidate"
    if jq -e --arg name "$name" '.reference_answers[$name] // "" | test("\\S")' "$PREFIX.klaren.json" >/dev/null; then
        { printf '<!-- Candidate reference from klaren: human review required before use in yilong. -->\n\n'
          jq -r --arg name "$name" '.reference_answers[$name]' "$PREFIX.klaren.json"; } > "$candidate"
    fi
done
jq -r '
    "| Question | Verdict | Note |\n| --- | --- | --- |",
    (.questions[] | "| \(.title) | \(.label // .verdict) | \(.reason | gsub("[\\r\\n|]"; " ")) |"),
    "\nMissing references or evidence mean n/a, not zero. Candidate references require human review and do not count as an answer key for this run."
' "$PREFIX.klaren.json" > "$PREFIX.klaren-verdicts.md"
for name in root_cause fix; do
    if [[ -s "$PREFIX.klaren-candidate-$name.md" ]]; then
        printf '\n[Candidate %s reference — review before use](artifact://runs/%s/%s.klaren-candidate-%s.md)\n' \
            "$name" "$ENTRY_ID" "${PREFIX##*/}" "$name" >> "$PREFIX.klaren-verdicts.md"
    fi
done
cat "$PREFIX.klaren-verdicts.md"
exit "$FAILED"
