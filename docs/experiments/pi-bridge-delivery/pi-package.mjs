// Resolve only the pinned package's declared public SDK and CLI entry points.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

export function pinnedPi(pkg) {
  assert.equal(process.platform, 'linux');
  assert.equal(process.versions.node, '22.23.2');
  assert(pkg && path.isAbsolute(pkg), 'PI_TEST_PACKAGE must be absolute');
  const dir = fs.realpathSync(pkg);
  const manifest = JSON.parse(fs.readFileSync(path.join(dir, 'package.json'), 'utf8'));
  assert.equal(manifest.name, '@earendil-works/pi-coding-agent');
  assert.equal(manifest.version, '1.5.2');
  function entry(relative) {
    assert.equal(typeof relative, 'string');
    const file = fs.realpathSync(path.join(dir, relative));
    assert(file.startsWith(`${dir}/`) && fs.statSync(file).isFile(), 'public Pi entry must be inside its package');
    return file;
  }
  return { sdk: pathToFileURL(entry(manifest.exports['.'].import)), cli: entry(manifest.bin.pi) };
}
