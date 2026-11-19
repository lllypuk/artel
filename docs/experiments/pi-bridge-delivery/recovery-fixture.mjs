// Fresh-process fixture for protocol.test.mjs; all paths and values are temporary/fictional.
import assert from 'node:assert/strict';
import path from 'node:path';
import { FakeArtel, LocalJournal, TestBridge } from './protocol.mjs';

const [phase, root] = process.argv.slice(2);
assert(phase === 'prepare' || phase === 'recover');
const identity = { messageId: 'message-process-recovery', generation: 3, sessionId: 'pi-session-process' };
const payload = { content: 'fictional recovery message', details: { marker: 'RECOVERY_FIXTURE' } };
const serverPath = path.join(root, 'server.json');
const journalPath = path.join(root, 'journal.json');

if (phase === 'prepare') {
  const artel = await FakeArtel.create(serverPath, {
    sessionId: identity.sessionId,
    generation: identity.generation,
  }, {
    afterCommit: async operation => {
      if (operation === 'spend') {
        process.send({ type: 'spend-committed' });
        // The parent kills only this child at this explicit post-commit/pre-response barrier.
        await new Promise(() => {});
      }
    },
  });
  const journal = await LocalJournal.create(journalPath);
  const bridge = new TestBridge({ artel, journal, transmit: async () => {
    throw new Error('a held spend response must never transmit');
  } });
  await bridge.deliver(identity, payload);
} else {
  // No object or memory from prepare survives: reopen both durable files by path.
  const artel = await FakeArtel.open(serverPath);
  const journal = await LocalJournal.open(journalPath);
  let transmissions = 0;
  const bridge = new TestBridge({ artel, journal, transmit: async () => { transmissions += 1; } });
  const result = await bridge.deliver(identity, payload);
  assert.equal(result.status, 'unknown');
  assert.equal(result.transmitted, false);
  assert.equal(transmissions, 0);
  process.send({ type: 'recovered', status: result.status, transmissions });
}
