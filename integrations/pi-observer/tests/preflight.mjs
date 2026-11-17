import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { spawnSync } from 'node:child_process';
import { VERSION } from '@earendil-works/pi-coding-agent';

const [major, minor, patch] = process.versions.node.split('.').map(Number);
assert.ok(major > 22 || (major === 22 && (minor > 23 || (minor === 23 && patch >= 2))), 'Node >=22.23.2 required');
assert.equal(process.platform, 'linux', 'Linux PTY fixture required');
assert.equal(VERSION, '1.5.2', 'Pi test dependency must be exactly 1.5.2');
const manifest = JSON.parse(readFileSync(new URL('../package.json', import.meta.url)));
const lock = JSON.parse(readFileSync(new URL('../package-lock.json', import.meta.url)));
for (const [name, version] of Object.entries(manifest.devDependencies)) {
  assert.equal(lock.packages[''].devDependencies[name], version);
  assert.equal(lock.packages['node_modules/' + name].version, version);
  const installed = JSON.parse(readFileSync(new URL('../node_modules/' + name + '/package.json', import.meta.url)));
  assert.equal(installed.version, version, `Run npm ci --ignore-scripts in integrations/pi-observer (${name})`);
}
const python = spawnSync('python3', ['-B', '-c', 'import pty, termios, fcntl, unittest, http.server'], { timeout: 2000 });
assert.equal(python.status, 0, 'Python 3 with Unix PTY modules required');
console.log(`observer preflight: Pi ${VERSION}; Node ${process.versions.node}; Linux/Python PTY available`);
