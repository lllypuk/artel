import assert from 'node:assert/strict';
import { execFileSync, spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import test from 'node:test';
import {
  FakeArtel,
  LocalJournal,
  TestBridge,
} from './protocol.mjs';

process.umask(0o077);

const identity = { messageId: 'message-001', generation: 7, sessionId: 'pi-session-test' };
const payload = {
  content: 'fictional owner consultation',
  details: { message_id: 'message-001', marker: 'FICTIONAL_DELIVERY_TEST' },
};

function observationFor(identity, payload) {
  return {
    type: 'before_agent_start',
    messageType: 'custom',
    sessionId: identity.sessionId,
    details: structuredClone(payload.details),
  };
}

async function fixture(t, { serverHooks, bridgeHooks, generation = identity.generation } = {}) {
  const root = await fs.mkdtemp(path.join(tmpdir(), 'artel-delivery-protocol-'));
  await fs.chmod(root, 0o700);
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const serverPath = path.join(root, 'server.json');
  const journalPath = path.join(root, 'journal.json');
  const artel = await FakeArtel.create(serverPath, {
    sessionId: identity.sessionId,
    generation,
  }, serverHooks);
  const journal = await LocalJournal.create(journalPath);
  const transmissions = [];
  const bridge = new TestBridge({
    artel,
    journal,
    transmit: async transmission => { transmissions.push(transmission); },
    hooks: bridgeHooks,
  });
  return { root, serverPath, journalPath, artel, journal, bridge, transmissions };
}

function deferred() {
  let resolve;
  const promise = new Promise(r => { resolve = r; });
  return { promise, resolve };
}

async function childMessage(child, type, timeout = 5000) {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => reject(new Error(`timed out waiting for ${type}`)), timeout);
    const onMessage = message => {
      if (message?.type === type) {
        cleanup();
        resolve(message);
      }
    };
    const onExit = (code, signal) => {
      cleanup();
      reject(new Error(`child exited before ${type}: code=${code} signal=${signal}`));
    };
    const onError = error => { cleanup(); reject(error); };
    const cleanup = () => {
      clearTimeout(timer);
      child.off('message', onMessage);
      child.off('exit', onExit);
      child.off('error', onError);
    };
    child.on('message', onMessage);
    child.on('exit', onExit);
    child.on('error', onError);
  });
}

function childExit(child) {
  return new Promise(resolve => {
    let timedOut = false;
    const timer = setTimeout(() => {
      timedOut = true;
      child.kill('SIGKILL');
    }, 5000);
    const done = outcome => { clearTimeout(timer); resolve(timedOut ? { ...outcome, timedOut } : outcome); };
    child.once('exit', (code, signal) => done({ code, signal }));
    child.once('error', error => done({ error }));
  });
}

test('non-lossless payloads are rejected before admission, intent or spend', async t => {
  const { artel, journal, bridge, serverPath, journalPath, transmissions } = await fixture(t);
  const files = () => Promise.all([serverPath, journalPath].map(file => fs.readFile(file)));
  const before = await files();
  const shared = {};
  const cycle = {};
  cycle.self = cycle;
  for (const value of [
    -0, undefined, NaN, Infinity, 1n, new Date(0), new Map(), [, 'hole'],
    Object.assign([1], { extra: 2 }), { [Symbol('hidden')]: true },
    Object.defineProperty({}, 'hidden', { value: true }),
    { get value() { assert.fail('validation must not invoke getters'); } },
    { toJSON() { assert.fail('validation must not invoke toJSON'); } },
    new Proxy({}, { ownKeys() { assert.fail('validation must not invoke proxy traps'); } }),
    cycle, [shared, shared],
  ]) {
    const invalid = { ...payload, details: { ...payload.details, optional: { nested: value } } };
    await assert.rejects(bridge.deliver(identity, invalid), { code: 'INVALID_PAYLOAD' });
    await assert.rejects(artel.admit(identity, invalid), { code: 'INVALID_PAYLOAD' });
    await assert.rejects(journal.recordIntent(identity, invalid), { code: 'INVALID_PAYLOAD' });
    assert.deepEqual(await files(), before);
  }
  assert.equal(transmissions.length, 0);
  const valid = { ...payload, details: { ...payload.details, nested: [0, 2.5, null, { enabled: false }] } };
  assert.equal((await bridge.deliver(identity, valid)).transmitted, true);
  assert.equal((await bridge.deliver(identity, valid)).transmitted, false);
  assert.deepEqual(journal.entry(identity.messageId).payload, valid);
  assert.deepEqual(transmissions.map(item => item.payload), [valid]);
});

