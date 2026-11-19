import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import fs from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import test from 'node:test';
import {
  FakeArtel,
  LocalJournal,
  TestBridge,
  observationFor,
} from './protocol.mjs';

process.umask(0o077);

const identity = { messageId: 'message-001', generation: 7, sessionId: 'pi-session-test' };
const payload = {
  content: 'fictional owner consultation',
  details: { message_id: 'message-001', marker: 'FICTIONAL_DELIVERY_TEST' },
};

async function fixture(t, { serverHooks, bridgeHooks } = {}) {
  const root = await fs.mkdtemp(path.join(tmpdir(), 'artel-delivery-protocol-'));
  await fs.chmod(root, 0o700);
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const serverPath = path.join(root, 'server.json');
  const journalPath = path.join(root, 'journal.json');
  const artel = await FakeArtel.create(serverPath, {
    sessionId: identity.sessionId,
    generation: identity.generation,
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
  return new Promise(resolve => child.once('exit', (code, signal) => resolve({ code, signal })));
}

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
  const binding = { ...identity, ownerPid: 4101, journalId: 'journal-original' };
  const first = await artel.spend(binding);
  assert(first.right);
  const sameRetry = await artel.spend(binding);
  assert.equal(sameRetry.status, 'unknown');
  assert.equal(sameRetry.right, null);
  for (const incompatibleOwner of [
    { ...binding, ownerPid: 4102 },
    { ...binding, journalId: 'journal-replacement' },
  ]) {
    const retry = await artel.spend(incompatibleOwner);
    assert.equal(retry.status, 'unknown');
    assert.equal(retry.right, null);
  }
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
  const { journalPath, artel, transmissions } = await fixture(t);
  await fs.writeFile(journalPath, '{not-json\n', { mode: 0o600 });
  await assert.rejects(LocalJournal.open(journalPath), error => error.code === 'CORRUPT_STATE');
  assert.equal(artel.snapshot(identity.messageId), undefined);
  assert.equal(transmissions.length, 0);
});

test('unreadable journal fails closed before any transmission', async t => {
  const { journalPath, artel, transmissions } = await fixture(t);
  await fs.chmod(journalPath, 0o000);
  await assert.rejects(LocalJournal.open(journalPath));
  assert.equal(artel.snapshot(identity.messageId), undefined);
  assert.equal(transmissions.length, 0);
});

test('mismatched journal identity and unproven ownership fail closed', async t => {
  const { journalPath, artel, journal, transmissions } = await fixture(t);
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

test('new owner and new journal cannot restore an already spent right', async t => {
  const { root, artel, bridge, transmissions } = await fixture(t);
  await bridge.deliver(identity, payload);
  const replacement = await LocalJournal.create(path.join(root, 'replacement-journal.json'));
  const secondBridge = new TestBridge({
    artel,
    journal: replacement,
    ownerPid: process.pid + 1,
    transmit: async () => { transmissions.push('forbidden'); },
  });
  const result = await secondBridge.deliver(identity, payload);
  assert.equal(result.status, 'unknown');
  assert.equal(result.transmitted, false);
  assert.equal(transmissions.length, 1);
});

test('fresh-process recovery reopens both files and does not infer a right from a lost spend response', async t => {
  const root = await fs.mkdtemp(path.join(tmpdir(), 'artel-delivery-recovery-'));
  await fs.chmod(root, 0o700);
  t.after(() => fs.rm(root, { recursive: true, force: true }));
  const fixturePath = new URL('./recovery-fixture.mjs', import.meta.url).pathname;
  const prepare = spawn(process.execPath, [fixturePath, 'prepare', root], {
    stdio: ['ignore', 'ignore', 'pipe', 'ipc'],
  });
  let prepareErrors = '';
  prepare.stderr.on('data', chunk => { prepareErrors += chunk; });
  t.after(() => { if (prepare.exitCode === null && prepare.signalCode === null) prepare.kill('SIGKILL'); });
  const exited = childExit(prepare);
  await childMessage(prepare, 'spend-committed').catch(error => {
    throw new Error(`${error.message}: ${prepareErrors}`);
  });
  prepare.kill('SIGKILL');
  assert.equal((await exited).signal, 'SIGKILL');

  const recover = spawn(process.execPath, [fixturePath, 'recover', root], {
    stdio: ['ignore', 'ignore', 'pipe', 'ipc'],
  });
  let recoverErrors = '';
  recover.stderr.on('data', chunk => { recoverErrors += chunk; });
  t.after(() => { if (recover.exitCode === null && recover.signalCode === null) recover.kill('SIGKILL'); });
  const recovered = await childMessage(recover, 'recovered').catch(error => {
    throw new Error(`${error.message}: ${recoverErrors}`);
  });
  assert.deepEqual(recovered, { type: 'recovered', status: 'unknown', transmissions: 0 });
  assert.deepEqual(await childExit(recover), { code: 0, signal: null });
  assert.equal(recoverErrors, '');
});

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
