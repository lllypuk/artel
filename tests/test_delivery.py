"""Delivery checks run without installing packages, services or host dependencies."""
import importlib.machinery
import importlib.util
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import unittest
from unittest.mock import patch

ROOT = Path(__file__).resolve().parent.parent
loader = importlib.machinery.SourceFileLoader("package_check", str(ROOT / "scripts/package-check"))
spec = importlib.util.spec_from_loader(loader.name, loader)
package = importlib.util.module_from_spec(spec)
loader.exec_module(package)


class DeliveryTests(unittest.TestCase):
    def test_snapshot_uses_current_tree_and_excludes_private_files(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            subprocess.run(["git", "init", "-q", str(root)], check=True)
            (root / "cmd").mkdir()
            (root / "cmd/main.go").write_text("current uncommitted source")
            (root / "desktop").mkdir()
            (root / "desktop/build").mkdir()
            (root / "desktop/build/artel").write_text("stale binary")
            (root / "desktop/bin").mkdir()
            (root / "desktop/bin/build").write_text("build script")
            (root / "local-owner.token").write_text("fictional secret")
            (root / ".env").write_text("fictional secret")
            (root / "data").mkdir()
            (root / "data/artel.sqlite").write_text("not source")
            observer = root / 'integrations/pi-observer'
            (observer / 'src').mkdir(parents=True)
            (observer / 'src/index.ts').write_text('observer source')
            (observer / 'node_modules').mkdir()
            (observer / 'node_modules/private.js').write_text('not shipped')
            (observer / 'dist').mkdir()
            (observer / 'dist/index.mjs').write_text('stale bundle')
            (observer / 'private-config.json').write_text('not source')
            archive = root / "snapshot.tar.gz"
            package.snapshot(root, archive)
            with tarfile.open(archive) as source:
                self.assertEqual(source.getnames(), ["artel/cmd/main.go", "artel/desktop/bin/build", "artel/integrations/pi-observer/src/index.ts"])
                self.assertEqual(source.extractfile("artel/cmd/main.go").read(), b"current uncommitted source")
            (root / "cmd/link").symlink_to(root / "local-owner.token")
            with self.assertRaisesRegex(ValueError, "Non-regular"):
                package.snapshot(root, archive)

    def test_unexpected_package_entries_rejected_before_extraction(self):
        for extra in ("usr/share/artel/local-owner.token", "../escape", "data/artel.sqlite", "install"):
            with self.subTest(extra=extra), patch.object(package.subprocess, "check_output", return_value=extra + "\n"), patch.object(package, "run") as run:
                with self.assertRaisesRegex(ValueError, "manifest"):
                    package.inspect_package(Path("fake.pkg"), Path("unused"))
                run.assert_not_called()

    def test_bad_package_ownership_or_modes_rejected(self):
        manifest = "\n".join(package.PAYLOAD | {".PKGINFO", ".BUILDINFO", ".MTREE"})
        for metadata in ("-rw-r--r-- 0 1000 1000 10 fixture", "lrwxrwxrwx 0 0 0 10 fixture", "-rwxrwxrwx 0 0 0 10 fixture"):
            with self.subTest(metadata=metadata), patch.object(package.subprocess, "check_output", side_effect=[manifest, metadata]), patch.object(package, "run") as run:
                with self.assertRaises(ValueError):
                    package.inspect_package(Path("fake.pkg"), Path("unused"))
                run.assert_not_called()

    def test_missing_dependencies_are_errors_without_installer(self):
        with patch.object(package.shutil, "which", return_value=None), patch.object(package, "run") as run:
            with self.assertRaisesRegex(ValueError, "Missing package-check tools"):
                package.main()
            run.assert_not_called()

    def test_missing_arch_dependencies_do_not_trigger_build(self):
        result = subprocess.CompletedProcess([], 127, "qt6-base>=6.8\n", "")
        with patch.object(package.shutil, "which", return_value="tool"), patch.object(package.os, "getuid", return_value=1000), patch.object(package.subprocess, "check_output", return_value="\tdepends = qt6-base>=6.8\n"), patch.object(package.subprocess, "run", return_value=result) as command, patch.object(package, "snapshot") as snapshot:
            with self.assertRaisesRegex(ValueError, "qt6-base>=6.8"):
                package.main()
            self.assertEqual(command.call_args.args[0], ["pacman", "-T", "qt6-base>=6.8"])
            snapshot.assert_not_called()

    def test_package_build_uses_no_install_or_dependency_install_flags(self):
        commands = []

        def fake_run(args, **kwargs):
            commands.append(args)
            if args[0] == "makepkg":
                work = Path(kwargs["cwd"])
                (work / "artel-2.0.0-1-x86_64.pkg.tar.zst").write_bytes(b"fixture")
                self.assertIn("sha256sums=('", (work / "PKGBUILD").read_text())
                self.assertNotIn("SKIP", (work / "PKGBUILD").read_text())

        def fake_snapshot(root, archive):
            archive.write_bytes(b"current working tree fixture")

        result = subprocess.CompletedProcess([], 0, "", "")
        with patch.object(package.shutil, "which", return_value="tool"), patch.object(package.os, "getuid", return_value=1000), patch.object(package.subprocess, "check_output", return_value=""), patch.object(package.subprocess, "run", return_value=result), patch.object(package, "run", side_effect=fake_run), patch.object(package, "snapshot", side_effect=fake_snapshot), patch.object(package, "inspect_package"), patch.object(package, "PAYLOAD", set()):
            package.main()
        self.assertEqual(commands[0], ["makepkg", "--config", "/etc/makepkg.conf", "--cleanbuild", "--noconfirm"])
        self.assertEqual(commands[1][:3], ["python3", "-B", str(ROOT / "tests/staged_acceptance.py")])

    def test_root_refused_before_build(self):
        with patch.object(package.shutil, "which", return_value="tool"), patch.object(package.os, "getuid", return_value=0), patch.object(package, "run") as run:
            with self.assertRaisesRegex(ValueError, "unprivileged"):
                package.main()
            run.assert_not_called()

    def test_preflight_failure_exit_codes(self):
        for env, message in (({"QMAKE": "/nonexistent-artel-qmake"}, "Missing dependency"),
                             ({"GOLANGCI_LINT": "/nonexistent-artel-lint"}, "golangci-lint 2.10.1 required")):
            with self.subTest(env=env):
                result = subprocess.run([str(ROOT / "scripts/preflight")], cwd=ROOT, env=dict(os.environ, **env), capture_output=True, text=True, check=False)
                self.assertNotEqual(result.returncode, 0)
                self.assertIn(message, result.stdout + result.stderr)

    def test_observer_artifact_is_explicit_and_standalone(self):
        import json
        manifest = json.loads((ROOT / 'integrations/pi-observer/package.json').read_text())
        self.assertEqual(manifest['pi']['extensions'], ['./dist/index.mjs'])
        self.assertNotIn('dependencies', manifest)
        recipe = (ROOT / 'packaging/arch/PKGBUILD').read_text()
        self.assertIn('/usr/share/artel/pi-observer/', recipe)
        self.assertIn('ci --ignore-scripts', recipe)
        self.assertNotIn('.pi/', recipe)
        required = {f'usr/share/artel/pi-observer/{name}' for name in ('index.mjs', 'package.json', 'README.md')}
        self.assertTrue(required <= package.PAYLOAD)
        self.assertIn('$(MAKE) observer-check', (ROOT / 'Makefile').read_text())

    def test_local_pilot_pauses_only_remote_native_job(self):
        workflow = (ROOT / '.github/workflows/ci.yml').read_text()
        go, native = workflow.split('  native-full-stack:\n', 1)
        self.assertIn('  go:\n', go)
        self.assertNotIn('if: ${{ false }}', go)
        self.assertIn('go test ./...', go)
        self.assertIn('go test -race ./...', go)
        self.assertIn('go build ./cmd/...', go)
        self.assertIn('    if: ${{ false }}\n', native)
        self.assertIn('make check', native)
        self.assertIn('make package-check', native)
        makefile = (ROOT / 'Makefile').read_text()
        self.assertIn('$(MAKE) test-e2e', makefile)
        self.assertIn('$(MAKE) observer-check', makefile)
        self.assertIn('systemd-analyze', (ROOT / 'scripts/package-check').read_text())
        self.assertIn('Re-enable the job', (ROOT / 'docs/development.md').read_text())

    def test_delivery_has_no_activation_hooks(self):
        recipe = (ROOT / "packaging/arch/PKGBUILD").read_text()
        self.assertNotIn("install=", recipe)
        self.assertNotIn("systemctl", recipe)
        self.assertNotIn("pacman", recipe)
        unit = (ROOT / "packaging/systemd/arteld.service").read_text()
        self.assertIn("ExecStart=/usr/bin/arteld\n", unit)
        self.assertIn("KillSignal=SIGTERM\n", unit)
        for forbidden in ("ExecStartPre", "ExecStartPost", "network-online", "docker", "pkill"):
            self.assertNotIn(forbidden, unit)
        desktop = (ROOT / "packaging/arch/artel.desktop").read_text()
        self.assertIn("Exec=artel\n", desktop)
        self.assertIn("Icon=artel\n", desktop)


if __name__ == "__main__":
    unittest.main()
