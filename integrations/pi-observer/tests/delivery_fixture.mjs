// Test-only delivery controller. Drives the shipping writer's test seam over a
// private control socket; never ships as observer behavior.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';

const controlPath = process.env.ARTEL_DELIVERY_CONTROL;
const journalPath = process.env.ARTEL_DELIVERY_JOURNAL;
const root = process.env.ARTEL_DELIVERY_ROOT;
assert(controlPath?.startsWith('/tmp/artel-delivery-') || root?.startsWith('/tmp/artel-delivery-'), 'private temp root required');
assert(journalPath?.startsWith('/'), 'journal path required');

const TEST_HOOK = Symbol.for('artel.delivery.test');
const hooks = { holdReadyMs: 0, sends: 0 };
globalThis[TEST_HOOK] = hooks;

let server;
const sockets = new Set();

function handle(command) {
  if (command.type === 'hold-ready') {
    assert(Number.isInteger(command.ms) && command.ms >= 0 && command.ms <= 30000, 'invalid hold');
    hooks.holdReadyMs = command.ms;
    return { held: command.ms };
  }
  if (command.type === 'state') {
    return { holdReadyMs: hooks.holdReadyMs, sends: hooks.sends };
  }
  if (command.type === 'second-writer') {
    // The stable lock object must reject a concurrent writer while the shipping
    // writer owns it, and only the kernel can release that ownership.
    return new Promise(resolve => {
      const probe = net.createServer();
      probe.once('error', error => resolve({ locked: error.code === 'EADDRINUSE', code: error.code }));
      probe.once('listening', () => {
        probe.close(() => resolve({ locked: false }));
      });
      probe.listen(path.join(path.dirname(journalPath), 'writer.lock'));
    });
  }
  throw new Error(`unknown control command ${command.type}`);
}

export default function deliveryFixture() {
  // Nothing to subscribe to: the fixture only owns the control socket and hooks.
}

// The control listener starts at import time so early commands never race the
// writer pickup; Pi loads extensions before session events fire.
server = net.createServer(socket => {
  sockets.add(socket);
  socket.setNoDelay(true);
  let buffered = '';
  socket.on('data', chunk => {
    buffered += chunk;
    while (buffered.includes('\n')) {
      const index = buffered.indexOf('\n');
      const line = buffered.slice(0, index);
      buffered = buffered.slice(index + 1);
      if (!line) continue;
      let command;
      try {
        command = JSON.parse(line);
      } catch {
        socket.write(`${JSON.stringify({ ok: false, error: 'invalid-json' })}\n`);
        continue;
      }
      Promise.resolve(handle(command)).then(
        result => socket.write(`${JSON.stringify({ ok: true, result })}\n`),
        error => socket.write(`${JSON.stringify({ ok: false, error: String(error?.message ?? error) })}\n`),
      );
    }
  });
  socket.on('close', () => sockets.delete(socket));
});
server.listen(controlPath, () => fs.chmodSync(controlPath, 0o600));

process.on('exit', () => {
  for (const socket of sockets) socket.destroy();
  try { server?.close(); } catch { /* already closed */ }
  fs.rmSync(controlPath, { force: true });
});
