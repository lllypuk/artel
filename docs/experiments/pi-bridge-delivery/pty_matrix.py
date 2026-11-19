#!/usr/bin/env python3
"""Bounded real-Pi TUI delivery matrix in owned, isolated Linux PTYs."""
import http.server
import json
import os
from pathlib import Path
import pty
import select
import signal
import socket
import struct
import subprocess
import termios
import fcntl
import threading
import time

EXPECTED_NODE = Path('/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/bin/node')
EXPECTED_PACKAGE = Path('/home/sasha/.local/share/pi-node/node-v22.23.2-linux-x64/lib/node_modules/@earendil-works/pi-coding-agent')
HERE = Path(__file__).resolve().parent
PROJECT = HERE.parents[2]
ROOT = Path(os.environ.get('PROBE_ROOT', ''))
PACKAGE = Path(os.environ.get('PI_TEST_PACKAGE', ''))
ALLOWED_ENV = {
    'PATH', 'HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME',
    'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR', 'PI_OFFLINE', 'PROBE_ROOT',
    'PI_TEST_PACKAGE', 'LANG', 'LC_ALL', 'TMPDIR',
}


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def private_json(path, value):
    path.write_text(json.dumps(value, indent=2) + '\n', encoding='utf-8')
    path.chmod(0o600)


def event_rows(path):
    if not path.exists():
        return []
    rows = []
    for line in path.read_text(encoding='utf-8').splitlines():
        if line:
            rows.append(json.loads(line))
    return rows


class ProviderRequest:
    def __init__(self, sequence, token, headers, raw, body):
        self.sequence = sequence
        self.token = token
        self.headers = headers
        self.raw = raw
        self.body = body
        self.action = None
        self.ready = threading.Event()


class NumericProvider:
    def __init__(self, directory):
        self.requests = []
        self.errors = []
        self.trace_path = directory / 'provider-requests.jsonl'
        self.condition = threading.Condition()
        provider = self

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def do_POST(self):
                record = None
                try:
                    require(self.path == '/v1/chat/completions', 'unexpected provider path')
                    length = int(self.headers.get('Content-Length', '0'))
                    require(0 < length < 200000, 'provider body limit')
                    raw = self.rfile.read(length).decode('utf-8')
                    body = json.loads(raw)
                    token = self.headers.get('x-artel-probe-token')
                    require(isinstance(token, str) and token, 'provider request lacks token')
                    record = ProviderRequest(len(provider.requests) + 1, token,
                                             dict(self.headers), raw, body)
                    with provider.condition:
                        provider.requests.append(record)
                        with provider.trace_path.open('a', encoding='utf-8') as trace:
                            trace.write(json.dumps({
                                'sequence': record.sequence, 'token': token,
                                'headers': record.headers, 'rawBody': raw, 'body': body,
                            }) + '\n')
                        provider.condition.notify_all()
                    record.ready.wait()
                    if record.action == 'disconnect':
                        self.close_connection = True
                        try:
                            self.connection.shutdown(socket.SHUT_RDWR)
                        except OSError:
                            pass
                        return
                    require(record.action == 'respond', 'provider request was not released')
                    chunks = [
                        {'choices': [{'index': 0, 'delta': {'role': 'assistant', 'content': '42'},
                                      'finish_reason': None}]},
                        {'choices': [{'index': 0, 'delta': {}, 'finish_reason': 'stop'}]},
                    ]
                    self.send_response(200)
                    self.send_header('Content-Type', 'text/event-stream')
                    self.end_headers()
                    for chunk in chunks:
                        payload = dict(chunk, id=f'numeric-{record.sequence}',
                                       object='chat.completion.chunk', created=1, model='fixture')
                        self.wfile.write(('data: ' + json.dumps(payload) + '\n\n').encode())
                    self.wfile.write(b'data: [DONE]\n\n')
                    self.wfile.flush()
                except Exception as error:  # The parent turns this into a failed scenario.
                    provider.errors.append(str(error))
                    try:
                        self.send_response(500)
                        self.end_headers()
                    except OSError:
                        pass

        self.server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Handler)
        self.server.daemon_threads = True
        self.thread = threading.Thread(target=self.server.serve_forever, daemon=True)
        self.thread.start()
        self.port = self.server.server_port
        require(isinstance(self.port, int) and self.port > 0, 'numeric loopback port required')

    def wait(self, predicate=lambda _request: True, timeout=8):
        deadline = time.monotonic() + timeout
        with self.condition:
            while True:
                match = next((request for request in self.requests if predicate(request)), None)
                if match:
                    return match
                remaining = deadline - time.monotonic()
                require(remaining > 0, 'provider request barrier timeout')
                self.condition.wait(remaining)

    def release(self, record):
        require(record in self.requests and record.action is None, 'only a held request may be released')
        record.action = 'respond'
        record.ready.set()

    def drop(self, record):
        if record.action is None:
            record.action = 'disconnect'
            record.ready.set()

    def close(self):
        for request in self.requests:
            self.drop(request)
        self.server.shutdown()
        self.server.server_close()
        self.thread.join(timeout=3)
        require(not self.thread.is_alive(), 'provider thread cleanup failed')
        require(not self.errors, f'provider fixture errors: {self.errors}')


