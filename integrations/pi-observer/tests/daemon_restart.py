"""Actual temporary arteld + shipping extension public-SDK restart test."""
import json
import os
from pathlib import Path
import select
import shutil
import signal
import subprocess
import tempfile
import threading
import time
import urllib.request


def check_restart(case, package, provider):
    with tempfile.TemporaryDirectory(prefix='artel-pi-daemon-') as tmp:
        root = Path(tmp)
        for name in ['agent', 'work', 'private', 'home']:
            (root / name).mkdir(mode=0o700)
        for name in ['arteld', 'artelctl']:
            subprocess.run(['go', 'build', '-o', str(root / name), './cmd/' + name],
                           cwd=package.parents[1], check=True, capture_output=True, timeout=60)
        env = {'PATH': str(Path(shutil.which('node')).parent) + ':/usr/bin:/bin',
               'HOME': str(root / 'home'), 'PI_CODING_AGENT_DIR': str(root / 'agent'),
               'XDG_CONFIG_HOME': str(root / 'config'), 'XDG_DATA_HOME': str(root / 'data'),
               'XDG_CACHE_HOME': str(root / 'cache'), 'PI_OFFLINE': '1',
               'ARTEL_PROBE_TRACE': str(root / 'trace.jsonl'),
               'ARTEL_FIXTURE_SESSION': str(root / 'original-session'),
               'ARTEL_OBSERVER_CONFIG': str(root / 'private/config'),
               'ARTEL_FIXTURE_HOLD': '1'}
        token_path = str(root / 'private/owner.token')
        socket = str(root / 'private/observer.sock')
        daemon, child = None, None
        import http.server
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), provider)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        env['ARTEL_FIXTURE_URL'] = f'http://127.0.0.1:{server.server_port}/v1'

        def launch(listen):
            process = subprocess.Popen([str(root / 'arteld'), '--listen', listen,
                                        '--data-dir', str(root / 'db'), '--token-file', token_path,
                                        '--observer-socket', socket], env=env,
                                       stdout=subprocess.PIPE, stderr=subprocess.STDOUT)
            try:
                case.assertTrue(select.select([process.stdout], [], [], 10)[0], 'daemon startup deadline')
                line = process.stdout.readline().decode().strip()
                case.assertTrue(line.startswith('arteld: listening '), line)
                return process, line.split()[-1]
            except BaseException:
                process.kill()
                process.wait(timeout=3)
                process.stdout.close()
                raise

        def terminate(process, kill=False):
            if process is not None:
                if process.poll() is None:
                    process.send_signal(signal.SIGKILL if kill else signal.SIGTERM)
                process.wait(timeout=3)
                if process.stdout:
                    process.stdout.close()

        try:
            daemon, address = launch('127.0.0.1:0')
            endpoint = 'http://' + address
            result = subprocess.run([str(root / 'artelctl'), 'observer', 'provision',
                                     '--name', 'Fictional SDK', '--observer-socket', socket,
                                     '--output', env['ARTEL_OBSERVER_CONFIG'],
                                     '--endpoint', endpoint, '--token-file', token_path],
                                    env=env, capture_output=True, timeout=10)
            case.assertEqual(result.returncode, 0, result.stderr.decode())
            owner_token = Path(token_path).read_text().strip()
            provider.forbidden = Path(env['ARTEL_OBSERVER_CONFIG'] + '.token').read_text().strip()

            opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

            def projection():
                request = urllib.request.Request(endpoint + '/v1/observers',
                                                 headers={'Authorization': 'Bearer ' + owner_token})
                with opener.open(request, timeout=2) as response:
                    return json.load(response)

            def wait(predicate):
                until = time.monotonic() + 12
                while time.monotonic() < until:
                    value = projection()
                    if predicate(value):
                        return value
                    time.sleep(0.03)
                case.fail('daemon observer projection deadline')

            child = subprocess.Popen([shutil.which('node'), '--experimental-strip-types',
                                      str(package / 'tests/sdk.ts')], cwd=root / 'work', env=env,
                                     stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            case.assertTrue(select.select([child.stdout], [], [], 12)[0], 'SDK startup deadline')
            case.assertEqual(child.stdout.readline().strip(), b'observer restart ready')
            before = wait(lambda p: len(p['observers'][0]['runtimes']) == 1 and
                          p['observers'][0]['runtimes'][0]['freshness'] == 'fresh')
            runtime = before['observers'][0]['runtimes'][0]['registration']['runtime_id']
            terminate(daemon, kill=True)
            daemon, _ = launch(address)
            restarted = projection()
            case.assertEqual(restarted['database_instance_id'], before['database_instance_id'])
            case.assertIn(restarted['observers'][0]['runtimes'][0]['freshness'], ['stale', 'unknown'])
            after = wait(lambda p: p['observers'][0]['runtimes'][0]['freshness'] == 'fresh')
            case.assertEqual(len(after['observers'][0]['runtimes']), 1)
            case.assertEqual(after['observers'][0]['runtimes'][0]['registration']['runtime_id'], runtime)
            case.assertEqual(provider.calls, 0, 'daemon restart cannot start a prompt')
            stdout, stderr = child.communicate(b'continue\n', timeout=15)
            case.assertEqual(child.returncode, 0, stderr.decode())
            case.assertEqual(provider.calls, 2)
            case.assertNotIn(provider.forbidden.encode(), stdout + stderr + result.stdout + result.stderr)
        finally:
            if child is not None:
                if child.poll() is None:
                    child.kill()
                child.communicate(timeout=3)
            terminate(daemon)
            server.shutdown()
            server.server_close()
            thread.join(timeout=2)
        case.assertFalse(Path(socket).exists())
    case.assertFalse(root.exists())
