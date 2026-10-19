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
        for name in ("klaren.sh", "bk-tool-audit-v2.sh", "dd-metrics.sh", "dd-publish.sh"):
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
        self.assertEqual([q["score"] for q in data["questions"]], [0.5, 0.5, 0])
        self.assertEqual(data["questions"][1]["verdict"], "scooby-doo")
        self.assertIn("Sherlock requires", result.stdout)
        self.assertIn("Detailed review preserved", (self.root / "run.klaren.md").read_text())
        self.assertIn("human review required", (self.root / "run.klaren-candidate-root_cause.md").read_text())
        self.assertIn("Isolate model", (self.root / "run.klaren-candidate-fix.md").read_text())
        self.assertEqual((self.refs / "root_cause.md").read_text(), "Reviewed root cause: leaked test model.")
        prompt = (self.root / "prompt.md").read_text()
        self.assertIn("Reviewed root cause", prompt)
        self.assertIn("Diagnose and propose", prompt)
        metrics = self.metrics()
        for key, value in (("conclusion", 0.5), ("investigation", 0.5), ("fix", 0)):
            series = metrics[f"mcp_eval.klaren.{key}"]
            self.assertEqual(series["points"][0]["value"], value)
            self.assertIn("evaluator:klaren", series["tags"])
        self.assertFalse(any(".yilong." in key for key in metrics))

    def test_generated_candidates_do_not_replace_missing_references(self):
        shutil.rmtree(self.refs)
        data, result = self.run_review()
        self.assertEqual({q["verdict"] for q in data["questions"]}, {"n/a"})
        self.assertTrue((self.root / "run.klaren-candidate-root_cause.md").exists())
        self.assertIn("No reference answer available", result.stdout)
        self.assertIn("No reference fix available", result.stdout)
        self.assertFalse(self.refs.exists())
        metrics = self.metrics()
        self.assertEqual([key for key in metrics if ".klaren." in key], ["mcp_eval.klaren.undetermined"])
        self.assertEqual(metrics["mcp_eval.klaren.undetermined"]["points"][0]["value"], 0)

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
