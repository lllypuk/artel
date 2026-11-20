"""Stage-4a slice: real arteld, both production listeners, pinned bound Pi.

The owner enables delivery for one bound runtime, commissions a task message and
sees it observed; negative controls prove no Pi call happens before readiness,
on replayed spends, for other projects or after an instance/database swap.
"""
import hashlib
import http.server
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import socket
import subprocess
import tempfile
import threading
import time
import unittest
import urllib.error
import urllib.request
import uuid

PACKAGE = Path(__file__).resolve().parents[1]
REPO = PACKAGE.parents[1]
CLI = PACKAGE / 'node_modules/@earendil-works/pi-coding-agent/dist/cli.js'


class Provider(http.server.BaseHTTPRequestHandler):
    calls = 0

    def log_message(self, *_args):
        pass

    def do_POST(self):
        assert self.path == '/v1/chat/completions'
        payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model'] == 'local'
        type(self).calls += 1
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        for chunk in [
            {'choices': [{'index': 0, 'delta': {'role': 'assistant', 'content': 'Fictional answer'}, 'finish_reason': None}]},
            {'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop'}]}]:
            self.wfile.write(('data: ' + json.dumps(chunk) + '\n\n').encode())
        self.wfile.write(b'data: [DONE]\n\n')
        self.wfile.flush()


class Owner:
    def __init__(self, address, token):
        self.address = address
        self.token = token

    def request(self, method, path, body=None, key=None, token=None):
        data = json.dumps(body).encode() if body is not None else None
        headers = {'Authorization': 'Bearer ' + (token or self.token)}
        if data is not None:
            headers['Content-Type'] = 'application/json'
        if key:
            headers['Idempotency-Key'] = key
        req = urllib.request.Request(self.address + path, data=data, headers=headers, method=method)
        try:
            with urllib.request.urlopen(req, timeout=5) as response:
                return response.status, json.loads(response.read())
        except urllib.error.HTTPError as error:
            return error.code, json.loads(error.read())
        except urllib.error.URLError:
            return 0, None


class Control:
    def __init__(self, path):
        self.socket = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.socket.connect(str(path))
        self.socket.settimeout(10)
        self.buffered = b''
        self.next_id = 0

    def command(self, **payload):
        self.next_id += 1
        payload['id'] = self.next_id
        self.socket.sendall(json.dumps(payload).encode() + b'\n')
        while b'\n' not in self.buffered:
            chunk = self.socket.recv(65536)
            if not chunk:
                raise AssertionError('control socket closed')
            self.buffered += chunk
        line, self.buffered = self.buffered.split(b'\n', 1)
        reply = json.loads(line)
        if not reply['ok']:
            raise AssertionError(reply['error'])
        return reply['result']

    def close(self):
        self.socket.close()


