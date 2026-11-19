// Test-only delivery protocol fixture. This is not a production Artel API or Pi bridge.
import assert from 'node:assert/strict';
import { constants } from 'node:fs';
import fs from 'node:fs/promises';
import path from 'node:path';
import { createHash, randomUUID } from 'node:crypto';
import net from 'node:net';
import { setTimeout as delay } from 'node:timers/promises';
import { isDeepStrictEqual, types } from 'node:util';

export class ProtocolError extends Error {
  constructor(code, message, options) {
    super(message, options);
    this.name = 'ProtocolError';
    this.code = code;
  }
}

function checkedIdentity(identity) {
  assert.equal(typeof identity?.messageId, 'string');
  assert(Number.isSafeInteger(identity?.generation) && !Object.is(identity.generation, -0),
    'generation must be a safe integer other than -0');
  assert(!Object.hasOwn(Object.prototype, identity.messageId));
  assert.equal(typeof identity?.sessionId, 'string');
  return {
    messageId: identity.messageId,
    generation: identity.generation,
    sessionId: identity.sessionId,
  };
}

function checkedPayload(payload, messageId) {
  // Inspect the original value, not a structured clone (which can already drop
  // properties). Accept only a JSON tree; do not invoke accessors or toJSON.
  const invalid = () => { throw new ProtocolError('INVALID_PAYLOAD', 'payload must be lossless JSON'); };
  const seen = new Set();
  function check(value) {
    if (value === null || typeof value === 'string' || typeof value === 'boolean') return;
    if (typeof value === 'number' && Number.isFinite(value) && !Object.is(value, -0)) return;
    if (typeof value !== 'object' || types.isProxy(value) || seen.has(value)) invalid();
    seen.add(value);
    const array = Array.isArray(value);
    if (Object.getPrototypeOf(value) !== (array ? Array.prototype : Object.prototype)) invalid();
    const entries = Reflect.ownKeys(value);
    if (array && entries.length !== value.length + 1) invalid();
    for (const key of entries) {
      if (array && key === 'length') continue;
      if (typeof key !== 'string' || (array && (!/^(0|[1-9]\d*)$/.test(key) || Number(key) >= value.length))) invalid();
      const descriptor = Object.getOwnPropertyDescriptor(value, key);
      if (!descriptor.enumerable || !Object.hasOwn(descriptor, 'value')) invalid();
      check(descriptor.value);
    }
  }
  check(payload);
  if (!payload || typeof payload !== 'object' || Array.isArray(payload)
    || !Object.hasOwn(payload, 'content') || typeof payload.content !== 'string'
    || !Object.hasOwn(payload, 'details') || !payload.details || typeof payload.details !== 'object'
    || Array.isArray(payload.details)) invalid();
  if (!Object.hasOwn(payload.details, 'message_id') || payload.details.message_id !== messageId) {
    throw new ProtocolError('INVALID_PAYLOAD', 'payload details.message_id must match immutable message ID');
  }
  const persisted = JSON.parse(JSON.stringify(payload));
  if (!isDeepStrictEqual(payload, persisted)) invalid();
  return persisted;
}

function clone(value) {
  return structuredClone(value);
}

async function checkedDirectory(file) {
  const stat = await fs.lstat(path.dirname(file));
  if (!stat.isDirectory() || stat.uid !== process.getuid() || (stat.mode & 0o777) !== 0o700) {
    throw new ProtocolError('UNPROVEN_OWNERSHIP', `private owned directory required: ${path.dirname(file)}`);
  }
}

