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
        for name in ("yilong.sh", "yilong-summarize.py", "bk-tool-audit-v2.sh", "dd-metrics.sh", "dd-publish.sh"):
            shutil.copy2(source / name, scripts / name)
        shutil.copytree(source.parent / "prompts", self.root / "prompts")
        self.reference = self.root / "expected" / "diagnose" / "leaked-model"
        self.reference.mkdir(parents=True)
        (self.reference / "root_cause.md").write_text("A leaked test model breaks UUID search.")
        (self.reference / "fix.md").write_text("Isolate the test model; preserve the assertion.")
        self.judge = self.root / "run.judge.json"
        self.metadata = {"entry": "case", "prompt": "diagnose", "scenario": "leaked-model",
                         "goal": "Diagnose the failed build", "agent": "claude", "diff_status": "not_applicable"}
        self.judge.write_text(json.dumps(self.metadata))
        (self.root / "run.eval-final.md").write_text("The UUID search fails; isolate the test model.")
        self.transcript = self.root / "run.transcript.jsonl"
        self.transcript.write_text("\n".join(json.dumps(row) for row in [
            {"message": {"content": [{"type": "tool_use", "id": "a", "name": "get_logs", "input": {"job": "failing"}}]}},
            {"message": {"content": [{"type": "tool_result", "tool_use_id": "a", "content": "NameError: SoftDestroyableModel bkua_abcdefghijklmnop"}]}},
        ]))
        (self.root / "run.tools.txt").write_text('{"tool":"get_logs","input":{"job":"failing"}}\n')
        self.conclusion_level = 0
        self.status = 200
        self.answer = None
        self.requests = []
        self.headers = []
        self.reject_default = False
        self.reject_long = False
        self.reject_summary = False
        self.fallback_key = "investigation"
        self.haiku_requests = []
        self.haiku_keys = []
        self.haiku_stop = "end_turn"
        self.haiku_text = "Observed a NameError; proposed isolation but verification is missing. sk-ant-abcdefghijklmnop"
        owner = self

        class Handler(BaseHTTPRequestHandler):
            def log_message(self, *_):
                pass

            def do_POST(self):
                body = json.loads(self.rfile.read(int(self.headers["Content-Length"])))
                if self.path.endswith("/v1/messages"):
                    owner.haiku_requests.append(body)
                    owner.haiku_keys.append(self.headers.get("x-api-key"))
                    self.send_response(200)
                    self.end_headers()
                    self.wfile.write(json.dumps({"stop_reason": owner.haiku_stop,
                                                "content": [{"type": "text", "text": owner.haiku_text}],
                                                "usage": {"input_tokens": 100, "output_tokens": 20}}).encode())
                    return
                owner.requests.append(body)
                owner.headers.append(dict(self.headers))
                if owner.status != 200:
                    self.send_response(owner.status)
                    self.end_headers()
                    self.wfile.write(b'{"detail":{"error_type":"invalid_request"}}')
                    return
                key = next(iter(body["questions"]))
                if key == owner.fallback_key:
                    long = self.headers.get("TypeSafe-Danger-Zone-Long-Context") == "true"
                    summarized = "evidence_notice" in body["state"]
                    if ((summarized and owner.reject_summary)
                            or (not summarized and (owner.reject_long if long else owner.reject_default))):
                        self.send_response(400)
                        self.end_headers()
                        self.wfile.write(b'{"detail":{"error_type":"max_tokens_exceeded"}}')
                        return
                level = owner.conclusion_level if key == "conclusion" else 2
                answer = {"probabilities": {str(i): 1 if i == level else 0 for i in range(3)},
                          "score": level, "confidence": 1}
                if owner.answer is not None:
                    answer = owner.answer
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
                    "YILONG_API_URL": f"http://127.0.0.1:{server.server_port}", "YILONG_DRY_RUN": "false",
                    "ANTHROPIC_BASE_URL": f"http://127.0.0.1:{server.server_port}", "ANTHROPIC_API_KEY": "haiku-test-key"}

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
        self.assertIn("No reference answer available: add evals/expected/diagnose/leaked-model/root_cause.md", result.stdout)
        self.assertIn("No reference fix available: add evals/expected/diagnose/leaked-model/fix.md", result.stderr)
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

    def test_original_grades_are_retained_and_receive_tool_results(self):
        for level in (0, 1, 2):
            with self.subTest(conclusion_level=level):
                self.conclusion_level = level
                questions, _ = self.run_judge()
                self.assertEqual(questions["investigation"]["verdict"], "sherlock")
                self.assertEqual(questions["investigation"]["score"], 1)
        state = next(r for r in self.requests if "investigation" in r["questions"])["state"]
        self.assertEqual(state["expected_root_cause"], "A leaked test model breaks UUID search.")
        self.assertIn("NameError: SoftDestroyableModel", state["trace"])
        self.assertNotIn("abcdefghijklmnop", state["trace"])

    def test_cursor_results_reach_investigation(self):
        for agent in ("cursor", "cursor-cloud"):
            with self.subTest(agent=agent):
                self.judge.write_text(json.dumps({**self.metadata, "agent": agent}))
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

    def test_complete_evidence_and_shared_rubric_reach_api(self):
        # The decisive evidence is in the middle and past every former cutoff.
        result_text = "a" * 40000 + "DECISIVE EVIDENCE" + "z" * 40000
        self.transcript.write_text(json.dumps({"message": {"content": [
            {"type": "tool_use", "id": "a", "name": "get_logs", "input": {}},
            {"type": "tool_result", "tool_use_id": "a", "content": result_text},
        ]}}))
        patch = "a" * 40000 + "TEST SKIP" + "z" * 40000
        (self.root / "run.diff.patch").write_text(patch)
        self.judge.write_text(json.dumps({**self.metadata, "diff_status": "available"}))
        questions, result = self.run_judge()
        self.assertEqual(questions["fix"]["verdict"], "sweeet")
        trace = self.requests[1]["state"]["trace"]
        self.assertEqual(json.loads(trace.split("\n", 1)[1])["result"], result_text)
        self.assertTrue(self.requests[2]["state"]["fix"].endswith(patch))
        rubric = json.loads((self.root / "prompts/rubric.json").read_text())
        self.assertEqual(self.requests[2]["questions"]["fix"]["criteria"],
                         [level["description"] for level in rubric["questions"][2]["levels"]])
        self.assertIn("artifact://runs/case/run.diff.patch", result.stdout)

    def test_missing_patch_never_falls_back_to_claimed_fix(self):
        self.judge.write_text(json.dumps({**self.metadata, "diff_status": "available"}))
        questions, result = self.run_judge()
        self.assertEqual(questions["fix"]["verdict"], "undetermined")
        self.assertIsNone(questions["fix"].get("score"))
        self.assertIn("Complete pushed diff unavailable", result.stdout)
        self.assertEqual(len(self.requests), 2)
        # An empty, captured patch is evidence of no changes, not missing evidence.
        (self.root / "run.diff.patch").write_text("")
        self.run_judge()
        self.assertIn("empty patch means no changes", self.requests[-1]["state"]["fix"])

    def test_unrelated_api_errors_do_not_trigger_context_fallbacks(self):
        self.status = 400
        questions, result = self.run_judge()
        self.assertEqual({q["verdict"] for q in questions.values()}, {"undetermined"})
        self.assertTrue(all(q["score"] is None for q in questions.values()))
        self.assertIn("invalid_request", result.stdout)
        self.assertIn("evidence not truncated", result.stdout)
        self.assertEqual(len(self.requests), 3)
        self.assertEqual(self.haiku_requests, [])

    def test_token_limit_retries_identical_full_request_with_long_context(self):
        self.reject_default = True
        questions, result = self.run_judge()
        self.assertEqual(questions["investigation"]["verdict"], "sherlock")
        self.assertEqual(questions["investigation"]["evidence_mode"], "full")
        self.assertEqual(questions["investigation"]["context_mode"], "long")
        self.assertNotIn("\\*", result.stdout)
        self.assertEqual(self.requests[1], self.requests[2])
        self.assertNotIn("TypeSafe-Danger-Zone-Long-Context", self.headers[1])
        self.assertEqual(self.headers[2]["TypeSafe-Danger-Zone-Long-Context"], "true")
        self.assertEqual(len(self.requests), 4)
        self.assertEqual(self.haiku_requests, [])

    def oversized_trace(self):
        evidence = "start witness\n" + "a🦊" * 14000 + "\nMIDDLE CONTRADICTION\n" + "z" * 55000 + "\nfinal verification failed"
        self.transcript.write_text(json.dumps({"message": {"content": [
            {"type": "tool_use", "id": "a", "name": "get_logs", "input": {}},
            {"type": "tool_result", "tool_use_id": "a", "content": evidence},
        ]}}))
        self.reject_default = True
        self.reject_long = True

    def test_oversized_evidence_is_summarized_completely_and_marked(self):
        self.oversized_trace()
        questions, result = self.run_judge()
        grade = questions["investigation"]
        self.assertEqual(grade["score"], 1)
        self.assertEqual(grade["evidence_mode"], "summarized")
        self.assertIn("Based on summarized evidence", grade["reason"])
        self.assertIn("**Some verdicts are based on summarized evidence", result.stdout)
        self.assertIn("| Quality of investigation | **Sherlock**\\* |", result.stdout)
        self.assertIn("| Correctness of conclusion | **Bollocks** |", result.stdout)
        self.assertIn("| Correctness of fix | **Sweeet** |", result.stdout)
        self.assertIn("\\* Jev evaluated Haiku-summarized evidence (lossy)", result.stdout)
        original = json.loads((self.root / grade["evidence_artifact"]).read_text())
        summary = json.loads((self.root / grade["summary_artifact"]).read_text())
        self.assertEqual(original, self.requests[1])
        self.assertEqual(summary, self.requests[3])
        for field in ("goal", "expected_root_cause", "conclusion"):
            self.assertEqual(summary["state"][field], original["state"][field])
        self.assertEqual(summary["questions"], original["questions"])
        # Reconstruct the actual chunks independently: no head/tail truncation,
        # middle omissions, lost UTF-8 bytes, or reference answers sent to Haiku.
        pieces = [r["messages"][0]["content"].split("\n\n", 1)[1] for r in self.haiku_requests]
        self.assertGreater(len(pieces), 1)
        self.assertEqual("".join(pieces), original["state"]["trace"])
        self.assertNotIn("A leaked test model breaks UUID search.", json.dumps(self.haiku_requests))
        self.assertNotIn("abcdefghijklmnop", json.dumps(summary))
        self.assertEqual(grade["summarization"]["calls"], len(pieces))
        self.assertIn("artifact://runs/case/run.yilong-investigation.summary.json", result.stdout)
        self.assertEqual(questions["conclusion"]["evidence_mode"], "full")
        self.assertEqual(questions["fix"]["evidence_mode"], "full")

    def test_fix_summary_preserves_reference_and_covers_late_test_deletion(self):
        self.reject_default = self.reject_long = True
        self.fallback_key = "fix"
        patch = "+ actual code\n" + " unchanged context\n" * 5000 + "- assert_valid_result()\n+ pytest.skip()\n"
        (self.root / "run.diff.patch").write_text(patch)
        self.judge.write_text(json.dumps({**self.metadata, "diff_status": "available", "final_build_state": "passed"}))
        questions, _ = self.run_judge()
        grade = questions["fix"]
        self.assertEqual(grade["evidence_mode"], "summarized")
        original = json.loads((self.root / grade["evidence_artifact"]).read_text())
        summary = json.loads((self.root / grade["summary_artifact"]).read_text())
        for field in ("expected", "outcome"):
            self.assertEqual(summary["state"][field], original["state"][field])
        evidence = "".join(r["messages"][0]["content"].split("\n\n", 1)[1] for r in self.haiku_requests)
        self.assertTrue(evidence.endswith(patch))
        self.assertNotIn("Isolate the test model; preserve the assertion.", json.dumps(self.haiku_requests))

    def test_chunk_summaries_are_reduced_without_dropping_parts(self):
        self.oversized_trace()
        self.transcript.write_text(self.transcript.read_text().replace("final verification failed", "z" * 300000))
        self.haiku_text = "observations " * 600
        questions, _ = self.run_judge()
        self.assertEqual(questions["investigation"]["evidence_mode"], "summarized")
        payloads = [r["messages"][0]["content"].split("\n\n", 1)[1] for r in self.haiku_requests]
        # These chunk summaries need another pass. Its input must include all
        # of them, regardless of chunk boundaries or the number of chunks.
        first_pass = [text for text in payloads if "observations" not in text]
        reductions = [text for text in payloads if "observations" in text]
        self.assertTrue(reductions)
        self.assertEqual("".join(reductions).count(self.haiku_text.strip()), len(first_pass))

    def test_unusable_summary_or_rejected_summary_withholds_score(self):
        self.oversized_trace()
        for stop, text, reject in (("max_tokens", "partial summary", False),
                                   ("end_turn", "", False),
                                   ("end_turn", None, False),
                                   ("end_turn", "x" * 130000, False),
                                   ("end_turn", "valid concise summary", True)):
            with self.subTest(stop=stop, reject=reject):
                self.haiku_stop, self.haiku_text, self.reject_summary = stop, text, reject
                questions, result = self.run_judge()
                self.assertEqual(questions["investigation"]["verdict"], "undetermined")
                self.assertIsNone(questions["investigation"]["score"])
                self.assertIn("grade withheld", result.stdout)

    def test_invalid_haiku_credentials_never_appear_in_diagnostics(self):
        self.oversized_trace()
        sentinel = "synthetic-credential-not-a-real-token"
        for credential in ("ANTHROPIC_API_KEY", "BUILDKITE_AGENT_ACCESS_TOKEN"):
            with self.subTest(credential=credential):
                self.env["ANTHROPIC_BASE_URL"] = self.env["YILONG_API_URL"] if credential == "ANTHROPIC_API_KEY" else ""
                self.env["ANTHROPIC_API_KEY"] = ""
                self.env["BUILDKITE_AGENT_ENDPOINT"] = self.env["YILONG_API_URL"]
                self.env[credential] = sentinel + "\n"
                questions, result = self.run_judge()
                grade = questions["investigation"]
                self.assertEqual(grade["verdict"], "undetermined")
                self.assertIsNone(grade["score"])
                self.assertNotIn(sentinel, result.stdout + result.stderr)
                for artifact in self.root.glob("run.*"):
                    self.assertNotIn(sentinel, artifact.read_text(), artifact.name)
                self.assertIn("Haiku client or data error", grade["reason"])
        self.assertEqual(self.haiku_requests, [])

    def test_missing_haiku_credentials_withhold_score(self):
        self.oversized_trace()
        for name in ("ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "BUILDKITE_AGENT_ENDPOINT", "BUILDKITE_AGENT_ACCESS_TOKEN"):
            self.env.pop(name, None)
        questions, result = self.run_judge()
        self.assertEqual(questions["investigation"]["verdict"], "undetermined")
        self.assertIsNone(questions["investigation"]["score"])
        self.assertIn("Haiku summarization failed", result.stdout)
        self.assertIn("Haiku requires Buildkite Hosted Models credentials", result.stdout)
        self.assertEqual(self.haiku_requests, [])

    def test_invalid_probabilities_withhold_grades_and_metrics(self):
        for probabilities in ({}, {"0": 0, "1": 1}, [0, 0, 1],
                              {"0": 0, "1": "0", "2": 1},
                              {"0": -0.1, "1": 0, "2": 1.1},
                              {"0": 0, "1": 0, "2": 0},
                              {"0": 0.2, "1": 0.2, "2": 0.2},
                              {"0": 0, "1": 0, "2": 1, "3": 0}):
            with self.subTest(probabilities=probabilities):
                self.answer = {"probabilities": probabilities, "score": 2, "confidence": 1}
                questions, result = self.run_judge()
                for question in questions.values():
                    self.assertEqual(question["verdict"], "undetermined")
                    self.assertIsNone(question["score"])
                    self.assertIsNone(question["expected_score"])
                self.assertIn("Invalid Jev probability distribution", result.stdout)
        metrics = subprocess.run([str(self.root / "scripts/dd-metrics.sh")], check=True,
                                 env={**self.env, "DD_DRY_RUN": "true", "ENTRY_ID": "case",
                                      "EVALUATOR": "yilong", "YILONG_FILE": str(self.root / "run.yilong.json")},
                                 text=True, capture_output=True)
        scores = {s["metric"]: s["points"][0]["value"]
                  for s in json.loads(metrics.stdout)["series"] if ".yilong." in s["metric"]}
        self.assertEqual(scores, {"mcp_eval.yilong.undetermined": 3})

    def test_most_probable_grade_uses_lower_level_on_ties(self):
        for probabilities, score in (({"0": 0, "1": 0.5, "2": 0.5}, 0.5),
                                     ({"0": 0.5, "1": 0.5, "2": 0}, 0),
                                     ({"0": 0.1, "1": 0.2, "2": 0.7}, 1),
                                     ({"0": 0.3333333, "1": 0.3333334, "2": 0.3333333}, 0.5)):
            with self.subTest(probabilities=probabilities):
                self.answer = {"probabilities": probabilities, "score": 1, "confidence": 0.5}
                questions, _ = self.run_judge()
                self.assertEqual([q["score"] for q in questions.values()], [score] * 3)

    def test_references_follow_prompt_scenario_not_entry(self):
        self.judge.write_text(json.dumps({**self.metadata, "entry": "another-model"}))
        questions, _ = self.run_judge()
        self.assertEqual(questions["conclusion"]["verdict"], "bollocks")
        for change in ({"scenario": "other"}, {"prompt": "other"}, {"scenario": ""}):
            with self.subTest(change=change):
                self.requests.clear()
                self.judge.write_text(json.dumps({**self.metadata, **change}))
                questions, _ = self.run_judge()
                self.assertEqual({q["verdict"] for q in questions.values()}, {"n/a"})
                self.assertEqual(self.requests, [])

    def test_ci_downloads_patch_and_publishes_verdict_metrics(self):
        stub = self.root / "bin"
        stub.mkdir()
        agent = stub / "buildkite-agent"
        agent.write_text('''#!/bin/bash
set -euo pipefail
if [[ "$1" == artifact && "$2" == download ]]; then
    mkdir -p "$4/runs/case"
    pattern=${3##*/}
    for file in "$STUB_ROOT"/$pattern; do
        [[ ! -f "$file" ]] || cp "$file" "$4/runs/case/"
    done
elif [[ "$1" == artifact && "$2" == upload ]]; then
    cp "$3" "$STUB_ROOT/"
elif [[ "$1" == annotate ]]; then
    cat > "$STUB_ROOT/annotation.md"
else exit 1; fi
''')
        agent.chmod(0o755)
        patch = "complete patch with final hunk\n"
        (self.root / "run.diff.patch").write_text(patch)
        self.judge.write_text(json.dumps({**self.metadata, "diff_status": "available"}))
        (self.root / "run.dd.json").write_text(json.dumps({"entry": "case", "evaluator": "yilong"}))
        self.oversized_trace()
        env = {**self.env, "PATH": f"{stub}:{os.environ['PATH']}", "STUB_ROOT": str(self.root),
               "ANTHROPIC_BASE_URL": "", "ANTHROPIC_API_KEY": "",
               "BUILDKITE_AGENT_ENDPOINT": self.env["YILONG_API_URL"], "BUILDKITE_AGENT_ACCESS_TOKEN": "hosted-job-token"}
        subprocess.run([str(self.root / "scripts/yilong.sh")], env=env, capture_output=True, text=True, check=True)
        self.assertEqual(set(self.haiku_keys), {"hosted-job-token"})
        self.assertTrue(self.requests[-1]["state"]["fix"].endswith(patch))
        self.assertIn("artifact://runs/case/run.diff.patch", (self.root / "annotation.md").read_text())
        self.assertTrue((self.root / "run.yilong-investigation.evidence.json").is_file())
        self.assertTrue((self.root / "run.yilong-investigation.summary.json").is_file())
        self.assertIn("based on summarized evidence", (self.root / "annotation.md").read_text())
        result = subprocess.run([str(self.root / "scripts/dd-publish.sh")],
                                env={**env, "DD_DRY_RUN": "true", "DD_API_KEY": "fixture"},
                                capture_output=True, text=True, check=True)
        payload, _ = json.JSONDecoder().raw_decode(result.stdout[result.stdout.index("{"):])
        scores = {s["metric"]: s["points"][0]["value"] for s in payload["series"] if ".yilong." in s["metric"]}
        self.assertEqual(scores, {"mcp_eval.yilong.conclusion": 0, "mcp_eval.yilong.investigation": 1,
                                  "mcp_eval.yilong.fix": 1, "mcp_eval.yilong.undetermined": 0})


if __name__ == "__main__":
    unittest.main()