class Scenario:
    def __init__(self, name):
        self.name = name
        self.dir = ROOT / name
        self.dir.mkdir(mode=0o700)
        self.identity = {
            'messageId': f'message-{name}',
            'generation': 7,
            'sessionId': f'fictional-{name}',
        }
        self.marker = 'MARKER_' + name.upper().replace('-', '_')
        self.payload = {
            'content': f'Fictional numeric consultation {name}; literal marker {self.marker}',
            'details': {
                'message_id': self.identity['messageId'],
                'marker': self.marker,
                'author': 'developer',
                'recipient': 'product',
            },
        }
        self.provider = NumericProvider(self.dir)
        self.oracle_path = self.dir / 'oracle-events.jsonl'
        self.bridge_path = self.dir / 'bridge-events.jsonl'
        self.processes = []
        self.deadline = time.monotonic() + 30

    def start(self):
        process = PtyProcess(self)
        self.processes.append(process)
        process.start()
        return process

    def tokens(self):
        return [row for row in event_rows(self.oracle_path) if row.get('type') == 'token-event']

    def pi_sends(self):
        return [row for row in event_rows(self.bridge_path) if row.get('type') == 'pi-send']

    def server_record(self):
        data = json.loads((self.dir / 'server.json').read_text(encoding='utf-8'))
        return data['messages'].get(self.identity['messageId'])

    def journal_record(self):
        data = json.loads((self.dir / 'journal.json').read_text(encoding='utf-8'))
        return data['messages'].get(self.identity['messageId'])

    def correlate(self, user_prompts=()):
        tokens = self.tokens()
        by_token = {}
        for event in tokens:
            require(event['token'] not in by_token, 'before_agent_start token was reused')
            by_token[event['token']] = event
        users = [event for event in tokens if event['messageType'] == 'user']
        require([event.get('prompt') for event in users] == list(user_prompts),
                f'user initiators differ from unique manual turns: {self.name}')
        per_token = {}
        for request in self.provider.requests:
            event = by_token.get(request.token)
            require(event is not None, 'provider request has no before_agent_start token')
            require(request.token not in per_token, 'duplicate provider request for one token')
            per_token[request.token] = request
            require(request.body.get('model') == 'fixture', 'unexpected model in provider body')
            require(not request.body.get('tools'), 'provider body declares tools')
            if event['messageType'] == 'custom':
                require(self.marker in request.raw, 'scenario marker absent from actual provider body')
                require(event['details'] == self.payload['details'], 'custom details changed')
                require(event['sessionId'] == self.identity['sessionId'], 'custom session changed')
                require(self.payload['content'] in request.raw, 'custom content absent from actual body')
                record = self.server_record()
                require(record and record.get('spend'), 'custom start has no spent right')
                require(event['messageId'] == self.identity['messageId'], 'custom message ID changed')
                require(event['generation'] == self.identity['generation'], 'generation correlation changed')
                require(event['attemptId'] == record['spend']['attemptId'], 'attempt correlation changed')
                require(event['journalId'] == record['spend']['journalId'], 'journal correlation changed')
                require(event['ownerPid'] == record['spend']['ownerPid'], 'owner correlation changed')
            else:
                require(event.get('attemptId') is None, 'manual turn acquired bridge attempt correlation')
                require(event['prompt'] in request.raw, 'manual prompt absent from actual body')
                require(request.raw.count(event['prompt']) == 1, 'manual prompt lost or duplicated in body')
        require(len(per_token) == len(self.provider.requests), 'provider/token correlation incomplete')
        require(not self.provider.errors, 'provider rejected a request')

    def summary(self, **extra):
        tokens = self.tokens()
        record = self.server_record()
        status = ('absent' if not record else 'observed' if record.get('receipt')
                  else 'unknown' if record.get('spend') else 'admitted')
        result = {
            'name': self.name,
            'result': 'PASS',
            'piSends': len(self.pi_sends()),
            'providerRequests': len(self.provider.requests),
            'beforeAgentStart': len(tokens),
            'customStarts': sum(row['messageType'] == 'custom' for row in tokens),
            'userStarts': sum(row['messageType'] == 'user' for row in tokens),
            'serverStatus': status,
            **extra,
        }
        RESULTS.append(result)
        print(f"PASS {self.name}: pi={result['piSends']} provider={result['providerRequests']} "
              f"starts={result['beforeAgentStart']} server={status}", flush=True)

    def close(self):
        for process in reversed(self.processes):
            process.close()
        self.provider.close()