async function readCheckedJson(file, ownerUid) {
  await checkedDirectory(file);
  let handle;
  try {
    handle = await fs.open(file, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    const stat = await handle.stat();
    if (!stat.isFile() || stat.uid !== ownerUid || (stat.mode & 0o777) !== 0o600) {
      throw new ProtocolError('UNPROVEN_OWNERSHIP', `private owned regular file required: ${file}`);
    }
    const text = await handle.readFile('utf8');
    try {
      return JSON.parse(text);
    } catch (cause) {
      throw new ProtocolError('CORRUPT_STATE', `invalid JSON state: ${file}`, { cause });
    }
  } finally {
    await handle?.close();
  }
}

async function fsyncDirectory(directory) {
  const handle = await fs.open(directory, constants.O_RDONLY | constants.O_DIRECTORY);
  try {
    await handle.sync();
  } finally {
    await handle.close();
  }
}

// Linux abstract sockets are exclusive across processes and are released by the
// kernel on process death; no stale on-disk lock or PID reclamation is needed.
async function withDocumentLock(file, operation) {
  await checkedDirectory(file);
  const directory = await fs.realpath(path.dirname(file));
  const key = createHash('sha256').update(path.join(directory, path.basename(file))).digest('hex');
  const listener = net.createServer(socket => socket.destroy());
  const deadline = performance.now() + 4000;
  for (;;) {
    try {
      await new Promise((resolve, reject) => {
        listener.once('error', reject);
        listener.listen(`\0artel-delivery-${key}`, () => {
          listener.removeListener('error', reject);
          resolve();
        });
      });
      break;
    } catch (error) {
      listener.removeAllListeners('listening');
      if (error.code !== 'EADDRINUSE' || performance.now() >= deadline) throw error;
      await delay(5);
    }
  }
  try {
    return await operation();
  } finally {
    await new Promise((resolve, reject) => listener.close(error => error ? reject(error) : resolve()));
  }
}

function attemptBinding(right) {
  return { attemptId: right.attemptId, ownerPid: right.ownerPid, ownerInstance: right.ownerInstance, journalId: right.journalId };
}

const processOwner = { ownerPid: process.pid, ownerInstance: randomUUID() };

function requireCurrentOwner(binding) {
  if (binding.ownerPid !== processOwner.ownerPid || binding.ownerInstance !== processOwner.ownerInstance) {
    throw new ProtocolError('OWNER_MISMATCH', 'spend and verification require the current process owner');
  }
}

function validateDocument(data, kind, idKey, expectedId) {
  const server = kind === 'fake-artel-state';
  const object = value => value && typeof value === 'object' && !Array.isArray(value);
  try {
    assert(data?.kind === kind && typeof data[idKey] === 'string' && object(data.messages));
    if (server) {
      assert(Number.isSafeInteger(data.activeGeneration));
      assert.equal(typeof data.sessionId, 'string');
    }
    for (const [id, record] of Object.entries(data.messages)) {
      assert.deepEqual(record.identity, checkedIdentity(record.identity));
      assert.equal(record.identity.messageId, id);
      checkedPayload(record.payload, id);
      const attempt = record[server ? 'spend' : 'attempt'];
      if (attempt !== null) {
        assert(object(attempt) && Number.isSafeInteger(attempt.ownerPid) && attempt.ownerPid > 0);
        for (const key of ['attemptId', 'ownerInstance', 'journalId']) assert.equal(typeof attempt[key], 'string');
        if (!server) assert.equal(attempt.journalId, data.journalId);
      }
      assert(record.receipt === null || (object(record.receipt) && attempt));
      if (record.receipt) assert.deepEqual(checkedObservation(record.receipt, record), record.receipt);
    }
  } catch (cause) {
    throw new ProtocolError('CORRUPT_STATE', `invalid ${kind} document`, { cause });
  }
  if (expectedId !== undefined && data[idKey] !== expectedId) {
    throw new ProtocolError('JOURNAL_MISMATCH', `unexpected ${idKey}`);
  }
}

class CheckedJsonDocument {
  #file;
  #kind;
  #idKey;
  #ownerUid;
  #failure;
  #poisoned = false;
  #tail = Promise.resolve();
  #data;

  constructor(file, kind, idKey, data, ownerUid) {
    this.#file = file;
    this.#kind = kind;
    this.#idKey = idKey;
    this.#ownerUid = ownerUid;
    this.#data = data;
  }

  get data() {
    if (this.#poisoned) throw new ProtocolError('UNUSABLE_STATE', 'state requires checked reopen');
    return this.#data;
  }

  static async create(file, kind, idKey, data, ownerUid = process.getuid()) {
    assert.equal(process.platform, 'linux');
    validateDocument(data, kind, idKey);
    return withDocumentLock(file, async () => {
      const handle = await fs.open(file, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
      try {
        await handle.writeFile(`${JSON.stringify(data)}\n`, 'utf8');
        await handle.sync();
      } finally {
        await handle.close();
      }
      await fsyncDirectory(path.dirname(file));
      return new CheckedJsonDocument(file, kind, idKey, data, ownerUid);
    });
  }

  static async open(file, kind, idKey, expectedId, ownerUid = process.getuid()) {
    assert.equal(process.platform, 'linux');
    return withDocumentLock(file, async () => {
      const data = await readCheckedJson(file, ownerUid);
      validateDocument(data, kind, idKey, expectedId);
      // Resolve a previous rename whose directory sync did not return success.
      await fsyncDirectory(path.dirname(file));
      return new CheckedJsonDocument(file, kind, idKey, data, ownerUid);
    });
  }

  failNextWrite(phase = 'beforeWrite') {
    assert(['beforeWrite', 'afterRename'].includes(phase));
    this.#failure = phase;
  }

  async serial(operation, { reload = false } = {}) {
    const run = async () => {
      const previous = this.data;
      return withDocumentLock(this.#file, async () => {
        try {
          const current = await readCheckedJson(this.#file, this.#ownerUid);
          validateDocument(current, this.#kind, this.#idKey, previous[this.#idKey]);
          if (!reload && !isDeepStrictEqual(current, previous)) {
            throw new ProtocolError('JOURNAL_CHANGED', 'journal changed outside this live writer');
          }
          await fsyncDirectory(path.dirname(this.#file));
          this.#data = current;
        } catch (error) {
          this.#poisoned = true;
          throw error;
        }
        return operation();
      });
    };
    const result = this.#tail.then(run);
    this.#tail = result.catch(() => {});
    return result;
  }

  // Only called inside serial(): checked reload and rename share the same lock.
  async replace(next) {
    const failure = this.#failure;
    this.#failure = undefined;
    if (failure === 'beforeWrite') {
      throw new ProtocolError('INJECTED_WRITE_FAILURE', `injected write failure: ${this.#file}`);
    }
    validateDocument(next, this.#kind, this.#idKey, this.data[this.#idKey]);
    const temporary = path.join(
      path.dirname(this.#file),
      `.${path.basename(this.#file)}.${process.pid}.${randomUUID()}.tmp`,
    );
    let handle;
    let renamed = false;
    try {
      handle = await fs.open(temporary, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
      await handle.writeFile(`${JSON.stringify(next)}\n`, 'utf8');
      await handle.sync();
      await handle.close();
      handle = undefined;
      await fs.rename(temporary, this.#file);
      renamed = true;
      if (failure === 'afterRename') throw new Error('injected failure after rename and before directory sync');
      await fsyncDirectory(path.dirname(this.#file));
      this.#data = next;
    } catch (cause) {
      await handle?.close().catch(() => {});
      await fs.unlink(temporary).catch(() => {});
      if (!renamed) throw cause;
      this.#poisoned = true;
      throw new ProtocolError('UNCERTAIN_WRITE', 'rename visible but directory sync not confirmed', { cause });
    }
  }
}

function publicState(record) {
  return {
    status: record.receipt ? 'observed' : record.spend ? 'unknown' : 'admitted',
    attemptId: record.spend?.attemptId,
  };
}

export class FakeArtel {
  #document;
  #hooks;

  constructor(document, hooks = {}) {
    this.#document = document;
    this.#hooks = hooks;
  }

  static async create(file, { sessionId, generation = 1 }, hooks = {}) {
    assert.equal(typeof sessionId, 'string');
    const data = {
      kind: 'fake-artel-state',
      serverId: randomUUID(),
      activeGeneration: generation,
      sessionId,
      messages: {},
    };
    return new FakeArtel(await CheckedJsonDocument.create(file, data.kind, 'serverId', data), hooks);
  }

  static async open(file, hooks = {}) {
    return new FakeArtel(await CheckedJsonDocument.open(file, 'fake-artel-state', 'serverId'), hooks);
  }

  get activeGeneration() {
    return this.#document.data.activeGeneration;
  }

  get sessionId() {
    return this.#document.data.sessionId;
  }

  failNextWrite(phase) {
    this.#document.failNextWrite(phase);
  }

  snapshot(messageId) {
    const record = this.#document.data.messages[messageId];
    return record ? clone(record) : undefined;
  }

  async #serial(operation) {
    return this.#document.serial(operation, { reload: true });
  }

  async #response(operation, record) {
    await this.#hooks.afterCommit?.(operation, clone(record));
    const response = publicState(record);
    await this.#hooks.beforeResponse?.(operation, clone(response));
    return response;
  }

  async admit(identityInput, payloadInput) {
    const identity = checkedIdentity(identityInput);
    const payload = checkedPayload(payloadInput, identity.messageId);
    return this.#serial(async () => {
      const data = this.#document.data;
      const existing = data.messages[identity.messageId];
      if (existing) {
        if (!isDeepStrictEqual(existing.identity, identity) || !isDeepStrictEqual(existing.payload, payload)) {
          throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'message identity or payload is incompatible');
        }
        return this.#response('admission-replay', existing);
      }
      if (identity.sessionId !== data.sessionId || identity.generation !== data.activeGeneration) {
        throw new ProtocolError('REVOKED_GENERATION', 'message does not belong to the active test session generation');
      }
      const record = { identity, payload, spend: null, receipt: null };
      await this.#hooks.beforeCommit?.('admission', clone(record));
      await this.#document.replace({ ...data, messages: { ...data.messages, [identity.messageId]: record } });
      return this.#response('admission', record);
    });
  }

  async spend(bindingInput) {
    const binding = checkedIdentity(bindingInput);
    assert.equal(typeof bindingInput.ownerPid, 'number');
    assert.equal(typeof bindingInput.journalId, 'string');
    if (bindingInput.ownerPid !== process.pid) {
      throw new ProtocolError('OWNER_MISMATCH', 'spend requires the current process PID');
    }
    const journalId = bindingInput.journalId;
    return this.#serial(async () => {
      const data = this.#document.data;
      const record = data.messages[binding.messageId];
      if (!record || !isDeepStrictEqual(record.identity, binding)) {
        throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'spend does not match the admitted immutable identity');
      }
      if (record.spend) {
        // A replay is state only. A new owner or journal never recovers the spent right.
        return { ...(await this.#response('spend-replay', record)), right: null };
      }
      if (record.identity.sessionId !== data.sessionId || record.identity.generation !== data.activeGeneration) {
        throw new ProtocolError('REVOKED_GENERATION', 'generation was revoked before spend');
      }
      const nextRecord = {
        ...record,
        spend: { attemptId: randomUUID(), ...processOwner, journalId },
      };
      await this.#hooks.beforeCommit?.('spend', clone(nextRecord));
      await this.#document.replace({
        ...data,
        messages: { ...data.messages, [binding.messageId]: nextRecord },
      });
      const state = await this.#response('spend', nextRecord);
      return {
        ...state,
        right: {
          ...binding,
          ...nextRecord.spend,
        },
      };
    });
  }

  async verify(right) {
    requireCurrentOwner(right);
    return this.#serial(async () => {
      const data = this.#document.data;
      const record = data.messages[right.messageId];
      return Boolean(
        record
        && record.identity.generation === data.activeGeneration
        && record.identity.sessionId === data.sessionId
        && isDeepStrictEqual(record.identity, checkedIdentity(right))
        && isDeepStrictEqual(record.spend, attemptBinding(right)),
      );
    });
  }

  async publishReceipt(right, observation) {
    return this.#serial(async () => {
      const data = this.#document.data;
      const record = data.messages[right.messageId];
      if (!record?.spend || !isDeepStrictEqual(record.identity, checkedIdentity(right))
        || !isDeepStrictEqual(record.spend, attemptBinding(right))) {
        throw new ProtocolError('ATTEMPT_MISMATCH', 'receipt does not match the spent attempt');
      }
      const receipt = checkedObservation(observation, record);
      if (record.receipt) {
        if (!isDeepStrictEqual(record.receipt, receipt)) {
          throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'receipt payload is incompatible');
        }
        return this.#response('receipt-replay', record);
      }
      // A late observation is historical evidence; it does not reactivate the generation.
      const nextRecord = { ...record, receipt: clone(receipt) };
      await this.#hooks.beforeCommit?.('receipt', clone(nextRecord));
      await this.#document.replace({
        ...data,
        messages: { ...data.messages, [right.messageId]: nextRecord },
      });
      return this.#response('receipt', nextRecord);
    });
  }

  async revokeGeneration() {
    return this.#serial(async () => {
      const data = this.#document.data;
      const activeGeneration = data.activeGeneration + 1;
      await this.#document.replace({ ...data, activeGeneration });
      return activeGeneration;
    });
  }
}

