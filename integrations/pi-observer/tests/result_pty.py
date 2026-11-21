#!/usr/bin/env python3
"""Shared 4a/result acceptance: one daemon/task, pinned Pi PTY and production QML.

The local provider returns Fictional answer. The Qt runner, not Pi, creates the
Git commits and imports only a synthetic manifest/findings pair. No revmux or
agterm process is launched and all children belong to this temporary fixture.
"""
import fcntl
import http.server
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time

from delivery_pty import CLI, Control, Owner, Provider

ROOT = Path(__file__).resolve().parents[3]
PACKAGE = ROOT / 'integrations/pi-observer'


def uid():
    import uuid
    return str(uuid.uuid4())


class ResultFixture:
    def __init__(self, root):
        self.root = root
        self.repo = root / 'work'
        for name in ['runtime', 'agent', 'private', 'work']:
            (root / name).mkdir(mode=0o700)
        self.socket = str(root / 'private/observer.sock')
        self.token_path = root / 'private/owner.token'
        self.delivery_config = root / 'private/delivery.json'
        self.children = []
        self.daemon = self.pi = self.control = None
        self.master = None
        self.output = bytearray()
        self.secrets = []
        self.env = {
            'PATH': str(Path(shutil.which('node')).parent) + ':/usr/bin:/bin',
            'HOME': str(root), 'XDG_RUNTIME_DIR': str(root / 'runtime'),
            'XDG_CONFIG_HOME': str(root / 'config'), 'XDG_DATA_HOME': str(root / 'xdg-data'),
            'XDG_CACHE_HOME': str(root / 'cache'), 'PI_CODING_AGENT_DIR': str(root / 'agent'),
            'PI_OFFLINE': '1', 'TERM': 'xterm-256color', 'LANG': 'C.UTF-8',
            'ARTEL_PROBE_TRACE': str(root / 'trace.jsonl'),
            'ARTEL_FIXTURE_SESSION': str(root / 'session'),
            'ARTEL_DELIVERY_CONTROL': str(root / 'private/control.sock'),
            'ARTEL_DELIVERY_JOURNAL': str(root / 'private/journal.json'),
            'ARTEL_DELIVERY_ROOT': str(root),
        }
        assert not any(name.startswith('AGTERM') for name in self.env)
        Provider.calls = 0
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.env['ARTEL_FIXTURE_URL'] = f'http://127.0.0.1:{self.server.server_port}/v1'

    def spawn(self, args, env=None, **kwargs):
        # Arm parent-death cleanup before exec so a killed watchdog cannot orphan
        # daemon, Pi, Qt or CLI processes.
        child = subprocess.Popen([sys.executable, '-B', __file__, '--owned-child',
                                  str(os.getpid()), *args],
                                 env=self.env if env is None else env,
                                 start_new_session=True, **kwargs)
        self.children.append(child)
        return child

    @staticmethod
    def stop(child, kill=False):
        if child is None:
            return
        if child.poll() is None:
            os.killpg(child.pid, signal.SIGKILL if kill else signal.SIGTERM)
        child.wait(timeout=5)
        if child.stdout:
            child.stdout.close()
        if child.stderr and child.stderr != child.stdout:
            child.stderr.close()

    def run(self, args):
        child = self.spawn(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        try:
            out, err = child.communicate(timeout=20)
        except BaseException:
            self.stop(child, True)
            raise
        assert child.returncode == 0, (args[0], err.decode()[-2000:])
        assert all(secret.encode() not in out + err for secret in self.secrets)
        return out

    def request(self, method, path, body=None, status=200):
        code, payload = self.owner.request(method, path, body, key=uid() if body is not None else None)
        assert code == status, (path, code, payload)
        return payload

    def launch(self):
        self.daemon = self.spawn([str(ROOT / 'build/bin/arteld'), '--listen', '127.0.0.1:0',
                                  '--data-dir', str(self.root / 'db'), '--token-file', str(self.token_path),
                                  '--observer-socket', self.socket],
                                 stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert select.select([self.daemon.stdout], [], [], 10)[0], 'daemon startup deadline'
        line = self.daemon.stdout.readline().decode().strip()
        assert line.startswith('arteld: listening '), (line, self.daemon.poll())
        self.endpoint = 'http://' + line.split()[-1]
        owner_token = self.token_path.read_text().strip()
        self.secrets.append(owner_token)
        self.owner = Owner(self.endpoint, owner_token)

        self.run([str(ROOT / 'build/bin/artelctl'), 'observer', 'provision',
                  '--name', 'fictional-result', '--observer-socket', self.socket,
                  '--output', str(self.root / 'private/observer.json'),
                  '--endpoint', self.endpoint, '--token-file', str(self.token_path)])
        config = json.loads((self.root / 'private/observer.json').read_text())
        self.observer_id = config['observer_id']
        self.secrets.append(Path(config['token_file']).read_text().strip())
        self.env['ARTEL_OBSERVER_CONFIG'] = str(self.root / 'private/observer.json')

        subprocess.run(['git', 'init', '-q', str(self.repo)], check=True, env=self.env,
                       stdout=subprocess.PIPE, stderr=subprocess.PIPE, timeout=20)
        self.project = self.request('POST', '/v1/projects', {
            'name': 'Fictional shared result', 'root_path': str(self.repo)}, 201)['id']
        self.task = self.request('POST', f'/v1/projects/{self.project}/tasks', {
            'title': 'Shared Pi delivery and reviewed result',
            'description': 'Original task description'}, 201)['id']
        self.request('PUT', f'/v1/projects/{self.project}/queue', {
            'expected_queue_version': 1, 'task_ids': [self.task]})
        self.instance = self.request('GET', '/v1/snapshot')['instance_id']

    def pump(self):
        if self.master is not None and select.select([self.master], [], [], 0.05)[0]:
            try:
                data = os.read(self.master, 65536)
            except OSError:
                return
            self.output.extend(data)
            assert len(self.output) < 1024 * 1024, 'PTY output bound'
            if b'\x1b[6n' in data:
                os.write(self.master, b'\x1b[1;1R')

    def wait(self, predicate, label, timeout=30):
        # Actual control/API/PTY readiness drives progress; there are no fixed
        # sleeps between lifecycle steps.
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            self.pump()
            value = predicate()
            if value:
                return value
            if self.pi is not None and self.pi.poll() is not None:
                # The requested exit can land between the predicate and this
                # poll; recheck before calling a successful quit premature.
                value = predicate()
                if value:
                    return value
                break
        raise AssertionError(f'{label}: terminal={bytes(self.output[-2000:])!r}')

    def start_pi(self):
        master, slave = pty.openpty()
        self.master = master
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 120, 0, 0))
        args = [shutil.which('node'), str(CLI), '--offline', '--no-approve',
                '--no-extensions', '--no-skills', '--no-prompt-templates',
                '--no-context-files', '--no-themes', '--no-mcp', '--no-builtin-tools',
                '-e', str(PACKAGE / 'dist/index.mjs'),
                '-e', str(PACKAGE / 'tests/probe.ts'),
                '-e', str(PACKAGE / 'tests/fixture.ts'),
                '-e', str(PACKAGE / 'tests/delivery_fixture.mjs'),
                '--model', 'artel-fixture/local']
        self.pi = self.spawn(args, cwd=self.repo, stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)

        def connect_control():
            if self.control is None:
                try:
                    self.control = Control(self.root / 'private/control.sock')
                except OSError:
                    return False
            return True
        self.wait(connect_control, 'delivery control socket')

        def registered():
            registry = self.request('GET', '/v1/observers')
            for row in registry['observers']:
                if row['observer_id'] != self.observer_id or not row.get('runtimes'):
                    continue
                return row['runtimes'][0]['registration']
            return None
        registration = self.wait(registered, 'observer registration')
        self.runtime = registration['runtime_id']
        self.pi_session = registration['pi_session_id']

    def enable_delivery(self):
        record = self.request('GET', '/v1/observers')
        row = next(row for row in record['observers'] if row['observer_id'] == self.observer_id)
        self.request('PATCH', f'/v1/observers/{self.observer_id}/binding', {
            'expected_version': row['version'], 'runtime_id': self.runtime,
            'project_id': self.project})
        row = next(row for row in self.request('GET', '/v1/observers')['observers']
                   if row['observer_id'] == self.observer_id)
        self.run([str(ROOT / 'build/bin/artelctl'), 'delivery', 'enable',
                  '--id', self.observer_id, '--runtime-id', self.runtime,
                  '--project-id', self.project, '--pi-session-id', self.pi_session,
                  '--observer-socket', self.socket, '--output', str(self.delivery_config),
                  '--expected-version', str(row['version']), '--endpoint', self.endpoint,
                  '--token-file', str(self.token_path)])
        config = json.loads(self.delivery_config.read_text())
        self.binding = config['delivery_binding_id']
        self.secrets.append(Path(config['token_file']).read_text().strip())
        self.wait(lambda: self.request(
            'GET', f'/v1/observers/{self.observer_id}/delivery/{self.binding}'
        )['status'] == 'active', 'writer attach')
        self.wait(lambda: self.request('GET', f'/v1/tasks/{self.task}/delivery')['ready'],
                  'writer ready')
        assert self.control.command(type='state')['sends'] == 0

    def run_qml(self):
        # Do not inherit Pi credentials/config or any host agterm variable in Qt.
        env = {name: self.env[name] for name in [
            'PATH', 'HOME', 'XDG_RUNTIME_DIR', 'XDG_CONFIG_HOME', 'XDG_DATA_HOME',
            'XDG_CACHE_HOME', 'LANG']}
        env.update({
            'QT_QPA_PLATFORM': 'offscreen', 'QT_QUICK_BACKEND': 'software',
            'QT_FORCE_STDERR_LOGGING': '1',
            'ARTEL_TEST_CLI': str(ROOT / 'build/bin/artelctl'),
            'ARTEL_RESULT_ENDPOINT': self.endpoint,
            'ARTEL_RESULT_OWNER_TOKEN': str(self.token_path),
            'ARTEL_RESULT_PROJECT': self.project, 'ARTEL_RESULT_TASK': self.task,
            'ARTEL_RESULT_REPO': str(self.repo),
        })
        assert not any(name.startswith('AGTERM') for name in env)
        log_path = self.root / 'private/qml.log'
        with log_path.open('wb') as log:
            child = self.spawn([str(ROOT / 'desktop/build/e2e/acceptance'),
                                'reviewedTaskResult', '-maxwarnings', '0'],
                               env=env, stdout=log, stderr=subprocess.STDOUT)
        end = time.monotonic() + 120
        while child.poll() is None and time.monotonic() < end:
            self.pump()
        if child.poll() is None:
            self.stop(child, True)
            raise AssertionError('shared production QML watchdog')
        output = log_path.read_bytes()
        assert child.returncode == 0, (output.decode()[-4000:], bytes(self.output[-2000:]))
        assert all(secret.encode() not in output for secret in self.secrets)

    def scenario(self):
        self.launch()
        self.start_pi()
        self.enable_delivery()
        self.run_qml()
        self.wait(lambda: Provider.calls == 1, 'exactly one provider call')
        self.wait(lambda: b'Fictional answer' in self.output, 'deterministic Pi answer')
        assert self.control.command(type='state')['sends'] == 1
        messages = self.request('GET', f'/v1/tasks/{self.task}/messages')['messages']
        assert len(messages) == 1 and messages[0]['status'] == 'observed'
        assert self.task in messages[0]['text']
        results = self.request('GET', f'/v1/tasks/{self.task}/results?after=0&limit=50')
        assert results['current_result_id'] and len(results['entries']) == 3
        snapshot = self.request('GET', '/v1/snapshot')
        task = next(row for row in snapshot['tasks'] if row['id'] == self.task)
        assert task['state'] == 'backlog', 'manual accept/return did not finish'
        self.command('/fixture-quit')
        self.wait(lambda: self.pi.poll() is not None, 'Pi quit')
        assert self.pi.returncode == 0
        assert all(secret.encode() not in self.output for secret in self.secrets)

    def command(self, text):
        os.write(self.master, text.encode() + b'\r')

    def close(self):
        for child in reversed(self.children):
            self.stop(child, True)
        if self.control is not None:
            self.control.close()
        if self.master is not None:
            os.close(self.master)
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
        assert not self.thread.is_alive()


def main():
    if len(sys.argv) > 1 and sys.argv[1] == '--owned-child':
        import ctypes
        libc = ctypes.CDLL(None, use_errno=True)
        if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0 or os.getppid() != int(sys.argv[2]):
            raise SystemExit(125)
        os.execvpe(sys.argv[3], sys.argv[3:], os.environ)

    def interrupted(_signal, _frame):
        raise SystemExit('reviewed result watchdog interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    with tempfile.TemporaryDirectory(prefix='artel-delivery-result-') as tmp:
        fixture = ResultFixture(Path(tmp))
        try:
            fixture.scenario()
        finally:
            fixture.close()
    assert not Path(tmp).exists()
    print('Shared Pi 4a/daemon/Git/production-QML reviewed result: PASS')


if __name__ == '__main__':
    main()