test('signed-zero generation is rejected before admission or intent, while zero delivers', async t => {
  const { artel, journal, bridge, serverPath, journalPath, transmissions } = await fixture(t, { generation: 0 });
  const files = () => Promise.all([serverPath, journalPath].map(file => fs.readFile(file)));
  const before = await files();
  const invalid = { ...identity, generation: -0 };
  await assert.rejects(bridge.deliver(invalid, payload), /generation must be a safe integer other than -0/);
  await assert.rejects(artel.admit(invalid, payload), /generation must be a safe integer other than -0/);
  await assert.rejects(journal.recordIntent(invalid, payload), /generation must be a safe integer other than -0/);
  assert.deepEqual(await files(), before);
  assert.equal(transmissions.length, 0);
  assert.equal((await bridge.deliver({ ...identity, generation: 0 }, payload)).transmitted, true);
});

test('success persists intent, attempt and only custom before_agent_start as the positive receipt', async t => {
  const { artel, journal, bridge, transmissions } = await fixture(t);
  const result = await bridge.deliver(identity, payload);
  assert.deepEqual(result, {
    status: 'unknown',
    attemptId: artel.snapshot(identity.messageId).spend.attemptId,
    transmitted: true,
  });
  assert.equal(transmissions.length, 1);
  assert.equal(journal.entry(identity.messageId).receipt, null);

  const observed = await bridge.recordObservation(identity.messageId, observationFor(identity, payload));
  assert.equal(observed.status, 'observed');
  assert.equal(artel.snapshot(identity.messageId).receipt.type, 'before_agent_start');
  assert.equal(journal.entry(identity.messageId).receipt.messageType, 'custom');

  const replay = await bridge.deliver(identity, payload);
  assert.equal(replay.status, 'observed');
  assert.equal(replay.transmitted, undefined);
  await bridge.recordObservation(identity.messageId, observationFor(identity, payload));
  assert.equal(transmissions.length, 1);
});

test('a final busy check after spend returns unknown without a Pi call or a recovered right', async t => {
  const { artel, journal } = await fixture(t);
  const bridge = new TestBridge({
    artel,
    journal,
    transmit: () => false,
  });
  const result = await bridge.deliver(identity, payload);
  assert.equal(result.status, 'unknown');
  assert.equal(result.transmitted, false);
  assert(artel.snapshot(identity.messageId).spend);
  assert(journal.entry(identity.messageId).attempt);
  assert.equal(journal.entry(identity.messageId).receipt, null);

  const replay = await bridge.deliver(identity, payload);
  assert.equal(replay.status, 'unknown');
  assert.equal(replay.transmitted, false);
});