function checkedObservation(observation, record) {
  if (observation?.type !== 'before_agent_start' || observation.messageType !== 'custom') {
    throw new ProtocolError('INVALID_OBSERVATION', 'only custom before_agent_start is a positive receipt');
  }
  const checked = {
    type: 'before_agent_start',
    messageType: 'custom',
    sessionId: observation.sessionId,
    details: clone(observation.details),
  };
  if (checked.sessionId !== record.identity.sessionId || checked.details?.message_id !== record.identity.messageId
    || !isDeepStrictEqual(checked.details, record.payload.details)) {
    throw new ProtocolError('INVALID_OBSERVATION', 'observation session or details do not match immutable identity');
  }
  return checked;
}

export class LocalJournal {
  #document;

  constructor(document) {
    this.#document = document;
  }

  static async create(file, { journalId = randomUUID() } = {}) {
    const data = { kind: 'bridge-delivery-journal', journalId, messages: {} };
    return new LocalJournal(await CheckedJsonDocument.create(file, data.kind, 'journalId', data));
  }

  static async open(file, { expectedJournalId, ownerUid } = {}) {
    if (typeof expectedJournalId !== 'string' || expectedJournalId.length === 0) {
      throw new ProtocolError('JOURNAL_ID_REQUIRED', 'recovery requires the original journal ID');
    }
    return new LocalJournal(await CheckedJsonDocument.open(
      file,
      'bridge-delivery-journal',
      'journalId',
      expectedJournalId,
      ownerUid,
    ));
  }

