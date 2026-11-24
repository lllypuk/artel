#!/usr/bin/env python3
"""Stage-4b thin slice: real Pi tool, daemon, production QML and 4a delivery.

Every process, credential, provider and database is synthetic and private to a
temporary fixture. The provider counts a custom answer only as the fresh last
message of a request, never as later transcript history.
"""
import fcntl
import hashlib
import http.client
import http.server
import io
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import socket
import sqlite3
import socketserver
import struct
import subprocess
import sys
import tarfile
import tempfile
import termios
import threading
import time
import unittest
import urllib.error
import urllib.request
import uuid

ROOT = Path(__file__).resolve().parents[3]
PACKAGE = ROOT / 'integrations/pi-observer'
CLI = PACKAGE / 'node_modules/@earendil-works/pi-coding-agent/dist/cli.js'


def uid():
    return str(uuid.uuid4())


def text_content(message):
    content = message.get('content', '')
    if isinstance(content, str):
        return content
    if isinstance(content, list):
        return '\n'.join(str(part.get('text', '')) for part in content if isinstance(part, dict))
    return ''


class Provider(http.server.BaseHTTPRequestHandler):
    task_id = ''
    tool_calls = 0
    answer_turns = 0
    fresh = []
    tool_lists = {}
    forbidden = []
    hold_started = threading.Event()
    hold_release = threading.Event()

    def log_message(self, *_args):
        pass

    def do_POST(self):
        assert self.path == '/v1/chat/completions'
        payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model'] == 'local'
        encoded = json.dumps(payload)
        assert all(secret not in encoded for secret in type(self).forbidden)
        last = payload['messages'][-1]
        content = text_content(last)
        type(self).fresh.append(content)
        type(self).tool_lists[content] = {tool['function']['name'] for tool in payload.get('tools', [])}
        delta = {'role': 'assistant', 'content': 'Fictional completion'}
        reason = 'stop'
        if 'HOLD_BUSY' in content and last.get('role') == 'user':
            type(self).hold_started.set()
            assert type(self).hold_release.wait(25), 'busy fixture release deadline'
        elif 'ASK_OWNER' in content and last.get('role') == 'user':
            assert type(self).task_id in content, 'owner did not hand off the explicit task UUID'
            question = 'SECOND_QUESTION' if 'SECOND_QUESTION' in content else 'FIRST_QUESTION'
            if 'MULTILINE' in content:
                question = 'FIRST_QUESTION "quoted"\nAnswer: still part of the question'
            type(self).tool_calls += 1
            delta = {'role': 'assistant', 'tool_calls': [{
                'index': 0, 'id': f'fictional-owner-question-{type(self).tool_calls}', 'type': 'function',
                'function': {'name': 'artel_ask_owner',
                             'arguments': json.dumps({'task_id': type(self).task_id, 'text': question})}}]}
            reason = 'tool_calls'
        elif content.startswith('Artel owner answer\n') and last.get('role') == 'user':
            type(self).answer_turns += 1
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        for chunk in [
                {'choices': [{'index': 0, 'delta': delta, 'finish_reason': None}]},
                {'choices': [{'index': 0, 'delta': {}, 'finish_reason': reason}]}]:
            self.wfile.write(('data: ' + json.dumps(chunk) + '\n\n').encode())
        self.wfile.write(b'data: [DONE]\n\n')
        self.wfile.flush()


class UnixConnection(http.client.HTTPConnection):
    def __init__(self, path):
        super().__init__('artel-observer', timeout=3)
        self.path = path

    def connect(self):
        self.sock = socket.socket(socket.AF_UNIX)
        self.sock.settimeout(3)
        self.sock.connect(self.path)


class SpendDropProxy:
    """Private UDS proxy for delivery barriers and an optional lost spend response."""
    def __init__(self, listen_path, target_path):
        self.deny_attach = False
        self.attach_started = threading.Event()
        self.release_attach = threading.Event()
        self.disconnected_attach = threading.Event()
        self.denied_attach = threading.Event()
        self.legacy_refusal = None
        self.drop_next = False
        self.dropped = 0
        self.writer_contexts = 0
        fixture = self

        class Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
            daemon_threads = True

        class Handler(socketserver.BaseRequestHandler):
            def handle(self):
                request = self._read(self.request)
                if request.startswith(b'GET /delivery/v1/context '):
                    fixture.writer_contexts += 1
                if fixture.deny_attach and request.startswith(b'POST /delivery/v1/attach '):
                    # Only the first attach can satisfy the barrier. A retry
                    # must not mask a timeout on the connection we held.
                    first = not fixture.attach_started.is_set()
                    if first:
                        fixture.attach_started.set()
                        while True:
                            released = fixture.release_attach.wait(.02)
                            # The full request was read; readability here is peer EOF.
                            if select.select([self.request], [], [], 0)[0]:
                                fixture.disconnected_attach.set()
                                return
                            if released:
                                break
                    body = b'{"error":"owner_conflict"}'
                    try:
                        self.request.sendall(b'HTTP/1.1 409 Conflict\r\nContent-Type: application/json\r\n'
                                             + f'Content-Length: {len(body)}\r\nConnection: close\r\n\r\n'.encode() + body)
                    except (BrokenPipeError, ConnectionResetError):
                        if first:
                            fixture.disconnected_attach.set()
                    else:
                        if first:
                            fixture.denied_attach.set()
                    return
                upstream = socket.socket(socket.AF_UNIX)
                upstream.settimeout(3)
                upstream.connect(target_path)
                try:
                    upstream.sendall(request)
                    response = self._read(upstream)
                finally:
                    upstream.close()
                if fixture.drop_next and request.startswith(b'POST /delivery/v1/spend '):
                    fixture.drop_next = False
                    fixture.dropped += 1
                    return
                self.request.sendall(response)
                if request.startswith(b'POST /delivery/v1/attach '):
                    request_body = json.loads(request.split(b'\r\n\r\n', 1)[1])
                    if 'capability' not in request_body and response.startswith(b'HTTP/1.1 409 '):
                        reason = json.loads(response.split(b'\r\n\r\n', 1)[1]).get('error')
                        fixture.legacy_refusal = (request_body['expected_previous'], reason)

            @staticmethod
            def _read(connection):
                data = bytearray()
                while b'\r\n\r\n' not in data:
                    chunk = connection.recv(65536)
                    if not chunk:
                        raise AssertionError('truncated proxy headers')
                    data.extend(chunk)
                    assert len(data) <= 65536
                head, body = bytes(data).split(b'\r\n\r\n', 1)
                length = 0
                for line in head.split(b'\r\n')[1:]:
                    if line.lower().startswith(b'content-length:'):
                        length = int(line.split(b':', 1)[1])
                while len(body) < length:
                    chunk = connection.recv(length - len(body))
                    if not chunk:
                        raise AssertionError('truncated proxy body')
                    body += chunk
                return head + b'\r\n\r\n' + body

        self.server = Server(listen_path, Handler)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()

    def close(self):
        self.release_attach.set()
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
        assert not self.thread.is_alive()