test('admission and spend retries return state without another Pi right', async t => {
  const { artel } = await fixture(t);
  assert.equal((await artel.admit(identity, payload)).status, 'admitted');
  assert.equal((await artel.admit(identity, payload)).status, 'admitted');
  const binding = { ...identity, ownerPid: process.pid, journalId: 'journal-original' };
  const first = await artel.spend(binding);
  assert(first.right);
  const sameRetry = await artel.spend(binding);
  assert.equal(sameRetry.status, 'unknown');
  assert.equal(sameRetry.right, null);
  await assert.rejects(artel.spend({ ...binding, ownerPid: process.pid + 1 }), { code: 'OWNER_MISMATCH' });
  await assert.rejects(artel.verify({ ...first.right, ownerPid: process.pid + 1 }), { code: 'OWNER_MISMATCH' });
  await assert.rejects(artel.verify({ ...first.right, ownerInstance: 'spoofed' }), { code: 'OWNER_MISMATCH' });
  assert.equal(await artel.verify(first.right), true);
  const retry = await artel.spend({ ...binding, journalId: 'journal-replacement' });
  assert.equal(retry.status, 'unknown');
  assert.equal(retry.right, null);
  assert.equal(artel.snapshot(identity.messageId).spend.attemptId, first.right.attemptId);
});

test('incompatible repeated payload or immutable identity conflicts without transmission', async t => {
  const { bridge, transmissions } = await fixture(t);
  await bridge.deliver(identity, payload);
  await assert.rejects(
    bridge.deliver(identity, { ...payload, content: 'changed body' }),
    error => error.code === 'IDEMPOTENCY_CONFLICT',
  );
  await assert.rejects(
    bridge.deliver({ ...identity, sessionId: 'another-pi-session' }, payload),
    error => error.code === 'IDEMPOTENCY_CONFLICT',
  );
  await assert.rejects(
    bridge.deliver({ ...identity, generation: identity.generation + 1 }, payload),
    error => error.code === 'IDEMPOTENCY_CONFLICT',
  );
  assert.equal(transmissions.length, 1);
});

test('parallel duplicate entry spends and transmits once', async t => {
  const { artel, bridge, transmissions } = await fixture(t);
  const results = await Promise.all(Array.from({ length: 12 }, () => bridge.deliver(identity, payload)));
  assert.equal(transmissions.length, 1);
  assert.equal(results.filter(result => result.transmitted).length, 1);
  assert(results.every(result => result.attemptId === artel.snapshot(identity.messageId).spend.attemptId));
});

test('a failed local attempt write fails closed with zero sends and stays unknown on retry', async t => {
  const { artel, journal, bridge, transmissions } = await fixture(t);
  journal.failNextWrite(); // intent write
  await assert.rejects(bridge.deliver(identity, payload), error => error.code === 'INJECTED_WRITE_FAILURE');
  assert.equal(transmissions.length, 0);
  assert.equal(artel.snapshot(identity.messageId).spend, null);

  await journal.recordIntent(identity, payload);
  journal.failNextWrite(); // attempt write, after the server has spent the right
  await assert.rejects(bridge.deliver(identity, payload), error => error.code === 'ATTEMPT_WRITE_FAILED');
  assert.equal(transmissions.length, 0);
  assert(artel.snapshot(identity.messageId).spend);
  const replay = await bridge.deliver(identity, payload);
  assert.equal(replay.status, 'unknown');
  assert.equal(replay.transmitted, false);
  assert.equal(transmissions.length, 0);
});

test('a failed fake Artel receipt write remains unknown and a later replay only publishes', async t => {
  const { artel, journal, bridge, transmissions } = await fixture(t);
  await bridge.deliver(identity, payload);
  artel.failNextWrite();
  await assert.rejects(
    bridge.recordObservation(identity.messageId, observationFor(identity, payload)),
    error => error.code === 'INJECTED_WRITE_FAILURE',
  );
  assert.equal(artel.snapshot(identity.messageId).receipt, null);
  assert(journal.entry(identity.messageId).receipt);

  const replay = await bridge.deliver(identity, payload);
  assert.equal(replay.status, 'observed');
  assert.equal(transmissions.length, 1);
  assert.equal(artel.snapshot(identity.messageId).receipt.messageType, 'custom');
});

