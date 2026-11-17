"""Real pinned Pi TUI, private resources, loopback-only deterministic provider."""
import http.server
import json
import os
from pathlib import Path
import pty
import select
import shutil
import signal
import subprocess
import tempfile
import threading
import time
import unittest
from observer_fixture import observer
from daemon_restart import check_restart

PACKAGE = Path(__file__).resolve().parents[1]
CLI = PACKAGE / 'node_modules/@earendil-works/pi-coding-agent/dist/cli.js'


class Provider(http.server.BaseHTTPRequestHandler):
    calls = 0
    forbidden = ''

    def log_message(self, *_args):
        pass

    def do_POST(self):
        assert self.path == '/v1/chat/completions'
        payload = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        assert payload['model'] == 'local'
        assert not type(self).forbidden or type(self).forbidden not in json.dumps(payload)
        type(self).calls += 1
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        if type(self).calls == 1:
            delta = {'role': 'assistant', 'tool_calls': [
                {'index': n, 'id': f'fictional-{n}', 'type': 'function',
                 'function': {'name': name, 'arguments': '{}'}}
                for n, name in enumerate(['fixture_a', 'fixture_b'])]}
            reason = 'tool_calls'
        else:
            delta, reason = {'role': 'assistant', 'content': 'Fictional answer'}, 'stop'
        for chunk in [
            {'choices': [{'index': 0, 'delta': delta, 'finish_reason': None}]},
            {'choices': [{'index': 0, 'delta': {}, 'finish_reason': reason}]}]:
            self.wfile.write(('data: ' + json.dumps(chunk) + '\n\n').encode())
        self.wfile.write(b'data: [DONE]\n\n')
        self.wfile.flush()


