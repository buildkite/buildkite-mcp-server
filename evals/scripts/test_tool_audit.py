"""Regression checks for the public struggles report (Python standard library only)."""

import json
from pathlib import Path
import subprocess
import tempfile
import unittest


AUDIT = Path(__file__).with_name("bk-tool-audit-v2.sh")


def transcript(agent, calls):
    events = []
    for i, (command, failed) in enumerate(calls):
        call_id = str(i)
        if agent == "claude":
            events.append({"type": "assistant", "message": {
                "id": f"message-{i}", "content": [{
                    "type": "tool_use", "id": call_id, "name": "Bash",
                    "input": {"command": command},
                }],
            }})
            if failed is not None:
                events.append({"type": "user", "message": {"content": [{
                    "type": "tool_result", "tool_use_id": call_id,
                    "is_error": failed, "content": "Exit code 1\n" if failed else "",
                }]}})
        else:
            tool = {"args": {"command": command}}
            events.append({"type": "tool_call", "subtype": "started",
                           "call_id": call_id, "tool_call": {"shellToolCall": tool}})
            if failed is not None:
                result = ({"failure": {"exitCode": 1, "stdout": "", "stderr": ""}}
                          if failed else {"success": {"exitCode": 0, "stdout": ""}})
                events.append({"type": "tool_call", "subtype": "completed",
                               "call_id": call_id,
                               "tool_call": {"shellToolCall": {**tool, "result": result}}})
    return events


class StrugglesTest(unittest.TestCase):
    def report(self, agent, calls):
        with tempfile.NamedTemporaryFile(mode="w", suffix=".jsonl") as log:
            for event in transcript(agent, calls):
                log.write(json.dumps(event) + "\n")
            log.flush()
            result = subprocess.run(
                ["bash", str(AUDIT), "--agent", agent, "struggles", log.name],
                check=True, capture_output=True, text=True,
            )
        return json.loads(result.stdout)

    def test_harmless_requires_an_unambiguous_shell_command(self):
        cases = [
            ("test -d /missing", True),
            ("grep -q 'no match' /tmp/input", True),
            ("command -v missing-program", True),
            ("test -d /tmp && python3 -c 'raise SystemExit(1)'", False),
            ("test -d /tmp; python3 -c 'raise SystemExit(1)'", False),
            ("test -d /tmp\npython3 -c 'raise SystemExit(1)'", False),
            ("test -d /tmp | python3 -c 'raise SystemExit(1)'", False),
            ("test -d /missing || python3 -c 'raise SystemExit(1)'", False),
            ("command python3 -c 'raise SystemExit(1)'", False),
            ("test -d /tmp > /missing/output", False),
        ]
        for agent in ("claude", "cursor"):
            for command, harmless in cases:
                with self.subTest(agent=agent, command=command):
                    report = self.report(agent, [(command, True)] * 2)
                    self.assertEqual(report["failed_calls"], 2)
                    self.assertEqual(report["likely_harmless"], 2 if harmless else 0)
                    self.assertEqual(report["longest_failure_streak"], 0 if harmless else 2)
                    self.assertEqual(len(report["repeated_failures"]), 0 if harmless else 1)
                    self.assertEqual(report["errors_by_type"].keys(),
                                     {"exit 1 (likely harmless)" if harmless else "exit 1"})

    def test_retry_recovery_requires_a_successful_result(self):
        for agent in ("claude", "cursor"):
            for retry_failed in (None, True, False):
                with self.subTest(agent=agent, retry_failed=retry_failed):
                    report = self.report(agent, [("python3 task.py", True),
                                                  ("python3 task.py", retry_failed)])
                    self.assertEqual(report["tool_calls"], 2)
                    self.assertEqual(report["failed_calls"], 2 if retry_failed else 1)
                    self.assertEqual(report["retries_after_failure"], 1)
                    self.assertEqual(report["recovered_after_retry"],
                                     1 if retry_failed is False else 0)


if __name__ == "__main__":
    unittest.main()