class PtyProcess:
    def __init__(self, scenario):
        self.scenario = scenario
        self.dir = scenario.dir
        self.master = None
        self.slave = None
        self.child = None
        self.control = None
        self.control_buffer = b''
        self.control_messages = []
        self.next_id = 0
        self.raw_path = None
        self.closed = False
        for name in ['home', 'config', 'cache', 'state', 'data', 'agent', 'sessions', 'work']:
            (self.dir / name).mkdir(exist_ok=True, mode=0o700)
        settings = {
            'compaction': {'enabled': False},
            'retry': {'enabled': False},
            'packages': [],
            'extensions': [],
            'defaultTools': [],
        }
        private_json(self.dir / 'agent/settings.json', settings)
        self.env = {
            'PATH': str(EXPECTED_NODE.parent) + ':/usr/bin:/bin',
            'HOME': str(self.dir / 'home'),
            'XDG_CONFIG_HOME': str(self.dir / 'config'),
            'XDG_CACHE_HOME': str(self.dir / 'cache'),
            'XDG_STATE_HOME': str(self.dir / 'state'),
            'XDG_DATA_HOME': str(self.dir / 'data'),
            'PI_CODING_AGENT_DIR': str(self.dir / 'agent'),
            'PI_CODING_AGENT_SESSION_DIR': str(self.dir / 'sessions'),
            'PI_OFFLINE': '1',
            'PI_SKIP_VERSION_CHECK': '1',
            'PI_IMAGE_PROTOCOL': 'none',
            'PI_PROGRAM_STATUS': '0',
            'PI_HYPERLINKS': '0',
            'TERM': 'xterm-256color',
            'LANG': 'C.UTF-8',
            'PROBE_ROOT': str(ROOT),
            'PI_TEST_PACKAGE': str(PACKAGE),
            'ARTEL_PTY_PROVIDER_URL': f'http://127.0.0.1:{scenario.provider.port}/v1',
            'ARTEL_PTY_ORACLE_EVENTS': str(scenario.oracle_path),
            'ARTEL_PTY_BRIDGE_EVENTS': str(scenario.bridge_path),
            'ARTEL_PTY_CONTROL': str(self.dir / 'control.sock'),
            'ARTEL_PTY_PROTOCOL': str(HERE / 'protocol.mjs'),
            'ARTEL_PTY_SERVER': str(self.dir / 'server.json'),
            'ARTEL_PTY_JOURNAL': str(self.dir / 'journal.json'),
            'ARTEL_PTY_SESSION_ID': scenario.identity['sessionId'],
            'ARTEL_PTY_GENERATION': str(scenario.identity['generation']),
        }
        self.args = cli_args(self.dir / 'sessions', scenario.identity['sessionId'])

    def start(self):
        startup_provider_count = len(self.scenario.provider.requests)
        self.master, self.slave = pty.openpty()
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 140, 0, 0))
        number = len(self.scenario.processes) + 1
        self.raw_path = self.dir / f'pty-{number}.raw'
        raw = self.raw_path.open('ab', buffering=0)
        raw.close()
        self.child = subprocess.Popen(
            [str(EXPECTED_NODE), str(PACKAGE / 'dist/cli.js'), *self.args[1:]],
            cwd=self.dir / 'work', env=self.env,
            stdin=self.slave, stdout=self.slave, stderr=self.slave,
            start_new_session=True, close_fds=True,
        )
        CLEANUP['started'] += 1
        os.close(self.slave)
        self.slave = None
        self.wait_file_event(self.scenario.bridge_path,
                             lambda row: row.get('type') == 'control-ready'
                             and row.get('processPid') == self.child.pid, 'control socket startup')
        self.control = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.control.setblocking(False)
        self.control.connect(str(self.dir / 'control.sock'))
        initial = self.command('state')
        require(initial['idle'] and not initial['pending'], 'TUI did not start idle')
        require(initial['editorText'] == '', 'TUI started with an unexpected draft')
        start = self.wait_file_event(self.scenario.oracle_path,
                                     lambda row: row.get('type') == 'session-start'
                                     and row.get('sessionId') == self.scenario.identity['sessionId']
                                     and row.get('processPid') == self.child.pid,
                                     'real TUI session_start')
        require(start['mode'] == 'tui' and start['activeTools'] == [], 'not isolated real TUI')
        require(len(self.scenario.provider.requests) == startup_provider_count,
                'startup caused a provider request')

    def pump(self):
        readers = []
        if self.master is not None:
            readers.append(self.master)
        if self.control is not None:
            readers.append(self.control)
        if not readers:
            return
        ready, _, _ = select.select(readers, [], [], 0.05)
        for source in ready:
            try:
                data = os.read(source, 65536) if isinstance(source, int) else source.recv(65536)
            except (OSError, BlockingIOError):
                continue
            if not data:
                continue
            if isinstance(source, int):
                with self.raw_path.open('ab', buffering=0) as raw:
                    raw.write(data)
                require(self.raw_path.stat().st_size < 2 * 1024 * 1024, 'PTY trace limit exceeded')
                if b'\x1b[6n' in data:
                    os.write(self.master, b'\x1b[1;1R')
            else:
                with (self.dir / 'control.jsonl').open('ab', buffering=0) as trace:
                    trace.write(data)
                self.control_buffer += data
                while b'\n' in self.control_buffer:
                    line, self.control_buffer = self.control_buffer.split(b'\n', 1)
                    if line:
                        self.control_messages.append(json.loads(line))

    def wait_control(self, predicate, label):
        while True:
            match = next((row for row in self.control_messages if predicate(row)), None)
            if match:
                return match
            require(time.monotonic() < self.scenario.deadline,
                    f'{self.scenario.name}: {label} watchdog expired')
            require(self.child.poll() is None,
                    f'{self.scenario.name}: Pi exited before {label}: {self.child.returncode}')
            self.pump()

    def wait_file_event(self, path, predicate, label):
        while True:
            match = next((row for row in event_rows(path) if predicate(row)), None)
            if match:
                return match
            require(time.monotonic() < self.scenario.deadline,
                    f'{self.scenario.name}: {label} watchdog expired')
            require(self.child is None or self.child.poll() is None,
                    f'{self.scenario.name}: Pi exited before {label}')
            self.pump()

    def send(self, command):
        self.next_id += 1
        command = dict(command, id=self.next_id)
        require(self.control is not None, 'control socket is unavailable')
        self.control.send((json.dumps(command) + '\n').encode())
        return self.next_id

    def response(self, command_id):
        row = self.wait_control(lambda item: item.get('type') == 'response'
                                and item.get('id') == command_id, f'response {command_id}')
        require(row.get('ok'), f'fixture command failed: {row.get("error")}')
        return row['result']

    def command(self, type_, **fields):
        return self.response(self.send(dict(fields, type=type_)))

    def start_deliver(self, pauses=()):
        return self.send({
            'type': 'deliver', 'identity': self.scenario.identity,
            'payload': self.scenario.payload, 'pauses': list(pauses),
        })

    def wait_barrier(self, name):
        return self.wait_control(lambda row: row.get('type') == 'barrier'
                                 and row.get('name') == name, name)

    def release(self, name):
        result = self.command('release', name=name)
        require(result['released'] == name, 'wrong barrier released')

    def wait_token(self, message_type, prompt=None):
        return self.wait_file_event(
            self.scenario.oracle_path,
            lambda row: row.get('type') == 'token-event' and row.get('messageType') == message_type
            and (prompt is None or row.get('prompt') == prompt),
            f'{message_type} before_agent_start',
        )

    def wait_settled(self, token):
        row = self.wait_file_event(self.scenario.oracle_path,
                                   lambda item: item.get('type') == 'agent-settled'
                                   and item.get('token') == token, 'agent_settled')
        require(row['idle'] and not row['pending'], 'settled event was not idle with empty Pi queue')
        return row

    def wait_receipt(self, status='observed'):
        row = self.wait_file_event(self.scenario.bridge_path,
                                   lambda item: item.get('type') == 'receipt-result',
                                   'custom receipt result')
        require(row['result']['status'] == status, 'unexpected receipt status')
        return row

    def wait_state(self, predicate, label):
        deadline = min(self.scenario.deadline, time.monotonic() + 8)
        while True:
            current = self.command('state')
            if predicate(current):
                return current
            require(time.monotonic() < deadline, f'{self.scenario.name}: state barrier {label} timeout')
            self.pump()

    def type(self, text, enter=False):
        require(self.master is not None, 'PTY is closed')
        os.write(self.master, text.encode() + (b'\r' if enter else b''))

    def stop(self):
        if self.closed:
            return
        if self.child.poll() is None:
            self.command('quit')
            try:
                self.child.wait(timeout=4)
            except subprocess.TimeoutExpired:
                os.killpg(self.child.pid, signal.SIGKILL)
                self.child.wait(timeout=3)
                raise AssertionError('graceful TUI shutdown required SIGKILL')
            require(self.child.returncode == 0, f'Pi clean exit failed: {self.child.returncode}')
        self.finish_cleanup()

    def kill(self):
        if self.closed:
            return
        require(self.child.poll() is None, 'owned Pi process already exited')
        os.killpg(self.child.pid, signal.SIGKILL)
        self.child.wait(timeout=4)
        require(self.child.returncode == -signal.SIGKILL, 'owned Pi SIGKILL was not reaped')
        self.finish_cleanup()

    def finish_cleanup(self):
        if self.closed:
            return
        try:
            os.killpg(self.child.pid, 0)
        except ProcessLookupError:
            pass
        else:
            raise AssertionError('owned process group survived Pi cleanup')
        if self.control is not None:
            self.control.close()
            self.control = None
        if self.master is not None:
            os.close(self.master)
            self.master = None
        if self.slave is not None:
            os.close(self.slave)
            self.slave = None
        (self.dir / 'control.sock').unlink(missing_ok=True)
        self.closed = True
        CLEANUP['reaped'] += 1

    def close(self):
        if self.closed:
            return
        if self.child and self.child.poll() is None:
            os.killpg(self.child.pid, signal.SIGKILL)
            self.child.wait(timeout=4)
        self.finish_cleanup()


