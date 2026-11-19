// Test-only Artel/bridge fixture controlled over a private Unix socket. No product code.
import assert from 'node:assert/strict';
import fs from 'node:fs';
import net from 'node:net';
import path from 'node:path';
import { pathToFileURL } from 'node:url';

const root = process.env.PROBE_ROOT;
const controlPath = process.env.ARTEL_PTY_CONTROL;
const eventPath = process.env.ARTEL_PTY_BRIDGE_EVENTS;
const protocolPath = process.env.ARTEL_PTY_PROTOCOL;
for (const value of [controlPath, eventPath, protocolPath, process.env.ARTEL_PTY_SERVER, process.env.ARTEL_PTY_JOURNAL]) {
  assert(value?.startsWith('/'), 'all bridge fixture paths must be absolute');
}
assert(root?.startsWith('/tmp/artel-pi-delivery-'));
for (const value of [controlPath, eventPath, process.env.ARTEL_PTY_SERVER, process.env.ARTEL_PTY_JOURNAL]) {
  const relative = path.relative(root, value);
  assert(relative && !relative.startsWith('..') && !path.isAbsolute(relative));
}
const { FakeArtel, LocalJournal, TestBridge } = await import(pathToFileURL(protocolPath));
const activeAttempts = new Map();
// The separately loaded provider oracle can look up attempts for correlation.
globalThis[Symbol.for('artel.pi-delivery-02.pty-bridge.lookup-attempt')] = messageId => activeAttempts.get(messageId);

let artel;
let journal;
let bridge;
let sessionContext;
let controlServer;
let piSends = 0;
let currentDelivery;
const releases = new Map();
const sockets = new Set();

function emit(row, socket) {
  const message = { ...row, processPid: process.pid };
  fs.appendFileSync(eventPath, `${JSON.stringify(message)}\n`, { mode: 0o600 });
  if (socket && !socket.destroyed) socket.write(`${JSON.stringify(message)}\n`);
}

async function barrier(name, socket) {
  if (!currentDelivery?.pauses.includes(name)) return;
  emit({ type: 'barrier', name, commandId: currentDelivery.id }, socket);
  await new Promise(resolve => releases.set(name, resolve));
  emit({ type: 'barrier-released', name, commandId: currentDelivery.id }, socket);
}

function transmitSynchronouslyIfIdle(transmission) {
  assert(sessionContext, 'Pi session context is required');
  // This is deliberately the final synchronous check after every admission,
  // spend, journal and verification await in TestBridge.deliver(). Do not move
  // it earlier or insert an await between this check and sendMessage().
  if (!sessionContext.isIdle()) return false;
  assert.equal(transmission.ownerPid, process.pid, 'only the original owner may transmit');
  activeAttempts.set(transmission.messageId, transmission);
  piSends += 1;
  emit({
    type: 'pi-send',
    messageId: transmission.messageId,
    generation: transmission.generation,
    sessionId: transmission.sessionId,
    attemptId: transmission.attemptId,
    piSends,
  });
  sessionContextPi.sendMessage({
    customType: 'artel-delivery-pty-fixture',
    content: transmission.payload.content,
    display: true,
    details: transmission.payload.details,
  }, { triggerTurn: true });
  return true;
}

let sessionContextPi;

async function setupSession(pi, ctx) {
  sessionContext = ctx;
  sessionContextPi = pi;
  const mode = process.env.ARTEL_PTY_STATE_MODE;
  assert(['bootstrap', 'recover'].includes(mode));
  artel = mode === 'recover'
    ? await FakeArtel.open(process.env.ARTEL_PTY_SERVER)
    : await FakeArtel.create(process.env.ARTEL_PTY_SERVER, {
      sessionId: process.env.ARTEL_PTY_SESSION_ID,
      generation: Number(process.env.ARTEL_PTY_GENERATION),
    });
  assert.equal(artel.sessionId, ctx.sessionManager.getSessionId());
  const expectedJournalId = process.env.ARTEL_PTY_JOURNAL_ID;
  if (mode === 'recover') assert(expectedJournalId, 'recovery requires the bootstrap journal ID');
  journal = mode === 'recover'
    ? await LocalJournal.open(process.env.ARTEL_PTY_JOURNAL, { expectedJournalId })
    : await LocalJournal.create(process.env.ARTEL_PTY_JOURNAL);
  bridge = new TestBridge({
    artel,
    journal,
    transmit: transmitSynchronouslyIfIdle,
    hooks: {
      beforeSpend: () => barrier('bridge:beforeSpend', controlSocket),
      afterSpendResponse: () => barrier('bridge:afterSpendResponse', controlSocket),
      afterAttemptCommit: () => barrier('bridge:afterAttemptCommit', controlSocket),
      afterFinalVerification: () => barrier('bridge:afterFinalVerification', controlSocket),
      afterReceiptCommit: () => barrier('bridge:afterReceiptCommit', controlSocket),
    },
  });
}

// The fixture expects one active control connection and routes barriers to
// the most recently accepted socket. Releases can arrive during delivery.
let controlSocket;

