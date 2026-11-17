"""Run the documented CLI commands only against a private temporary daemon."""
import json
import os
from pathlib import Path
import re
import selectors
import shlex
import subprocess
import tempfile
from string import Template
import unittest

ROOT = Path(__file__).resolve().parent.parent


class DocumentationTests(unittest.TestCase):
    def test_active_document_links_resolve(self):
        for name in ("README.md", "docs/development.md", "docs/legacy-cleanup.md"):
            text = (ROOT / name).read_text()
            for target in re.findall(r"\]\(([^)]+)\)", text):
                if "://" not in target:
                    with self.subTest(document=name, target=target):
                        self.assertTrue(((ROOT / name).parent / target.split("#")[0]).exists())

    def test_active_instruction_plan_paths_resolve(self):
        for name in ("CLAUDE.md", "docs/architecture/20261117-artel-v2-direction.md"):
            paths = re.findall(r"`(docs/plans/[^`]+\.md)`", (ROOT / name).read_text())
            self.assertTrue(paths, name)
            for target in paths:
                with self.subTest(document=name, target=target):
                    self.assertTrue((ROOT / target).is_file())

    def test_documented_cli_examples_on_temporary_instance(self):
        text = (ROOT / "docs/development.md").read_text()
        block = re.search(r"<!-- smoke:temporary-cli -->\n```sh\n(.*?)\n```", text, re.S)
        self.assertIsNotNone(block)
        lines = block.group(1).splitlines()
        self.assertEqual(len(lines), 3)
        with tempfile.TemporaryDirectory(prefix="artel-doc-") as directory:
            root = Path(directory)
            for command in ("arteld", "artelctl"):
                subprocess.run(["go", "build", "-o", str(root / command), "./cmd/" + command],
                               cwd=ROOT, check=True, capture_output=True, timeout=90)
            home = root / "home"
            home.mkdir(mode=0o700)
            env = dict(os.environ, HOME=str(home), XDG_CONFIG_HOME=str(home / "config"),
                       XDG_DATA_HOME=str(home / "data"))
            variables = dict(ARTELD=str(root / "arteld"), ARTELCTL=str(root / "artelctl"),
                             ARTEL_DATA=str(root / "data"), ARTEL_TOKEN=str(root / "private/owner.token"))

            def args(line):
                return shlex.split(Template(line).substitute(variables))

            daemon = subprocess.Popen(args(lines[0]), env=env, stdout=subprocess.PIPE,
                                      stderr=subprocess.PIPE, text=True)
            try:
                with selectors.DefaultSelector() as selector:
                    selector.register(daemon.stdout, selectors.EVENT_READ)
                    self.assertTrue(selector.select(timeout=15), "daemon startup timed out")
                address = daemon.stdout.readline().strip()
                self.assertRegex(address, r"^arteld: listening 127\.0\.0\.1:\d+$")
                variables["ARTEL_ENDPOINT"] = "http://" + address.removeprefix("arteld: listening ")
                for line in lines[1:]:
                    result = subprocess.run(args(line), env=env, capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, 0, "documented command failed")
                    self.assertEqual(result.stderr, "")
                    data = json.loads(result.stdout)
                    if "status" in args(line):
                        self.assertEqual(data, {"status": "ok"})
                    else:
                        self.assertTrue(data["instance_id"])
                        self.assertEqual(data["event_seq"], 0)
                        for key in ("projects", "tasks", "queues"):
                            self.assertEqual(data[key], [])
                # Public liveness needs no credential; private snapshot must fail safely.
                variables["ARTEL_TOKEN"] = str(root / "missing.token")
                for line, expected in zip(lines[1:], (0, 1)):
                    result = subprocess.run(args(line), env=env, capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, expected)
                    if expected:
                        self.assertEqual(result.stdout, "")
                        self.assertEqual(result.stderr, "artelctl: credential unavailable or unsafe\n")
                invalid = subprocess.run([variables["ARTELCTL"], "snapshot", "--unknown"],
                                         env=env, capture_output=True, timeout=10)
                self.assertEqual(invalid.returncode, 2)
            finally:
                daemon.terminate()
                try:
                    daemon.communicate(timeout=10)
                except subprocess.TimeoutExpired:
                    daemon.kill()
                    daemon.communicate(timeout=5)
            self.assertEqual(daemon.returncode, 0)
