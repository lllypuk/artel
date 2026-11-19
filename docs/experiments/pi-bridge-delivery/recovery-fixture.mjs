// Fresh-process fixture for protocol.test.mjs; all paths and values are temporary/fictional.
import assert from 'node:assert/strict';
import path from 'node:path';
import { FakeArtel, LocalJournal, TestBridge } from './protocol.mjs';

const [phase, root, expectedJournalId] = process.argv.slice(2);
assert(['prepare-before-spend', 'prepare-after-spend', 'recover'].includes(phase));
const identity = { messageId: 'message-process-recovery', generation: 3, sessionId: 'pi-session-process' };
const payload = { content: 'fictional recovery message', details: { message_id: identity.messageId, marker: 'RECOVERY_FIXTURE' } };
const serverPath = path.join(root, 'server.json');
const journalPath = path.join(root, 'journal.json');

if (phase.startsWith('prepare-')) {
  const journal = await LocalJournal.create(journalPath);
  const hold = async () => {
    process.on('message', () => {}); // Keep IPC referenced until the parent kills us.
    process.send({ type: 'prepared', journalId: journal.journalId });
    // The parent kills only this child at the selected explicit barrier.
    await new Promise(() => {});
  };
  const artel = await FakeArtel.create(serverPath, {
    sessionId: identity.sessionId,
    generation: identity.generation,
  }, {
    afterCommit: async operation => {
      if (operation === 'spend' && phase === 'prepare-after-spend') await hold();
    },
  });
  const bridge = new TestBridge({ artel, journal,
    hooks: phase === 'prepare-before-spend' ? { beforeSpend: hold } : {},
    transmit: () => assert.fail('the prepare process must never transmit'),
  });
  await bridge.deliver(identity, payload);
} else {
  // The parent passes the ID reported by prepare, not an ID read from the recovery path.
  const artel = await FakeArtel.open(serverPath);
  let transmissions = 0;
  try {
    const journal = await LocalJournal.open(journalPath, { expectedJournalId });
    const bridge = new TestBridge({ artel, journal, transmit: () => { transmissions += 1; } });
    const result = await bridge.deliver(identity, payload);
    process.send({ type: 'recovered', status: result.status, transmitted: result.transmitted, transmissions });
  } catch (error) {
    if (error.code !== 'JOURNAL_MISMATCH') throw error;
    process.send({ type: 'recovered', code: error.code, transmissions });
  }
}
