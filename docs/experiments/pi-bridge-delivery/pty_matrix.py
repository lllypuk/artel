#!/usr/bin/env python3
"""Bounded real-Pi TUI delivery matrix in owned, isolated Linux PTYs."""
import ctypes
import http.client
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
import sys
import termios
import fcntl
import threading
import time
from tempfile import mkdtemp
from types import SimpleNamespace
from uuid import uuid4
from unittest.mock import patch

NODE = Path(os.environ.get('PI_TEST_NODE', ''))
HERE = Path(__file__).resolve().parent
PROJECT = HERE.parents[2]
ROOT = Path(os.environ.get('PROBE_ROOT', ''))
PACKAGE = Path(os.environ.get('PI_TEST_PACKAGE', ''))
ALLOWED_ENV = {
    'PATH', 'HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME',
    'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR', 'PI_OFFLINE', 'PROBE_ROOT',
    'PI_TEST_PACKAGE', 'PI_TEST_NODE', 'LANG', 'LC_ALL', 'TMPDIR',
}


def require(condition, message):
    if not condition:
        raise AssertionError(message)


class Interrupted(RuntimeError):
    pass


INTERRUPTED = False


def on_sigterm(_signum, _frame):
    global INTERRUPTED
    # Do not interrupt openpty/Popen or cleanup between ownership bookkeeping steps.
    INTERRUPTED = True


def check_interrupted():
    if INTERRUPTED:
        raise Interrupted('runner received SIGTERM')


def remaining(deadline, label):
    seconds = deadline - time.monotonic()
    require(seconds > 0, f'{label} watchdog expired')
    return seconds


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
    def __init__(self, directory, deadline):
        self.deadline = deadline
        self.requests = []
        self.errors = []
        self.trace_path = directory / 'provider-requests.jsonl'
        self.condition = threading.Condition()
        self.active = set()
        self.closing = threading.Event()
        self.draining = False
        provider = self

        class Server(http.server.ThreadingHTTPServer):
            # Explicitly track and drain handlers on one bounded cleanup budget.
            daemon_threads = True
            block_on_close = False
            timeout = 0.05

            def process_request(self, request, address):
                with provider.condition:
                    provider.active.add(request)
                    provider.condition.notify_all()
                try:
                    super().process_request(request, address)
                except BaseException:
                    self.remove_request(request)
                    raise

            def process_request_thread(self, request, address):
                try:
                    super().process_request_thread(request, address)
                finally:
                    self.remove_request(request)

            def remove_request(self, request):
                with provider.condition:
                    provider.active.remove(request)
                    provider.condition.notify_all()

            def handle_error(self, _request, _address):
                with provider.condition:
                    provider.errors.append(str(sys.exc_info()[1]))
                    provider.condition.notify_all()

        class Handler(http.server.BaseHTTPRequestHandler):
            def log_message(self, *_args):
                pass

            def send_error(self, code, message=None, explain=None):
                with provider.condition:
                    provider.errors.append(f'invalid provider request: HTTP {code}')
                    provider.condition.notify_all()
                super().send_error(code, message, explain)

            def do_POST(self):
                record = None
                try:
                    require(self.path == '/v1/chat/completions', 'unexpected provider path')
                    length = int(self.headers.get('Content-Length', '0'))
                    require(0 < length < 200000, 'provider body limit')
                    raw_bytes = self.rfile.read(length)
                    require(len(raw_bytes) == length, 'provider request body was truncated')
                    raw = raw_bytes.decode('utf-8')
                    body = json.loads(raw)
                    token = self.headers.get('x-artel-probe-token')
                    require(isinstance(token, str) and token, 'provider request lacks token')
                    with provider.condition:
                        record = ProviderRequest(len(provider.requests) + 1, token,
                                                 {key.lower(): value for key, value in self.headers.items()}, raw, body)
                        provider.requests.append(record)
                        if provider.draining:
                            provider.drop(record)
                        provider.condition.notify_all()
                    with provider.trace_path.open('a', encoding='utf-8') as trace:
                        trace.write(json.dumps({
                            'sequence': record.sequence, 'token': token,
                            'headers': record.headers, 'rawBody': raw, 'body': body,
                        }) + '\n')
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
                    with provider.condition:
                        provider.errors.append(str(error))
                        provider.condition.notify_all()
                    try:
                        self.send_response(500)
                        self.end_headers()
                    except OSError:
                        pass

        self.server = Server(('127.0.0.1', 0), Handler)
        self.thread = threading.Thread(target=self.serve, daemon=True)
        try:
            self.thread.start()
        except BaseException:
            self.server.server_close()
            raise
        self.port = self.server.server_port
        require(isinstance(self.port, int) and self.port > 0, 'numeric loopback port required')

    def serve(self):
        try:
            while not self.closing.is_set():
                self.server.handle_request()
            # Workers are reaped before closing. Take any connection already in the
            # listen backlog, even if its handler will finish during the drain.
            while select.select([self.server], [], [], 0)[0]:
                self.server.handle_request()
        except Exception as error:
            with self.condition:
                self.errors.append(str(error))
                self.condition.notify_all()

    def wait(self, predicate=lambda _request: True):
        with self.condition:
            while True:
                check_interrupted()
                remaining(self.deadline, 'provider request barrier')
                require(not self.errors, f'provider fixture errors: {self.errors}')
                match = next((request for request in self.requests if predicate(request)), None)
                if match:
                    remaining(self.deadline, 'provider request barrier')
                    return match
                self.condition.wait(min(0.1, remaining(self.deadline, 'provider request barrier')))

    def release(self, record):
        with self.condition:
            require(record in self.requests and record.action is None, 'only a held request may be released')
            record.action = 'respond'
            record.ready.set()

    def drop(self, record):
        with self.condition:
            if record.action is None:
                record.action = 'disconnect'
                record.ready.set()

    def close(self, deadline):
        with self.condition:
            self.draining = True
            self.closing.set()
            for request in self.requests:
                self.drop(request)
        try:
            self.thread.join(timeout=max(0, deadline - time.monotonic()))
            require(not self.thread.is_alive(), 'provider accept thread cleanup timed out')
            with self.condition:
                while self.active:
                    self.condition.wait(remaining(deadline, 'provider handler drain'))
                require(not self.errors, f'provider fixture errors: {self.errors}')
        finally:
            self.server.server_close()
            with self.condition:
                for sock in self.active:
                    try:
                        sock.shutdown(socket.SHUT_RDWR)
                    except OSError:
                        pass