test('a failed local receipt write leaves fake Artel unknown and can be replayed without a send', async t => {
  const { artel, journal, bridge, transmissions } = await fixture(t);
  await bridge.deliver(identity, payload);
  journal.failNextWrite();
  await assert.rejects(
    bridge.recordObservation(identity.messageId, observationFor(identity, payload)),
    error => error.code === 'INJECTED_WRITE_FAILURE',
  );
  assert.equal(artel.snapshot(identity.messageId).receipt, null);
  assert.equal(journal.entry(identity.messageId).receipt, null);

  const replay = await bridge.recordObservation(identity.messageId, observationFor(identity, payload));
  assert.equal(replay.status, 'observed');
  assert.equal(transmissions.length, 1);
});

test('completion and provider ACK are not accepted as bridge receipts', async t => {
  const { artel, bridge, transmissions } = await fixture(t);
  await bridge.deliver(identity, payload);
  for (const invalid of [
    { ...observationFor(identity, payload), type: 'agent_end' },
    { ...observationFor(identity, payload), type: 'provider_ack' },
    { ...observationFor(identity, payload), messageType: 'user' },
    { ...observationFor(identity, payload), sessionId: 'wrong-pi-session' },
    { ...observationFor(identity, payload), details: { message_id: 'wrong-message' } },
  ]) {
    await assert.rejects(
      bridge.recordObservation(identity.messageId, invalid),
      error => error.code === 'INVALID_OBSERVATION',
    );
  }
  assert.equal(artel.snapshot(identity.messageId).receipt, null);
  assert.equal(transmissions.length, 1);
});

test('an incompatible receipt conflicts and does not replace the recorded observation', async t => {
  const { artel, bridge } = await fixture(t);
  await bridge.deliver(identity, payload);
  await bridge.recordObservation(identity.messageId, observationFor(identity, payload));
  await assert.rejects(
    bridge.recordObservation(identity.messageId, {
      ...observationFor(identity, payload),
      details: { message_id: 'different-receipt' },
    }),
    error => error.code === 'INVALID_OBSERVATION' || error.code === 'IDEMPOTENCY_CONFLICT',
  );
  assert.deepEqual(artel.snapshot(identity.messageId).receipt, observationFor(identity, payload));
});

test('corrupt journal fails closed before any transmission', async t => {
  const { journalPath, artel, journal, transmissions } = await fixture(t);
  await fs.writeFile(journalPath, '{not-json\n', { mode: 0o600 });
  await assert.rejects(LocalJournal.open(journalPath, { expectedJournalId: journal.journalId }), error => error.code === 'CORRUPT_STATE');
  assert.equal(artel.snapshot(identity.messageId), undefined);
  assert.equal(transmissions.length, 0);
});

test('unreadable journal fails closed before any transmission', async t => {
  const { journalPath, artel, journal, transmissions } = await fixture(t);
  await fs.chmod(journalPath, 0o000);
  await assert.rejects(LocalJournal.open(journalPath, { expectedJournalId: journal.journalId }));
  assert.equal(artel.snapshot(identity.messageId), undefined);
  assert.equal(transmissions.length, 0);
});

test('mismatched journal identity and unproven ownership fail closed', async t => {
  const { journalPath, artel, journal, transmissions } = await fixture(t);
  await assert.rejects(LocalJournal.open(journalPath), { code: 'JOURNAL_ID_REQUIRED' });
  await assert.rejects(LocalJournal.open(journalPath, { expectedJournalId: '' }), { code: 'JOURNAL_ID_REQUIRED' });
  await assert.rejects(
    LocalJournal.open(journalPath, { expectedJournalId: 'different-journal' }),
    error => error.code === 'JOURNAL_MISMATCH',
  );
  await assert.rejects(
    LocalJournal.open(journalPath, { expectedJournalId: journal.journalId, ownerUid: process.getuid() + 1 }),
    error => error.code === 'UNPROVEN_OWNERSHIP',
  );
  assert.equal(artel.snapshot(identity.messageId), undefined);
  assert.equal(transmissions.length, 0);
});

