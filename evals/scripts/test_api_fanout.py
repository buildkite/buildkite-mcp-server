"""Run with python3 evals/scripts/test_api_fanout.py (requires bash and jq)."""

import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).parent


class FanoutTest(unittest.TestCase):
    def test_measurement_availability(self):
        started = {"source": "mcp", "type": "started"}
        request = {"source": "mcp", "method": "GET", "url": "https://api.buildkite.com/v2/user",
                   "status": 200, "tool": "get_build", "tool_call_id": "one"}
        # A real request also proves logging worked for older logs without a marker.
        for records, expected in ((None, None), ([], None), ([started], 0),
                                  ([request], 1), ([started, request], 1)):
            with self.subTest(records=records), tempfile.TemporaryDirectory() as tmp:
                log = Path(tmp) / "requests.jsonl"
                if records is not None:
                    log.write_text("".join(json.dumps(r) + "\n" for r in records))
                metrics = Path(tmp) / "metrics.json"
                metrics.write_text(json.dumps({"tool_calls_by_name": [
                    "2 mcp__buildkite-mcp-server__get_build"]}))
                result = subprocess.run(
                    ["bash", str(SCRIPTS / "api-fanout.sh"), "json", str(log), str(metrics)],
                    check=True, capture_output=True, text=True,
                )
                fanout = json.loads(result.stdout)
                self.assertEqual(fanout.get("available"), expected is not None)
                self.assertEqual(fanout.get("total_requests"), expected)
                if expected is None:
                    self.assertIsNone(fanout.get("errors"))
                else:
                    self.assertEqual(fanout["mcp"]["tools"][0]["calls"], 2)
                    self.assertEqual(fanout["mcp"]["tools"][0]["requests"], expected)
                    self.assertEqual(fanout["mcp"]["tools"][0]["avg_per_call"], expected / 2)
                summary = Path(tmp) / "fanout.json"
                summary.write_text(result.stdout)
                for command in ("markdown", "errors-markdown"):
                    markdown = subprocess.run(
                        ["bash", str(SCRIPTS / "api-fanout.sh"), command, str(summary)],
                        check=True, capture_output=True, text=True,
                    ).stdout
                    if expected is None:
                        self.assertIn("not available", markdown.lower())
                        self.assertNotIn("every API request got a successful response", markdown)
                metrics.write_text(json.dumps({"api_fanout": fanout}))
                payload = subprocess.run(
                    ["bash", str(SCRIPTS / "dd-metrics.sh")],
                    env={**os.environ, "DD_DRY_RUN": "true", "METRICS_FILE": str(metrics)},
                    check=True, capture_output=True, text=True,
                )
                series = {s["metric"]: s["points"][0]["value"]
                          for s in json.loads(payload.stdout)["series"]}
                if expected is None:
                    self.assertNotIn("mcp_eval.api_requests.total", series)
                    self.assertNotIn("mcp_eval.api_requests.mcp", series)
                else:
                    self.assertEqual(series["mcp_eval.api_requests.total"], expected)


if __name__ == "__main__":
    unittest.main()