class Fixture:
    def __init__(self, root, allow_questions, extension_dir=None, daemon_binary=None, spend_proxy=False):
        self.root = root
        self.allow_questions = allow_questions
        self.extension_dir = extension_dir
        self.daemon_binary = daemon_binary or ROOT / 'build/bin/arteld'
        self.spend_proxy = None
        self.use_spend_proxy = spend_proxy
        self.tool_selection = None
        self.children = []
        self.daemon = self.pi = None
        self.master = None
        self.output = bytearray()
        for name in ['agent', 'private', 'work', 'runtime']:
            (root / name).mkdir(mode=0o700)
        self.socket = str(root / 'private/observer.sock')
        self.daemon_socket = str(root / 'private/daemon-observer.sock') if spend_proxy else self.socket
        self.token_path = root / 'private/owner.token'
        self.delivery_config = root / 'private/delivery.json'
        self.env = {
            'PATH': str(Path(shutil.which('node')).parent) + ':/usr/bin:/bin',
            'HOME': str(root), 'XDG_RUNTIME_DIR': str(root / 'runtime'),
            'XDG_CONFIG_HOME': str(root / 'config'), 'XDG_DATA_HOME': str(root / 'xdg-data'),
            'XDG_CACHE_HOME': str(root / 'cache'), 'PI_CODING_AGENT_DIR': str(root / 'agent'),
            'PI_OFFLINE': '1', 'TERM': 'xterm-256color', 'LANG': 'C.UTF-8',
            'ARTEL_PROBE_TRACE': str(root / 'trace.jsonl'),
            'ARTEL_FIXTURE_SESSION': str(root / 'session'),
            'QT_QPA_PLATFORM': 'offscreen', 'QT_QUICK_BACKEND': 'software',
        }
        Provider.task_id = ''
        Provider.tool_calls = Provider.answer_turns = 0
        Provider.fresh = []
        Provider.tool_lists = {}
        Provider.forbidden = []
        Provider.hold_started = threading.Event()
        Provider.hold_release = threading.Event()
        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.env['ARTEL_FIXTURE_URL'] = f'http://127.0.0.1:{self.server.server_port}/v1'
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def spawn(self, args, **kwargs):
        child = subprocess.Popen([sys.executable, '-B', __file__, '--owned-child', str(os.getpid()), *args],
                                 env=self.env, start_new_session=True, **kwargs)
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
        if child.stderr:
            child.stderr.close()

    def run(self, args):
        child = self.spawn(args, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        out, err = child.communicate(timeout=20)
        assert child.returncode == 0, (args[0], err.decode()[-2000:])
        assert all(secret.encode() not in out + err for secret in Provider.forbidden)
        return out

    def launch(self):
        if self.use_spend_proxy:
            self.spend_proxy = SpendDropProxy(self.socket, self.daemon_socket)
        self.daemon = self.spawn([str(self.daemon_binary), '--listen', '127.0.0.1:0',
                                  '--data-dir', str(self.root / 'db'), '--token-file', str(self.token_path),
                                  '--observer-socket', self.daemon_socket], stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        assert select.select([self.daemon.stdout], [], [], 10)[0], 'daemon startup deadline'
        line = self.daemon.stdout.readline().decode().strip()
        assert line.startswith('arteld: listening '), (line, self.daemon.poll(),
            self.daemon.stderr.read().decode() if self.daemon.poll() is not None else '')
        self.endpoint = 'http://' + line.split()[-1]
        self.owner = self.token_path.read_text().strip()
        Provider.forbidden.append(self.owner)

    def owner_request(self, method, path, body=None, status=200, key=None):
        headers = {'Authorization': 'Bearer ' + self.owner}
        if body is not None:
            headers.update({'Content-Type': 'application/json',
                            'Idempotency-Key': key or uid()})
        request = urllib.request.Request(self.endpoint + path, method=method, headers=headers,
                                         data=None if body is None else json.dumps(body).encode())
        try:
            response = self.opener.open(request, timeout=3)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            payload = response.read()
            assert response.code == status, (path, response.code, payload)
            return json.loads(payload)

    def delivery_request(self, method, path, body=None, status=200, key=None):
        token = self.delivery_token
        connection = UnixConnection(self.socket)
        headers = {'Authorization': 'Bearer ' + token, 'Content-Type': 'application/json'}
        if key:
            headers['Idempotency-Key'] = key
        try:
            connection.request(method, path, None if body is None else json.dumps(body), headers)
            response = connection.getresponse()
            payload = response.read()
            assert response.status == status, (path, response.status, payload)
            return json.loads(payload)
        finally:
            connection.close()

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

    def wait(self, predicate, label, timeout=30):
        end = time.monotonic() + timeout
        while time.monotonic() < end:
            self.pump()
            value = predicate()
            if value:
                return value
            if self.pi is not None and self.pi.poll() is not None:
                break
            time.sleep(.02)
        raise AssertionError(f'{label}: terminal={bytes(self.output[-2000:])!r}')

    def command(self, text):
        os.write(self.master, text.encode() + b'\r')

    def manual_turn(self, prompt):
        trace = self.root / 'trace.jsonl'
        offset = trace.stat().st_size
        self.command(prompt)
        self.wait(lambda: prompt in Provider.tool_lists, 'provider received ' + prompt)

        def settled():
            with trace.open('rb') as events:
                events.seek(offset)
                return b'"event":"agent_settled"' in events.read()

        self.wait(settled, 'Pi settled after manual turn')
        return Provider.tool_lists[prompt]

    def record(self):
        registry = self.owner_request('GET', '/v1/observers')
        return next(row for row in registry['observers'] if row['observer_id'] == self.observer_id)

    def questions(self):
        return self.owner_request('GET', f"/v1/tasks/{self.task}/questions?instance_id={self.instance}&after=0&limit=5")['questions']

    def start_pi(self, resume=False):
        if self.master is not None:
            os.close(self.master)
        master, slave = pty.openpty()
        self.master = master
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 120, 0, 0))
        artifact = shutil.copytree(self.extension_dir or PACKAGE / 'dist',
                                   self.root / ('installed-observer-resume' if resume else 'installed-observer'))
        args = [shutil.which('node'), str(CLI), '--offline', '--no-approve',
                '--no-extensions', '--no-skills', '--no-prompt-templates', '--no-context-files',
                '--no-themes', '--no-mcp', '--no-builtin-tools', '-e', str(Path(artifact) / 'index.mjs'),
                '-e', str(PACKAGE / 'tests/probe.ts'), '-e', str(PACKAGE / 'tests/fixture.ts'),
                '--model', 'artel-fixture/local']
        if self.tool_selection:
            args.extend(['--tools', self.tool_selection])
        if resume:
            args.extend(['--session', (self.root / 'session').read_text().strip()])
        self.pi = self.spawn(args, cwd=self.root / 'work', stdin=slave, stdout=slave, stderr=slave)
        os.close(slave)

        previous_runtime = getattr(self, 'runtime', '')
        previous_session = getattr(self, 'pi_session', '')

        def registered():
            for row in self.record()['runtimes']:
                registration = row['registration']
                if resume and (registration['runtime_id'] == previous_runtime or
                               registration['pi_session_id'] != previous_session):
                    continue
                return registration
            return None
        registration = self.wait(registered, 'replacement Pi registration' if resume else 'original Pi registration')
        self.runtime = registration['runtime_id']
        self.pi_session = registration['pi_session_id']

    def delivery_args(self):
        record = self.record()
        args = [str(ROOT / 'build/bin/artelctl'), 'delivery', 'enable', '--id', self.observer_id,
                '--expected-version', str(record['version']), '--runtime-id', self.runtime,
                '--project-id', self.project, '--pi-session-id', self.pi_session,
                '--observer-socket', self.socket, '--output', str(self.delivery_config),
                '--endpoint', self.endpoint, '--token-file', str(self.token_path)]
        if self.allow_questions:
            args.append('--allow-owner-questions')
        return args

    def enable_delivery(self, wait_ready=True):
        self.run(self.delivery_args())
        config = json.loads(self.delivery_config.read_text())
        self.binding = config['delivery_binding_id']
        assert config.get('allow_owner_questions', False) == self.allow_questions
        self.delivery_token = Path(config['token_file']).read_text().strip()
        Provider.forbidden.append(self.delivery_token)

        def active():
            row = self.owner_request('GET', f'/v1/observers/{self.observer_id}/delivery/{self.binding}')
            return row['status'] == 'active'
        if not wait_ready:
            return
        self.wait(active, 'writer attach')
        self.wait(lambda: self.delivery_request('GET', '/delivery/v1/context')['ready'], 'writer ready')

    def start_qml(self, question, answer, commission='', observed=True, total=1, trace=False,
                  stop_directory=None, history_reason=None):
        env = self.env.copy()
        env.update({'ARTEL_QUESTION_ENDPOINT': self.endpoint,
                    'ARTEL_QUESTION_OWNER_TOKEN': str(self.token_path),
                    'ARTEL_QUESTION_PROJECT': self.project, 'ARTEL_QUESTION_TASK': self.task,
                    'ARTEL_QUESTION_TEXT': question, 'ARTEL_QUESTION_ANSWER': answer,
                    'ARTEL_QUESTION_COMMISSION': commission,
                    'ARTEL_QUESTION_EXPECT_OBSERVED': '1' if observed else '0',
                    'ARTEL_QUESTION_TOTAL': str(total)})
        if trace:
            env['ARTEL_QUESTION_TRACE'] = str(self.root / 'trace.jsonl')
        if stop_directory is not None:
            env['ARTEL_QUESTION_STOP_DIRECTORY'] = str(stop_directory)
        if history_reason is not None:
            env['ARTEL_QUESTION_HISTORY_REASON'] = history_reason
        child = subprocess.Popen([sys.executable, '-B', __file__, '--owned-child', str(os.getpid()),
                                  str(ROOT / 'desktop/build/e2e/acceptance'), 'ownerQuestionRoundTrip', '-maxwarnings', '0'],
                                 env=env, start_new_session=True, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        self.children.append(child)
        return child

    def wait_qml(self, child):
        end = time.monotonic() + 30
        while child.poll() is None and time.monotonic() < end:
            self.pump()
        if child.poll() is None:
            self.stop(child, True)
            raise AssertionError('production QML watchdog')
        out, err = child.communicate()
        assert child.returncode == 0, (out.decode()[-3000:], err.decode()[-2000:], bytes(self.output[-1000:]))
        assert all(secret.encode() not in out + err for secret in Provider.forbidden)

    def qml(self, question, answer, commission='', observed=True, total=1, trace=False):
        self.wait_qml(self.start_qml(question, answer, commission, observed, total, trace))

    def prepare(self, enable=True):
        self.launch()
        self.run([str(ROOT / 'build/bin/artelctl'), 'observer', 'provision', '--name', 'fictional-questions',
                  '--observer-socket', self.socket, '--output', str(self.root / 'private/observer.json'),
                  '--endpoint', self.endpoint, '--token-file', str(self.token_path)])
        config = json.loads((self.root / 'private/observer.json').read_text())
        self.observer_id = config['observer_id']
        Provider.forbidden.append(Path(config['token_file']).read_text().strip())
        self.env['ARTEL_OBSERVER_CONFIG'] = str(self.root / 'private/observer.json')
        project = self.owner_request('POST', '/v1/projects',
                                     {'name': 'Fictional questions', 'root_path': str(self.root / 'work')}, 201)
        self.project = project['id']
        self.task = self.owner_request('POST', f'/v1/projects/{self.project}/tasks',
                                       {'title': 'Initially empty fictional task'}, 201)['id']
        Provider.task_id = self.task
        self.instance = self.owner_request('GET', '/v1/snapshot')['instance_id']
        self.start_pi()
        record = self.record()
        self.owner_request('PATCH', f'/v1/observers/{self.observer_id}/binding',
                           {'expected_version': record['version'], 'runtime_id': self.runtime,
                            'project_id': self.project})
        if enable:
            self.enable_delivery()

    def enable_base_writer_question_without_publishing(self):
        """Create a real opted-in grant and answer before the old writer picks it up."""
        record = self.record()
        self.binding = uid()
        self.delivery_token = uid().replace('-', '') * 2
        journal_id = uid()
        binding_dir = self.root / 'private/delivery' / self.binding
        binding_dir.mkdir(parents=True, mode=0o700)
        token_path = binding_dir / 'token'
        token_path.write_text(self.delivery_token + '\n')
        token_path.chmod(0o600)
        journal_path = binding_dir / 'journal.json'
        journal_path.write_text(json.dumps({'journal_id': journal_id, 'entries': {}}) + '\n')
        journal_path.chmod(0o600)
        Provider.forbidden.append(self.delivery_token)
        granted = self.owner_request('PUT', f'/v1/observers/{self.observer_id}/delivery', {
            'expected_version': record['version'], 'delivery_binding_id': self.binding,
            'journal_id': journal_id, 'verifier': hashlib.sha256(self.delivery_token.encode()).hexdigest(),
            'runtime_id': self.runtime, 'project_id': self.project, 'pi_session_id': self.pi_session,
            'instance_id': self.instance, 'allow_owner_questions': True})
        assert granted['status'] == 'pending', 'new grant must be pending before attach'
        capable_writer = uid()
        self.delivery_request('POST', '/delivery/v1/attach', {
            'writer_id': capable_writer, 'expected_previous': '', 'journal_id': journal_id,
            'runtime_id': self.runtime, 'pi_session_id': self.pi_session,
            'instance_id': self.instance, 'capability': 'owner_questions_v1'})
        self.delivery_request('POST', '/delivery/v1/ready', {'writer_id': capable_writer, 'instance_id': self.instance})
        question = self.delivery_request('POST', '/delivery/v1/questions', {
            'instance_id': self.instance, 'task_id': self.task, 'writer_id': capable_writer,
            'text': 'BASE_WRITER_MUST_NOT_READ'}, key=uid())
        answered = self.owner_request('POST', f"/v1/tasks/{self.task}/questions/{question['id']}/answer", {
            'instance_id': self.instance, 'text': 'BASE_WRITER_MUST_NOT_SPEND'}, key=uid())
        config = {'status': 'confirmed', 'delivery_binding_id': self.binding,
                  'database_instance_id': self.instance, 'observer_socket': self.socket,
                  'token_file': str(token_path), 'journal_file': str(journal_path),
                  'journal_id': journal_id, 'allow_owner_questions': True}
        self.delivery_config.write_text(json.dumps(config) + '\n')
        self.delivery_config.chmod(0o600)
        return capable_writer, question['id'], answered['message_id']

    def quit_pi(self):
        if self.pi.poll() is None:
            self.command('/fixture-quit')
            self.wait(lambda: self.pi.poll() is not None, 'Pi quit')
        assert self.pi.returncode == 0
        assert all(secret.encode() not in self.output for secret in Provider.forbidden)

    def close(self):
        Provider.hold_release.set()
        if self.spend_proxy is not None:
            self.spend_proxy.close()
        for child in reversed(self.children):
            self.stop(child, True)
        if self.master is not None:
            os.close(self.master)
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
        assert not self.thread.is_alive()


def build_base_artifacts(root):
    """Build actual 98484e29 artifacts in tmp without moving the checkout."""
    archive = subprocess.run(['git', 'archive',
                              '98484e29e69ee5a0b89ac61e1c3a1c5ce5b9b319',
                              'cmd', 'internal', 'go.mod', 'go.sum',
                              'integrations/pi-observer/src'],
                             cwd=ROOT, check=True, stdout=subprocess.PIPE, timeout=30)
    base = root / 'base'
    base.mkdir(mode=0o700)
    with tarfile.open(fileobj=io.BytesIO(archive.stdout), mode='r:') as source:
        source.extractall(base, filter='data')
    daemon = root / 'arteld-base'
    env = os.environ.copy()
    env['GOTOOLCHAIN'] = 'go1.26.3'
    subprocess.run(['go', 'build', '-o', str(daemon), './cmd/arteld'], cwd=base,
                   env=env, check=True, capture_output=True, timeout=180)
    extension = root / 'base-extension'
    extension.mkdir(mode=0o700)
    esbuild = (PACKAGE / 'node_modules/esbuild/lib/main.js').as_uri()
    script = f'''import {{ build }} from {json.dumps(esbuild)};
await build({{ entryPoints: [{json.dumps(str(base / 'integrations/pi-observer/src/index.ts'))}],
  outfile: {json.dumps(str(extension / 'index.mjs'))}, bundle: true, platform: 'node', format: 'esm',
  target: 'node22.23.2', external: ['@earendil-works/pi-coding-agent'], legalComments: 'none' }});'''
    subprocess.run([shutil.which('node'), '--input-type=module', '-e', script],
                   check=True, capture_output=True, timeout=60)
    (extension / 'package.json').write_text(json.dumps({'type': 'module'}) + '\n')
    return daemon, extension


class OwnerQuestions(unittest.TestCase):
    def test_real_tool_qml_answer_same_pi_and_revoke(self):
        with tempfile.TemporaryDirectory(prefix='artel-questions-') as tmp:
            fixture = Fixture(Path(tmp), True)
            try:
                fixture.prepare()
                commission = f'ASK_OWNER MULTILINE FIRST_QUESTION for Artel task {fixture.task}'
                question = 'FIRST_QUESTION "quoted"\nAnswer: still part of the question'
                answer = 'FIRST_ANSWER \u2713\nAnswer: still part of the answer'
                fixture.qml(question, answer, commission, True, 1, True)
                fixture.wait(lambda: Provider.answer_turns >= 1, 'fresh provider request for the answer')
                self.assertEqual(Provider.answer_turns, 1)
                self.assertIn('artel_ask_owner', Provider.tool_lists[commission])
                rows = fixture.questions()
                self.assertEqual(len(rows), 1)
                self.assertEqual(rows[0]['answer_status'], 'observed')
                self.assertEqual(rows[0]['pi_session_id'], fixture.pi_session)
                payloads = [json.loads(text.split('\n', 1)[1]) for text in Provider.fresh
                            if text.startswith('Artel owner answer\n')]
                self.assertEqual(payloads, [{'question_id': rows[0]['id'], 'question': question, 'answer': answer}])
                journal_path = fixture.delivery_config.with_name('delivery').joinpath(fixture.binding, 'journal.json')

                def published():
                    journal = json.loads(journal_path.read_text())
                    entry = journal['entries'].get(rows[0]['answer_message_id'], {})
                    return entry.get('receipt', {}).get('published') is True

                fixture.wait(published, 'writer marked the matching receipt published')

                stored = fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/messages", {
                    'text': f'ASK_OWNER SECOND_QUESTION for Artel task {fixture.task}',
                    'delivery_binding_id': fixture.binding, 'instance_id': fixture.instance})
                self.assertEqual(stored['status'], 'stored')
                fixture.wait(lambda: any(row['text'] == 'SECOND_QUESTION' for row in fixture.questions()),
                             'second real tool question')
                fixture.command('HOLD_BUSY while owner revokes')
                fixture.wait(lambda: Provider.hold_started.is_set(), 'original Pi held busy')
                fixture.qml('SECOND_QUESTION', 'SECOND_ANSWER', observed=False, total=2)
                fixture.run([str(ROOT / 'build/bin/artelctl'), 'delivery', 'revoke', '--id', fixture.observer_id,
                             '--binding-id', fixture.binding, '--output', str(fixture.delivery_config),
                             '--endpoint', fixture.endpoint, '--token-file', str(fixture.token_path)])
                revoked = fixture.owner_request('GET', f'/v1/observers/{fixture.observer_id}/delivery/{fixture.binding}')
                self.assertEqual(revoked['status'], 'revoked')
                Provider.hold_release.set()
                before = Provider.answer_turns
                time.sleep(3)
                fixture.pump()
                self.assertEqual(before, 1)
                self.assertEqual(Provider.answer_turns, 1, 'revoked answer reached a fresh Pi turn')
                second = next(row for row in fixture.questions() if row['text'] == 'SECOND_QUESTION')
                self.assertEqual(second['answer_text'], 'SECOND_ANSWER')
                self.assertEqual(second['answer_status'], 'unknown')
                fixture.wait_qml(fixture.start_qml('SECOND_QUESTION', 'SECOND_ANSWER',
                                                  history_reason='delivery_revoked'))
                fixture.quit_pi()
            finally:
                fixture.close()
        self.assertFalse(Path(tmp).exists())

    def test_stopped_original_pi_qml_answer_and_replacement_refusal(self):
        with tempfile.TemporaryDirectory(prefix='artel-questions-stopped-') as tmp:
            fixture = Fixture(Path(tmp), True)
            try:
                fixture.prepare()
                fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/messages", {
                    'text': f'ASK_OWNER FIRST_QUESTION for Artel task {fixture.task}',
                    'delivery_binding_id': fixture.binding, 'instance_id': fixture.instance})
                question = fixture.wait(lambda: next((row for row in fixture.questions()
                                                      if row['text'] == 'FIRST_QUESTION'), None),
                                        'question before stopping original Pi')
                stop_directory = fixture.root / 'private/qml-stop'
                stop_directory.mkdir(mode=0o700)
                child = fixture.start_qml('FIRST_QUESTION', 'ANSWER_AFTER_STOP', observed=False,
                                          trace=True, stop_directory=stop_directory)
                fixture.wait(lambda: (stop_directory / 'stop-request').exists(),
                             'production QML answer ready')
                original_runtime = fixture.runtime
                original_session = fixture.pi_session
                fixture.quit_pi()
                deadline = time.monotonic() + 15
                while fixture.owner_request(
                        'GET', f"/v1/tasks/{fixture.task}/delivery")['ready']:
                    assert time.monotonic() < deadline, 'original writer readiness expired'
                    time.sleep(.05)
                (stop_directory / 'writer-stopped').write_text(original_runtime + '\n')
                fixture.wait_qml(child)

                row = next(row for row in fixture.questions() if row['id'] == question['id'])
                self.assertEqual(row['answer_text'], 'ANSWER_AFTER_STOP')
                self.assertEqual(row['answer_status'], 'stored')
                database = f"file:{fixture.root / 'db/artel.sqlite'}?mode=ro"
                with sqlite3.connect(database, uri=True) as connection:
                    self.assertEqual(connection.execute(
                        'SELECT count(*) FROM owner_questions WHERE id=? AND status=\'answered\'',
                        (question['id'],)).fetchone()[0], 1)
                    self.assertEqual(connection.execute(
                        'SELECT count(*) FROM delivery_messages WHERE id=? AND json_extract(record,\'$.kind\')=\'answer\'',
                        (row['answer_message_id'],)).fetchone()[0], 1)

                fixture.start_pi(resume=True)
                self.assertNotEqual(fixture.runtime, original_runtime)
                self.assertEqual(fixture.pi_session, original_session)
                time.sleep(3)
                fixture.pump()
                self.assertEqual(Provider.answer_turns, 0)
                self.assertFalse(any('ANSWER_AFTER_STOP' in text for text in Provider.fresh))

                context = fixture.delivery_request('GET', '/delivery/v1/context')
                self.assertFalse(context['ready'])
                replacement_writer = uid()
                config = json.loads(fixture.delivery_config.read_text())
                denied = fixture.delivery_request('POST', '/delivery/v1/attach', {
                    'writer_id': replacement_writer, 'expected_previous': context['writer_id'],
                    'journal_id': config['journal_id'], 'runtime_id': fixture.runtime,
                    'pi_session_id': fixture.pi_session,
                    'instance_id': fixture.instance, 'capability': 'owner_questions_v1'}, status=409)
                self.assertEqual(denied['error'], 'assignment_mismatch')
                denied = fixture.delivery_request(
                    'GET', f'/delivery/v1/next?writer_id={replacement_writer}&instance_id={fixture.instance}', status=409)
                self.assertEqual(denied['error'], 'owner_conflict')
                self.assertNotIn('ANSWER_AFTER_STOP', json.dumps(denied))
                denied = fixture.delivery_request('POST', '/delivery/v1/spend', {
                    'instance_id': fixture.instance, 'writer_id': replacement_writer, 'message_id': row['answer_message_id']}, status=409)
                self.assertEqual(denied['error'], 'owner_conflict')
                self.assertEqual(Provider.answer_turns, 0)
                row = next(row for row in fixture.questions() if row['id'] == question['id'])
                self.assertEqual(row['answer_status'], 'stored')
                fixture.quit_pi()
            finally:
                fixture.close()
        self.assertFalse(Path(tmp).exists())

    def test_manual_turn_precedes_answer_and_ambiguous_spend_never_repeats(self):
        with tempfile.TemporaryDirectory(prefix='artel-questions-manual-') as tmp:
            fixture = Fixture(Path(tmp), True, spend_proxy=True)
            try:
                fixture.prepare()
                fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/messages", {
                    'text': f'ASK_OWNER FIRST_QUESTION for Artel task {fixture.task}',
                    'delivery_binding_id': fixture.binding, 'instance_id': fixture.instance})
                question = fixture.wait(lambda: next((row for row in fixture.questions()
                                                      if row['text'] == 'FIRST_QUESTION'), None), 'question before manual turn')
                manual = 'HOLD_BUSY MANUAL_BEFORE_ANSWER'
                fixture.command(manual)
                fixture.wait(Provider.hold_started.is_set, 'manual provider turn held open')
                answered = fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/questions/{question['id']}/answer", {
                    'instance_id': fixture.instance, 'text': 'ANSWER_AFTER_MANUAL'}, key=uid())
                # These context reads are serial within one writer. The second proves
                # the first post-storage cycle finished while the provider was held.
                before = fixture.spend_proxy.writer_contexts
                fixture.wait(lambda: fixture.spend_proxy.writer_contexts >= before + 2,
                             'writer completed a cycle while the manual turn was held')
                self.assertFalse(Provider.hold_release.is_set())
                self.assertEqual(Provider.answer_turns, 0)
                self.assertFalse(any('ANSWER_AFTER_MANUAL' in text for text in Provider.fresh))
                with sqlite3.connect(f"file:{fixture.root / 'db/artel.sqlite'}?mode=ro", uri=True) as db:
                    self.assertEqual(db.execute('SELECT count(*) FROM delivery_attempts a JOIN delivery_messages m '
                                                'ON m.seq=a.message_seq WHERE m.id=?',
                                                (answered['message_id'],)).fetchone()[0], 0)
                Provider.hold_release.set()
                fixture.wait(lambda: Provider.answer_turns == 1, 'answer after held manual turn')
                fixture.wait(lambda: any(row.get('answer_status') == 'observed' for row in fixture.questions()),
                             'matching receipt after manual turn')
                answer_turns = [text for text in Provider.fresh if 'ANSWER_AFTER_MANUAL' in text]
                self.assertEqual(len(answer_turns), 1)
                self.assertIn('FIRST_QUESTION', answer_turns[0])
                self.assertLess(Provider.fresh.index(manual), Provider.fresh.index(answer_turns[0]))
                fixture.quit_pi()
            finally:
                fixture.close()
        self.assertFalse(Path(tmp).exists())

        with tempfile.TemporaryDirectory(prefix='artel-questions-spend-ack-') as tmp:
            fixture = Fixture(Path(tmp), True, spend_proxy=True)
            try:
                fixture.prepare()
                fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/messages", {
                    'text': f'ASK_OWNER FIRST_QUESTION for Artel task {fixture.task}',
                    'delivery_binding_id': fixture.binding, 'instance_id': fixture.instance})
                question = fixture.wait(lambda: next((row for row in fixture.questions()
                                                      if row['text'] == 'FIRST_QUESTION'), None), 'question before lost spend ACK')
                fixture.spend_proxy.drop_next = True
                fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/questions/{question['id']}/answer", {
                    'instance_id': fixture.instance, 'text': 'UNKNOWN_SPEND_ANSWER'}, key=uid())
                fixture.wait(lambda: fixture.spend_proxy.dropped == 1, 'real spend response dropped')
                time.sleep(3)
                fixture.pump()
                self.assertEqual(Provider.answer_turns, 0, 'independent provider counter saw an automatic retry')
                self.assertFalse(any('UNKNOWN_SPEND_ANSWER' in text for text in Provider.fresh))
                row = next(row for row in fixture.questions() if row['id'] == question['id'])
                self.assertEqual(row['answer_status'], 'unknown')
                fixture.quit_pi()
            finally:
                fixture.close()
        self.assertFalse(Path(tmp).exists())

    def assert_base_daemon_refuses_v7(self, fixture, old_daemon):
        fixture.stop(fixture.daemon)
        db_path = fixture.root / 'db/artel.sqlite'

        def database_identity():
            with sqlite3.connect(f"file:{db_path}?mode=ro", uri=True) as db:
                return (db.execute('PRAGMA user_version').fetchone()[0],
                        db.execute('SELECT instance_id FROM metadata WHERE id=1').fetchone()[0])

        before = database_identity()
        self.assertEqual(before, (7, fixture.instance))
        child = fixture.spawn([str(old_daemon), '--listen', '127.0.0.1:0',
                               '--data-dir', str(fixture.root / 'db'), '--token-file', str(fixture.token_path),
                               '--observer-socket', fixture.daemon_socket],
                              stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        out, err = child.communicate(timeout=10)
        self.assertEqual(child.returncode, 1)
        self.assertNotIn(b'listening', out)
        # The real base binary uses store schema v3 and deliberately masks the
        # store's unsupported-v7 error. Its v3 startup is the positive control below.
        self.assertEqual(err.strip(), b'arteld: cannot initialize database')
        self.assertEqual(database_identity(), before)

    def test_actual_base_extension_and_old_daemon_compatibility(self):
        with tempfile.TemporaryDirectory(prefix='artel-base-artifacts-') as artifacts:
            old_daemon, old_extension = build_base_artifacts(Path(artifacts))
            with tempfile.TemporaryDirectory(prefix='artel-base-writer-') as tmp:
                fixture = Fixture(Path(tmp), False, extension_dir=old_extension, spend_proxy=True)
                try:
                    fixture.prepare()
                    fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/messages", {
                        'text': 'PLAIN_BASE_4A', 'delivery_binding_id': fixture.binding,
                        'instance_id': fixture.instance})
                    fixture.wait(lambda: any('PLAIN_BASE_4A' in text for text in Provider.fresh),
                                 'actual base extension ordinary 4a')
                    def commission_observed():
                        messages = fixture.owner_request('GET', f"/v1/tasks/{fixture.task}/messages")['messages']
                        return any(row['text'] == 'PLAIN_BASE_4A' and row['status'] == 'observed' for row in messages)

                    fixture.wait(commission_observed, 'actual base extension ordinary 4a receipt')
                    fixture.run([str(ROOT / 'build/bin/artelctl'), 'delivery', 'revoke', '--id', fixture.observer_id,
                                 '--binding-id', fixture.binding, '--output', str(fixture.delivery_config),
                                 '--endpoint', fixture.endpoint, '--token-file', str(fixture.token_path)])
                    capable_writer, question_id, answer_id = fixture.enable_base_writer_question_without_publishing()
                    refused = fixture.wait(lambda: fixture.spend_proxy.legacy_refusal,
                                           'daemon rejected actual base extension attach')
                    self.assertEqual(refused, (capable_writer, 'capability_required'))
                    self.assertEqual(fixture.delivery_request('GET', '/delivery/v1/context')['writer_id'], capable_writer)
                    self.assertEqual(Provider.answer_turns, 0)
                    row = next(row for row in fixture.questions() if row['id'] == question_id)
                    self.assertEqual(row['answer_message_id'], answer_id)
                    self.assertEqual(row['answer_status'], 'stored')
                    pending = fixture.delivery_request('GET', f'/delivery/v1/next?writer_id={capable_writer}&instance_id={fixture.instance}')
                    self.assertEqual(pending['message']['id'], answer_id, 'old writer read or spent the 4b answer')
                    fixture.quit_pi()
                    self.assert_base_daemon_refuses_v7(fixture, old_daemon)
                finally:
                    fixture.close()
            self.assertFalse(Path(tmp).exists())

            with tempfile.TemporaryDirectory(prefix='artel-old-daemon-') as tmp:
                fixture = Fixture(Path(tmp), True, daemon_binary=old_daemon)
                try:
                    fixture.prepare(enable=False)
                    child = fixture.spawn(fixture.delivery_args(), stdout=subprocess.PIPE, stderr=subprocess.PIPE)
                    out, err = child.communicate(timeout=20)
                    self.assertNotEqual(child.returncode, 0, (out, err))
                    self.assertFalse(fixture.delivery_config.exists())
                    binding_dirs = list((fixture.root / 'private/delivery').iterdir())
                    self.assertEqual(len(binding_dirs), 1)
                    fixture.owner_request('GET', f"/v1/observers/{fixture.observer_id}/delivery/{binding_dirs[0].name}",
                                          status=404)
                    fixture.quit_pi()
                finally:
                    fixture.close()
            self.assertFalse(Path(tmp).exists())
        self.assertFalse(Path(artifacts).exists())

    def test_tree_restores_only_current_owner_question_capability(self):
        with tempfile.TemporaryDirectory(prefix='artel-tree-tools-') as tmp:
            fixture = Fixture(Path(tmp), True)
            try:
                def branch_to(prompt):
                    fixture.output.clear()
                    fixture.command('/tree')
                    fixture.wait(lambda: b'Type to search:' in fixture.output, 'Pi tree selector')
                    fixture.command(prompt)
                    fixture.wait(lambda: b'Summarize branch?' in fixture.output, 'selected branch')
                    os.write(fixture.master, b'\r')  # No summary.
                    fixture.wait(lambda: b'Navigated to selected point' in fixture.output, 'tree navigation complete')
                    os.write(fixture.master, b'\x15')  # Clear any recalled single-line prompt.

                fixture.prepare(enable=False)
                before = 'Fictional branch without Artel tool'
                active = 'Fictional branch with attached tool'
                inactive_tools = fixture.manual_turn(before)
                self.assertNotIn('artel_ask_owner', inactive_tools)
                fixture.enable_delivery()
                active_tools = fixture.manual_turn(active)
                self.assertEqual(active_tools, inactive_tools | {'artel_ask_owner'})
                branch_to(before)
                self.assertEqual(fixture.manual_turn('Fictional turn after restoring ungranted branch'), active_tools)

                fixture.run([str(ROOT / 'build/bin/artelctl'), 'delivery', 'revoke', '--id', fixture.observer_id,
                             '--binding-id', fixture.binding, '--output', str(fixture.delivery_config),
                             '--endpoint', fixture.endpoint, '--token-file', str(fixture.token_path)])
                fixture.wait(lambda: b'delivery stopped' in fixture.output, 'writer noticed revocation')
                branch_to(active)
                self.assertEqual(fixture.manual_turn('Fictional turn after restoring old granted branch'), inactive_tools)
                self.assertEqual(fixture.questions(), [])
                fixture.quit_pi()
            finally:
                fixture.close()

    def test_revoked_question_tool_stays_hidden_on_registry_refresh(self):
        with tempfile.TemporaryDirectory(prefix='artel-refresh-tools-') as tmp:
            fixture = Fixture(Path(tmp), True)
            try:
                owner_tool, marker = 'artel_ask_owner', 'fixture_registry_refresh'
                fixture.tool_selection = f'{owner_tool},{marker}'
                fixture.prepare()
                self.assertEqual(fixture.manual_turn('Fictional turn with attached writer'), {owner_tool})
                fixture.run([str(ROOT / 'build/bin/artelctl'), 'delivery', 'revoke', '--id', fixture.observer_id,
                             '--binding-id', fixture.binding, '--output', str(fixture.delivery_config),
                             '--endpoint', fixture.endpoint, '--token-file', str(fixture.token_path)])
                fixture.wait(lambda: b'delivery stopped' in fixture.output, 'writer noticed revocation')
                self.assertEqual(fixture.manual_turn('Fictional turn after revoke'), set())
                fixture.command('/fixture-refresh-tools')
                self.assertEqual(fixture.manual_turn('Fictional turn after registry refresh'), {marker})
                fixture.enable_delivery()
                self.assertEqual(fixture.manual_turn('Fictional turn after new opted-in attach'), {marker, owner_tool})
                fixture.quit_pi()
            finally:
                fixture.close()

    def test_question_requires_explicit_opt_in(self):
        with tempfile.TemporaryDirectory(prefix='artel-questions-off-') as tmp:
            fixture = Fixture(Path(tmp), False)
            try:
                fixture.prepare()
                fixture.owner_request('POST', f"/v1/tasks/{fixture.task}/messages", {
                    'text': f'ASK_OWNER FIRST_QUESTION for Artel task {fixture.task}',
                    'delivery_binding_id': fixture.binding, 'instance_id': fixture.instance})
                fixture.wait(lambda: Provider.tool_calls == 1, 'provider attempted unavailable tool')
                fixture.wait(lambda: len(Provider.fresh) >= 2, 'unknown tool turn completed')
                self.assertEqual(fixture.questions(), [])
                prompt = f'ASK_OWNER FIRST_QUESTION for Artel task {fixture.task}'
                self.assertNotIn('artel_ask_owner', Provider.tool_lists[prompt])
                context = fixture.delivery_request('GET', '/delivery/v1/context')
                error = fixture.delivery_request('POST', '/delivery/v1/questions', {
                    'instance_id': fixture.instance, 'task_id': fixture.task,
                    'writer_id': context['writer_id'], 'text': 'FIRST_QUESTION'}, 409, uid())
                self.assertEqual(error['error'], 'capability_required')
                self.assertEqual(fixture.questions(), [])
                self.assertEqual(Provider.answer_turns, 0)
                fixture.quit_pi()
            finally:
                fixture.close()
        self.assertFalse(Path(tmp).exists())

    def test_opted_in_tool_stays_hidden_when_attach_is_refused(self):
        with tempfile.TemporaryDirectory(prefix='artel-attach-refused-') as tmp:
            fixture = Fixture(Path(tmp), True, spend_proxy=True)
            try:
                fixture.tool_selection = 'artel_ask_owner'
                fixture.prepare(enable=False)
                fixture.spend_proxy.deny_attach = True
                fixture.enable_delivery(wait_ready=False)
                fixture.wait(fixture.spend_proxy.attach_started.is_set, 'opted-in attach held')
                prompt = 'Manual fictional turn before attach completes'
                fixture.command(prompt)
                proxy = fixture.spend_proxy
                fixture.wait(lambda: prompt in Provider.tool_lists or proxy.disconnected_attach.is_set(),
                             'provider tool list during first attach')
                self.assertFalse(proxy.disconnected_attach.is_set(), 'first attach ended before the provider sample')
                self.assertNotIn('artel_ask_owner', Provider.tool_lists[prompt])
                proxy.release_attach.set()
                fixture.wait(lambda: proxy.denied_attach.is_set() or proxy.disconnected_attach.is_set(),
                             'first attach refused or disconnected')
                self.assertTrue(proxy.denied_attach.is_set(), 'sampled attach disconnected before refusal')
                prompt = 'Manual fictional turn after attach refusal'
                fixture.command(prompt)
                fixture.wait(lambda: prompt in Provider.tool_lists, 'provider tool list after attach refusal')
                self.assertNotIn('artel_ask_owner', Provider.tool_lists[prompt])
                self.assertEqual(fixture.questions(), [])
                fixture.quit_pi()
            finally:
                fixture.close()
        self.assertFalse(Path(tmp).exists())


def main():
    if len(sys.argv) > 1 and sys.argv[1] == '--owned-child':
        import ctypes
        libc = ctypes.CDLL(None, use_errno=True)
        if libc.prctl(1, signal.SIGKILL, 0, 0, 0) != 0 or os.getppid() != int(sys.argv[2]):
            raise SystemExit(125)
        os.execvpe(sys.argv[3], sys.argv[3:], os.environ)

    def interrupted(_signal, _frame):
        raise SystemExit('owner questions watchdog interrupted')
    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    unittest.main()


if __name__ == '__main__':
    main()
