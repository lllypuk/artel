// Bundle only Artel code. Pi's public API is supplied by the explicitly loading host.
import { build } from 'esbuild';
import { mkdir, writeFile, copyFile } from 'node:fs/promises';
const outdir = new URL('./dist/', import.meta.url);
await mkdir(outdir, { recursive: true });
await build({
  entryPoints: [new URL('./src/index.ts', import.meta.url).pathname],
  outfile: new URL('index.mjs', outdir).pathname,
  bundle: true, platform: 'node', format: 'esm', target: 'node22.23.2',
  external: ['@earendil-works/pi-coding-agent'], legalComments: 'none',
});
await writeFile(new URL('package.json', outdir), JSON.stringify({
  name: '@artel/pi-observer', version: '2.0.0', private: true, type: 'module',
  pi: { extensions: ['./index.mjs'] }, engines: { node: '>=22.23.2' },
}, null, 2) + '\n');
await copyFile(new URL('./README.md', import.meta.url), new URL('README.md', outdir));