  get journalId() {
    return this.#document.data.journalId;
  }

  entry(messageId) {
    const entry = this.#document.data.messages[messageId];
    return entry ? clone(entry) : undefined;
  }

  failNextWrite(phase) {
    this.#document.failNextWrite(phase);
  }

  async #put(messageId, entry) {
    const data = this.#document.data;
    await this.#document.replace({ ...data, messages: { ...data.messages, [messageId]: entry } });
  }

  async recordIntent(identityInput, payloadInput) {
    const identity = checkedIdentity(identityInput);
    const payload = checkedPayload(payloadInput, identity.messageId);
    return this.#document.serial(async () => {
      const existing = this.entry(identity.messageId);
      if (existing) {
        if (!isDeepStrictEqual(existing.identity, identity) || !isDeepStrictEqual(existing.payload, payload)) {
          throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'journal identity or payload is incompatible');
        }
        return;
      }
      await this.#put(identity.messageId, { identity, payload, attempt: null, receipt: null });
    });
  }

  #matchingAttempt(right) {
    const existing = this.entry(right.messageId);
    if (!existing || !isDeepStrictEqual(existing.identity, checkedIdentity(right))
      || !isDeepStrictEqual(existing.attempt, attemptBinding(right))) {
      throw new ProtocolError('ATTEMPT_MISMATCH', 'no matching persisted local attempt');
    }
    return existing;
  }

  async checkAttempt(right) {
    await this.#document.serial(() => this.#matchingAttempt(right));
  }

  async recordAttempt(right) {
    return this.#document.serial(async () => {
      const existing = this.entry(right.messageId);
      if (!existing || !isDeepStrictEqual(existing.identity, checkedIdentity(right))) {
        throw new ProtocolError('ATTEMPT_MISMATCH', 'attempt has no matching local intent');
      }
      if (existing.attempt) {
        this.#matchingAttempt(right);
        return;
      }
      if (right.journalId !== this.journalId) {
        throw new ProtocolError('JOURNAL_MISMATCH', 'attempt belongs to another journal');
      }
      await this.#put(right.messageId, { ...existing, attempt: attemptBinding(right) });
    });
  }

  async recordReceipt(right, observation) {
    return this.#document.serial(async () => {
      const existing = this.#matchingAttempt(right);
      const receipt = checkedObservation(observation, existing);
      if (existing.receipt) {
        if (!isDeepStrictEqual(existing.receipt, receipt)) {
          throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'local receipt is already different');
        }
      } else {
        await this.#put(right.messageId, { ...existing, receipt });
      }
      return receipt;
    });
  }
}