async function writeObservation(messageId, observation) {
  const socket = controlSocket;
  emit({ type: 'receipt-write-start', messageId });
  try {
    await barrier('receipt:beforeWrite', socket);
    const result = await bridge.recordObservation(messageId, observation);
    emit({ type: 'receipt-result', messageId, result }, socket);
  } catch (error) {
    emit({ type: 'receipt-error', messageId, code: error.code, message: error.message }, socket);
  }
}

function state(ctx) {
  const messageId = currentDelivery?.identity.messageId;
  const server = messageId ? artel.snapshot(messageId) : undefined;
  const local = messageId ? journal.entry(messageId) : undefined;
  return {
    sessionId: ctx.sessionManager.getSessionId(),
    idle: ctx.isIdle(),
    pending: ctx.hasPendingMessages(),
    editorText: ctx.ui.getEditorText(),
    piSends,
    localIntent: Boolean(local),
    localAttemptId: local?.attempt?.attemptId ?? null,
    localReceiptType: local?.receipt?.type ?? null,
    serverStatus: server ? (server.receipt ? 'observed' : server.spend ? 'unknown' : 'admitted') : 'absent',
    serverAttemptId: server?.spend?.attemptId ?? null,
    serverReceiptType: server?.receipt?.type ?? null,
  };
}

async function deliver(command, socket) {
  assert(sessionContext && bridge && journal);
  currentDelivery = {
    id: command.id,
    identity: command.identity,
    payload: command.payload,
    pauses: command.pauses ?? [],
  };
  if (!sessionContext.isIdle()) {
    // A manual-first delivery never asks fake Artel for admission or spend.
    // The intent remains only in the local journal; no settled handler sends it.
    await journal.recordIntent(command.identity, command.payload);
    return { status: 'pending-local', transmitted: false, piSends };
  }
  return bridge.deliver(command.identity, command.payload);
}

async function handle(command, socket, pi, ctx) {
  if (command.type === 'state') return state(ctx);
  if (command.type === 'deliver') return deliver(command, socket);
  if (command.type === 'release') {
    const release = releases.get(command.name);
    assert(release, `no owned barrier named ${command.name}`);
    releases.delete(command.name);
    release();
    return { released: command.name };
  }
  if (command.type === 'quit') {
    setImmediate(() => ctx.shutdown());
    return { quitting: true };
  }
  throw new Error(`unknown fixture command ${command.type}`);
}

function startControl(pi, ctx) {
  controlServer = net.createServer(socket => {
    sockets.add(socket);
    controlSocket = socket;
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
        } catch (error) {
          emit({ type: 'control-error', message: error.message }, socket);
          continue;
        }
        void handle(command, socket, pi, ctx).then(result => {
          emit({ type: 'response', id: command.id, ok: true, result }, socket);
        }, error => {
          emit({
            type: 'response', id: command.id, ok: false,
            error: { code: error.code, message: error.message },
          }, socket);
        });
      }
    });
    socket.on('close', () => {
      sockets.delete(socket);
      if (controlSocket === socket) controlSocket = undefined;
    });
  });
  controlServer.on('error', error => {
    emit({ type: 'control-error', message: error.message });
    process.exitCode = 1;
    setImmediate(() => ctx.shutdown());
  });
  // AF_UNIX addresses are short, regardless of the caller's private root path.
  const address = path.relative(process.cwd(), controlPath);
  assert.equal(address, '../control.sock');
  controlServer.listen(address, () => {
    fs.chmodSync(controlPath, 0o600);
    emit({ type: 'control-ready', controlPath });
  });
}

function stopControl() {
  for (const socket of sockets) socket.destroy();
  sockets.clear();
  controlSocket = undefined;
  if (controlServer) controlServer.close();
  controlServer = undefined;
  fs.rmSync(controlPath, { force: true });
}

export default function bridgeFixture(pi) {
  pi.on('session_start', async (_event, ctx) => {
    try {
      await setupSession(pi, ctx);
      startControl(pi, ctx);
      emit({ type: 'bridge-ready', sessionId: ctx.sessionManager.getSessionId(), journalId: journal.journalId });
    } catch (error) {
      emit({ type: 'setup-error', code: error.code, message: error.message });
      process.exitCode = 1;
      setImmediate(() => ctx.shutdown());
    }
  });

  pi.on('before_agent_start', (event, ctx) => {
    if (event.messageType !== 'custom') return;
    const messageId = event.message.details?.message_id;
    const attempt = activeAttempts.get(messageId);
    if (!attempt) {
      emit({ type: 'bridge-error', message: 'custom start has no active spent attempt' });
      return;
    }
    const observation = {
      type: 'before_agent_start',
      messageType: 'custom',
      sessionId: ctx.sessionManager.getSessionId(),
      details: structuredClone(event.message.details),
    };
    // Do not await persistence in Pi's lifecycle hook. Only this exact custom
    // observation may become a receipt; provider and completion events cannot.
    queueMicrotask(() => { void writeObservation(messageId, observation); });
  });

  pi.on('session_shutdown', event => {
    stopControl();
    emit({ type: 'session-shutdown', reason: event.reason, piSends });
  });
}