def cli_args(session_dir, session_id):
    return [
        'fictional-pty-preflight',
        '--offline', '--no-approve',
        '--no-extensions', '--no-skills', '--no-prompt-templates',
        '--no-context-files', '--no-themes', '--no-mcp', '--no-builtin-tools',
        '-e', str(HERE / 'pty-oracle.mjs'),
        '-e', str(HERE / 'pty-bridge.mjs'),
        '--model', 'artel-fixture/fixture', '--thinking', 'off',
        '--session-id', session_id, '--session-dir', str(session_dir),
        '--system-prompt', 'Isolated numeric transport fixture. No tools. Reply only 42.',
    ]


def preflight():
    require(EXPECTED_NODE.is_file(), 'absolute pinned Node binary is required')
    require(PACKAGE.is_dir() and PACKAGE.resolve() == EXPECTED_PACKAGE,
            'PI_TEST_PACKAGE must be the expected pinned package')
    require(ROOT.is_absolute() and str(ROOT).startswith('/tmp/artel-pi-delivery-'),
            'PROBE_ROOT must be private /tmp/artel-pi-delivery-*')
    require(ROOT.stat().st_uid == os.getuid() and (ROOT.stat().st_mode & 0o777) == 0o700,
            'PROBE_ROOT must be owned mode 0700')
    require(ROOT.resolve() == ROOT and not str(ROOT).startswith(str(PROJECT)),
            'PROBE_ROOT must be real and outside the worktree')
    unknown = set(os.environ) - ALLOWED_ENV
    require(not unknown, f'unapproved inherited environment: {sorted(unknown)}')
    require(os.environ.get('PI_OFFLINE') == '1', 'PI_OFFLINE=1 required')
    for key in ['HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME',
                'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR']:
        value = Path(os.environ.get(key, ''))
        require(value.is_absolute() and value.exists(), f'{key} must be an existing private path')
        require(value.resolve().is_relative_to(ROOT), f'{key} must be inside PROBE_ROOT')
    manifest = json.loads((PACKAGE / 'package.json').read_text(encoding='utf-8'))
    require(manifest['version'] == '1.5.2', 'Pi 1.5.2 required')
    result = subprocess.run(
        [str(EXPECTED_NODE), str(HERE / 'pty-preflight.mjs'), str(PACKAGE),
         *cli_args(ROOT / 'sessions', 'fictional-pty-preflight')[1:]],
        cwd=ROOT, env=dict(os.environ), capture_output=True, timeout=8, check=False,
    )
    require(result.returncode == 0, f'pinned CLI preflight failed: {result.stderr.decode()}')


