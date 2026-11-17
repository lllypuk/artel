#!/usr/bin/env python3
"""Real daemon/CLI + pinned Pi PTY + production QML + independent HTTP oracle.
All children, credentials and databases belong to this temporary fixture.
"""
import fcntl
import http.client
import http.server
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import socket
import struct
import subprocess
import sys
import tempfile
import termios
import threading
import time
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[2]
PACKAGE = ROOT / 'integrations/pi-observer'
sys.path.insert(0, str(PACKAGE / 'tests'))
from pty_probe import Provider  # noqa: E402


def uid():
    return str(uuid.uuid4())


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('artel-observer', timeout=2)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(2)
        self.sock.connect(self.path)


class UnixHTTP(http.server.ThreadingHTTPServer):
    address_family = socket.AF_UNIX

    def server_bind(self):
        self.socket.bind(self.server_address)
        self.server_name, self.server_port = 'artel-observer', 0


class Acceptance:
    def __init__(self, root):
        self.root = root
        self.children = []
        self.daemon = self.pi = None
        self.master = None
        self.output = bytearray()
        self.dropped = False
        self.duplicates = 0
        self.proxy = self.proxy_thread = None
        self.env = {'PATH': str(Path(shutil.which('node')).parent) + ':/usr/bin:/bin',
                    'HOME': str(root), 'XDG_RUNTIME_DIR': str(root / 'runtime'),
                    'XDG_CONFIG_HOME': str(root / 'config'), 'XDG_DATA_HOME': str(root / 'data'),
                    'XDG_CACHE_HOME': str(root / 'cache'), 'PI_CODING_AGENT_DIR': str(root / 'agent'),
                    'PI_OFFLINE': '1', 'TERM': 'xterm-256color', 'LANG': 'C.UTF-8',
                    'ARTEL_FIXTURE_DELAY': '3000', 'ARTEL_PROBE_TRACE': str(root / 'trace'),
                    'ARTEL_FIXTURE_SESSION': str(root / 'session'),
                    'QT_QPA_PLATFORM': 'offscreen', 'QT_QUICK_BACKEND': 'software'}
        for name in ['runtime', 'agent', 'private', 'work']:
            (root / name).mkdir(mode=0o700)
        self.socket = str(root / 'private/observer.sock')
        self.token_path = root / 'private/owner.token'
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        Provider.calls = 0
        self.env['ARTEL_FIXTURE_URL'] = f'http://127.0.0.1:{self.server.server_port}/v1'

    def spawn(self, args, env=None, **kw):
        # The tiny exec wrapper arms Linux parent-death SIGKILL before exec.
        # Even a killed watchdog supervisor must not orphan Pi/daemon/Qt.
        child = subprocess.Popen([sys.executable, '-B', __file__, '--owned-child',
                                  str(os.getpid()), *args],
                                 env=self.env if env is None else env, start_new_session=True, **kw)
        self.children.append(child)
        if os.environ.get('ARTEL_E2E_OBSERVER_PIDS'):
            with open(os.environ['ARTEL_E2E_OBSERVER_PIDS'], 'a', encoding='ascii') as manifest:
                manifest.write(str(child.pid) + '\n')
        return child

    def run(self, args, **kw):
        child = self.spawn(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE, **kw)
        try:
            out, err = child.communicate(timeout=20)
        except BaseException:
            self.stop(child, True)
            raise
        assert child.returncode == 0, (args[0], err.decode()[-2000:])
        if hasattr(self, 'secret'):
            assert self.secret.encode() not in out + err
        return out

    @staticmethod
    def stop(child, kill=False):
        if child is not None:
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGKILL if kill else signal.SIGTERM)
            child.wait(timeout=5)
            if child.stdout:
                child.stdout.close()
            if child.stderr:
                child.stderr.close()

    def launch(self, address='127.0.0.1:0', data='db'):
        self.daemon = self.spawn([str(ROOT / 'build/bin/arteld'), '--listen', address,
                                  '--data-dir', str(self.root / data), '--token-file', str(self.token_path),
                                  '--observer-socket', self.socket], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert select.select([self.daemon.stdout], [], [], 10)[0], 'daemon startup deadline'
        line = self.daemon.stdout.readline().decode().strip()
        assert line.startswith('arteld: listening '), line
        self.address = line.split()[-1]
        self.endpoint = 'http://' + self.address
        self.owner = self.token_path.read_text().strip()

    def owner_request(self, method, path, body=None, status=200):
        headers = {'Authorization': 'Bearer ' + self.owner}
        if body is not None:
            headers.update({'Content-Type': 'application/json', 'Idempotency-Key': uid()})
        request = urllib.request.Request(self.endpoint + path, method=method, headers=headers,
                                         data=None if body is None else json.dumps(body).encode())
        try:
            response = self.opener.open(request, timeout=2)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            assert response.code == status, (path, response.code, response.read())
            return json.load(response)

    def observer_request(self, method, path, body=None, status=200):
        connection = UnixConnection(self.socket)
        try:
            connection.request(method, '/observer/v1/' + path,
                               body=None if body is None else json.dumps(body),
                               headers={'Authorization': 'Bearer ' + self.secret, 'Content-Type': 'application/json'})
            response = connection.getresponse()
            payload = response.read()
            assert response.status == status, (path, response.status, payload)
            return json.loads(payload)
        finally:
            connection.close()

    def projection(self):
        return self.owner_request('GET', '/v1/observers')

    def record(self):
        return next(o for o in self.projection()['observers'] if o['observer_id'] == self.observer_id)

    def runtime(self, runtime):
        return next((r for r in self.record()['runtimes'] if r['registration']['runtime_id'] == runtime), {})

    def pump(self):
        if self.master is not None and select.select([self.master], [], [], 0.03)[0]:
            try:
                data = os.read(self.master, 65536)
            except OSError:
                return
            self.output.extend(data)
            assert len(self.output) < 1024 * 1024, 'PTY output bound'
            if b'\x1b[6n' in data:
                os.write(self.master, b'\x1b[1;1R')

    def wait(self, predicate, label, timeout=16):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            self.pump()
            value = predicate()
            if value:
                return value
            time.sleep(.02)
        raise AssertionError('acceptance deadline: ' + label)

    def command(self, text):
        os.write(self.master, text.encode() + b'\r')

    def provision(self, name):
        config = self.root / 'private' / name
        result = self.run([str(ROOT / 'build/bin/artelctl'), 'observer', 'provision', '--name', name,
                           '--observer-socket', self.socket, '--output', str(config),
                           '--endpoint', self.endpoint, '--token-file', str(self.token_path)])
        value = json.loads(config.read_text())
        assert value['status'] == 'confirmed'
        self.observer_id = value['observer_id']
        self.secret = Path(value['token_file']).read_text().strip()
        Provider.forbidden = self.secret
        assert self.secret.encode() not in result
        self.env['ARTEL_OBSERVER_CONFIG'] = str(config)
        if self.proxy is None:
            fixture = self

            class Forward(http.server.BaseHTTPRequestHandler):
                def log_message(self, *_args):
                    pass

                def forward(self):
                    connection = UnixConnection(fixture.socket)
                    try:
                        body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
                        connection.request(self.command, self.path, body,
                                           {'Authorization': self.headers['Authorization'],
                                            'Content-Type': 'application/json'})
                        response = connection.getresponse()
                        payload = response.read()
                        if self.command == 'PUT' and response.status == 200:
                            value = json.loads(payload)
                            if value['result'] == 'duplicate':
                                fixture.duplicates += 1
                            if not fixture.dropped:
                                fixture.dropped = True
                                self.close_connection = True
                                return  # Server committed; bridge never receives the ACK.
                        self.send_response(response.status)
                        self.end_headers()
                        self.wfile.write(payload)
                    except OSError:
                        self.send_response(503)
                        self.end_headers()
                        self.wfile.write(b'{"error":"unavailable"}')
                    finally:
                        connection.close()

                do_GET = do_POST = do_PUT = forward

            self.proxy_path = str(self.root / 'private/fault.sock')
            self.proxy = UnixHTTP(self.proxy_path, Forward)
            Path(self.proxy_path).chmod(0o600)
            self.proxy_thread = threading.Thread(target=self.proxy.serve_forever, daemon=True)
            self.proxy_thread.start()
        value['observer_socket'] = self.proxy_path
        config.write_text(json.dumps(value))

    def bind(self, runtime):
        record = self.record()
        return self.owner_request('PATCH', '/v1/observers/' + self.observer_id + '/binding',
                                  {'expected_version': record['version'], 'runtime_id': runtime,
                                   'project_id': self.project})

    def action(self, action):
        return self.owner_request('POST', '/v1/observers/' + self.observer_id + '/' + action,
                                  {'expected_version': self.record()['version']})

    def qml(self, runtime, **expected):
        env = self.env.copy()
        env.update({'ARTEL_OBSERVER_ENDPOINT': self.endpoint,
                    'ARTEL_OBSERVER_OWNER_TOKEN': str(self.token_path),
                    'ARTEL_OBSERVER_EXPECTED': json.dumps({'runtime_id': runtime, 'navigable': False, **expected})})
        # Drain PTY while Qt starts; Pi must not block on terminal output.
        child = self.spawn([str(ROOT / 'desktop/build/e2e/acceptance'),
                            'observerExternal', '-maxwarnings', '0'], env=env,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        end = time.monotonic() + 15
        while child.poll() is None and time.monotonic() < end:
            self.pump()
        assert child.poll() is not None, 'QML watchdog'
        out, err = child.communicate()
        assert child.returncode == 0, (out.decode()[-3000:], err.decode()[-1000:])
        assert self.secret.encode() not in out + err

    def start_pi(self):
        previous = {r['registration']['runtime_id'] for r in self.record()['runtimes']}
        if self.master is not None:
            os.close(self.master)
        master, slave = pty.openpty()
        self.master = master
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 120, 0, 0))
        args = [shutil.which('node'), str(PACKAGE / 'node_modules/@earendil-works/pi-coding-agent/dist/cli.js'),
                '--offline', '--no-approve', '--no-extensions', '--no-skills', '--no-prompt-templates',
                '--no-context-files', '--no-themes', '--no-mcp', '--no-builtin-tools',
                '-e', str(PACKAGE / 'src/index.ts'), '-e', str(PACKAGE / 'tests/probe.ts'),
                '-e', str(PACKAGE / 'tests/fixture.ts'), '--model', 'artel-fixture/local']
        self.pi = self.spawn(args, cwd=self.root / 'work', stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)
        return self.wait(lambda: [r for r in self.record()['runtimes']
                                  if r['registration']['runtime_id'] not in previous],
                         'Pi registration')[-1]['registration']['runtime_id']

    def scenario(self):
        self.launch()
        self.provision('fictional-one')
        project = self.owner_request('POST', '/v1/projects',
                                     {'name': 'Fictional project', 'root_path': str(self.root / 'work')}, 201)
        self.project = project['id']
        tasks = [self.owner_request('POST', '/v1/projects/' + self.project + '/tasks',
                                    {'title': 'Fictional task ' + str(n)}, 201)['id'] for n in range(2)]
        self.owner_request('PUT', '/v1/projects/' + self.project + '/queue',
                           {'task_ids': tasks, 'expected_queue_version': 1})
        baseline = self.owner_request('GET', '/v1/snapshot')
        assert baseline['event_seq'] >= 4 and baseline['tasks'] and baseline['queues']
        runtime = self.start_pi()
        self.wait(lambda: self.runtime(runtime).get('freshness') == 'fresh', 'initial heartbeat')
        self.wait(lambda: self.dropped and self.duplicates > 0, 'lost ACK identical retry/dedup')
        self.qml(runtime, disposition='candidate', freshness='fresh')
        if os.environ.get('ARTEL_E2E_OBSERVER_NEGATIVE'):
            raise AssertionError('intentional full-stack observer assertion')
        assert Provider.calls == 0, 'observer must not start prompts'
        self.bind(runtime)
        self.wait(lambda: self.runtime(runtime).get('generation') == 1 and
                  self.runtime(runtime).get('freshness') == 'fresh', 'bound heartbeat')
        self.command('Fictional input')
        self.wait(lambda: self.runtime(runtime).get('activity') == 'working', 'working')
        self.qml(runtime, disposition='bound', activity='working', freshness='fresh')
        self.wait(lambda: self.runtime(runtime).get('activity') == 'idle', 'settled')
        self.command('/fixture-wait')
        self.wait(lambda: self.runtime(runtime).get('activity') == 'waiting_user', 'UI wait')
        self.qml(runtime, activity='waiting_user')
        self.wait(lambda: self.runtime(runtime).get('activity') == 'idle', 'UI finished')
        self.qml(runtime, activity='idle')
        calls = Provider.calls
        before = self.projection()
        self.stop(self.daemon, True)
        # Pi keeps running offline; only latest state is retained by the client.
        self.command('/fixture-wait')
        until = time.monotonic() + 4
        while time.monotonic() < until:
            self.pump()
        self.launch(self.address)
        assert self.projection()['database_instance_id'] == before['database_instance_id']
        assert self.runtime(runtime)['freshness'] in ['stale', 'unknown']
        self.wait(lambda: self.runtime(runtime).get('freshness') == 'fresh', 'same-runtime reconnect', 40)
        assert len(self.record()['runtimes']) == 1
        assert Provider.calls == calls, 'restart must not prompt'
        self.qml(runtime, disposition='bound', freshness='fresh')  # new desktop process
        for command in ['/fixture-fork', '/fixture-new', '/fixture-reload']:
            old_ids = {r['registration']['runtime_id'] for r in self.record()['runtimes']}
            self.command(command)
            rows = self.wait(lambda: [r for r in self.record()['runtimes']
                                     if r['registration']['runtime_id'] not in old_ids], command)
            new = rows[0]['registration']['runtime_id']
            assert rows[0]['disposition'] == 'candidate'
            assert self.record()['binding']['runtime_id'] == runtime
            self.bind(new)
            runtime = new
            self.wait(lambda: self.runtime(runtime).get('freshness') == 'fresh', 'explicit rebind')
        self.qml(runtime, disposition='bound')
        self.stop(self.pi, True)
        self.wait(lambda: self.runtime(runtime).get('freshness') == 'stale', 'SIGKILL stale', 25)
        self.qml(runtime, freshness='stale')
        assert self.record()['binding']['runtime_id'] == runtime
        # Independent wire oracle: dedup does not extend freshness, old generations fail.
        report = {'runtime_id': runtime, 'generation': self.record()['generation'], 'report_seq': 100000,
                  'activity': 'idle', 'tool_count': 0, 'lifecycle': 'running'}
        assert self.observer_request('PUT', 'report', report)['result'] == 'accepted'
        seen = self.runtime(runtime)['last_seen']
        assert self.observer_request('PUT', 'report', report)['result'] == 'duplicate'
        assert self.runtime(runtime)['last_seen'] == seen
        self.observer_request('PUT', 'report', {**report, 'generation': 0}, 409)
        retired = next(r['registration'] for r in self.record()['runtimes']
                       if r['registration']['runtime_id'] != runtime)
        self.action('prune')
        self.stop(self.daemon)
        self.launch(self.address)
        assert self.observer_request('POST', 'register', retired, 409)['error'] == 'runtime_retired'
        # Fill the real payload quota through the public surface, never SQL.
        for _ in range(7):
            registration = {**retired, 'runtime_id': uid(), 'pi_session_id': uid()}
            self.observer_request('POST', 'register', registration)
        blocked = {**retired, 'runtime_id': uid(), 'pi_session_id': uid()}
        error = self.observer_request('POST', 'register', blocked, 409)
        assert error['error'] == 'capacity_exceeded' and error['capacity'] == 'runtime_payloads'
        # Prune releases payloads, never retired identity slots. Exhaust all
        # 1024 identities through HTTP, preserving the one current binding.
        self.action('prune')
        while self.record()['identity_count'] < 1024:
            for _ in range(min(7, 1024 - self.record()['identity_count'])):
                self.observer_request('POST', 'register',
                                      {**retired, 'runtime_id': uid(), 'pi_session_id': uid()})
            self.action('prune')
        error = self.observer_request('POST', 'register', blocked, 409)
        assert error['error'] == 'capacity_exceeded' and error['capacity'] == 'runtime_identities'
        assert self.record()['binding']['runtime_id'] == runtime
        assert self.observer_request('PUT', 'report', {**report, 'report_seq': 100001})['result'] == 'accepted'
        old_id, old_secret = self.observer_id, self.secret
        self.provision('fictional-replacement')
        assert not self.record().get('binding'), 'no automatic binding transfer'
        replacement = self.start_pi()
        assert self.runtime(replacement)['disposition'] == 'candidate'
        self.bind(replacement)
        self.wait(lambda: self.runtime(replacement).get('freshness') == 'fresh', 'replacement')
        self.qml(replacement, disposition='bound', freshness='fresh')
        new_id, new_secret = self.observer_id, self.secret
        self.observer_id, self.secret = old_id, old_secret
        self.action('revoke')
        self.observer_request('GET', 'context?runtime_id=' + runtime, status=401)
        self.observer_id, self.secret = new_id, new_secret
        assert self.owner_request('GET', '/v1/snapshot') == baseline, 'manual domain changed'
        assert self.secret.encode() not in self.output and old_secret.encode() not in self.output
        # Give the new DB the same verifier: this proves the UUID pin, rather
        # than merely testing 401 against an empty principal registry.
        self.stop(self.daemon)
        self.launch(self.address, 'replacement-db')
        assert self.projection()['database_instance_id'] != before['database_instance_id']
        config = json.loads(Path(self.env['ARTEL_OBSERVER_CONFIG']).read_text())
        self.owner_request('PUT', '/v1/observers/' + self.observer_id, config['payload'])
        until = time.monotonic() + 8
        while time.monotonic() < until:
            self.pump()
        assert self.record()['runtimes'] == [], 'database pin must prevent registration'
        self.stop(self.pi, True)
        self.stop(self.daemon)
        self.launch(self.address)
        # A newly started bridge can use the original DB again, but is still
        # a candidate, not an inherited binding. Revoke while it is live.
        live = self.start_pi()
        assert self.runtime(live)['disposition'] == 'candidate'
        self.action('revoke')
        self.observer_request('GET', 'context?runtime_id=' + live, status=401)
        count = len(self.record()['runtimes'])
        until = time.monotonic() + 6
        while time.monotonic() < until:
            self.pump()
        assert len(self.record()['runtimes']) == count
        assert self.owner_request('GET', '/v1/snapshot') == baseline
        assert Provider.calls == calls

    def close(self):
        for child in reversed(self.children):
            self.stop(child, True)
            assert child.poll() is not None
        if self.master is not None:
            os.close(self.master)
        if self.proxy is not None:
            self.proxy.shutdown()
            self.proxy.server_close()
            self.proxy_thread.join(timeout=3)
            assert not self.proxy_thread.is_alive()
            Path(self.proxy_path).unlink()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=2)
        assert not self.thread.is_alive()


