"""Run the documented CLI commands only against a private temporary daemon."""
import json
import os
from pathlib import Path
import re
import selectors
import shlex
import shutil
import subprocess
import tempfile
from string import Template
import unittest
import xml.etree.ElementTree as ET

ROOT = Path(__file__).resolve().parent.parent


class DocumentationTests(unittest.TestCase):
    def test_active_document_links_resolve(self):
        for name in ("README.md", "DESIGN.md", "PRODUCT.md", "docs/development.md",
                     "docs/screenshots/README.md", "docs/legacy-cleanup.md",
                     "docs/design/20261118-artel-desktop-brief.md",
                     "docs/design/20261118-team-overview-exploration.md",
                     "docs/design/reference/README.md"):
            text = (ROOT / name).read_text()
            for target in re.findall(r"\]\(([^)]+)\)", text):
                if "://" not in target:
                    with self.subTest(document=name, target=target):
                        self.assertTrue(((ROOT / name).parent / target.split("#")[0]).exists())

    def test_active_instruction_plan_paths_resolve(self):
        for name in ("docs/architecture/20261117-artel-v2-direction.md",):
            paths = re.findall(r"`(docs/plans/[^`]+\.md)`", (ROOT / name).read_text())
            self.assertTrue(paths, name)
            for target in paths:
                with self.subTest(document=name, target=target):
                    self.assertTrue((ROOT / target).is_file())

    def test_instruction_links_survive_ralphex_move(self):
        active_plan = 'docs/plans/20261117-artel-v2-pi-observer.md'
        moved_plan = 'docs/plans/completed/20261117-artel-v2-pi-observer.md'
        with tempfile.TemporaryDirectory(prefix='artel-plan-move-') as directory:
            tree = Path(directory)
            shutil.copytree(ROOT / 'docs', tree / 'docs')
            shutil.copytree(ROOT / 'api', tree / 'api')
            shutil.copyfile(ROOT / 'DESIGN.md', tree / 'DESIGN.md')
            if (tree / active_plan).exists():
                (tree / active_plan).rename(tree / moved_plan)
            self.assertFalse((tree / active_plan).exists())
            for name in ('AGENTS.md', '.github/copilot-instructions.md',
                         'README.md', 'docs/development.md', 'docs/legacy-cleanup.md'):
                text = (ROOT / name).read_text()
                destination = tree / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                destination.write_text(text)
                if name != 'docs/legacy-cleanup.md':
                    self.assertIn('docs/development.md' if name != 'docs/development.md' else 'stable development contract', text)
                self.assertNotIn(active_plan, text)
                self.assertNotIn(moved_plan, text)
                # Cleanup inventory contains names of intentionally deleted files.
                if name != 'docs/legacy-cleanup.md':
                    for target in re.findall(r'`(docs/[^`]+\.md)`', text):
                        self.assertTrue((tree / target).is_file(), (name, target))
                for target in re.findall(r'\]\(([^)]+)\)', text):
                    if '://' not in target:
                        self.assertTrue((destination.parent / target.split('#')[0]).exists(), (name, target))

    def test_native_resources_and_documented_evidence(self):
        source = ROOT / 'desktop/src'
        resources = ET.parse(source / 'resources.qrc').findall('.//file')
        self.assertTrue(resources)
        aliases = set()
        for resource in resources:
            path = source / resource.text
            self.assertTrue(path.is_file(), path)
            alias = resource.get('alias', resource.text)
            self.assertNotIn(alias, aliases)
            aliases.add(alias)
        self.assertTrue({'Main.qml', 'TeamOverview.qml', 'ManualWorkspace.qml',
                         'ObserverPanel.qml', 'ui/Theme.qml', 'ui/icons/search.svg'} <= aliases)
        for path in (source / 'ui').rglob('*'):
            if path.suffix in ('.qml', '.svg'):
                self.assertIn(path.relative_to(source).as_posix(), aliases)
        evidence = (ROOT / 'docs/screenshots/README.md').read_text()
        self.assertIn('fictional', evidence)
        self.assertIn('offscreen', evidence)
        self.assertIn('historical', evidence)
        for target in re.findall(r'\]\(([^)]+\.png)\)', evidence):
            self.assertEqual((ROOT / 'docs/screenshots' / target).read_bytes()[:8],
                             b'\x89PNG\r\n\x1a\n')

    def test_redesign_links_survive_completed_plan_move(self):
        active = Path('docs/plans/20261118-artel-desktop-team-redesign.md')
        with tempfile.TemporaryDirectory(prefix='artel-redesign-docs-') as directory:
            tree = Path(directory)
            shutil.copytree(ROOT / 'docs', tree / 'docs')
            shutil.copytree(ROOT / 'api', tree / 'api')
            for name in ('README.md', 'DESIGN.md', 'PRODUCT.md', 'AGENTS.md',
                         '.github/copilot-instructions.md'):
                destination = tree / name
                destination.parent.mkdir(parents=True, exist_ok=True)
                shutil.copyfile(ROOT / name, destination)
            if (tree / active).exists():
                (tree / active).rename(tree / active.parent / 'completed' / active.name)
            self.assertFalse((tree / active).exists())
            for name in ('README.md', 'DESIGN.md', 'PRODUCT.md', 'AGENTS.md',
                         '.github/copilot-instructions.md',
                         'docs/development.md', 'docs/screenshots/README.md',
                         'docs/design/reference/README.md'):
                document = tree / name
                for target in re.findall(r'\]\(([^)]+)\)', document.read_text()):
                    if '://' not in target:
                        self.assertTrue((document.parent / target.split('#')[0]).exists(),
                                        (name, target))

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
                             ARTEL_DATA=str(root / "data"), ARTEL_TOKEN=str(root / "private/owner.token"),
                             ARTEL_SOCKET=str(root / 'observer/observer.sock'),
                             ARTEL_CONFIG=str(root / 'observer/config.json'))

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
                observer_block = re.search(r'<!-- smoke:observer-cli -->\n```sh\n(.*?)\n```', text, re.S)
                self.assertIsNotNone(observer_block)
                for line in observer_block.group(1).splitlines():
                    result = subprocess.run(args(line), env=env, capture_output=True, text=True, timeout=10)
                    self.assertEqual(result.returncode, 0, result.stderr)
                    self.assertEqual(result.stderr, '')
                    public = json.loads(result.stdout)
                    self.assertTrue(public)
                    config = json.loads(Path(variables['ARTEL_CONFIG']).read_text())
                    self.assertEqual(config['status'], 'confirmed')
                    secret = Path(config['token_file']).read_text().strip()
                    self.assertNotIn(secret, result.stdout + result.stderr)
                self.assertEqual(len(public['observers']), 1)
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