def run(name, body):
    scenario = Scenario(name)
    try:
        body(scenario)
    finally:
        scenario.close()


def positive_control():
    def body(s):
        p = s.start()
        delivery = p.start_deliver()
        token = p.wait_token('custom')
        request = s.provider.wait()
        require(request.token == token['token'], 'positive token did not reach provider')
        result = p.response(delivery)
        require(result['transmitted'] and result['status'] == 'unknown', 'custom was not transmitted')
        p.wait_receipt()
        s.provider.release(request)
        p.wait_settled(token['token'])
        replay = p.command('deliver', identity=s.identity, payload=s.payload)
        require(replay['status'] == 'observed', 'redelivery did not publish observed state')
        require(len(s.provider.requests) == 1 and len(s.pi_sends()) == 1,
                'idempotent redelivery caused a second call')
        s.correlate()
        p.stop()
        s.summary(redeliveries=1, restarts=0)
    run('positive-custom-redelivery', body)


def manual_first():
    def body(s):
        p = s.start()
        manual = 'MANUAL_FIRST_UNIQUE_FICTIONAL_TEXT'
        p.type(manual, enter=True)
        token = p.wait_token('user', manual)
        request = s.provider.wait()
        result = p.command('deliver', identity=s.identity, payload=s.payload)
        require(result == {'status': 'pending-local', 'transmitted': False, 'piSends': 0},
                'manual-first delivery did not remain local')
        state = p.command('state')
        require(state['serverStatus'] == 'absent' and state['localIntent'], 'busy delivery admitted/spent')
        require(not state['localAttemptId'] and not state['pending'], 'busy delivery entered Pi queue')
        require(len(s.tokens()) == 1 and len(s.pi_sends()) == 0, 'busy delivery reached Pi')
        s.provider.release(request)
        p.wait_settled(token['token'])
        state = p.command('state')
        require(state['idle'] and not state['pending'] and state['serverStatus'] == 'absent',
                'settled handler sent the pending Artel message')
        require(len(s.provider.requests) == 1 and not s.server_record(), 'manual-first extra call/admission')
        s.correlate([manual])
        p.stop()
        s.summary(admissions=0, spends=0, localPending=1, restarts=0)
    run('manual-first-busy-local-pending', body)