def main():
    if len(sys.argv) > 1 and sys.argv[1] == '--owned-child':
        import ctypes
        libc = ctypes.CDLL(None, use_errno=True)
        if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0 or os.getppid() != int(sys.argv[2]):
            raise SystemExit(125)
        os.execvpe(sys.argv[3], sys.argv[3:], os.environ)
    def interrupted(_signal, _frame):
        raise SystemExit('observer acceptance watchdog interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    if '--negative-probe' in sys.argv:
        with tempfile.TemporaryDirectory(prefix='artel-observer-negative-') as tmp:
            resources = Path(tmp) / 'resources'
            resources.mkdir()
            manifest = Path(tmp) / 'owned-pids'
            env = {**os.environ, 'TMPDIR': str(resources), 'ARTEL_E2E_OBSERVER_NEGATIVE': '1',
                   'ARTEL_E2E_OBSERVER_PIDS': str(manifest)}
            result = subprocess.run([sys.executable, '-B', __file__], env=env,
                                    capture_output=True, timeout=60)
            assert result.returncode == 1, result.stderr.decode()
            assert b'intentional full-stack observer assertion' in result.stderr
            assert list(resources.iterdir()) == [], 'negative probe leaked private files'
            pids = manifest.read_text().splitlines()
            assert len(pids) >= 3, 'negative probe did not launch real processes'
            assert all(not Path('/proc', pid).exists() for pid in pids), 'negative probe leaked a process'
        print('Observer intentional-failure/owned-process cleanup: PASS')
        return
    with tempfile.TemporaryDirectory(prefix='ao-') as tmp:
        fixture = Acceptance(Path(tmp))
        try:
            fixture.scenario()
        finally:
            fixture.close()
    assert not Path(tmp).exists()
    print('Full-stack observer Pi/daemon/production-QML recovery: PASS')


if __name__ == '__main__':
    main()
