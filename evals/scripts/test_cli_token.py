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
                   if k not in ("BUILDKITE_API_TOKEN", "ENTRY_TEST_TOKEN", "DD_API_KEY",
                                "BUILDKITE_REPLAY", "EVAL_ENTRY")}
            env.update(HOME=tmp, EVALS_CONFIG=str(root / "evals.json"), RUN_IN_CI="true",
                       LOCAL_CI="false", DEBUG_PERMISSIONS="false",
                       LOCAL_BYPASS_PERMISSION="true",
                       PATH=str(bin_dir) + os.pathsep + env["PATH"])
            yield root, env

    def run_harness(self, root, env, entry):
        entries = entry if isinstance(entry, list) else [entry]
        (root / "evals.json").write_text(json.dumps({"matrix": entries}))
        return subprocess.run(["bash", str(root / "scripts/babystand.sh")],
                              env=env, capture_output=True, text=True, timeout=15)

    def test_replay_selection_and_token_do_not_leak_between_entries(self):
        with self.harness() as (root, env):
            (root / 'fixtures').mkdir()
            replay = root / 'fixtures/snapshot.har'
            replay.write_text('{"log":{"entries":[]}}')
            (root / 'prompts').mkdir()
            (root / 'prompts/investigate.md').write_text('Inspect the build.')
            server = root / 'bin/buildkite-mcp-server'
            server.write_text('#!/bin/bash\necho fixture-server\n')
            server.chmod(0o755)
            runner = root / 'scripts/claude.sh'
            runner.write_text('''#!/bin/bash
jq -n -c --arg replay "$BUILDKITE_REPLAY" --arg token "$BUILDKITE_API_TOKEN" \\
    '{replay: $replay, token: $token}' >> "$CAPTURE"
# Stop before audit/judging: no model or API is invoked by this check.
exit 17
''')
            runner.chmod(0o755)
            capture = root / 'calls.jsonl'
            env.update(BUILDKITE_API_TOKEN='ambient-fixture-token', CAPTURE=str(capture))
            base = {'agent': 'claude', 'mode': 'mcp', 'prompt': 'investigate'}
            entries = [
                {**base, 'id': 'replay', 'token_env': 'ENTRY_TEST_TOKEN',
                 'scenario': {'replay': 'fixtures/snapshot.har'}},
                {**base, 'id': 'live'},
            ]
            result = self.run_harness(root, env, entries)
            self.assertEqual(result.returncode, 1, result.stdout + result.stderr)
            self.assertEqual([json.loads(line) for line in capture.read_text().splitlines()],
                             [{'replay': str(replay), 'token': ''},
                              {'replay': '', 'token': 'ambient-fixture-token'}])

    def test_invalid_replay_fails_before_scenario_setup(self):
        for mode, agent, fixture, expected in (
            ('mcp', 'claude', 'missing.har', 'replay fixture not found'),
            ('cli', 'claude', 'missing.har', 'requires mode mcp and agent claude'),
            ('mcp', 'cursor', 'missing.har', 'requires mode mcp and agent claude'),
        ):
            with self.subTest(mode=mode, agent=agent), self.harness() as (root, env):
                env['CURSOR_API_KEY'] = 'fixture-key'
                entry = {'id': 'invalid-replay', 'agent': agent, 'mode': mode,
                         'scenario': {'replay': fixture, 'setup': 'echo scenario-reached'}}
                result = self.run_harness(root, env, entry)
                output = result.stdout + result.stderr
                self.assertEqual(result.returncode, 1, output)
                self.assertIn(expected, output)
                self.assertNotIn('scenario-reached', output)

    def test_entry_selection_rejects_unknown_and_excludes_unselected_scenarios(self):
        for selector, selected in (('scheduler-one', ['scheduler-one']),
                                   ('scheduler-*', ['scheduler-one', 'scheduler-two']),
                                   ('missing', [])):
            with self.subTest(selector=selector), self.harness() as (root, env):
                env['EVAL_ENTRY'] = selector
                # Deliberately invalid replay paths fail before model calls.
                entries = [{'id': name, 'scenario': {'replay': name + '.har'}}
                           for name in ('scheduler-one', 'scheduler-two', 'live')]
                result = self.run_harness(root, env, entries)
                output = result.stdout + result.stderr
                self.assertEqual(result.returncode, 1, output)
                if not selected:
                    self.assertIn('unknown EVAL_ENTRY', output)
                for name in ('scheduler-one', 'scheduler-two', 'live'):
                    self.assertEqual('Eval entry: ' + name in output, name in selected, output)

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