def busy_after_spend():
    def body(s):
        p = s.start()
        delivery = p.start_deliver(['bridge:afterAttemptCommit'])
        p.wait_barrier('bridge:afterAttemptCommit')
        state = p.command('state')
        require(state['serverStatus'] == 'unknown' and state['localAttemptId'],
                'race did not pause after spend and attempt commit')
        manual = 'BUSY_AFTER_SPEND_UNIQUE_FICTIONAL_TEXT'
        p.type(manual, enter=True)
        token = p.wait_token('user', manual)
        request = s.provider.wait()
        p.release('bridge:afterAttemptCommit')
        result = p.response(delivery)
        require(result['status'] == 'unknown' and not result['transmitted'],
                'final idle check did not reject the late busy state')
        require(len(s.pi_sends()) == 0 and len(s.tokens()) == 1, 'late busy race called Pi')
        s.provider.release(request)
        p.wait_settled(token['token'])
        replay = p.command('deliver', identity=s.identity, payload=s.payload)
        require(replay['status'] == 'unknown' and not replay['transmitted'], 'spent right was recovered')
        require(len(s.provider.requests) == 1 and len(s.pi_sends()) == 0, 'busy-after-spend extra call')
        s.correlate([manual])
        p.stop()
        s.summary(spentRight=1, finalIdleRejected=1, restarts=0)
    run('busy-after-spend-final-idle-check', body)


