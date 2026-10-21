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
