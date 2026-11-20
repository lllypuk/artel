"""Exercise only extracted executables/artifact; never install them on the host."""
import json
import os
from pathlib import Path
import selectors
import signal
import sqlite3
import stat
import subprocess
import sys
import tempfile
import time
import uuid

ROOT = Path(__file__).resolve().parents[1]


def main(stage):
    stage = Path(stage).resolve()
    with tempfile.TemporaryDirectory(prefix='artel-installed-') as temporary:
        root = Path(temporary)
        home = root / 'home'
        home.mkdir(mode=0o700)
        data = root / 'data'
        data.mkdir(mode=0o700)
        identity = str(uuid.uuid4())
        # Historical immutable v1 schema; package startup must upgrade, not recreate.
        with sqlite3.connect(data / 'artel.sqlite') as db:
            db.executescript((ROOT / 'internal/store/testdata/v1.sql').read_text())
            db.execute('INSERT INTO metadata VALUES(1,?)', (identity,))
        empty = root / 'no-tools'
        empty.mkdir()
        runtime = root / 'runtime'
        runtime.mkdir(mode=0o700)
        # Do not inherit Pi, agterm, provider credentials or user Qt settings.
        env = dict(PATH=str(empty), HOME=str(home), LANG='C.UTF-8',
                   XDG_RUNTIME_DIR=str(runtime), XDG_CACHE_HOME=str(home / 'cache'),
                   XDG_CONFIG_HOME=str(home / 'config'), XDG_DATA_HOME=str(home / 'data'),
                   QT_QPA_PLATFORM='offscreen', QT_QUICK_BACKEND='software',
                   QSG_RHI_BACKEND='software')
        parent = os.getpid()

        def owned_child():
            import ctypes
            libc = ctypes.CDLL(None, use_errno=True)
            if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0 or os.getppid() != parent:
                os._exit(125)
        token = root / 'private/owner.token'
        daemon = subprocess.Popen([str(stage / 'usr/bin/arteld'), '--data-dir', str(data),
                                   '--token-file', str(token), '--listen', '127.0.0.1:0'],
                                  env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True,
                                  preexec_fn=owned_child)
        ui = None
        try:
            with selectors.DefaultSelector() as selector:
                selector.register(daemon.stdout, selectors.EVENT_READ)
                assert selector.select(15), 'staged daemon startup deadline'
            line = daemon.stdout.readline().strip()
            assert line.startswith('arteld: listening 127.0.0.1:'), line
            options = ['--endpoint', 'http://' + line.removeprefix('arteld: listening '),
                       '--token-file', str(token)]
            snapshot = subprocess.run([str(stage / 'usr/bin/artelctl'), 'snapshot', '--json', *options],
                                      env=env, capture_output=True, check=True, timeout=10)
            baseline = json.loads(snapshot.stdout)
            assert baseline['instance_id'] == identity
            assert baseline['event_seq'] == 0 and baseline['projects'] == []
            assert baseline['tasks'] == [] and baseline['queues'] == []
            ui = subprocess.Popen([str(stage / 'usr/bin/artel'), *options], env=env,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                                  preexec_fn=owned_child)
            if os.environ.get('ARTEL_STAGED_FAILURE_PROBE'):
                Path(os.environ['ARTEL_STAGED_FAILURE_PROBE']).write_text(
                    f'{daemon.pid}\n{ui.pid}\n{root}\n')
                raise AssertionError('intentional staged cleanup assertion')
            time.sleep(3)  # includes initial overview projection and a poll tick
            assert ui.poll() is None, 'staged no-Pi desktop exited'
            assert daemon.poll() is None
            after = subprocess.run([str(stage / 'usr/bin/artelctl'), 'snapshot', '--json', *options],
                                   env=env, capture_output=True, check=True, timeout=10)
            assert json.loads(after.stdout) == baseline, 'packaged startup mutated manual state'
            observers = subprocess.run([str(stage / 'usr/bin/artelctl'), 'observer', 'list', *options],
                                       env=env, capture_output=True, check=True, timeout=10)
            assert json.loads(observers.stdout)['observers'] == []
            assert not any(stat.S_ISSOCK(path.lstat().st_mode) for path in root.rglob('*')), \
                'default no-Pi daemon unexpectedly opened an observer listener'
        finally:
            captured = []
            for child in (ui, daemon):
                if child is not None:
                    child.terminate()
                    try:
                        out, err = child.communicate(timeout=10)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        out, err = child.communicate(timeout=5)
                    captured.append((child, out, err))
            # Validate only after every child is reaped: a warning/secret
            # assertion about the UI must not leave the daemon running.
            credential = token.read_bytes().strip() if token.exists() else b''
            for child, out, err in captured:
                assert not Path('/proc', str(child.pid)).exists(), 'staged child leaked'
                logs = (out + err).encode() if isinstance(out, str) else out + err
                assert not credential or credential not in logs, 'credential leaked into staged logs'
                if child is ui:
                    assert not err.strip(), 'packaged QML/resource/icon warnings: ' + err.decode()
        assert daemon.returncode == 0
        with sqlite3.connect(data / 'artel.sqlite') as db:
            assert db.execute('PRAGMA user_version').fetchone()[0] == 3
            assert db.execute('SELECT instance_id FROM metadata').fetchone()[0] == identity
    # Public Pi loader supplies the one external API import. Artel's payload has
    # no node_modules, source tree, transpiler, relative checkout imports or secrets.
    artifact = stage / 'usr/share/artel/pi-observer/index.mjs'
    subprocess.run([sys.executable, '-B', str(ROOT / 'integrations/pi-observer/tests/pty_probe.py'),
                    'Lifecycle.test_sdk'], check=True, timeout=40,
                   env=dict(os.environ, ARTEL_OBSERVER_ARTIFACT=str(artifact)))
    print('Staged no-Pi daemon/CLI/desktop, v1 migration and standalone public-Pi artifact: PASS')


if __name__ == '__main__':
    if '--negative-probe' in sys.argv:
        with tempfile.TemporaryDirectory(prefix='artel-staged-negative-') as directory:
            manifest = Path(directory) / 'owned'
            result = subprocess.run([sys.executable, '-B', __file__, sys.argv[1]],
                                    env=dict(os.environ, ARTEL_STAGED_FAILURE_PROBE=str(manifest)),
                                    capture_output=True, timeout=30)
            assert result.returncode != 0
            assert b'intentional staged cleanup assertion' in result.stderr, result.stderr.decode()
            daemon, ui, root = manifest.read_text().splitlines()
            assert not Path(root).exists(), 'staged private files leaked'
            assert all(not Path('/proc', pid).exists() for pid in (daemon, ui)), 'staged process leaked'
        print('Staged intentional-failure/owned-process/temp cleanup: PASS')
    else:
        main(sys.argv[1])