test('new journal cannot restore an already spent right', async t => {
  const { root, artel, bridge, transmissions } = await fixture(t);
  await bridge.deliver(identity, payload);
  const replacement = await LocalJournal.create(path.join(root, 'replacement-journal.json'));
  const secondBridge = new TestBridge({
    artel,
    journal: replacement,
    transmit: async () => { transmissions.push('forbidden'); },
  });
  const result = await secondBridge.deliver(identity, payload);
  assert.equal(result.status, 'unknown');
  assert.equal(result.transmitted, false);
  assert.equal(transmissions.length, 1);
});

for (const boundary of ['lost spend response', 'replacement before spend']) {
  test(`fresh-process recovery fails closed after ${boundary}`, async t => {
    const root = await fs.mkdtemp(path.join(tmpdir(), 'artel-delivery-recovery-'));
    t.after(() => fs.rm(root, { recursive: true, force: true }));
    const fixturePath = fileURLToPath(new URL('./recovery-fixture.mjs', import.meta.url));
    function start(phase, ...args) {
      const child = spawn(process.execPath, [fixturePath, phase, root, ...args], {
        stdio: ['ignore', 'ignore', 'pipe', 'ipc'],
      });
      const exit = childExit(child);
      let errors = '';
      child.stderr.on('data', chunk => { errors += chunk; });
      t.after(async () => {
        if (child.exitCode === null && child.signalCode === null) child.kill('SIGKILL');
        await exit;
      });
      return { child, exit, message: type => childMessage(child, type).catch(error => {
        throw new Error(`${error.message}: ${errors}`);
      }) };
    }
    const replace = boundary === 'replacement before spend';
    const prepare = start(replace ? 'prepare-before-spend' : 'prepare-after-spend');
    const { journalId } = await prepare.message('prepared');
    assert(journalId);
    prepare.child.kill('SIGKILL');
    assert.deepEqual(await prepare.exit, { code: null, signal: 'SIGKILL' });

    const journalPath = path.join(root, 'journal.json');
    const serverPath = path.join(root, 'server.json');
    const record = Object.values(JSON.parse(await fs.readFile(serverPath)).messages).at(0);
    assert(record);
    assert.equal(Boolean(record.spend), !replace);
    if (replace) {
      await fs.unlink(journalPath);
      const replacement = await LocalJournal.create(journalPath);
      assert.notEqual(replacement.journalId, journalId);
    }
    const files = () => Promise.all([serverPath, journalPath].map(file => fs.readFile(file)));
    const before = await files();
    const recover = start('recover', journalId);
    assert.deepEqual(await recover.message('recovered'), replace
      ? { type: 'recovered', code: 'JOURNAL_MISMATCH', transmissions: 0 }
      : { type: 'recovered', status: 'unknown', transmitted: false, transmissions: 0 });
    assert.deepEqual(await recover.exit, { code: 0, signal: null });
    assert.deepEqual(await files(), before);
  });
}

test('revocation before admission and before spend produce zero calls', async t => {
  const beforeAdmission = await fixture(t);
  await beforeAdmission.artel.revokeGeneration();
  await assert.rejects(
    beforeAdmission.bridge.deliver(identity, payload),
    error => error.code === 'REVOKED_GENERATION',
  );
  assert.equal(beforeAdmission.transmissions.length, 0);

  const beforeSpend = await fixture(t);
  await beforeSpend.artel.admit(identity, payload);
  await beforeSpend.artel.revokeGeneration();
  await assert.rejects(
    beforeSpend.bridge.deliver(identity, payload),
    error => error.code === 'REVOKED_GENERATION',
  );
  assert.equal(beforeSpend.transmissions.length, 0);
});