class Lifecycle(unittest.TestCase):
    def test_actual_daemon_restart(self):
        Provider.calls = 0
        check_restart(self, PACKAGE, Provider)

    def test_sdk(self):
        Provider.calls = 0
        with tempfile.TemporaryDirectory(prefix='artel-pi-sdk-') as tmp:
            root = Path(tmp)
            (root / 'agent').mkdir(mode=0o700)
            (root / 'work').mkdir(mode=0o700)
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            env = {'PATH': str(Path(shutil.which('node')).parent) + ':/usr/bin:/bin',
                   'HOME': tmp, 'PI_CODING_AGENT_DIR': tmp + '/agent', 'PI_OFFLINE': '1',
                   'XDG_CONFIG_HOME': tmp + '/config', 'XDG_DATA_HOME': tmp + '/data',
                   'XDG_CACHE_HOME': tmp + '/cache',
                   'ARTEL_PROBE_TRACE': tmp + '/trace.jsonl',
                   'ARTEL_FIXTURE_SESSION': tmp + '/original-session',
                   'ARTEL_FIXTURE_URL': f'http://127.0.0.1:{server.server_port}/v1'}
            oracle = observer(root, env)
            observed = oracle.__enter__()
            Provider.forbidden = observed['token']
            try:
                result = subprocess.run([shutil.which('node'), '--experimental-strip-types',
                                         str(PACKAGE / 'tests/sdk.ts')],
                                        cwd=root / 'work', env=env, capture_output=True, timeout=20)
                self.assertEqual(result.returncode, 0, result.stderr.decode())
                self.assertEqual(Provider.calls, 2)
                self.assertIn(b'public SDK probe passed', result.stdout)
                self.assertTrue(observed['registrations'])
                self.assertTrue(observed['reports'])
                self.assertNotIn(observed['token'].encode(), result.stdout + result.stderr)
            finally:
                oracle.__exit__(None, None, None)
                server.shutdown()
                server.server_close()
                thread.join(timeout=2)
        self.assertFalse(root.exists())

    def test_negative_cleanup(self):
        with tempfile.TemporaryDirectory(prefix='artel-pi-negative-') as tmp:
            result = subprocess.run([shutil.which('python3'), '-B', __file__,
                                     'Lifecycle.test_pty_lifecycle'],
                                    env={**os.environ, 'TMPDIR': tmp,
                                         'ARTEL_FIXTURE_NEGATIVE': '1'},
                                    capture_output=True, timeout=25)
            self.assertNotEqual(result.returncode, 0)
            self.assertIn(b'intentional shipping observer assertion', result.stderr)
            self.assertEqual(list(Path(tmp).iterdir()), [])

    def test_pty_lifecycle(self):
        Provider.calls = 0
        with tempfile.TemporaryDirectory(prefix='artel-pi-probe-') as tmp:
            root = Path(tmp)
            (root / 'agent').mkdir(mode=0o700)
            (root / 'work').mkdir(mode=0o700)
            # No shell startup files, inherited keys, developer extensions or skills.
            env = {'PATH': str(Path(shutil.which('node')).parent) + ':/usr/bin:/bin',
                   'HOME': tmp, 'XDG_CONFIG_HOME': tmp + '/config',
                   'XDG_DATA_HOME': tmp + '/data', 'XDG_CACHE_HOME': tmp + '/cache',
                   'PI_CODING_AGENT_DIR': tmp + '/agent', 'PI_OFFLINE': '1',
                   'TERM': 'xterm-256color', 'LANG': 'C.UTF-8',
                   'ARTEL_PROBE_TRACE': tmp + '/trace.jsonl',
                   'ARTEL_FIXTURE_SESSION': tmp + '/original-session'}
            server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Provider)
            thread = threading.Thread(target=server.serve_forever, daemon=True)
            thread.start()
            env['ARTEL_FIXTURE_URL'] = f'http://127.0.0.1:{server.server_port}/v1'
            oracle = observer(root, env)
            observed = oracle.__enter__()
            Provider.forbidden = observed['token']
            master, slave = pty.openpty()
            import fcntl
            import struct
            import termios
            fcntl.ioctl(slave, termios.TIOCSWINSZ, struct.pack('HHHH', 40, 120, 0, 0))
            args = [shutil.which('node'), str(CLI), '--offline', '--no-approve',
                    '--no-extensions', '--no-skills', '--no-prompt-templates',
                    '--no-context-files', '--no-themes', '--no-mcp', '--no-builtin-tools',
                    '-e', str(PACKAGE / 'src/index.ts'),
                    '-e', str(PACKAGE / 'tests/probe.ts'),
                    '-e', str(PACKAGE / 'tests/fixture.ts'), '--model', 'artel-fixture/local']
            child = subprocess.Popen(args, cwd=root / 'work', env=env,
                                     stdin=slave, stdout=slave, stderr=slave, start_new_session=True)
            os.close(slave)
            output = bytearray()

            def rows():
                path = root / 'trace.jsonl'
                return [json.loads(line) for line in path.read_text().splitlines()] if path.exists() else []

            def pump():
                if select.select([master], [], [], 0.03)[0]:
                    try:
                        data = os.read(master, 65536)
                    except OSError:
                        return
                    output.extend(data)
                    self.assertLess(len(output), 1024 * 1024)
                    if b'\x1b[6n' in data:
                        os.write(master, b'\x1b[1;1R')

            def wait(predicate, label):
                until = time.monotonic() + 15
                while time.monotonic() < until:
                    pump()
                    if predicate():
                        return
                    if child.poll() is not None:
                        break
                # Output is fictional only; never include inherited env/credentials.
                self.fail(f'{label}: trace={rows()} terminal={bytes(output[-3000:])!r}')

            def command(text):
                os.write(master, text.encode() + b'\r')

            try:
                wait(lambda: len(rows()) >= 1, 'startup')
                # Wait for editor initialization, assert no observer-authored run.
                for _ in range(30):
                    pump()
                self.assertEqual(Provider.calls, 0)
                self.assertEqual([r['event'] for r in rows()], ['session_start'])
                wait(lambda: len(observed['registrations']) == 1, 'shipping observer registration')
                if os.environ.get('ARTEL_FIXTURE_NEGATIVE'):
                    self.fail('intentional shipping observer assertion')
                command('Fictional input')
                wait(lambda: any(r['event'] == 'agent_settled' for r in rows()), 'settled')
                self.assertEqual(Provider.calls, 2)
                self.assertIn(2, [r['tools'] for r in rows()])
                ended = next(i for i, r in enumerate(rows()) if r['event'] == 'agent_end')
                settled = next(i for i, r in enumerate(rows()) if r['event'] == 'agent_settled')
                self.assertLess(ended, settled)
                self.assertTrue(rows()[settled]['idle'])
                wait(lambda: any(r['tool_count'] == 2 for r in observed['reports']), 'shipping overlap report')
                wait(lambda: any(r['activity'] == 'idle' and r['report_seq'] > 1 for r in observed['reports']), 'shipping settled report')
                old_seq = observed['reports'][-1]['report_seq']
                old_runtime = observed['registrations'][0]['runtime_id']
                observed['stop']()
                wait(lambda: b'offline / reconnecting' in bytes(output), 'shipping offline')
                observed['start']()
                wait(lambda: observed['reports'][-1]['report_seq'] > old_seq, 'shipping reconnect after server restart')
                self.assertEqual(len(observed['registrations']), 1)
                self.assertEqual(observed['reports'][-1]['runtime_id'], old_runtime)
                command('/fixture-wait')
                wait(lambda: any(r['event'] == 'ui_prompt_end' for r in rows()), 'UI wait')
                self.assertTrue(next(r for r in rows() if r['event'] == 'ui_prompt_start')['waiting'])
                wait(lambda: any(r['activity'] == 'waiting_user' for r in observed['reports']), 'shipping wait report')
                wait(lambda: observed['reports'][-1]['activity'] == 'idle', 'shipping wait end')
                for number, reason in enumerate(['reload', 'new', 'resume', 'fork'], start=2):
                    command('/fixture-' + reason)
                    wait(lambda: any(r['event'] == 'session_start' and r.get('reason') == reason
                                     for r in rows()), reason)
                    wait(lambda: len(observed['registrations']) == number, 'shipping ' + reason)
                    wait(lambda: any(r['runtime_id'] == observed['registrations'][-1]['runtime_id'] for r in observed['reports']), 'shipping report ' + reason)
                starts = [r for r in rows() if r['event'] == 'session_start']
                self.assertEqual(len({r['runtime'] for r in starts}), 5)
                self.assertEqual(starts[0]['session'], starts[1]['session'])
                self.assertNotEqual(starts[1]['session'], starts[2]['session'])
                self.assertEqual(starts[0]['session'], starts[3]['session'])
                self.assertNotEqual(starts[3]['session'], starts[4]['session'])
                self.assertEqual(Provider.calls, 2)  # lifecycle never auto-prompts
                self.assertEqual([r['reason'] for r in observed['registrations']], ['startup', 'reload', 'new', 'resume', 'fork'])
                self.assertEqual(len({r['runtime_id'] for r in observed['registrations']}), 5)
                self.assertNotIn(observed['token'].encode(), bytes(output))
                self.assertNotIn(observed['token'], ' '.join(args))
                command('/fixture-quit')
                wait(lambda: any(r.get('reason') == 'quit' for r in rows()), 'quit')
                child.wait(timeout=3)
                self.assertEqual(child.returncode, 0)
                boundaries = [(r['event'], r['reason']) for r in rows()
                              if r['event'] in ('session_start', 'session_shutdown')]
                expected = [('session_start', 'startup')]
                for reason in ['reload', 'new', 'resume', 'fork']:
                    expected.extend([('session_shutdown', reason), ('session_start', reason)])
                expected.append(('session_shutdown', 'quit'))
                self.assertEqual(boundaries, expected)
                for row in rows():
                    self.assertLessEqual(set(row), {'event', 'runtime', 'session', 'idle', 'tools', 'waiting', 'reason'})
                # SIGKILL cannot run shutdown; parent owns and removes the fixture directory.
                before = len(rows())
                child = subprocess.Popen(args, cwd=root / 'work', env=env,
                                         stdin=master, stdout=subprocess.DEVNULL,
                                         stderr=subprocess.DEVNULL, start_new_session=True)
                wait(lambda: len(rows()) > before, 'kill fixture startup')
                os.killpg(child.pid, signal.SIGKILL)
                child.wait(timeout=3)
                self.assertEqual(child.returncode, -signal.SIGKILL)
            finally:
                if child.poll() is None:
                    os.killpg(child.pid, signal.SIGKILL)
                    child.wait(timeout=3)
                os.close(master)
                oracle.__exit__(None, None, None)
                server.shutdown()
                server.server_close()
                thread.join(timeout=2)
        self.assertFalse(root.exists())


if __name__ == '__main__':
    unittest.main()
