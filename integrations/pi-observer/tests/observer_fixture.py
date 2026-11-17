"""Private Unix HTTP oracle for the SHIPPING extension, not injected hooks."""
import contextlib
import hashlib
import http.server
import json
import secrets
import socket
import threading
import time
import uuid


class UnixHTTP(http.server.HTTPServer):
    address_family = socket.AF_UNIX

    def server_bind(self):
        self.socket.bind(self.server_address)
        self.server_name = 'artel-observer'
        self.server_port = 0


@contextlib.contextmanager
def observer(root, env):
    private = root / 'observer'
    private.mkdir(mode=0o700)
    token = secrets.token_hex(32)
    observer_id, database = str(uuid.uuid4()), str(uuid.uuid4())
    config = {'status': 'confirmed', 'observer_id': observer_id,
              'database_instance_id': database, 'observer_socket': str(private / 'socket'),
              'token_file': str(private / 'token'), 'provisioning_key': str(uuid.uuid4()),
              'payload': {'label': 'Fictional', 'verifier': hashlib.sha256(token.encode()).hexdigest()}}
    for name, data in [('config', json.dumps(config)), ('token', token + '\n')]:
        path = private / name
        path.write_text(data)
        path.chmod(0o600)
    env['ARTEL_OBSERVER_CONFIG'] = str(private / 'config')
    state = {'registrations': [], 'reports': [], 'report_times': [], 'token': token, 'bodies': [], 'errors': []}

    class Handler(http.server.BaseHTTPRequestHandler):
        protocol_version = 'HTTP/1.0'

        def log_message(self, *_args):
            pass

        def handle_request(self):
            if self.headers.get('Host') != 'artel-observer' or self.headers.get('Authorization') != 'Bearer ' + token:
                state['errors'].append('authority/auth mismatch')
            body = self.rfile.read(int(self.headers.get('Content-Length', 0)))
            state['bodies'].append(body.decode())
            value = json.loads(body) if body else {}
            if self.command == 'GET':
                runtime = self.path.split('runtime_id=')[1]
            elif self.command == 'POST':
                state['registrations'].append(value)
                runtime = value['runtime_id']
            else:
                state['reports'].append(value)
                state['report_times'].append((value['runtime_id'], time.monotonic()))
                result = {k: value[k] for k in ['runtime_id', 'generation', 'report_seq']}
                result['result'] = 'accepted'
                self.reply(result)
                return
            self.reply({'database_instance_id': database, 'observer_id': observer_id,
                        'runtime_id': runtime, 'generation': 0, 'disposition': 'candidate'})

        def reply(self, result):
            self.send_response(200)
            self.end_headers()
            self.wfile.write(json.dumps(result).encode())

        do_GET = handle_request
        do_POST = handle_request
        do_PUT = handle_request

    server, thread = None, None

    def start():
        nonlocal server, thread
        server = UnixHTTP(str(private / 'socket'), Handler)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()

    def stop():
        nonlocal server, thread
        if server is not None:
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)
            server, thread = None, None
        (private / 'socket').unlink(missing_ok=True)

    state['start'], state['stop'] = start, stop
    start()
    try:
        yield state
        assert not state['errors'], state['errors']
        assert all(token not in body for body in state['bodies'])
        for r in state['registrations']:
            assert set(r) <= {'runtime_id', 'pi_session_id', 'reason', 'pi_version', 'bridge_version', 'cwd', 'provider', 'model', 'terminal_hint'}
        for runtime in {r[0] for r in state['report_times']}:
            times = [t for rid, t in state['report_times'] if rid == runtime]
            assert all(times[i + 4] - times[i] >= 1 for i in range(len(times) - 4)), 'report rate exceeded'
        for r in state['reports']:
            assert set(r) <= {'runtime_id', 'generation', 'report_seq', 'activity', 'tool_count', 'lifecycle', 'provider', 'model'}
    finally:
        stop()