class DeliverySlice(unittest.TestCase):
    def test_owner_to_one_bound_pi_session(self):
        Provider.calls = 0
        with tempfile.TemporaryDirectory(prefix='artel-delivery-') as tmp:
            root = Path(tmp)
            (root / 'agent').mkdir(mode=0o700)
            (root / 'work').mkdir(mode=0o700)
            (root / 'work-b').mkdir(mode=0o700)
            (root / 'data').mkdir(mode=0o700)
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()

            probe = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
            probe.bind(('127.0.0.1', 0))
            port = probe.getsockname()[1]
            probe.close()
            address = f'http://127.0.0.1:{port}'

            binary = REPO / 'build/bin/arteld'
            if not binary.exists():
                subprocess.run(['go', 'build', '-o', str(root / 'arteld'), './cmd/arteld'],
                               cwd=REPO, check=True, capture_output=True, timeout=180)
                binary = root / 'arteld'
            daemon = subprocess.Popen([str(binary), '--data-dir', str(root / 'data'),
                                       '--token-file', str(root / 'token'),
                                       '--listen', f'127.0.0.1:{port}',
                                       '--observer-socket', str(root / 'observer.sock')],
                                      stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            owner = None
            for _ in range(100):
                if (root / 'token').exists():
                    owner = Owner(address, (root / 'token').read_text().strip())
                    status, _ = owner.request('GET', '/healthz')
                    if status == 200:
                        break
                time.sleep(0.1)
            else:
                self.fail('arteld did not start')
            try:
                self.artel_flow(self, root, owner, server)
            finally:
                daemon.terminate()
                try:
                    daemon.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    daemon.kill()
                    daemon.wait(timeout=5)
                server.shutdown()
                server.server_close()
                thread.join(timeout=2)
        self.assertFalse(root.exists())

    def artel_flow(self, case, root, owner, server):
        case = self
        status, project_a = owner.request('POST', '/v1/projects', {'name': 'Fictional A', 'root_path': str(root / 'work')}, key=str(uuid.uuid4()))
        case.assertEqual(status, 201, project_a)
        status, project_b = owner.request('POST', '/v1/projects', {'name': 'Fictional B', 'root_path': str(root / 'work-b')}, key=str(uuid.uuid4()))
        case.assertEqual(status, 201, project_b)
        status, task = owner.request('POST', f"/v1/projects/{project_a['id']}/tasks", {'title': 'Fictional task'}, key=str(uuid.uuid4()))
        case.assertEqual(status, 201, task)
        status, task_b = owner.request('POST', f"/v1/projects/{project_b['id']}/tasks", {'title': 'Other project task'}, key=str(uuid.uuid4()))
        case.assertEqual(status, 201, task_b)

        private = root / 'pi-private'
        private.mkdir(mode=0o700)
        observer_token = uuid.uuid4().hex + uuid.uuid4().hex
        observer_id = str(uuid.uuid4())
        status, snapshot = owner.request('GET', '/v1/snapshot')
        case.assertEqual(status, 200)
        instance = snapshot['instance_id']
        status, _ = owner.request('PUT', f'/v1/observers/{observer_id}',
                                  {'label': 'Fictional', 'verifier': hashlib.sha256(observer_token.encode()).hexdigest()},
                                  key=str(uuid.uuid4()))
        case.assertEqual(status, 200)
        config = {'status': 'confirmed', 'observer_id': observer_id, 'database_instance_id': instance,
                  'observer_socket': str(root / 'observer.sock'), 'token_file': str(private / 'token'),
                  'provisioning_key': str(uuid.uuid4()), 'payload': {'label': 'Fictional',
                  'verifier': hashlib.sha256(observer_token.encode()).hexdigest()}}
        for name, data in [('config', json.dumps(config)), ('token', observer_token + '\n')]:
            path = private / name
            path.write_text(data)
            path.chmod(0o600)

        env = {'PATH': str(Path(shutil.which('node')).parent) + ':/usr/bin:/bin',
               'HOME': str(root), 'PI_CODING_AGENT_DIR': str(root / 'agent'), 'PI_OFFLINE': '1',
               'XDG_CONFIG_HOME': str(root / 'config'), 'XDG_DATA_HOME': str(root / 'data-xdg'),
               'XDG_CACHE_HOME': str(root / 'cache'), 'TERM': 'xterm-256color', 'LANG': 'C.UTF-8',
               'ARTEL_PROBE_TRACE': str(root / 'trace.jsonl'),
               'ARTEL_FIXTURE_SESSION': str(root / 'original-session'),
               'ARTEL_FIXTURE_URL': f'http://127.0.0.1:{server.server_port}/v1',
               'ARTEL_OBSERVER_CONFIG': str(private / 'config'),
               'ARTEL_DELIVERY_CONTROL': str(private / 'control.sock'),
               'ARTEL_DELIVERY_JOURNAL': str(private / 'journal.json'),
               'ARTEL_DELIVERY_ROOT': str(root)}
        master, slave = pty.openpty()
        import fcntl
        import struct
        import termios
        fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 120, 0, 0))
        args = [shutil.which('node'), str(CLI), '--offline', '--no-approve',
                '--no-extensions', '--no-skills', '--no-prompt-templates',
                '--no-context-files', '--no-themes', '--no-mcp', '--no-builtin-tools',
                '-e', str(PACKAGE / 'dist/index.mjs'),
                '-e', str(PACKAGE / 'tests/probe.ts'),
                '-e', str(PACKAGE / 'tests/fixture.ts'),
                '-e', str(PACKAGE / 'tests/delivery_fixture.mjs'),
                '--model', 'artel-fixture/local']
        child = subprocess.Popen(args, cwd=root / 'work', env=env,
                                 stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
        os.close(slave)
        output = bytearray()
        control = None
        for _ in range(100):
            if (private / 'control.sock').exists():
                try:
                    control = Control(private / 'control.sock')
                    break
                except OSError:
                    pass
            time.sleep(0.1)
        case.assertIsNotNone(control, 'fixture control socket did not start')

        def pump():
            if select.select([master], [], [], 0.05)[0]:
                try:
                    data = os.read(master, 65536)
                except OSError:
                    return
                output.extend(data)
                case.assertLess(len(output), 1024 * 1024)
                if b'\x1b[6n' in data:
                    os.write(master, b'\x1b[1;1R')

        def wait(predicate, label, timeout=30):
            until = time.monotonic() + timeout
            while time.monotonic() < until:
                pump()
                if predicate():
                    return
                if child.poll() is not None:
                    break
            case.fail(f'{label}: terminal={bytes(output[-2000:])!r}')

        def command(text):
            os.write(master, text.encode() + b'\r')

        def binding_status():
            status, body = owner.request('GET', f"/v1/observers/{observer_id}/delivery/{BINDING['id']}")
            return body.get('status') if status == 200 else None

        def task_ready():
            status, body = owner.request('GET', f"/v1/tasks/{task['id']}/delivery")
            return body.get('ready') is True if status == 200 else False

        def message_statuses():
            status, body = owner.request('GET', f"/v1/tasks/{task['id']}/messages")
            case.assertEqual(status, 200)
            return [m['status'] for m in body['messages']]

        try:
            BINDING = {'id': str(uuid.uuid4())}
            # The extension registers against the real daemon; owner binds it.
            runtime = {}
            def registered():
                status, registry = owner.request('GET', '/v1/observers')
                if status != 200 or not registry['observers']:
                    return False
                entry = registry['observers'][0]
                if not entry.get('runtimes'):
                    return False
                runtime['id'] = entry['runtimes'][0]['registration']['runtime_id']
                runtime['session'] = entry['runtimes'][0]['registration']['pi_session_id']
                return True
            wait(registered, 'observer registration')
            status, _ = owner.request('PATCH', f'/v1/observers/{observer_id}/binding',
                                      {'expected_version': 1, 'runtime_id': runtime['id'], 'project_id': project_a['id']},
                                      key=str(uuid.uuid4()))
            case.assertEqual(status, 200)

            delivery_token = uuid.uuid4().hex + uuid.uuid4().hex
            journal_id = str(uuid.uuid4())
            status, binding = owner.request('PUT', f'/v1/observers/{observer_id}/delivery', {
                'expected_version': 2, 'delivery_binding_id': BINDING['id'], 'journal_id': journal_id,
                'verifier': hashlib.sha256(delivery_token.encode()).hexdigest(),
                'runtime_id': runtime['id'], 'project_id': project_a['id'],
                'pi_session_id': runtime['session'], 'instance_id': instance}, key=str(uuid.uuid4()))
            case.assertEqual(status, 200, binding)
            case.assertEqual(binding['status'], 'pending')

            # Pending (no writer yet): refusal without a record.
            status, error = owner.request('POST', f"/v1/tasks/{task['id']}/messages",
                                          {'text': 'Fictional commission', 'delivery_binding_id': BINDING['id'], 'instance_id': instance},
                                          key=str(uuid.uuid4()))
            case.assertEqual(status, 409, error)
            case.assertEqual(error['code'], 'not_ready')
            case.assertEqual(message_statuses(), [])

            # Writer pickup: hold readiness first, then publish the config.
            # The journal is created by the owner bootstrap exactly once; the
            # writer only opens it and never recreates a missing one.
            control.command(type='hold-ready', ms=8000)
            (private / 'journal.json').write_text(json.dumps({'journal_id': journal_id, 'entries': {}}) + '\n')
            (private / 'journal.json').chmod(0o600)
            delivery_config = {'status': 'confirmed', 'delivery_binding_id': BINDING['id'],
                               'database_instance_id': instance, 'observer_socket': str(root / 'observer.sock'),
                               'token_file': str(private / 'delivery.token'), 'journal_file': str(private / 'journal.json'),
                               'journal_id': journal_id}
            for name, data in [('delivery.json', json.dumps(delivery_config)), ('delivery.token', delivery_token + '\n')]:
                path = private / name
                path.write_text(data)
                path.chmod(0o600)
            wait(lambda: binding_status() == 'active', 'writer attach')
            # Attached but readiness held: refusal without a record.
            status, error = owner.request('POST', f"/v1/tasks/{task['id']}/messages",
                                          {'text': 'Fictional commission', 'delivery_binding_id': BINDING['id'], 'instance_id': instance},
                                          key=str(uuid.uuid4()))
            case.assertEqual(status, 409, error)
            case.assertEqual(message_statuses(), [])
            wait(task_ready, 'writer readiness')

            key = str(uuid.uuid4())
            body = {'text': 'Fictional commission', 'delivery_binding_id': BINDING['id'], 'instance_id': instance}
            status, stored = owner.request('POST', f"/v1/tasks/{task['id']}/messages", body, key=key)
            case.assertEqual(status, 200, stored)
            case.assertEqual(stored['status'], 'stored')
            # Same request replayed returns the original record, never a second call.
            status, replay = owner.request('POST', f"/v1/tasks/{task['id']}/messages", body, key=key)
            case.assertEqual(status, 200, replay)
            case.assertEqual(replay['id'], stored['id'])
            case.assertEqual(message_statuses(), ['stored'])
            changed = dict(body, text='Changed text')
            status, error = owner.request('POST', f"/v1/tasks/{task['id']}/messages", changed, key=key)
            case.assertEqual(status, 409, error)
            case.assertEqual(error['code'], 'idempotency_conflict')
            # Wrong database instance: refusal without replay or a record.
            status, error = owner.request('POST', f"/v1/tasks/{task['id']}/messages",
                                          dict(body, instance_id=str(uuid.uuid4())), key=str(uuid.uuid4()))
            case.assertEqual(status, 409, error)
            case.assertEqual(error['code'], 'instance_mismatch')
            # Task of another project: refusal without a record.
            status, error = owner.request('POST', f"/v1/tasks/{task_b['id']}/messages", body, key=str(uuid.uuid4()))
            case.assertEqual(status, 409, error)
            case.assertEqual(error['code'], 'assignment_mismatch')
            case.assertEqual(message_statuses(), ['stored'])

            wait(lambda: Provider.calls == 1, 'exactly one provider call')
            wait(lambda: message_statuses() == ['observed'], 'observed receipt')

            state = control.command(type='state')
            case.assertEqual(state['sends'], 1)
            second = control.command(type='second-writer')
            case.assertTrue(second['locked'], second)

            # A second commission on the same binding delivers exactly once more.
            status, stored2 = owner.request('POST', f"/v1/tasks/{task['id']}/messages",
                                            dict(body, text='Second fictional commission'), key=str(uuid.uuid4()))
            case.assertEqual(status, 200, stored2)
            wait(lambda: Provider.calls == 2, 'second provider call')
            wait(lambda: message_statuses() == ['observed', 'observed'], 'second observed receipt')
            case.assertEqual(control.command(type='state')['sends'], 2)

            command('/fixture-quit')
            wait(lambda: child.poll() is not None, 'quit')
            case.assertEqual(child.returncode, 0)
            case.assertNotIn(delivery_token.encode(), bytes(output))
            case.assertNotIn(observer_token.encode(), bytes(output))
        finally:
            control.close()
            if child.poll() is None:
                os.killpg(child.pid, signal.SIGKILL)
                child.wait(timeout=5)
            os.close(master)


if __name__ == '__main__':
    # --smoke accepts the stage gate's fast invocation; the slice matrix is the
    # single lifecycle, so the flag only selects this module's test.
    import sys
    sys.argv = [a for a in sys.argv if a != '--smoke']
    unittest.main()