class Scenario:
    def __init__(self, name, *, supervised_group=False):
        self.name = name
        self.supervised_group = supervised_group
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
        self.oracle_path = self.dir / 'oracle-events.jsonl'
        self.bridge_path = self.dir / 'bridge-events.jsonl'
        self.processes = []
        self.deadline = time.monotonic() + 30
        self.journal_id = None
        self.summary_options = None
        self.provider = NumericProvider(self.dir, self.deadline)

    def remaining(self, label):
        check_interrupted()
        return remaining(self.deadline, f'{self.name}: {label}')

    def start(self, *, expect_setup_error=None, on_spawn=None):
        self.remaining('start Pi')
        process = PtyProcess(self)
        self.processes.append(process)
        process.start(expect_setup_error=expect_setup_error, on_spawn=on_spawn)
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
        oracle_rows = event_rows(self.oracle_path)
        require(not [row for row in oracle_rows if row.get('type') == 'oracle-error'], 'oracle rejected provider hook')
        require(not [row for row in event_rows(self.bridge_path)
                     if row.get('type') in {'bridge-error', 'control-error', 'receipt-error'}], 'bridge fixture error')
        tokens = self.tokens()
        require([row['messageType'] for row in tokens].count('custom') == len(self.pi_sends()),
                'unexpected count of custom starts')
        require(len(tokens) == len(self.pi_sends()) + len(user_prompts), 'unexpected initiating turn types')
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
            require(event['sessionId'] == self.identity['sessionId'], 'turn session changed')
            require(request.headers.get('authorization') == 'Bearer fake-not-a-secret', 'unexpected fixture credential')
            messages = request.body['messages']
            expected = self.payload['content'] if event['messageType'] == 'custom' else event['prompt']

            def text(message):
                content = message.get('content')
                return content if isinstance(content, str) else ''.join(
                    part['text'] for part in (content or []) if part.get('type') == 'text')

            require(messages[-1]['role'] == 'user' and text(messages[-1]) == expected,
                    'last provider message must be the exact initiator, not history')
            require(sum(message['role'] == 'user' and text(message) == expected for message in messages) == 1,
                    'provider initiating text was lost or duplicated')
            if event['messageType'] == 'custom':
                require(event['details'] == self.payload['details'], 'custom details changed')
                record = self.server_record()
                require(record and record.get('spend'), 'custom start has no spent right')
                require(event['messageId'] == self.identity['messageId'], 'custom message ID changed')
                require(event['generation'] == self.identity['generation'], 'generation correlation changed')
                require(event['attemptId'] == record['spend']['attemptId'], 'attempt correlation changed')
                require(event['journalId'] == record['spend']['journalId'], 'journal correlation changed')
                require(event['ownerPid'] == record['spend']['ownerPid'], 'owner PID correlation changed')
                require(event['ownerInstance'] == record['spend']['ownerInstance'], 'owner instance correlation changed')
            else:
                require(event.get('attemptId') is None, 'manual turn acquired bridge attempt correlation')
        require(per_token.keys() == by_token.keys(), 'missing request for a recorded start')
        require(not self.provider.errors, 'provider rejected a request')

    def summary(self, *, user_prompts=(), intent_only=False, **extra):
        self.summary_options = (user_prompts, intent_only, extra)

    def finalize(self, expected_pi_sends):
        self.remaining('final result')
        require(self.summary_options is not None, 'scenario did not supply summary options')
        user_prompts, intent_only, extra = self.summary_options
        self.correlate(user_prompts)
        tokens = self.tokens()
        pi_sends = len(self.pi_sends())
        requests = len(self.provider.requests)
        expected_turns = expected_pi_sends + len(user_prompts)
        require((pi_sends, requests, len(tokens)) == (expected_pi_sends, expected_turns, expected_turns),
                f'{self.name}: unexpected final Pi/HTTP/start counts')
        record = self.server_record()
        if intent_only:
            require(record is None, 'manual-first final server record must remain absent (no admission or spend)')
            require(self.journal_record() == {'identity': self.identity, 'payload': self.payload,
                                              'attempt': None, 'receipt': None},
                    'manual-first final journal record must remain intent-only')
            extra = dict(extra, admissions=0, spends=0, localPending=1)
        status = ('absent' if not record else 'observed' if record.get('receipt')
                  else 'unknown' if record.get('spend') else 'admitted')
        return {
            'name': self.name,
            'result': 'PASS',
            'piSends': pi_sends,
            'providerRequests': requests,
            'beforeAgentStart': len(tokens),
            'customStarts': sum(row['messageType'] == 'custom' for row in tokens),
            'userStarts': sum(row['messageType'] == 'user' for row in tokens),
            'serverStatus': status,
            **extra,
        }

    def close(self):
        deadline = time.monotonic() + 5
        failures = []
        for cleanup in [*(p.close for p in reversed(self.processes)), self.provider.close]:
            try:
                cleanup(deadline)
            except Exception as error:
                failures.append(error)
        if failures:
            raise ExceptionGroup('scenario cleanup failed', failures)


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
            'PATH': str(NODE.parent) + ':/usr/bin:/bin',
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
            'ARTEL_PTY_STATE_MODE': 'bootstrap' if scenario.journal_id is None else 'recover',
            'ARTEL_PTY_JOURNAL_ID': scenario.journal_id or '',
        }
        self.args = cli_args(self.dir / 'sessions', scenario.identity['sessionId'])

    def start(self, *, expect_setup_error, on_spawn):
        startup_provider_count = len(self.scenario.provider.requests)
        self.master, self.slave = pty.openpty()
        os.set_blocking(self.master, False)
        fcntl.ioctl(self.slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 140, 0, 0))
        number = len(self.scenario.processes)
        self.raw_path = self.dir / f'pty-{number}.raw'
        raw = self.raw_path.open('ab', buffering=0)
        raw.close()
        self.scenario.remaining('spawn Pi')
        self.child = subprocess.Popen(
            [str(NODE), CLI, *self.args],
            cwd=self.dir / 'work', env=self.env,
            stdin=self.slave, stdout=self.slave, stderr=self.slave,
            # Probes inherit their supervisor-owned group. Ordinary scenarios
            # still own a private group for each Pi session.
            start_new_session=not self.scenario.supervised_group, close_fds=True,
        )
        CLEANUP['started'] += 1
        if on_spawn is not None:
            on_spawn()
        os.close(self.slave)
        self.slave = None
        event = self.wait_file_event(self.scenario.bridge_path,
                             lambda row: row.get('type') in {'bridge-ready', 'setup-error'}
                             and row.get('processPid') == self.child.pid, 'bridge setup')
        if expect_setup_error:
            require(event['type'] == 'setup-error' and event.get('code') == expect_setup_error,
                    f'wrong setup result: {event}')
            require(self.wait_exit('setup rejection exit') == 0, 'Pi must shut down cleanly after the explicit setup rejection')
            self.finish_cleanup()
            return
        require(event['type'] == 'bridge-ready', f'bridge setup failed: {event}')
        require(event['sessionId'] == self.scenario.identity['sessionId'], 'wrong bridge session')
        self.scenario.journal_id = event['journalId']
        self.wait_file_event(self.scenario.bridge_path,
                             lambda row: row.get('type') == 'control-ready'
                             and row.get('processPid') == self.child.pid, 'control socket startup')
        self.control = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
        self.control.setblocking(False)
        # Bind stays inside the owned mode-0700 directory; the kernel's sun_path
        # limit applies to this short lookup name, not the resolved directory.
        directory = os.open(self.dir, os.O_RDONLY | os.O_DIRECTORY | os.O_NOFOLLOW)
        try:
            self.control.connect(f'/proc/self/fd/{directory}/control.sock')
        finally:
            os.close(directory)
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
        budget = self.scenario.remaining('PTY I/O')
        readers = []
        if self.master is not None:
            readers.append(self.master)
        if self.control is not None:
            readers.append(self.control)
        if not readers:
            return
        ready, _, _ = select.select(readers, [], [], min(0.05, budget))
        for source in ready:
            if source is self.control:
                self.read_control()
                continue
            try:
                data = os.read(source, 65536)
            except OSError:
                continue
            if not data:
                continue
            with self.raw_path.open('ab', buffering=0) as raw:
                raw.write(data)
            require(self.raw_path.stat().st_size < 2 * 1024 * 1024, 'PTY trace limit exceeded')
            if b'\x1b[6n' in data:
                os.write(self.master, b'\x1b[1;1R')

    def read_control(self):
        if self.control is None:
            return False
        try:
            data = self.control.recv(65536)
        except BlockingIOError:
            return False
        if not data:
            return False
        with (self.dir / 'control.jsonl').open('ab', buffering=0) as trace:
            trace.write(data)
        self.control_buffer += data
        while b'\n' in self.control_buffer:
            line, self.control_buffer = self.control_buffer.split(b'\n', 1)
            if line:
                self.control_messages.append(json.loads(line))
        return True

    def wait_control(self, predicate, label):
        while True:
            self.scenario.remaining(label)
            match = next((row for row in self.control_messages if predicate(row)), None)
            if match:
                self.scenario.remaining(label)
                return match
            if self.child.poll() is not None:
                # The quit acknowledgement can be buffered after Pi has exited.
                if self.read_control():
                    continue
                raise AssertionError(f'{self.scenario.name}: Pi exited before {label}: {self.child.returncode}')
            self.pump()

    def wait_file_event(self, path, predicate, label):
        while True:
            self.scenario.remaining(label)
            match = next((row for row in event_rows(path) if predicate(row)), None)
            if match:
                self.scenario.remaining(label)
                return match
            if self.child is not None and self.child.poll() is not None:
                # Pi can append a final setup-error between the read and poll.
                self.scenario.remaining(label)
                match = next((row for row in event_rows(path) if predicate(row)), None)
                self.scenario.remaining(label)
                require(match is not None, f'{self.scenario.name}: Pi exited before {label}')
                return match
            self.pump()

    def send(self, command):
        self.scenario.remaining('control send')
        self.next_id += 1
        command = dict(command, id=self.next_id)
        require(self.control is not None, 'control socket is unavailable')
        self.control.sendall((json.dumps(command) + '\n').encode())
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
        while True:
            self.scenario.remaining(label)
            current = self.command('state')
            if predicate(current):
                self.scenario.remaining(label)
                return current
            self.pump()

    def type(self, text, enter=False):
        self.scenario.remaining('PTY input')
        require(self.master is not None, 'PTY is closed')
        data = text.encode() + (b'\r' if enter else b'')
        require(os.write(self.master, data) == len(data), 'short PTY input write')

    def wait_exit(self, label):
        while True:
            self.scenario.remaining(label)
            code = self.child.poll()
            if code is not None:
                return code
            self.pump()

    def stop(self):
        if self.closed:
            return
        require(self.child is not None and self.child.poll() is None, 'Pi exited before graceful quit')
        require(self.command('quit').get('quitting') is True, 'quit handshake missing')
        require(self.wait_exit('graceful exit') == 0, f'Pi clean exit failed: {self.child.returncode}')
        require(any(row.get('type') == 'session-shutdown' and row.get('processPid') == self.child.pid
                    for row in event_rows(self.scenario.bridge_path)), 'Pi did not emit session_shutdown')
        self.finish_cleanup()

    def kill(self):
        if self.closed:
            return
        self.scenario.remaining('crash injection')
        require(self.child.poll() is None, 'owned Pi process already exited')
        os.killpg(self.child.pid, signal.SIGKILL)
        require(self.wait_exit('SIGKILL exit') == -signal.SIGKILL, 'owned Pi SIGKILL was not reaped')
        self.finish_cleanup()

    def finish_cleanup(self):
        if self.closed:
            return
        try:
            if self.child is not None and not self.scenario.supervised_group:
                assert_group_gone(self.child.pid)
        finally:
            self.close_descriptors()
        self.closed = True
        if self.child is not None:
            CLEANUP['reaped'] += 1

    def close_descriptors(self):
        errors = []
        for name in ('control', 'master', 'slave'):
            obj = getattr(self, name)
            setattr(self, name, None)
            if obj is not None:
                try:
                    os.close(obj) if isinstance(obj, int) else obj.close()
                except OSError as error:
                    errors.append(error)
        try:
            (self.dir / 'control.sock').unlink(missing_ok=True)
        except OSError as error:
            errors.append(error)
        if errors:
            raise ExceptionGroup('cannot close PTY descriptors', errors)

    def close(self, deadline):
        if self.closed:
            return
        try:
            if self.child is not None:
                if self.scenario.supervised_group:
                    # Don't signal our own group; the supervisor owns it.
                    self.child.kill()
                else:
                    # The session is ours even if its leader has already exited.
                    try:
                        os.killpg(self.child.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                self.child.wait(timeout=max(0, deadline - time.monotonic()))
        finally:
            self.finish_cleanup()


def assert_group_gone(pid):
    try:
        os.killpg(pid, 0)
    except ProcessLookupError:
        return
    raise AssertionError('owned process group survived Pi cleanup')

def cli_args(session_dir, session_id):
    return [
        '--offline', '--no-approve',
        '--no-extensions', '--no-skills', '--no-prompt-templates',
        '--no-context-files', '--no-themes', '--no-mcp', '--no-builtin-tools',
        '-e', str(HERE / 'pty-oracle.mjs'),
        '-e', str(HERE / 'pty-bridge.mjs'),
        '--model', 'artel-fixture/fixture', '--thinking', 'off',
        '--session-id', session_id, '--session-dir', str(session_dir),
        '--system-prompt', 'Isolated numeric transport fixture. No tools. Reply only 42.',
    ]


def preflight(parent_root):
    require(sys.platform == 'linux', 'Linux is required')
    require(NODE.is_absolute() and NODE.is_file() and os.access(NODE, os.X_OK),
            'PI_TEST_NODE must be an absolute executable Node path')
    require(PACKAGE.is_absolute() and PACKAGE.is_dir(), 'PI_TEST_PACKAGE must be absolute')
    require(ROOT.is_absolute() and str(ROOT).startswith('/tmp/artel-pi-delivery-'),
            'PROBE_ROOT must be private /tmp/artel-pi-delivery-*')
    require(ROOT.stat().st_uid == os.getuid() and (ROOT.stat().st_mode & 0o777) == 0o700,
            'PROBE_ROOT must be owned mode 0700')
    require(ROOT.resolve() == ROOT and not ROOT.is_relative_to(PROJECT),
            'PROBE_ROOT must be real and outside the worktree')
    unknown = set(os.environ) - ALLOWED_ENV
    require(not unknown, f'unapproved inherited environment: {sorted(unknown)}')
    require(os.environ.get('PI_OFFLINE') == '1', 'PI_OFFLINE=1 required')
    for key in ['HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME',
                'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR']:
        value = Path(os.environ.get(key, ''))
        require(value.is_absolute() and value.exists(), f'{key} must be an existing private path')
        require(value.resolve() == value and value != parent_root and value.is_relative_to(parent_root),
                f'{key} must be a real directory inside PROBE_ROOT')
        require(value.is_dir() and value.stat().st_uid == os.getuid() and value.stat().st_mode & 0o777 == 0o700,
                f'{key} must be owned mode 0700')
    # Run the public CLI version command with a fresh, private preflight environment.
    env = dict(os.environ)
    for key in ['HOME', 'XDG_CONFIG_HOME', 'XDG_CACHE_HOME', 'XDG_STATE_HOME',
                'XDG_DATA_HOME', 'PI_CODING_AGENT_DIR', 'TMPDIR']:
        directory = ROOT / key.lower()
        directory.mkdir(mode=0o700)
        env[key] = str(directory)
    env['PI_SKIP_VERSION_CHECK'] = '1'
    result = subprocess.run([str(NODE), str(HERE / 'pty-preflight.mjs')], cwd=ROOT, env=env,
                            capture_output=True, timeout=8, text=True, check=True)
    cli = result.stdout.strip()
    version = subprocess.run([str(NODE), cli, '--version'], cwd=ROOT, env=env,
                             capture_output=True, timeout=8, text=True, check=True)
    require(version.stdout.strip() == '1.5.2', f'public Pi version mismatch: {version.stdout}')
    return cli


def run(name, expected_pi_sends, body, *, supervised_group=False):
    scenario = Scenario(name, supervised_group=supervised_group)
    try:
        body(scenario)
    finally:
        scenario.close()
    result = scenario.finalize(expected_pi_sends)
    scenario.remaining('publish result')
    RESULTS.append(result)
    print(f"PASS {name}: pi={result['piSends']} provider={result['providerRequests']} "
          f"starts={result['beforeAgentStart']} server={result['serverStatus']}", flush=True)


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
        p.stop()
        s.summary(redeliveries=1, restarts=0)
    run('positive-custom-redelivery', 1, body)


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
        p.stop()
        s.summary(user_prompts=[manual], intent_only=True, restarts=0)
    run('manual-first-busy-local-pending', 0, body)


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
        p.stop()
        s.summary(user_prompts=[manual], spentRight=1, finalIdleRejected=1, restarts=0)
    run('busy-after-spend-final-idle-check', 0, body)


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
        require(not state['idle'] and not state['pending'], 'draft typing entered Pi queue while waiting for the held provider response')
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
        require(len(json.loads((s.dir / 'server.json').read_text())['messages']) == 1,
                'old marker created a new bridge message/receipt')
        p.stop()
        # Count writer entry, not persisted state: an idempotent extra write
        # would otherwise be invisible. Read all events after Pi has exited.
        require(sum(row.get('type') == 'receipt-write-start' for row in event_rows(s.bridge_path)) == 1,
                'old-marker user turn must not start another receipt writer')
        s.summary(user_prompts=[manual, old_marker], draftPreserved=1, oldMarkerUserTurns=1,
                  oldMarkerAdditionalReceiptWrites=0, restarts=0)
    run('custom-first-draft-and-old-marker', 1, body)


def assert_persisted_restart(before, local, server_after, local_after):
    require(server_after == dict(before, receipt=local['receipt']),
            'restart changed the original persisted spend or published a different receipt')
    require(local_after == local, 'restart changed the original persisted local record')


def restart_after_spend():
    def body(s):
        p = s.start()
        p.start_deliver(['bridge:afterAttemptCommit'])
        p.wait_barrier('bridge:afterAttemptCommit')
        server, local = s.server_record(), s.journal_record()
        require(server['spend'] and server['spend'] == local['attempt'], 'spend/attempt absent or mismatched')
        require(server['receipt'] is None and local['receipt'] is None, 'receipt existed before crash')
        require(not s.pi_sends() and not s.provider.requests, 'call occurred before crash barrier')
        p.kill()
        recovered = s.start()
        result = recovered.command('deliver', identity=s.identity, payload=s.payload)
        require(result['status'] == 'unknown' and not result['transmitted'], 'restart recovered spent right')
        require(not s.pi_sends() and not s.provider.requests, 'restart after spend caused a call')
        recovered.stop()
        assert_persisted_restart(server, local, s.server_record(), s.journal_record())
        s.summary(restarts=1, crashBarrier='after-attempt-before-pi')
    run('restart-after-spend-before-pi', 0, body)


def restart_after_transmission():
    def body(s):
        p = s.start()
        p.start_deliver(['receipt:beforeWrite'])
        custom = p.wait_token('custom')
        request = s.provider.wait()
        require(request.token == custom['token'], 'transmission token mismatch')
        p.wait_barrier('receipt:beforeWrite')
        server, local = s.server_record(), s.journal_record()
        require(server['spend'] and server['spend'] == local['attempt'], 'spend/attempt absent or mismatched')
        require(server['receipt'] is None and local['receipt'] is None, 'receipt existed before crash')
        p.kill()
        s.provider.drop(request)
        recovered = s.start()
        result = recovered.command('deliver', identity=s.identity, payload=s.payload)
        require(result['status'] == 'unknown' and not result['transmitted'], 'unknown became a receipt on restart')
        require(len(s.pi_sends()) == 1 and len(s.provider.requests) == 1, 'restart repeated transmission')
        recovered.stop()
        assert_persisted_restart(server, local, s.server_record(), s.journal_record())
        s.summary(restarts=1, crashBarrier='after-transmission-before-receipt')
    run('restart-after-transmission-before-receipt', 1, body)


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
        require(server['spend'] and server['spend'] == local['attempt'], 'spend/attempt absent or mismatched')
        require(local['receipt'] and server['receipt'] is None, 'not paused after local receipt before POST')
        attempt_id = server['spend']['attemptId']
        p.kill()
        s.provider.drop(request)
        recovered = s.start()
        result = recovered.command('deliver', identity=s.identity, payload=s.payload)
        require(result['status'] == 'observed' and result['attemptId'] == attempt_id,
                'persisted receipt was not idempotently published')
        require(len(s.pi_sends()) == 1 and len(s.provider.requests) == 1, 'receipt recovery repeated Pi call')
        recovered.stop()
        assert_persisted_restart(server, local, s.server_record(), s.journal_record())
        s.summary(restarts=1, crashBarrier='after-local-receipt-before-post')
    run('restart-after-local-receipt-before-post', 1, body)


def cleanup_control():
    def body(s):
        killed = s.start()
        require(not s.provider.requests, 'cleanup control generated provider work')
        killed.kill()
        stopped = s.start()
        stopped.stop()
        s.summary(restarts=1, sigkillReaps=1, gracefulReaps=1, orphanProcesses=0)
    run('owned-process-pty-cleanup', 0, body)



def restart_missing_journal():
    def body(s):
        p = s.start()
        p.start_deliver(['bridge:beforeSpend'])
        p.wait_barrier('bridge:beforeSpend')
        before = s.server_record()
        require(before['spend'] is None and s.journal_record()['attempt'] is None,
                'expected admission without a spend')
        p.kill()
        journal = s.dir / 'journal.json'
        journal.unlink()
        s.start(expect_setup_error='ENOENT')
        require(not journal.exists() and s.server_record() == before,
                'recovery recreated a journal or spent without it')
        require(not s.pi_sends() and not s.provider.requests, 'missing journal allowed a send')
        s.summary(restarts=1, setupRejected=1)
    run('restart-with-missing-unspent-journal', 0, body)


def partial_startup_control():
    scenario = Scenario('partial-startup-check')
    with patch.object(subprocess, 'Popen', side_effect=OSError('injected spawn failure')):
        try:
            scenario.start()
        except OSError as error:
            require(str(error) == 'injected spawn failure', 'wrong startup error')
        else:
            raise AssertionError('injected Pi spawn unexpectedly succeeded')
        finally:
            scenario.close()
    process, = scenario.processes
    require(process.closed and process.master is None and process.slave is None
            and not scenario.provider.thread.is_alive(), 'partial start leaked resources')


def assert_rejected(action, expected):
    try:
        action()
    except AssertionError as error:
        require(expected in str(error), f'wrong rejection: {error}')
    else:
        raise AssertionError(f'expected rejection: {expected}')


def manual_first_final_state_control():
    for change, rejection in [('admission', 'server record must remain absent'),
                              ('spend', 'server record must remain absent'),
                              ('local-attempt', 'journal record must remain intent-only'),
                              ('missing-intent', 'journal record must remain intent-only')]:
        def body(s):
            server = s.dir / 'server.json'
            journal = s.dir / 'journal.json'
            message_id = s.identity['messageId']
            intent = {'identity': s.identity, 'payload': s.payload, 'attempt': None, 'receipt': None}
            private_json(server, {'messages': {}})
            private_json(journal, {'messages': {message_id: intent}})
            s.summary(intent_only=True)
            close = s.provider.close

            def close_with_late_change(deadline):
                close(deadline)
                # This is too late for any assertions in the scenario body.
                spend = {'attemptId': 'fictional-late-attempt'}
                if change in {'admission', 'spend'}:
                    private_json(server, {'messages': {message_id: {
                        'identity': s.identity, 'payload': s.payload,
                        'spend': spend if change == 'spend' else None, 'receipt': None,
                    }}})
                elif change == 'local-attempt':
                    private_json(journal, {'messages': {message_id: dict(intent, attempt=spend)}})
                else:
                    private_json(journal, {'messages': {}})

            s.provider.close = close_with_late_change

        count = len(RESULTS)
        assert_rejected(lambda: run(f'manual-first-late-{change}-check', 0, body), rejection)
        require(len(RESULTS) == count, 'manual-first passed with a late state change')


def late_provider_control(*, fresh):
    workers = []
    failures = []
    providers = []

    def body(s):
        provider = s.provider
        providers.append(provider)
        token = str(uuid4())
        late_token = str(uuid4()) if fresh else token
        prompt = s.payload['content'] if fresh else 'FICTIONAL_LATE_DUPLICATE_TEXT'
        token_event = {'type': 'token-event', 'token': token, 'messageType': 'user',
                       'prompt': prompt, 'sessionId': s.identity['sessionId']}
        messages = {}
        if fresh:
            spend = {'attemptId': 'fictional-attempt', 'journalId': 'fictional-journal',
                     'ownerInstance': 'fictional-owner', 'ownerPid': 123}
            messages[s.identity['messageId']] = {'spend': spend}
            token_event.update(s.identity, **spend, messageType='custom', details=s.payload['details'])
            s.bridge_path.write_text('{"type":"pi-send"}\n', encoding='utf-8')
        s.oracle_path.write_text(json.dumps(token_event) + '\n', encoding='utf-8')
        private_json(s.dir / 'server.json', {'messages': messages})
        payload = json.dumps({'model': 'fixture', 'messages': [{'role': 'user', 'content': prompt}]}).encode()
        headers = {'Content-Length': str(len(payload)), 'x-artel-probe-token': token,
                   'Authorization': 'Bearer fake-not-a-secret'}
        with socket.create_connection(('127.0.0.1', provider.port), timeout=3) as first:
            head = 'POST /v1/chat/completions HTTP/1.0\r\n' + ''.join(
                f'{key}: {value}\r\n' for key, value in headers.items()) + '\r\n'
            first.sendall(head.encode() + payload)
            provider.release(provider.wait())
            with http.client.HTTPResponse(first) as response:
                response.begin()
                require(response.status == 200 and b'[DONE]' in response.read(), 'late duplicate control setup failed')

        dup = http.client.HTTPConnection('127.0.0.1', provider.port, timeout=3)
        try:
            dup.putrequest('POST', '/v1/chat/completions')
            for key, value in dict(headers, **{'x-artel-probe-token': late_token}).items():
                dup.putheader(key, value)
            dup.endheaders()
            with provider.condition:
                address = dup.sock.getsockname()
                while not any(sock.getpeername() == address for sock in provider.active if sock.fileno() >= 0):
                    provider.condition.wait(s.remaining('duplicate acceptance'))
            require(len(provider.requests) == 1, 'duplicate body was not withheld')
            prompts = [] if fresh else [prompt]
            s.correlate(prompts)
            s.summary(user_prompts=prompts)

            def finish_body():
                try:
                    require(provider.closing.wait(5), 'late body missed the drain barrier')
                    if fresh:
                        with s.oracle_path.open('a', encoding='utf-8') as events:
                            events.write(json.dumps(dict(token_event, token=late_token)) + '\n')
                        with s.bridge_path.open('a', encoding='utf-8') as events:
                            events.write('{"type":"pi-send"}\n')
                    dup.send(payload)
                except Exception as error:
                    failures.append(error)
                finally:
                    dup.close()

            thread = threading.Thread(target=finish_body, daemon=True)
            thread.start()
            workers.append(thread)
        except BaseException:
            dup.close()
            raise

    count = len(RESULTS)
    try:
        if fresh:
            assert_rejected(lambda: run('late-fresh-delivery-check', 1, body), 'unexpected final Pi/HTTP/start counts')
        else:
            assert_rejected(lambda: run('late-provider-duplicate-check', 0, body), 'duplicate provider request for one token')
    finally:
        for thread in workers:
            thread.join(timeout=5)
            require(not thread.is_alive(), 'late duplicate client did not exit')
    require(len(RESULTS) == count and not failures and len(providers[0].requests) == 2,
            'late body was not counted against the original scenario')


def deadline_control():
    processes = []

    def body(s):
        process = PtyProcess(s)
        s.processes.append(process)
        processes.append(process)
        process.control_messages.append({'ok': True})
        s.oracle_path.write_text('{"ok": true}\n', encoding='utf-8')
        s.deadline = s.provider.deadline = time.monotonic() - 1
        assert_rejected(lambda: process.wait_control(lambda row: row['ok'], 'stale control'), 'watchdog expired')
        assert_rejected(lambda: process.wait_file_event(s.oracle_path, lambda row: row['ok'], 'stale file'), 'watchdog expired')
        assert_rejected(s.provider.wait, 'watchdog expired')
        s.summary()

    count = len(RESULTS)
    assert_rejected(lambda: run('expired-absolute-deadline-check', 0, body), 'final result watchdog expired')
    require(len(RESULTS) == count and processes[0].closed, 'expired scenario passed or bypassed cleanup')


def persisted_restart_control():
    before = {'spend': {'attemptId': 'original', 'ownerPid': 123}, 'receipt': None}
    local = {'receipt': {'type': 'before_agent_start'}}
    published = dict(before, receipt=local['receipt'])
    assert_persisted_restart(before, local, published, local)
    assert_rejected(lambda: assert_persisted_restart(before, local, before, local), 'different receipt')
    unknown = {'receipt': None}
    assert_rejected(lambda: assert_persisted_restart(before, unknown, published, unknown), 'different receipt')
    changed = dict(before, spend=dict(before['spend'], ownerPid=456))
    assert_rejected(lambda: assert_persisted_restart(before, unknown, changed, unknown), 'original persisted spend')


def buffered_quit_reply_control():
    s = Scenario('buffered-quit-reply-check')
    try:
        p = PtyProcess(s)
        # Model a child that's already been reaped with a response still unread.
        p.child = SimpleNamespace(poll=lambda: 0, returncode=0)
        reader, writer = socket.socketpair()
        with reader, writer:
            p.control = reader
            reader.setblocking(False)
            writer.sendall(b'{"quitting":true}\n')
            writer.close()
            require(p.wait_control(lambda row: row.get('quitting'), 'quit reply')['quitting'],
                    'buffered reply was lost after exit')
            assert_rejected(lambda: p.wait_control(lambda _: False, 'missing reply'), 'Pi exited before missing reply')
    finally:
        s.close()


def buffered_setup_error_control():
    s = Scenario('buffered-setup-error-check')
    try:
        process = PtyProcess(s)
        event = {'type': 'setup-error', 'code': 'ENOENT'}

        def exited():
            # Insert the final event exactly between the first read and poll.
            s.bridge_path.write_text(json.dumps(event) + '\n', encoding='utf-8')
            return 0

        process.child = SimpleNamespace(poll=exited)
        require(process.wait_file_event(s.bridge_path, lambda row: row == event, 'setup error') == event,
                'final setup error was lost after Pi exit')
    finally:
        s.close()


def sigterm_probe(mode):
    require(os.getsid(0) == os.getpid(), 'cleanup probe needs its own supervisor-owned session')

    def stall_before_ready():
        # Pi already exists, but the supervisor receives no group ID or readiness.
        os.write(sys.stdout.fileno(), b'spawned\n')
        while True:
            signal.pause()

    def body(s):
        process = s.start(on_spawn=stall_before_ready if mode == 'startup-stall' else None)
        if mode == 'stubborn':
            signal.signal(signal.SIGTERM, signal.SIG_IGN)
        os.write(sys.stdout.fileno(), b'ready\n')
        while not INTERRUPTED:
            process.pump()
    run('sigterm-owned-group', 0, body, supervised_group=True)


def enable_child_subreaper():
    # Linux PR_SET_CHILD_SUBREAPER: the supervisor can wait for Pi if the
    # probe dies first, even when the host/container's PID 1 does not reap.
    libc = ctypes.CDLL(None, use_errno=True)
    if libc.prctl(36, 1, 0, 0, 0) != 0:
        error = ctypes.get_errno()
        raise OSError(error, os.strerror(error))


def reap_adopted_group(pgid, deadline):
    outcomes = []
    while True:
        try:
            pid, status = os.waitpid(-pgid, os.WNOHANG)
        except ChildProcessError:
            return outcomes
        if pid:
            outcomes.append(os.waitstatus_to_exitcode(status))
        else:
            time.sleep(min(0.01, remaining(deadline, 'adopted Pi cleanup')))


def sigterm_control(parent_root, mode):
    check_interrupted()
    env = dict(os.environ, PROBE_ROOT=str(parent_root))
    process = subprocess.Popen([sys.executable, '-B', __file__, '--sigterm-probe', mode],
                               env=env, stdout=subprocess.PIPE, start_new_session=True)
    # Popen establishes this group before the probe can run. Pi inherits it;
    # the supervisor never needs a PID report from the probe.
    pgid = process.pid
    stalled = False
    pending = b''
    try:
        deadline = time.monotonic() + 12
        while True:
            check_interrupted()
            budget = deadline - time.monotonic()
            if budget <= 0:
                require(mode == 'startup-stall' and stalled, 'SIGTERM startup watchdog expired')
                break  # Deliberate startup deadline expiry after Pi was spawned.
            readable, _, _ = select.select([process.stdout], [], [], min(0.05, budget))
            if not readable:
                continue
            data = os.read(process.stdout.fileno(), 4096)
            require(data, 'cleanup probe exited before readiness')
            pending += data
            if pending == b'spawned\n' and mode == 'startup-stall' and not stalled:
                stalled = True
                pending = b''
            elif pending == b'ready\n':
                require(mode != 'startup-stall', 'stalled probe unexpectedly reported readiness')
                break

        check_interrupted()
        if not stalled:
            os.kill(pgid, signal.SIGTERM)
            deadline = time.monotonic() + (0.2 if mode == 'stubborn' else 8)
            # Observe exit without reaping the group leader: its PID must
            # remain reserved until the supervisor has signalled the group.
            while not os.waitid(os.P_PID, pgid, os.WEXITED | os.WNOHANG | os.WNOWAIT):
                check_interrupted()
                budget = deadline - time.monotonic()
                if budget <= 0:
                    require(mode == 'stubborn', 'SIGTERM cleanup watchdog expired')
                    break
                time.sleep(min(0.01, budget))
            else:
                require(mode == 'graceful', 'stubborn probe unexpectedly exited after SIGTERM')
    finally:
        with process.stdout:
            cleanup_deadline = time.monotonic() + 3
            try:
                os.killpg(pgid, signal.SIGKILL)
            except ProcessLookupError:
                pass
            process.wait(timeout=remaining(cleanup_deadline, 'probe reap'))
            adopted = reap_adopted_group(pgid, cleanup_deadline)
            assert_group_gone(pgid)
    if mode == 'graceful':
        require(process.returncode == 128 + signal.SIGTERM and not adopted,
                'SIGTERM did not unwind the probe and reap Pi gracefully')
    else:
        require(process.returncode == -signal.SIGKILL and adopted == [-signal.SIGKILL],
                'supervisor did not force-kill the probe and reap the adopted Pi')


RESULTS = []
CLEANUP = {'started': 0, 'reaped': 0}


def main():
    global ROOT, CLI
    os.umask(0o077)
    signal.signal(signal.SIGTERM, on_sigterm)
    parent_root = ROOT
    require(ROOT.is_absolute() and ROOT.resolve() == ROOT and ROOT.is_dir()
            and str(ROOT).startswith('/tmp/artel-pi-delivery-') and not ROOT.is_relative_to(PROJECT)
            and ROOT.stat().st_uid == os.getuid() and ROOT.stat().st_mode & 0o777 == 0o700,
            'PROBE_ROOT must be a private real directory outside the worktree')
    ROOT = Path(mkdtemp(prefix='pty-', dir=parent_root))
    CLI = preflight(parent_root)
    if len(sys.argv) == 3 and sys.argv[1] == '--sigterm-probe':
        require(sys.argv[2] in {'graceful', 'stubborn', 'startup-stall'}, 'unexpected cleanup probe mode')
        check_interrupted()
        sigterm_probe(sys.argv[2])
        return
    require(not sys.argv[1:], 'unexpected runner arguments')
    for scenario in [positive_control, manual_first, busy_after_spend, custom_first,
                     restart_after_spend, restart_after_transmission, restart_after_receipt,
                     restart_missing_journal, cleanup_control]:
        scenario()
    partial_startup_control()
    enable_child_subreaper()
    for mode in ['graceful', 'stubborn', 'startup-stall']:
        sigterm_control(parent_root, mode)
    buffered_quit_reply_control()
    buffered_setup_error_control()
    manual_first_final_state_control()
    late_provider_control(fresh=False)
    late_provider_control(fresh=True)
    deadline_control()
    persisted_restart_control()
    check_interrupted()
    require(len(RESULTS) == 9, 'incomplete PTY scenario results')
    require(CLEANUP['started'] == 14 and CLEANUP['reaped'] == CLEANUP['started'],
            f'owned process cleanup count mismatch: {CLEANUP}')
    output = {
        'piVersion': '1.5.2',
        'nodeVersion': '22.23.2',
        'scenarios': RESULTS,
        'processCleanup': {
            'result': 'PASS', 'ownedProcessesReaped': CLEANUP['reaped'],
            'orphanProcesses': 0, 'ptyDescriptorsClosed': CLEANUP['reaped'],
            'partialStartupCleanup': 'PASS', 'sigtermOwnedGroupCleanup': 'PASS',
            'sigtermForceKillCleanup': 'PASS', 'startupTimeoutBeforeReadyCleanup': 'PASS',
        },
        'runnerGuards': {
            'lateDuplicateRejectedAfterDrain': True,
            'lateFreshDeliveryRejectedAfterDrain': True,
            'manualFirstLateStateRejectedAfterDrain': True,
            'bufferedQuitReplyAfterExit': True,
            'bufferedSetupErrorAfterExit': True,
            'expiredDeadlineRejectedAndCleanedUp': True,
            'persistedRestartMismatchRejected': True,
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
    try:
        main()
    except Interrupted as error:
        print(str(error), file=sys.stderr)
        sys.exit(128 + signal.SIGTERM)