def custom_first():
    def body(s):
        p = s.start()
        delivery = p.start_deliver()
        custom = p.wait_token('custom')
        custom_request = s.provider.wait()
        require(p.response(delivery)['transmitted'], 'custom-first turn not started')
        p.wait_receipt()
        manual = 'CUSTOM_FIRST_DRAFT_UNIQUE_9F3C7A'
        p.type(manual, enter=False)
        state = p.wait_state(lambda value: value['editorText'] == manual, 'typed draft visible')
        require(not state['idle'] and not state['pending'], 'draft typing entered Pi queue during streaming')
        require(len(s.provider.requests) == 1 and len(s.pi_sends()) == 1, 'draft caused a provider/Pi call')
        s.provider.release(custom_request)
        p.wait_settled(custom['token'])
        state = p.command('state')
        require(state['idle'] and state['editorText'] == manual, 'TUI draft did not survive custom turn')
        p.type('', enter=True)
        user = p.wait_token('user', manual)
        require(user['token'] != custom['token'], 'custom and manual initiators shared a token')
        manual_request = s.provider.wait(lambda request: request.token == user['token'])
        s.provider.release(manual_request)
        p.wait_settled(user['token'])

        old_marker = 'MANUAL_OLD_MARKER ' + s.marker
        p.type(old_marker, enter=True)
        old = p.wait_token('user', old_marker)
        require(old['token'] not in {custom['token'], user['token']}, 'old-marker token was not fresh')
        old_request = s.provider.wait(lambda request: request.token == old['token'])
        s.provider.release(old_request)
        p.wait_settled(old['token'])
        replay = p.command('deliver', identity=s.identity, payload=s.payload)
        require(replay['status'] == 'observed', 'old-marker turn changed bridge receipt state')
        require(len(s.pi_sends()) == 1 and len(s.provider.requests) == 3,
                'custom/manual/old-marker calls were lost, substituted or duplicated')
        s.correlate([manual, old_marker])
        require(len(json.loads((s.dir / 'server.json').read_text())['messages']) == 1,
                'old marker created a new bridge message/receipt')
        p.stop()
        s.summary(draftPreserved=1, oldMarkerUserTurns=1, restarts=0)
    run('custom-first-draft-and-old-marker', body)