test('revocation after spend but before final verification produces zero calls and unknown', async t => {
  let artel;
  const fixtureValue = await fixture(t, {
    bridgeHooks: { afterSpendResponse: async () => { await artel.revokeGeneration(); } },
  });
  artel = fixtureValue.artel;
  const result = await fixtureValue.bridge.deliver(identity, payload);
  assert.equal(result.status, 'unknown');
  assert.equal(result.transmitted, false);
  assert.equal(fixtureValue.transmissions.length, 0);
  const replay = await fixtureValue.bridge.deliver(identity, payload);
  assert.equal(replay.status, 'unknown');
  assert.equal(replay.transmitted, false);
  assert.equal(fixtureValue.transmissions.length, 0);
});

test('revocation after transmission leaves historical receipt and active generation unchanged', async t => {
  const { artel, bridge, transmissions } = await fixture(t);
  await bridge.deliver(identity, payload);
  const nextGeneration = await artel.revokeGeneration();
  const result = await bridge.recordObservation(identity.messageId, observationFor(identity, payload));
  assert.equal(result.status, 'observed');
  assert.equal(artel.activeGeneration, nextGeneration);
  assert.equal(artel.snapshot(identity.messageId).identity.generation, identity.generation);
  assert.equal(transmissions.length, 1);
});

test('documented residual window: revocation after final verification cannot cancel the issued call', async t => {
  const entered = deferred();
  const release = deferred();
  let artel;
  const fixtureValue = await fixture(t, {
    bridgeHooks: {
      afterFinalVerification: async (_right, valid) => {
        assert.equal(valid, true);
        entered.resolve();
        await release.promise;
      },
    },
  });
  artel = fixtureValue.artel;
  const delivery = fixtureValue.bridge.deliver(identity, payload);
  await entered.promise;
  await artel.revokeGeneration();
  release.resolve();
  const result = await delivery;
  assert.equal(result.transmitted, true);
  assert.equal(fixtureValue.transmissions.length, 1);
  const receipt = await fixtureValue.bridge.recordObservation(identity.messageId, observationFor(identity, payload));
  assert.equal(receipt.status, 'observed');
});

test('post-commit response failures are recovered by idempotent replay, not another send', async t => {
  let failAdmissionResponse = true;
  let failReceiptResponse = true;
  const { artel, bridge, transmissions } = await fixture(t, {
    serverHooks: {
      beforeResponse: async operation => {
        if (operation === 'admission' && failAdmissionResponse) {
          failAdmissionResponse = false;
          throw new Error('injected lost admission response');
        }
        if (operation === 'receipt' && failReceiptResponse) {
          failReceiptResponse = false;
          throw new Error('injected lost receipt response');
        }
      },
    },
  });
  await assert.rejects(bridge.deliver(identity, payload), /lost admission response/);
  assert(artel.snapshot(identity.messageId));
  await bridge.deliver(identity, payload);
  assert.equal(transmissions.length, 1);
  await assert.rejects(
    bridge.recordObservation(identity.messageId, observationFor(identity, payload)),
    /lost receipt response/,
  );
  assert.equal(artel.snapshot(identity.messageId).receipt.type, 'before_agent_start');
  const replay = await bridge.deliver(identity, payload);
  assert.equal(replay.status, 'observed');
  assert.equal(transmissions.length, 1);
});


test('parallel distinct deliveries and receipts survive reopen; stale journal writer cannot erase them', async t => {
  const { bridge, journal, serverPath, journalPath, transmissions } = await fixture(t);
  const stale = await LocalJournal.open(journalPath, { expectedJournalId: journal.journalId });
  const messages = [identity, { ...identity, messageId: 'message-002' }];
  const payloadFor = id => ({ ...payload, details: { ...payload.details, message_id: id.messageId } });
  await Promise.all(messages.map(id => bridge.deliver(id, payloadFor(id))));
  await Promise.all(messages.map(id => bridge.recordObservation(id.messageId, observationFor(id, payloadFor(id)))));
  const stray = { ...identity, messageId: 'stray' };
  await assert.rejects(stale.recordIntent(stray, payloadFor(stray)), { code: 'JOURNAL_CHANGED' });
  const recovered = await LocalJournal.open(journalPath, { expectedJournalId: journal.journalId });
  const recoveredArtel = await FakeArtel.open(serverPath);
  for (const id of messages) {
    const local = recovered.entry(id.messageId);
    const server = recoveredArtel.snapshot(id.messageId);
    assert.deepEqual(local.attempt, server.spend);
    assert.deepEqual(local.receipt, observationFor(id, payloadFor(id)));
    assert.deepEqual(server.receipt, local.receipt);
  }
  assert.equal(transmissions.length, 2);
});