export class TestBridge {
  #artel;
  #journal;
  #transmit;
  #hooks;

  constructor({ artel, journal, transmit, hooks = {} }) {
    assert.equal(typeof transmit, 'function');
    this.#artel = artel;
    this.#journal = journal;
    this.#transmit = transmit;
    this.#hooks = hooks;
  }

  async deliver(identityInput, payloadInput) {
    const identity = checkedIdentity(identityInput);
    const payload = checkedPayload(payloadInput, identity.messageId);
    const existing = this.#journal.entry(identity.messageId);
    if (existing && (!isDeepStrictEqual(existing.identity, identity) || !isDeepStrictEqual(existing.payload, payload))) {
      throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'local journal entry is incompatible');
    }
    const admission = await this.#artel.admit(identity, payload);
    await this.#journal.recordIntent(identity, payload);
    const entry = this.#journal.entry(identity.messageId);
    if (entry.receipt) {
      return this.#artel.publishReceipt({ ...entry.attempt, ...entry.identity }, entry.receipt);
    }
    if (admission.status !== 'admitted') {
      return { ...admission, transmitted: false };
    }
    await this.#hooks.beforeSpend?.(identity);
    const spent = await this.#artel.spend({
      ...identity,
      ...processOwner,
      journalId: this.#journal.journalId,
    });
    await this.#hooks.afterSpendResponse?.(spent);
    if (!spent.right) {
      return { status: spent.status, attemptId: spent.attemptId, transmitted: false };
    }
    try {
      await this.#journal.recordAttempt(spent.right);
    } catch (cause) {
      throw new ProtocolError('ATTEMPT_WRITE_FAILED', 'attempt commit was not confirmed; transmission is forbidden', { cause });
    }
    await this.#hooks.afterAttemptCommit?.(spent.right);
    const valid = await this.#artel.verify(spent.right);
    await this.#hooks.afterFinalVerification?.(spent.right, valid);
    if (!valid) {
      return { status: 'unknown', attemptId: spent.right.attemptId, transmitted: false };
    }
    await this.#journal.checkAttempt(spent.right);
    // No lock can remove this residual verify-to-call window. Revocation after the
    // final check is not promised to cancel a call which is already being issued.
    const transmitted = await this.#transmit({
      ...spent.right,
      payload: clone(payload),
    });
    return { status: 'unknown', attemptId: spent.right.attemptId, transmitted: transmitted !== false };
  }

  async recordObservation(messageId, observation) {
    const entry = this.#journal.entry(messageId);
    if (!entry?.attempt) {
      throw new ProtocolError('ATTEMPT_MISMATCH', 'observation has no local spent attempt');
    }
    const right = { ...entry.attempt, ...entry.identity };
    const receipt = await this.#journal.recordReceipt(right, observation);
    await this.#hooks.afterReceiptCommit?.(right, receipt);
    // Artel remains unknown until this atomic server receipt write commits.
    return this.#artel.publishReceipt(right, receipt);
  }
}