def restart_after_spend():
    def body(s):
        p = s.start()
        p.start_deliver(['bridge:afterAttemptCommit'])
        p.wait_barrier('bridge:afterAttemptCommit')
        require(s.server_record()['spend'] and s.journal_record()['attempt'], 'spend/attempt absent')
        require(not s.pi_sends() and not s.provider.requests, 'call occurred before crash barrier')
        p.kill()
        recovered = s.start()
        result = recovered.command('deliver', identity=s.identity, payload=s.payload)
        require(result['status'] == 'unknown' and not result['transmitted'], 'restart recovered spent right')
        require(not s.pi_sends() and not s.provider.requests, 'restart after spend caused a call')
        recovered.stop()
        s.summary(restarts=1, crashBarrier='after-attempt-before-pi')
    run('restart-after-spend-before-pi', body)


def restart_after_transmission():
    def body(s):
        p = s.start()
        p.start_deliver(['receipt:beforeWrite'])
        custom = p.wait_token('custom')
        request = s.provider.wait()
        require(request.token == custom['token'], 'transmission token mismatch')
        p.wait_barrier('receipt:beforeWrite')
        require(not s.server_record()['receipt'] and not s.journal_record()['receipt'],
                'receipt existed before crash')
        p.kill()
        s.provider.drop(request)
        recovered = s.start()
        result = recovered.command('deliver', identity=s.identity, payload=s.payload)
        require(result['status'] == 'unknown' and not result['transmitted'], 'unknown became a receipt on restart')
        require(len(s.pi_sends()) == 1 and len(s.provider.requests) == 1, 'restart repeated transmission')
        s.correlate()
        recovered.stop()
        s.summary(restarts=1, crashBarrier='after-transmission-before-receipt')
    run('restart-after-transmission-before-receipt', body)


def restart_after_receipt():
    def body(s):
        p = s.start()
        p.start_deliver(['bridge:afterReceiptCommit'])
        custom = p.wait_token('custom')
        request = s.provider.wait()
        require(request.token == custom['token'], 'receipt token mismatch')
        p.wait_barrier('bridge:afterReceiptCommit')
        local = s.journal_record()
        server = s.server_record()
        require(local['receipt'] and not server['receipt'], 'not paused after local receipt before POST')
        attempt_id = server['spend']['attemptId']
        p.kill()
        s.provider.drop(request)
        recovered = s.start()
        result = recovered.command('deliver', identity=s.identity, payload=s.payload)
        require(result['status'] == 'observed' and result['attemptId'] == attempt_id,
                'persisted receipt was not idempotently published')
        require(len(s.pi_sends()) == 1 and len(s.provider.requests) == 1, 'receipt recovery repeated Pi call')
        s.correlate()
        recovered.stop()
        s.summary(restarts=1, crashBarrier='after-local-receipt-before-post')
    run('restart-after-local-receipt-before-post', body)


def cleanup_control():
    def body(s):
        killed = s.start()
        require(not s.provider.requests, 'cleanup control generated provider work')
        killed.kill()
        stopped = s.start()
        stopped.stop()
        s.summary(restarts=1, sigkillReaps=1, gracefulReaps=1, orphanProcesses=0)
    run('owned-process-pty-cleanup', body)


RESULTS = []
CLEANUP = {'started': 0, 'reaped': 0}


def main():
    os.umask(0o077)
    preflight()
    for scenario in [positive_control, manual_first, busy_after_spend, custom_first,
                     restart_after_spend, restart_after_transmission, restart_after_receipt,
                     cleanup_control]:
        scenario()
    require(CLEANUP['started'] == 12 and CLEANUP['reaped'] == CLEANUP['started'],
            f'owned process cleanup count mismatch: {CLEANUP}')
    output = {
        'piVersion': '1.5.2',
        'nodeVersion': '22.23.2',
        'scenarios': RESULTS,
        'processCleanup': {
            'result': 'PASS', 'ownedProcessesReaped': CLEANUP['reaped'],
            'orphanProcesses': 0, 'ptyDescriptorsClosed': CLEANUP['reaped'],
        },
        'limitations': [
            'Process crash only; no power-loss or external-provider claim',
            'No steer/follow-up queue probe; no task completion or exactly-once claim',
            'No real Artel API, agterm, host settings, credentials or tools used',
        ],
    }
    private_json(ROOT / 'result.json', output)
    print(f'RESULT {len(RESULTS)} real-TUI scenarios passed; private traces: {ROOT}', flush=True)


if __name__ == '__main__':
    main()
