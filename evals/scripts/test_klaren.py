"""Run with: python3 -B -m unittest discover -s evals/scripts -p 'test_*.py'."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import time
import unittest


class KlarenTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        source = Path(__file__).resolve().parent
        self.scripts = self.root / "scripts"
        self.scripts.mkdir()
        for name in ("babystand.sh", "klaren.sh", "bk-tool-audit-v2.sh", "bk-shell-commands.mjs",
                     "api-fanout.sh", "dd-metrics.sh", "dd-publish.sh"):
            shutil.copy2(source / name, self.scripts / name)
        shutil.copytree(source.parent / "prompts", self.root / "prompts")
        self.refs = self.root / "expected/diagnose/leaked-model"
        self.refs.mkdir(parents=True)
        (self.refs / "root_cause.md").write_text("Reviewed root cause: leaked test model.")
        (self.refs / "fix.md").write_text("Reviewed fix: isolate the test model.")
        self.prefix = self.root / "run"
        self.transcript = self.root / "transcript.jsonl"
        self.transcript.write_text('\n'.join(map(json.dumps, [
            {"message": {"content": [{"type": "tool_use", "id": "a", "name": "get_logs", "input": {}}]}},
            {"message": {"content": [{"type": "tool_result", "tool_use_id": "a", "content": "NameError: leaked model"}]}},
        ])))
        (self.root / "goal.md").write_text("Diagnose and propose the minimal fix.")
        (self.root / "final.md").write_text("The model leaked. Isolate it.")
        (self.root / "diff").write_text("")
        self.response = {
            "diff_review_complete": False,
            "struggles": [],
            "questions": {
                "conclusion": {"verdict": "sorta", "reason": "Missed a material cause.", "score": 999},
                "investigation": {"verdict": "sherlock", "reason": "Read the logs."},
                "fix": {"verdict": "airshot", "reason": "Does not fix the cause."},
            },
            "reference_answers": {"root_cause": "# Root cause\nLeaked model; see NameError in get_logs.",
                                  "fix": "# Fix\nIsolate model and rerun failing tests."},
        }
        stub = self.root / "bin"
        stub.mkdir()
        (stub / "claude").write_text('#!/bin/sh\nprintf "%s" "$2" > "$STUB_PROMPT"\ncat "$STUB_STREAM"\n')
        (stub / "claude").chmod(0o755)
        (stub / "buildkite-agent").write_text('#!/bin/sh\npattern=${3##*/}\nmkdir -p "$4/runs/case"\nfor file in "$STUB_ROOT"/$pattern; do\n  [ ! -f "$file" ] || cp "$file" "$4/runs/case/"\ndone\n')
        (stub / "buildkite-agent").chmod(0o755)
        self.env = {**os.environ, "PATH": f"{stub}:{os.environ['PATH']}", "RUN_IN_CI": "false",
                    "LOCAL_BYPASS_PERMISSION": "false", "ENTRY_ID": "case", "AGENT": "claude",
                    "PROMPT_NAME": "diagnose", "SCENARIO_ID": "leaked-model",
                    "STUB_STREAM": str(self.root / "stream.json"), "STUB_PROMPT": str(self.root / "prompt.md"),
                    "STUB_ROOT": str(self.root)}

    def run_review(self, text=None, returncode=0):
        if text is None:
            text = "Detailed review preserved.\n<!-- klaren-result -->\n```json\n" + json.dumps(self.response) + "\n```\n<!-- /klaren-result -->"
        (self.root / "review.md").write_text(text)
        (self.root / "stream.json").write_text(json.dumps({"type": "result", "result": text}) + "\n")
        result = subprocess.run([str(self.scripts / "klaren.sh"), str(self.transcript),
                                 str(self.root / "goal.md"), str(self.root / "final.md"),
                                 str(self.root / "diff"), str(self.prefix)],
                                env=self.env, text=True, capture_output=True)
        self.assertEqual(result.returncode, returncode, result.stderr)
        output = json.loads((self.root / "run.klaren.json").read_text())
        return output, result

    def metrics(self):
        timestamp = int(time.time()) - 60
        (self.root / "run.dd.json").write_text(json.dumps({
            "entry": "case", "prompt": "diagnose", "agent": "claude", "model": "reviewed-model",
            "goal": "unknown", "duration_seconds": 42, "end_ts": timestamp, "evaluator": "klaren",
        }))
        result = subprocess.run([str(self.scripts / "dd-publish.sh")], check=True, capture_output=True, text=True,
                                env={**self.env, "DD_DRY_RUN": "true", "DD_API_KEY": "dry-run-fixture"})
        self.assertIn("1 entry(ies) published, 0 failed", result.stdout)
        payload, _ = json.JSONDecoder().raw_decode(result.stdout[result.stdout.index("{"):])
        series = {s["metric"]: s for s in payload["series"]}
        self.assertEqual(series["mcp_eval.duration_seconds"]["points"], [{"timestamp": timestamp, "value": 42}])
        self.assertIn("model:reviewed-model", series["mcp_eval.run"]["tags"])
        return series

    def test_grades_candidates_and_datadog_share_the_rubric(self):
        data, result = self.run_review()
        # Even contradictory reviewer grades are preserved, not silently corrected.
        self.assertEqual([q["score"] for q in data["questions"]], [0.5, 1, 0])
        self.assertEqual(data["questions"][1]["verdict"], "sherlock")
        self.assertEqual(data["questions"][1]["reason"], "Read the logs.")
        self.assertIn("| Quality of investigation | Sherlock | Read the logs. |", result.stdout)
        self.assertIn("Detailed review preserved", (self.root / "run.klaren.md").read_text())
        self.assertIn("human review required", (self.root / "run.klaren-candidate-root_cause.md").read_text())
        self.assertIn("Isolate model", (self.root / "run.klaren-candidate-fix.md").read_text())
        self.assertEqual((self.refs / "root_cause.md").read_text(), "Reviewed root cause: leaked test model.")
        prompt = (self.root / "prompt.md").read_text()
        self.assertIn("Reviewed root cause", prompt)
        self.assertIn("Diagnose and propose", prompt)
        metrics = self.metrics()
        for key, value in (("conclusion", 0.5), ("investigation", 1), ("fix", 0)):
            series = metrics[f"mcp_eval.klaren.{key}"]
            self.assertEqual(series["points"][0]["value"], value)
            self.assertIn("evaluator:klaren", series["tags"])
        self.assertEqual({key for key in metrics if key.endswith(".investigation")}, {"mcp_eval.klaren.investigation"})

    def test_generated_candidates_do_not_replace_missing_references(self):
        shutil.rmtree(self.refs)
        data, result = self.run_review()
        self.assertEqual({q["verdict"] for q in data["questions"]}, {"n/a"})
        self.assertTrue((self.root / "run.klaren-candidate-root_cause.md").exists())
        self.assertIn("No reference answer available", result.stdout)
        self.assertIn("No reference fix available", result.stdout)
        self.assertIn("destination: `evals/expected/diagnose/leaked-model/root_cause.md`", result.stdout)
        self.assertIn("destination: `evals/expected/diagnose/leaked-model/fix.md`", result.stdout)
        self.assertIn("Review each candidate against the target build", result.stdout)
        self.assertIn("submit a PR for review", result.stdout)
        self.assertIn("Run the eval again using the commit containing the approved references", result.stdout)
        self.assertFalse(self.refs.exists())
        metrics = self.metrics()
        self.assertEqual([key for key in metrics if ".klaren." in key], ["mcp_eval.klaren.undetermined"])
        self.assertEqual(metrics["mcp_eval.klaren.undetermined"]["points"][0]["value"], 0)
        self.response["reference_answers"]["root_cause"] = None
        _, result = self.run_review()
        self.assertNotIn("Download candidate root_cause", result.stdout)
        self.assertIn("Download candidate fix", result.stdout)

    def test_references_follow_prompt_and_scenario_not_run_identity(self):
        data, _ = self.run_review()
        self.assertEqual(data["reference_path"], "evals/expected/diagnose/leaked-model")
        self.env.update(ENTRY_ID="another-model", AGENT="cursor")
        data, _ = self.run_review()
        self.assertEqual(data["questions"][0]["score"], 0.5)
        shutil.copytree(self.refs, self.root / "expected/another-model")
        # Neither a different scenario nor a different prompt may reuse this key.
        for prompt, scenario in (("diagnose", "another-build"), ("repair", "leaked-model"), ("diagnose", "")):
            with self.subTest(prompt=prompt, scenario=scenario):
                self.env.update(PROMPT_NAME=prompt, SCENARIO_ID=scenario)
                data, _ = self.run_review()
                self.assertEqual({q["verdict"] for q in data["questions"]}, {"n/a"})
                self.assertTrue((self.root / "run.klaren-candidate-root_cause.md").exists())
        self.assertIn("No scenario.id configured", data["questions"][0]["reason"])
        summary = (self.root / "run.klaren-verdicts.md").read_text()
        self.assertIn("Set a stable `scenario.id`", summary)
        self.assertIn("evals/expected/diagnose/<scenario.id>/root_cause.md", summary)
        self.assertNotIn("→ destination:", summary)

    def test_missing_results_and_invalid_output_do_not_publish_success(self):
        self.transcript.write_text(self.transcript.read_text().splitlines()[0] + "\n")
        data, _ = self.run_review()
        self.assertEqual(data["questions"][1]["verdict"], "n/a")
        self.transcript.write_text('{"message":{"content":[{"type":"tool_use","id":"a","name":"get_logs","input":{}},{"type":"tool_result","tool_use_id":"a","content":"failure"}]}}\n')
        data, _ = self.run_review(text="Prose but no valid scoring block.", returncode=1)
        self.assertEqual({q["verdict"] for q in data["questions"]}, {"undetermined"})
        self.assertEqual(self.metrics()["mcp_eval.klaren.undetermined"]["points"][0]["value"], 3)
        self.assertEqual((self.root / "run.klaren.md").read_text().strip(), "Prose but no valid scoring block.")
        self.assertFalse((self.root / "run.klaren-candidate-root_cause.md").exists())
        self.assertNotIn("Candidate reference answers — next steps", (self.root / "run.klaren-verdicts.md").read_text())
        self.assertIsNone(data["struggles"])
        self.assertIn("unavailable", (self.root / "run.klaren-struggles.md").read_text())

    def test_reviewer_struggles_require_evidence_and_consequence(self):
        finding = {"summary": "Summary exceeded the harness output limit.",
                   "evidence": "get_build_failure_summary: Error: result (76,730 characters across 2,723 lines) exceeds maximum allowed tokens.",
                   "consequence": "Agent repeated the call with logs disabled."}
        self.response["struggles"] = [finding]
        data, _ = self.run_review()
        self.assertEqual(data["struggles"], [finding])
        report = (self.root / "run.klaren-struggles.md").read_text()
        for value in finding.values():
            self.assertIn(value, report)
        self.response["struggles"] = []
        data, _ = self.run_review()
        self.assertEqual(data["struggles"], [])
        self.assertIn("None identified", (self.root / "run.klaren-struggles.md").read_text())
        for invalid in (None, {}, [{**finding, "evidence": " "}], [{"summary": "Slow"}]):
            with self.subTest(invalid=invalid):
                self.response["struggles"] = invalid
                data, _ = self.run_review(returncode=1)
                self.assertIsNone(data["struggles"])

    def test_implemented_fix_requires_complete_diff_review(self):
        self.env["DIFF_STATUS"] = "available"
        (self.root / "diff").write_text("A complete pushed patch.\n")
        self.response["questions"]["fix"]["verdict"] = "sweeet"
        for confirmation in (False, None, True):
            with self.subTest(confirmation=confirmation):
                self.response["diff_review_complete"] = confirmation
                data, _ = self.run_review()
                fix = data["questions"][2]
                self.assertEqual(fix["score"], 1 if confirmation else None)
                self.assertEqual(data["questions"][0]["score"], 0.5)
                if not confirmation:
                    self.assertEqual(fix["verdict"], "undetermined")
                    self.assertNotIn("mcp_eval.klaren.fix", self.metrics())
        # A claimed completed review cannot rescue a missing patch.
        (self.root / "diff").unlink()
        data, _ = self.run_review()
        self.assertEqual(data["diff_status"], "unavailable")
        self.assertEqual(data["questions"][2]["verdict"], "undetermined")

    def test_harness_preserves_large_pushed_diff_as_artifact(self):
        finding = {"summary": "Summary exceeded the harness output limit.",
                   "evidence": "get_build_failure_summary returned an output-limit message without is_error.",
                   "consequence": "Agent requested a compact summary instead."}
        self.response["struggles"] = [finding]
        self.response["diff_review_complete"] = True
        self.response["questions"]["fix"]["verdict"] = "duct-tape"
        self.run_review()  # Prepare the CLI's final review fixture.
        seed = self.root / "seed"
        seed.mkdir()
        def git(*args, cwd=seed):
            return subprocess.check_output(["git", "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid",
                                            "-C", str(cwd), *args], stderr=subprocess.DEVNULL)
        git("init", "-q", "-b", "main")
        (seed / "a-file.txt").write_text("before\n")
        (seed / "z-test.py").write_text("def test_bug(): pass\n")
        git("add", ".")
        git("commit", "-qm", "baseline")
        baseline = git("rev-parse", "HEAD").decode().strip()
        remote = self.root / "remote.git"
        git("clone", "--bare", str(seed), str(remote))
        (self.root / "large-change").write_text(("large change " + "x" * 80 + "\n") * 750)
        (self.root / "skipped-test").write_text('@pytest.mark.skip(reason="hide regression")\ndef test_bug(): pass\n')
        def executable(path, body):
            path.write_text("#!/bin/bash\nset -euo pipefail\n" + body)
            path.chmod(0o755)
        real_git = shutil.which("git")
        executable(self.root / "bin/git", 'if [[ "$1" == clone ]]; then exec "$REAL_GIT" clone "$FIXTURE_REPO" "$3"; fi\nexec "$REAL_GIT" "$@"\n')
        executable(self.root / "bin/gh", "exit 0\n")
        executable(self.root / "bin/yq", 'cat "$STUB_ROOT/matrix.json"\n')
        executable(self.root / "bin/curl", '''
for arg; do url="$arg"; done
if [[ "$url" == https://api.buildkite.com/* ]]; then printf '[{"state":"passed"}]'; exit 0; fi
while [[ "$1" != -o ]]; do shift; done
printf 'Pinned reviewed reference' > "$2"
printf 200
''')
        executable(self.scripts / "claude.sh", '''
if [[ "$ANNOTATION_CONTEXT_PREFIX" == eval-* ]]; then
    cp "$STUB_ROOT/large-change" a-file.txt
    cp "$STUB_ROOT/skipped-test" z-test.py
    git add .
    git -c user.name=Fixture -c user.email=fixture@example.invalid commit -qm 'Change and skip test'
    git push origin HEAD
    printf 'CLAUDE_TRANSCRIPT=%s/transcript.jsonl\nCLAUDE_RESULT_FILE=%s/final.md\nCLAUDE_RAW_LOG=%s/raw.jsonl\n' "$STUB_ROOT" "$STUB_ROOT" "$STUB_ROOT"
else
    cp "$1" "$STUB_PROMPT"
    printf 'CLAUDE_RESULT_FILE=%s/review.md\n' "$STUB_ROOT"
fi
''')
        executable(self.scripts / "bk-eval-compare.sh", "exit 0\n")
        executable(self.root / "bin/buildkite-agent", '''
if [[ "$1" == annotate ]]; then cat >> "$STUB_ROOT/annotations.md"
    printf '%s\n' "$*" >> "$STUB_ROOT/annotation-args"
elif [[ "$1" == artifact && "$2" == upload ]]; then
    mkdir -p "$STUB_ROOT/upload"
    for file in $3; do cp "$file" "$STUB_ROOT/upload/"; done
else exit 1; fi
''')
        matrix_entry = {
            "id": "case", "prompt": "fixture", "scenario": {"id": "leaked-model", "setup":
                'git switch -c scenario\ngit push origin HEAD\nprintf "SCENARIO_BRANCH=scenario\\n" >> "$SCENARIO_VARS_FILE"'},
        }
        (self.root / "evals.yaml").write_text("matrix fixture\n")
        (self.root / "prompts/fixture.md").write_text("Fix case.\n")
        for index, (evaluator, configured, override, signal) in enumerate((
            ("klaren", None, "", "reviewer"),
            ("yilong", "yilong", "", "flagged"),
            ("klaren", "yilong", "klaren", "none"),
            ("yilong", "klaren", "yilong", "retry"),
            ("klaren", None, "", "unavailable"),
        )):
            with self.subTest(evaluator=evaluator, configured=configured, override=override, signal=signal):
                entry = dict(matrix_entry)
                if configured is not None:
                    entry["evaluator"] = configured
                (self.root / "matrix.json").write_text(json.dumps([entry]))
                home = self.root / f"home-{index}"
                home.mkdir()
                (self.root / "annotations.md").write_text("")
                (self.root / "annotation-args").write_text("")
                # An unflagged harness error must not inflate exact failure counts.
                events = [
                    {"type": "assistant", "message": {"id": "one", "content": [
                        {"type": "tool_use", "id": "a", "name": "mcp__buildkite__get_build_failure_summary", "input": {}}]}},
                    {"type": "user", "message": {"content": [
                        {"type": "tool_result", "tool_use_id": "a", "content": "Error: result exceeds maximum allowed tokens."}]}},
                ]
                if signal == "flagged":
                    events[1]["message"]["content"][0]["is_error"] = True
                self.transcript.write_text("\n".join(map(json.dumps, events)) + "\n")
                self.response["struggles"] = [finding] if signal == "reviewer" else []
                self.run_review()
                if signal == "unavailable":
                    (self.root / "review.md").write_text("Reviewer stopped without structured output.")
                (self.root / "raw.jsonl").write_text(json.dumps({
                    "type": "system", "subtype": "api_retry", "error_status": 504,
                    "retry_delay_ms": 600, "attempt": 1, "max_retries": 10,
                }) + "\n" if signal == "retry" else "")
                for directory in ("runs", "upload"):
                    shutil.rmtree(self.root / directory, ignore_errors=True)
                # Reset the remote branch between independent runs.
                git("update-ref", "refs/heads/scenario", baseline, cwd=remote)
                result = subprocess.run([str(self.scripts / "babystand.sh")], cwd=self.root, capture_output=True, text=True,
                                        env={**self.env, "RUN_IN_CI": "true", "LOCAL_CI": "false", "DEBUG_PERMISSIONS": "false",
                                             "EVALUATOR": override,
                                             "HOME": str(home), "REAL_GIT": real_git, "FIXTURE_REPO": str(remote),
                                             "MCP_SRC_SHA": "a" * 40, "DD_API_KEY": ""}, timeout=30)
                self.assertEqual(result.returncode, 0, result.stdout + result.stderr)
                patch = next((self.root / "upload").glob("*.diff.patch"))
                expected = git("diff", baseline, "scenario", cwd=remote)
                self.assertGreater(len(expected), 32768)
                self.assertEqual(patch.read_bytes(), expected)
                self.assertIn(b"@pytest.mark.skip", patch.read_bytes())
                meta = json.loads(next((self.root / "upload").glob("*.dd.json")).read_text())
                self.assertEqual(meta["evaluator"], evaluator)
                struggles = next((self.root / "upload").glob("*.struggles.md")).read_text()
                self.assertIn(f"**Tool calls: 1, explicitly flagged failures: {int(signal == 'flagged')}**", struggles)
                metrics = json.loads(next((self.root / "upload").glob("*.metrics.json")).read_text())
                self.assertTrue(metrics["struggles"]["implemented"])
                annotations = (self.root / "annotations.md").read_text()
                self.assertIn("Eval — struggles", annotations)
                args = next(line for line in (self.root / "annotation-args").read_text().splitlines()
                            if "eval-struggles-case" in line)
                self.assertIn("--style " + ("info" if signal == "none" else "warning"), args)
                if evaluator == "klaren":
                    prompt = (self.root / "prompt.md").read_text()
                    self.assertIn(struggles.split("**Reviewer-observed struggles**")[0].strip(), prompt)
                    self.assertEqual(metrics["reviewer_struggles"],
                                     [finding] if signal == "reviewer" else None if signal == "unavailable" else [])
                    self.assertIn("Reviewer-observed struggles", annotations)
                    if signal == "reviewer":
                        self.assertIn(finding["consequence"], struggles)
                        self.assertIn("1 reviewer finding", annotations)
                    if signal == "unavailable":
                        self.assertIn("reviewer findings unavailable", annotations)
                        self.assertNotIn("None identified", struggles)
                    self.assertIn("Klaren — session review", annotations)
                    original_patch = next((self.root / "runs/case").glob("*.diff.patch"))
                    self.assertIn(str(original_patch), prompt)
                    summary = next((self.root / "upload").glob("*.klaren-verdicts.md")).read_text()
                    self.assertIn(f"artifact://runs/case/{patch.name}", summary)
                else:
                    judge = json.loads(next((self.root / "upload").glob("*.judge.json")).read_text())
                    self.assertEqual((judge["prompt"], judge["scenario"], judge["diff_status"]),
                                     ("fixture", "leaked-model", "available"))
                    self.assertFalse(list((self.root / "upload").glob("*.klaren*")))
                    self.assertNotIn("Klaren — session review", annotations)

    def test_ci_uses_pinned_harness_references(self):
        # The production wrapper still runs; only the external CLI/network edges are fixtures.
        (self.scripts / "claude.sh").write_text('#!/bin/sh\ncp "$1" "$STUB_PROMPT"\nprintf "CLAUDE_RESULT_FILE=%s/review.md\\n" "$STUB_ROOT"\n')
        (self.scripts / "claude.sh").chmod(0o755)
        curl = self.root / "bin/curl"
        curl.write_text('#!/bin/sh\nwhile [ "$1" != "-o" ]; do shift; done\nshift\nout=$1\nfor arg do url=$arg; done\nprintf "%s\\n" "$url" >> "$STUB_ROOT/urls"\nprintf "Pinned reviewed reference" > "$out"\nprintf 200\n')
        curl.chmod(0o755)
        self.env.update(RUN_IN_CI="true", MCP_SRC_SHA="a" * 40, STUB_ROOT=str(self.root))
        self.response["questions"]["conclusion"]["verdict"] = "yeah-baby"
        data, _ = self.run_review()
        self.assertEqual(data["questions"][1]["verdict"], "sherlock")
        self.assertIn("/" + "a" * 40 + "/evals/expected/diagnose/leaked-model/root_cause.md", (self.root / "urls").read_text())
        self.assertIn("Pinned reviewed reference", (self.root / "prompt.md").read_text())
        self.assertNotIn("Reviewed root cause", (self.root / "prompt.md").read_text())


if __name__ == "__main__":
    unittest.main()