test('server requires a spend and rejects invalid observations; corrupt journal receipts fail checked reopen', async t => {
  const { artel, bridge, journal, journalPath, transmissions } = await fixture(t);
  await artel.admit(identity, payload);
  const observation = observationFor(identity, payload);
  await assert.rejects(artel.publishReceipt(identity, observation), { code: 'ATTEMPT_MISMATCH' });
  await bridge.deliver(identity, payload);
  const right = transmissions[0];
  for (const invalid of [
    { ...observation, type: 'agent_end' },
    { ...observation, messageType: 'user' },
    { ...observation, sessionId: 'wrong' },
    { ...observation, details: { marker: 'wrong' } },
  ]) {
    await assert.rejects(artel.publishReceipt(right, invalid), { code: 'INVALID_OBSERVATION' });
    const data = JSON.parse(await fs.readFile(journalPath));
    data.messages[identity.messageId].receipt = invalid;
    await fs.writeFile(journalPath, JSON.stringify(data));
    await assert.rejects(LocalJournal.open(journalPath, { expectedJournalId: journal.journalId }), { code: 'CORRUPT_STATE' });
    assert.equal(artel.snapshot(identity.messageId).receipt, null);
  }
  assert.equal(transmissions.length, 1);
});

test('replaying the first observation against a distinct spent message leaves the second unknown', async t => {
  const { artel, bridge, journal, journalPath, serverPath, transmissions } = await fixture(t);
  const second = { ...identity, messageId: 'message-002' };
  for (const invalid of [payload, { ...payload, details: { marker: 'missing-ID' } }]) {
    await assert.rejects(artel.admit(second, invalid), { code: 'INVALID_PAYLOAD' });
    await assert.rejects(journal.recordIntent(second, invalid), { code: 'INVALID_PAYLOAD' });
  }
  assert.equal(artel.snapshot(second.messageId), undefined);
  assert.equal(journal.entry(second.messageId), undefined);

  const secondPayload = { ...payload, details: { ...payload.details, message_id: second.messageId } };
  await bridge.deliver(identity, payload);
  const firstReceipt = observationFor(identity, payload);
  await bridge.recordObservation(identity.messageId, firstReceipt);
  const original = await bridge.deliver(second, secondPayload);
  const server = await FakeArtel.open(serverPath);
  await assert.rejects(server.publishReceipt(transmissions[1], firstReceipt), { code: 'INVALID_OBSERVATION' });

  const local = JSON.parse(await fs.readFile(journalPath));
  local.messages[second.messageId].receipt = firstReceipt;
  await fs.writeFile(journalPath, JSON.stringify(local));
  await assert.rejects(LocalJournal.open(journalPath, { expectedJournalId: journal.journalId }), { code: 'CORRUPT_STATE' });
  local.messages[second.messageId].receipt = null;
  await fs.writeFile(journalPath, JSON.stringify(local));
  const recovered = new TestBridge({ artel: server, journal: await LocalJournal.open(journalPath, { expectedJournalId: journal.journalId }),
    transmit: () => assert.fail('replay cannot transmit') });
  assert.deepEqual(await recovered.deliver(second, secondPayload), { ...original, transmitted: false });
  assert.equal((await FakeArtel.open(serverPath)).snapshot(second.messageId).receipt, null);

  for (const [file, open] of [[serverPath, FakeArtel.open], [journalPath, file => LocalJournal.open(file, { expectedJournalId: journal.journalId })]]) {
    const corrupted = JSON.parse(await fs.readFile(file));
    corrupted.messages[second.messageId].payload = payload;
    await fs.writeFile(file, JSON.stringify(corrupted));
    await assert.rejects(open(file), { code: 'CORRUPT_STATE' });
  }
});

