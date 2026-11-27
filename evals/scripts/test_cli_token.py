"""Run with python3 -m unittest discover -s evals/scripts -p 'test_cli_token.py'.

Requires bash, jq and yq. No agent or network access is used.
"""

import json
import os
from contextlib import contextmanager
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).parent


class CliTokenTest(unittest.TestCase):
    @contextmanager
    def harness(self):
        with tempfile.TemporaryDirectory() as tmp:
            root = Path(tmp)
            scripts = root / "scripts"
            scripts.mkdir()
            shutil.copy(SCRIPTS / "babystand.sh", scripts)
            bin_dir = root / "bin"
            bin_dir.mkdir()
            # Avoid GitHub authentication and cloning; unexpected git
            # operations fail rather than reaching the network.
            for name, body in (
                ("gh", '[[ "$*" == "auth setup-git" ]]'),
                ("git", '[[ "$1" == "clone" ]] || exit 1\nmkdir -p "$3"'),
            ):
                path = bin_dir / name
                path.write_text("#!/bin/bash\nset -eu\n" + body + "\n")
                path.chmod(0o755)
            env = {k: v for k, v in os.environ.items()
                   if k not in ("BUILDKITE_API_TOKEN", "ENTRY_TEST_TOKEN", "DD_API_KEY")}
            env.update(HOME=tmp, EVALS_CONFIG=str(root / "evals.json"), RUN_IN_CI="true",
                       LOCAL_CI="false", DEBUG_PERMISSIONS="false",
                       LOCAL_BYPASS_PERMISSION="true",
                       PATH=str(bin_dir) + os.pathsep + env["PATH"])
            yield root, env

    def run_harness(self, root, env, entry):
        (root / "evals.json").write_text(json.dumps({"matrix": [entry]}))
        return subprocess.run(["bash", str(root / "scripts/babystand.sh")],
                              env=env, capture_output=True, text=True, timeout=15)

    def test_cli_requires_explicit_token_before_setup(self):
        for named, ambient, token, allowed in (
            (False, None, None, False),
            (False, "", None, False),
            (True, "ambient-test-token", None, False),
            (True, "ambient-test-token", "", False),
            (False, "ambient-test-token", None, True),
            (True, "", "entry-test-token", True),
        ):
            with self.subTest(named=named, ambient=ambient, token=token), \
                    self.harness() as (root, env):
                entry = {
                    "id": "token-check", "agent": "claude", "mode": "cli",
                    # Positive controls stop safely at CLI setup, before any
                    # download or scenario can run.
                    "mode_params": {"download_url": "http://invalid.test/bk"},
                }
                if named:
                    entry["token_env"] = "ENTRY_TEST_TOKEN"
                if ambient is not None:
                    env["BUILDKITE_API_TOKEN"] = ambient
                if token is not None:
                    env["ENTRY_TEST_TOKEN"] = token
                result = self.run_harness(root, env, entry)
                output = result.stdout + result.stderr
                if allowed:
                    self.assertEqual(result.returncode, 1, output)
                    self.assertIn("download_url must be an https:// URL", output)
                else:
                    self.assertEqual(result.returncode, 0, output)
                    self.assertIn("unset or empty; skipping", output)
                    self.assertNotIn("bk CLI setup", output)
                    self.assertFalse((root / "runs").exists())

    def test_mcp_toolsets_reach_the_agent(self):
        for agent, toolsets, expected in (
            ("claude", None, "all"),
            ("claude", "user,builds,logs", "user,builds,logs"),
            ("claude", "all; rm -rf /", None),
            ("claude", "user,bilds", None),
            ("cursor-cloud", "user,builds,logs", "user,builds,logs"),
        ):
            with self.subTest(agent=agent, toolsets=toolsets), self.harness() as (root, env):
                # The stub agent records the toolsets it was given, then fails
                # so the harness stops before auditing.
                (root / "prompts").mkdir()
                (root / "prompts/p.md").write_text("prompt\n")
                for path, body in (
                    (root / "scripts/claude.sh", 'echo "$BUILDKITE_TOOLSETS" > "$TOOLSETS_OUT"; exit 1'),
                    (root / "scripts/cursor-cloud.sh", 'echo "$BUILDKITE_TOOLSETS" > "$TOOLSETS_OUT"; exit 1'),
                    # Rejects an unknown name like the real server does.
                    (root / "bin/buildkite-mcp-server",
                     '[[ "${BUILDKITE_TOOLSETS:-}" != *bilds* ]] '
                     '|| { echo "error: invalid toolset names" >&2; exit 1; }\necho dev'),
                ):
                    path.write_text("#!/bin/bash\n" + body + "\n")
                    path.chmod(0o755)
                env.update(BUILDKITE_API_TOKEN="entry-test-token", CURSOR_API_KEY="test",
                           TOOLSETS_OUT=str(root / "toolsets"), TMPDIR=str(root))
                entry = {"id": "toolsets-check", "agent": agent, "prompt": "p",
                         "mode_params": {"toolsets": toolsets} if toolsets else {}}
                result = self.run_harness(root, env, entry)
                output = result.stdout + result.stderr
                if expected:
                    self.assertEqual((root / "toolsets").read_text().strip(), expected, output)
                else:
                    self.assertIn("toolsets", output)
                    if toolsets == "user,bilds":
                        self.assertIn("invalid toolset names", output)
                    self.assertIn("skipping", output)
                    self.assertFalse((root / "toolsets").exists(), output)

    def test_cursor_cloud_sends_toolsets_to_its_mcp_server(self):
        for toolsets, expected in ((None, "all"), ("user,builds,logs", "user,builds,logs")):
            with self.subTest(toolsets=toolsets), tempfile.TemporaryDirectory() as tmp:
                root = Path(tmp)
                (root / "scripts").mkdir()
                (root / "prompts").mkdir()
                shutil.copy(SCRIPTS / "cursor-cloud.sh", root / "scripts")
                shutil.copy(SCRIPTS.parent / "mcp_cursor_cloud.json", root)
                (root / "prompts/system.md").write_text("system\n")
                (root / "prompt.md").write_text("prompt\n")
                # The stub curl saves the launch request and fails it, so the
                # script stops before reaching Cursor.
                curl = root / "curl"
                curl.write_text('#!/bin/bash\nwhile [[ $# -gt 0 ]]; do [[ "$1" == "-d" ]] && printf "%s" "$2" > "$PAYLOAD_OUT"; shift; done\nprintf "error\\n500"\n')
                curl.chmod(0o755)
                env = {k: v for k, v in os.environ.items() if k != "BUILDKITE_TOOLSETS"}
                env.update(PATH=str(root) + os.pathsep + env["PATH"], CURSOR_API_KEY="test",
                           BUILDKITE_API_TOKEN="entry-test-token", MCP_SRC_SHA="abc123",
                           PAYLOAD_OUT=str(root / "payload.json"))
                if toolsets:
                    env["BUILDKITE_TOOLSETS"] = toolsets
                result = subprocess.run(["bash", str(root / "scripts/cursor-cloud.sh"), str(root / "prompt.md"),
                                         "--repo", "https://github.com/example/repo"],
                                        env=env, capture_output=True, text=True, timeout=15)
                self.assertIn("launch failed", result.stderr)
                server = json.loads((root / "payload.json").read_text())["mcpServers"][0]
                self.assertEqual(server["env"]["BUILDKITE_TOOLSETS"], expected)
                self.assertEqual(server["env"]["BUILDKITE_API_TOKEN"], "entry-test-token")

    def test_cli_requires_unambiguous_organization_before_scenario(self):
        for org, response, status, expected in (
            (None, '[]', 0, None),
            (None, '[{"slug":"one"},{"slug":"two"}]', 0, None),
            (None, '', 22, None),
            (None, '[{"slug":"discovered"}]', 0, 'discovered'),
            ('explicit', '', 22, 'explicit'),
        ):
            with self.subTest(org=org, response=response, status=status), \
                    self.harness() as (root, env):
                for name, body in (
                    ('bk', 'echo "bk version test"'),
                    ('curl', 'printf "%s" "$ORG_RESPONSE"\nexit "$ORG_STATUS"'),
                ):
                    path = root / 'bin' / name
                    path.write_text('#!/bin/bash\n' + body + '\n')
                    path.chmod(0o755)
                env.update(BUILDKITE_API_TOKEN='entry-test-token',
                           BUILDKITE_ORGANIZATION_SLUG='ambient-must-not-be-used',
                           ORG_RESPONSE=response, ORG_STATUS=str(status), TMPDIR=str(root))
                entry = {'id': 'org-check', 'agent': 'claude', 'mode': 'cli',
                         'mode_params': {'org_slug': org} if org else {},
                         'scenario': {'setup': 'echo scenario-reached; exit 19'}}
                result = self.run_harness(root, env, entry)
                output = result.stdout + result.stderr
                self.assertEqual(result.returncode, 1, output)
                if expected:
                    self.assertIn('org ' + expected + ')', output)
                    self.assertIn('scenario-reached', output)
                else:
                    self.assertIn('set mode_params.org_slug', output)
                    self.assertIn('bk CLI setup failed', output)
                    self.assertNotIn('scenario-reached', output)
