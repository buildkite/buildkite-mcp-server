"""Run with: python3 -m unittest discover -s evals/scripts -p 'test_yilong.py'."""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from threading import Thread


class YilongTest(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        scripts = self.root / "scripts"
        scripts.mkdir()
        source = Path(__file__).resolve().parent
        for name in ("yilong.sh", "bk-tool-audit-v2.sh", "dd-metrics.sh"):
            shutil.copy2(source / name, scripts / name)
        shutil.copytree(source.parent / "prompts", self.root / "prompts")
        self.reference = self.root / "expected" / "case"
        self.reference.mkdir(parents=True)
        (self.reference / "root_cause.md").write_text("A leaked test model breaks UUID search.")
        (self.reference / "fix.md").write_text("Isolate the test model; preserve the assertion.")
        self.judge = self.root / "run.judge.json"
        self.judge.write_text(json.dumps({"entry": "case", "goal": "Diagnose the failed build", "agent": "claude"}))
        (self.root / "run.eval-final.md").write_text("The UUID search fails; isolate the test model.")
        self.transcript = self.root / "run.transcript.jsonl"
        self.transcript.write_text("\n".join(json.dumps(row) for row in [
            {"message": {"content": [{"type": "tool_use", "id": "a", "name": "get_logs", "input": {"job": "failing"}}]}},
            {"message": {"content": [{"type": "tool_result", "tool_use_id": "a", "content": "NameError: SoftDestroyableModel bkua_abcdefghijklmnop"}]}},
        ]))
        (self.root / "run.tools.txt").write_text('{"tool":"get_logs","input":{"job":"failing"}}\n')
        self.conclusion_level = 0
        self.requests = []
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                owner.requests.append(body)
                key = next(iter(body["questions"]))
                level = owner.conclusion_level if key == "conclusion" else 2
                answer = {"probabilities": {str(i): 1 if i == level else 0 for i in range(3)},
                          "score": level, "confidence": 1}
                self.send_response(200)
                self.end_headers()
                self.wfile.write(json.dumps({"answers": {key: answer}}).encode())

        server = ThreadingHTTPServer(("127.0.0.1", 0), Handler)
        self.addCleanup(server.server_close)
        thread = Thread(target=server.serve_forever, daemon=True)
        thread.start()
        self.addCleanup(thread.join)
        self.addCleanup(server.shutdown)
        self.env = {**os.environ, "MCP_EVAL_FRAMEWORK_JEV_TOKEN": "test-token",
                    "YILONG_API_URL": f"http://127.0.0.1:{server.server_port}", "YILONG_DRY_RUN": "false"}

    def run_judge(self):
        result = subprocess.run([str(self.root / "scripts/yilong.sh"), str(self.judge)],
                                env=self.env, text=True, capture_output=True, check=True)
        questions = json.loads((self.root / "run.yilong.json").read_text())["questions"]
        return {q["key"]: q for q in questions}, result

    def test_no_reference_is_explicit_and_publishes_no_scores(self):
        for name in ("root_cause.md", "fix.md"):
            (self.reference / name).unlink()
        questions, result = self.run_judge()
        self.assertEqual({q["verdict"] for q in questions.values()}, {"n/a"})
        self.assertEqual(self.requests, [])
        self.assertIn("No reference answer available: add evals/expected/case/root_cause.md", result.stdout)
        self.assertIn("No reference fix available: add evals/expected/case/fix.md", result.stderr)
        metrics = subprocess.run([str(self.root / "scripts/dd-metrics.sh")], check=True,
                                 env={**self.env, "DD_DRY_RUN": "true", "ENTRY_ID": "case",
                                      "EVALUATOR": "yilong", "YILONG_FILE": str(self.root / "run.yilong.json")},
                                 text=True, capture_output=True)
        series = json.loads(metrics.stdout)["series"]
        self.assertEqual([s["metric"] for s in series if ".yilong." in s["metric"]],
                         ["mcp_eval.yilong.undetermined"])
        self.assertEqual(next(s for s in series if s["metric"].endswith(".undetermined"))["points"][0]["value"], 0)

    def test_blank_reference_and_missing_results_skip_investigation(self):
        (self.reference / "root_cause.md").write_text(" \n\t")
        questions, _ = self.run_judge()
        self.assertEqual(questions["investigation"]["verdict"], "n/a")
        self.assertEqual(questions["fix"]["verdict"], "sweeet")
        (self.reference / "root_cause.md").write_text("Leaked test model.")
        self.transcript.write_text(self.transcript.read_text().splitlines()[0] + "\n")
        questions, result = self.run_judge()
        self.assertEqual(questions["investigation"]["verdict"], "n/a")
        self.assertIn("No captured tool results", result.stderr)

    def test_sherlock_requires_correct_conclusion_and_receives_tool_results(self):
        for level, verdict in ((0, "scooby-doo"), (1, "scooby-doo"), (2, "sherlock")):
            with self.subTest(conclusion_level=level):
                self.conclusion_level = level
                questions, _ = self.run_judge()
                self.assertEqual(questions["investigation"]["verdict"], verdict)
                self.assertEqual(questions["investigation"]["score"], 1 if level == 2 else 0.5)
        state = next(r for r in self.requests if "investigation" in r["questions"])["state"]
        self.assertEqual(state["expected_root_cause"], "A leaked test model breaks UUID search.")
        self.assertIn("NameError: SoftDestroyableModel", state["trace"])
        self.assertNotIn("abcdefghijklmnop", state["trace"])

    def test_cursor_results_reach_investigation(self):
        for agent in ("cursor", "cursor-cloud"):
            with self.subTest(agent=agent):
                self.judge.write_text(json.dumps({"entry": "case", "goal": "Diagnose build", "agent": agent}))
                rows = []
                for status in ("started", "completed"):
                    payload = {"args": {"command": "cat failing.log"}}
                    if status == "completed":
                        payload["result"] = {"success": "NameError: leaked model"}
                    if agent == "cursor":
                        rows.append({"type": "tool_call", "subtype": status, "call_id": "a",
                                     "tool_call": {"shellToolCall": payload}})
                    else:
                        rows.append({"type": "tool_call", "data": {"callId": "a", "name": "shell",
                                     "status": status, **payload}})
                self.transcript.write_text("\n".join(map(json.dumps, rows)))
                questions, _ = self.run_judge()
                self.assertNotEqual(questions["investigation"]["verdict"], "n/a")
                state = self.requests[-2]["state"]
                self.assertIn("NameError: leaked model", state["trace"])
                self.assertIn("cat failing.log", state["trace"])


if __name__ == "__main__":
    unittest.main()
