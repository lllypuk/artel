"""Exercise only extracted executables/artifact; never install them on the host."""
import json
import os
from pathlib import Path
import selectors
import sqlite3
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
        env = dict(os.environ, PATH=str(empty), HOME=str(home),
                   XDG_CONFIG_HOME=str(home / 'config'), XDG_DATA_HOME=str(home / 'data'),
                   QT_QPA_PLATFORM='offscreen', QT_QUICK_BACKEND='software',
                   QSG_RHI_BACKEND='software')
        token = root / 'private/owner.token'
        daemon = subprocess.Popen([str(stage / 'usr/bin/arteld'), '--data-dir', str(data),
                                   '--token-file', str(token), '--listen', '127.0.0.1:0'],
                                  env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
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
            assert json.loads(snapshot.stdout)['instance_id'] == identity
            ui = subprocess.Popen([str(stage / 'usr/bin/artel'), *options], env=env,
                                  stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            time.sleep(2)
            assert ui.poll() is None, 'staged no-Pi desktop exited'
            assert daemon.poll() is None
        finally:
            for child in (ui, daemon):
                if child is not None:
                    child.terminate()
                    try:
                        child.communicate(timeout=10)
                    except subprocess.TimeoutExpired:
                        child.kill()
                        child.communicate(timeout=5)
        assert daemon.returncode == 0
        with sqlite3.connect(data / 'artel.sqlite') as db:
            assert db.execute('PRAGMA user_version').fetchone()[0] == 2
            assert db.execute('SELECT instance_id FROM metadata').fetchone()[0] == identity
    # Public Pi loader supplies the one external API import. Artel's payload has
    # no node_modules, source tree, transpiler, relative checkout imports or secrets.
    artifact = stage / 'usr/share/artel/pi-observer/index.mjs'
    subprocess.run([sys.executable, '-B', str(ROOT / 'integrations/pi-observer/tests/pty_probe.py'),
                    'Lifecycle.test_sdk'], check=True, timeout=40,
                   env=dict(os.environ, ARTEL_OBSERVER_ARTIFACT=str(artifact)))
    print('Staged no-Pi daemon/CLI/desktop, v1 migration and standalone public-Pi artifact: PASS')


if __name__ == '__main__':
    main(sys.argv[1])