for (const target of ['server', 'journal']) {
  test(`uncertain ${target} rename poisons the writer; checked reopen preserves spend without sending`, async t => {
    let artel, journal;
    const { bridge, serverPath, journalPath, transmissions, ...fixtureValue } = await fixture(t, {
      bridgeHooks: {
        beforeSpend: () => { if (target === 'server') artel.failNextWrite('afterRename'); },
        afterSpendResponse: () => { if (target === 'journal') journal.failNextWrite('afterRename'); },
      },
    });
    ({ artel, journal } = fixtureValue);
    const expectedJournalId = journal.journalId;
    const liveRead = target === 'server' ? () => artel.snapshot(identity.messageId) : () => journal.entry(identity.messageId);
    await assert.rejects(bridge.deliver(identity, payload), error =>
      error.code === 'UNCERTAIN_WRITE' || (error.code === 'ATTEMPT_WRITE_FAILED' && error.cause.code === 'UNCERTAIN_WRITE'));
    assert.throws(liveRead, { code: 'UNUSABLE_STATE' });
    await assert.rejects(bridge.deliver(identity, payload), { code: 'UNUSABLE_STATE' });
    const recoveredArtel = await FakeArtel.open(serverPath);
    const recoveredJournal = await LocalJournal.open(journalPath, { expectedJournalId });
    const recovered = new TestBridge({ artel: recoveredArtel, journal: recoveredJournal,
      transmit: () => assert.fail('uncertain spend cannot be refunded') });
    const spend = recoveredArtel.snapshot(identity.messageId).spend;
    assert(spend);
    assert.deepEqual(recoveredJournal.entry(identity.messageId).attempt, target === 'journal' ? spend : null);
    assert.deepEqual(await recovered.deliver(identity, payload), { status: 'unknown', attemptId: spend.attemptId, transmitted: false });
    assert.equal(transmissions.length, 0);
  });
}

for (const damage of ['missing', 'corrupt', 'replacement']) {
  test(`${damage} journal after attempt commit prevents transmission`, async t => {
    let file;
    const { journalPath, bridge, transmissions, artel } = await fixture(t, {
      bridgeHooks: { afterAttemptCommit: async () => {
        if (damage === 'corrupt') await fs.writeFile(file, '{}');
        else {
          await fs.unlink(file);
          if (damage === 'replacement') await LocalJournal.create(file);
        }
      } },
    });
    file = journalPath;
    await assert.rejects(bridge.deliver(identity, payload), {
      code: { missing: 'ENOENT', corrupt: 'CORRUPT_STATE', replacement: 'JOURNAL_MISMATCH' }[damage],
    });
    assert(artel.snapshot(identity.messageId).spend);
    assert.equal(transmissions.length, 0);
  });
}

test('a FIFO is rejected promptly without a writer', async t => {
  const { journalPath, journal } = await fixture(t);
  await fs.unlink(journalPath);
  execFileSync('mkfifo', ['-m', '600', journalPath]);
  // A blocking open cannot be cancelled by the Node test timeout. Bound the
  // owned subprocess instead; execFileSync waits for it to be reaped.
  execFileSync(process.execPath, ['--input-type=module', '--eval', `
    import assert from 'node:assert/strict';
    import { LocalJournal } from ${JSON.stringify(new URL('./protocol.mjs', import.meta.url).href)};
    const [journalPath, expectedJournalId] = process.argv.slice(1);
    await assert.rejects(LocalJournal.open(journalPath, { expectedJournalId }), { code: 'UNPROVEN_OWNERSHIP' });
  `, journalPath, journal.journalId], { timeout: 2000, killSignal: 'SIGKILL' });
});
