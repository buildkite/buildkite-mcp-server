"""Run with python3 -m unittest discover -s evals/scripts -p 'test_cli_token.py'.

Requires bash, jq and yq. No agent or network access is used.
"""

import json
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


SCRIPTS = Path(__file__).parent


class CliTokenTest(unittest.TestCase):
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
                    tempfile.TemporaryDirectory() as tmp:
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
                entry = {
                    "id": "token-check", "agent": "claude", "mode": "cli",
                    # Positive controls stop safely at CLI setup, before any
                    # download or scenario can run.
                    "mode_params": {"download_url": "http://invalid.test/bk"},
                }
                if named:
                    entry["token_env"] = "ENTRY_TEST_TOKEN"
                config = root / "evals.json"
                config.write_text(json.dumps({"matrix": [entry]}))
                env = {k: v for k, v in os.environ.items()
                       if k not in ("BUILDKITE_API_TOKEN", "ENTRY_TEST_TOKEN", "DD_API_KEY")}
                env.update(HOME=tmp, EVALS_CONFIG=str(config), RUN_IN_CI="true",
                           LOCAL_CI="false", DEBUG_PERMISSIONS="false",
                           LOCAL_BYPASS_PERMISSION="true",
                           PATH=str(bin_dir) + os.pathsep + env["PATH"])
                if ambient is not None:
                    env["BUILDKITE_API_TOKEN"] = ambient
                if token is not None:
                    env["ENTRY_TEST_TOKEN"] = token
                result = subprocess.run(["bash", str(scripts / "babystand.sh")],
                                        env=env, capture_output=True, text=True, timeout=15)
                output = result.stdout + result.stderr
                if allowed:
                    self.assertEqual(result.returncode, 1, output)
                    self.assertIn("download_url must be an https:// URL", output)
                else:
                    self.assertEqual(result.returncode, 0, output)
                    self.assertIn("unset or empty; skipping", output)
                    self.assertNotIn("bk CLI setup", output)
                    self.assertFalse((root / "runs").exists())
