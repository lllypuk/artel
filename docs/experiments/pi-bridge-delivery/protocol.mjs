// Test-only delivery protocol fixture. This is not a production Artel API or Pi bridge.
import assert from 'node:assert/strict';
import { constants } from 'node:fs';
import fs from 'node:fs/promises';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { isDeepStrictEqual } from 'node:util';

export class ProtocolError extends Error {
  constructor(code, message, options) {
    super(message, options);
    this.name = 'ProtocolError';
    this.code = code;
  }
}

function checkedIdentity(identity) {
  assert.equal(typeof identity?.messageId, 'string');
  assert.equal(typeof identity?.generation, 'number');
  assert.equal(typeof identity?.sessionId, 'string');
  return {
    messageId: identity.messageId,
    generation: identity.generation,
    sessionId: identity.sessionId,
  };
}

function checkedPayload(payload) {
  assert(payload && typeof payload === 'object' && !Array.isArray(payload));
  assert.equal(typeof payload.content, 'string');
  assert(payload.details && typeof payload.details === 'object' && !Array.isArray(payload.details));
  return structuredClone(payload);
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
    handle = await fs.open(file, constants.O_RDONLY | constants.O_NOFOLLOW);
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

class CheckedJsonDocument {
  #file;
  #kind;
  #idKey;
  #ownerUid;
  #failNextWrite = false;
  data;

  constructor(file, kind, idKey, data, ownerUid) {
    this.#file = file;
    this.#kind = kind;
    this.#idKey = idKey;
    this.#ownerUid = ownerUid;
    this.data = data;
  }

  static async create(file, kind, idKey, data, ownerUid = process.getuid()) {
    assert.equal(process.platform, 'linux', 'this fixture requires checked Linux fsync semantics');
    await checkedDirectory(file);
    let handle;
    try {
      handle = await fs.open(file, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
      await handle.writeFile(`${JSON.stringify(data)}\n`, 'utf8');
      await handle.sync();
    } finally {
      await handle?.close();
    }
    await fsyncDirectory(path.dirname(file));
    return new CheckedJsonDocument(file, kind, idKey, data, ownerUid);
  }

  static async open(file, kind, idKey, expectedId, ownerUid = process.getuid()) {
    assert.equal(process.platform, 'linux', 'this fixture requires checked Linux fsync semantics');
    const data = await readCheckedJson(file, ownerUid);
    if (data?.kind !== kind || typeof data?.[idKey] !== 'string') {
      throw new ProtocolError('CORRUPT_STATE', `wrong state document: ${file}`);
    }
    if (expectedId !== undefined && data[idKey] !== expectedId) {
      throw new ProtocolError('JOURNAL_MISMATCH', `unexpected ${idKey}`);
    }
    return new CheckedJsonDocument(file, kind, idKey, data, ownerUid);
  }

  failNextWrite() {
    this.#failNextWrite = true;
  }

  async replace(next) {
    if (this.#failNextWrite) {
      this.#failNextWrite = false;
      throw new ProtocolError('INJECTED_WRITE_FAILURE', `injected write failure: ${this.#file}`);
    }
    const current = await readCheckedJson(this.#file, this.#ownerUid);
    if (current.kind !== this.#kind || current[this.#idKey] !== this.data[this.#idKey]) {
      throw new ProtocolError('JOURNAL_MISMATCH', `state identity changed: ${this.#file}`);
    }
    const temporary = path.join(
      path.dirname(this.#file),
      `.${path.basename(this.#file)}.${process.pid}.${randomUUID()}.tmp`,
    );
    let handle;
    try {
      handle = await fs.open(temporary, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL | constants.O_NOFOLLOW, 0o600);
      await handle.writeFile(`${JSON.stringify(next)}\n`, 'utf8');
      await handle.sync();
      await handle.close();
      handle = undefined;
      await fs.rename(temporary, this.#file);
      await fsyncDirectory(path.dirname(this.#file));
      this.data = next;
    } catch (error) {
      await handle?.close().catch(() => {});
      await fs.unlink(temporary).catch(() => {});
      throw error;
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
  #tail = Promise.resolve();
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

  failNextWrite() {
    this.#document.failNextWrite();
  }

  snapshot(messageId) {
    const record = this.#document.data.messages[messageId];
    return record ? clone(record) : undefined;
  }

  async #serial(operation) {
    const result = this.#tail.then(operation, operation);
    this.#tail = result.then(() => {}, () => {});
    return result;
  }

  async #response(operation, record) {
    await this.#hooks.afterCommit?.(operation, clone(record));
    const response = publicState(record);
    await this.#hooks.beforeResponse?.(operation, clone(response));
    return response;
  }

  async admit(identityInput, payloadInput) {
    const identity = checkedIdentity(identityInput);
    const payload = checkedPayload(payloadInput);
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
      await this.#document.replace({ ...data, messages: { ...data.messages, [identity.messageId]: record } });
      return this.#response('admission', record);
    });
  }

  async spend(bindingInput) {
    const binding = checkedIdentity(bindingInput);
    assert.equal(typeof bindingInput.ownerPid, 'number');
    assert.equal(typeof bindingInput.journalId, 'string');
    const ownerPid = bindingInput.ownerPid;
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
        spend: { attemptId: randomUUID(), ownerPid, journalId },
      };
      await this.#document.replace({
        ...data,
        messages: { ...data.messages, [binding.messageId]: nextRecord },
      });
      const state = await this.#response('spend', nextRecord);
      return {
        ...state,
        right: {
          ...binding,
          ownerPid,
          journalId,
          attemptId: nextRecord.spend.attemptId,
        },
      };
    });
  }

  async verify(right) {
    return this.#serial(async () => {
      const data = this.#document.data;
      const record = data.messages[right.messageId];
      return Boolean(
        record
        && record.identity.generation === data.activeGeneration
        && record.identity.sessionId === data.sessionId
        && isDeepStrictEqual(record.identity, checkedIdentity(right))
        && record.spend?.attemptId === right.attemptId
        && record.spend?.ownerPid === right.ownerPid
        && record.spend?.journalId === right.journalId,
      );
    });
  }

  async publishReceipt(right, receipt) {
    return this.#serial(async () => {
      const data = this.#document.data;
      const record = data.messages[right.messageId];
      if (!record || !isDeepStrictEqual(record.identity, checkedIdentity(right))
        || record.spend?.attemptId !== right.attemptId
        || record.spend?.ownerPid !== right.ownerPid
        || record.spend?.journalId !== right.journalId) {
        throw new ProtocolError('ATTEMPT_MISMATCH', 'receipt does not match the spent attempt');
      }
      if (record.receipt) {
        if (!isDeepStrictEqual(record.receipt, receipt)) {
          throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'receipt payload is incompatible');
        }
        return this.#response('receipt-replay', record);
      }
      // A late observation is historical evidence; it does not reactivate the generation.
      const nextRecord = { ...record, receipt: clone(receipt) };
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
  if (checked.sessionId !== record.identity.sessionId || !isDeepStrictEqual(checked.details, record.payload.details)) {
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

  failNextWrite() {
    this.#document.failNextWrite();
  }

  async #put(messageId, entry) {
    const data = this.#document.data;
    await this.#document.replace({ ...data, messages: { ...data.messages, [messageId]: entry } });
  }

  async recordIntent(identityInput, payloadInput) {
    const identity = checkedIdentity(identityInput);
    const payload = checkedPayload(payloadInput);
    const existing = this.entry(identity.messageId);
    if (existing) {
      if (!isDeepStrictEqual(existing.identity, identity) || !isDeepStrictEqual(existing.payload, payload)) {
        throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'journal identity or payload is incompatible');
      }
      return;
    }
    await this.#put(identity.messageId, { identity, payload, attempt: null, receipt: null });
  }

  async recordAttempt(right) {
    const existing = this.entry(right.messageId);
    if (!existing || !isDeepStrictEqual(existing.identity, checkedIdentity(right))) {
      throw new ProtocolError('ATTEMPT_MISMATCH', 'attempt has no matching local intent');
    }
    if (existing.attempt) {
      if (!isDeepStrictEqual(existing.attempt, {
        attemptId: right.attemptId,
        ownerPid: right.ownerPid,
        journalId: right.journalId,
      })) {
        throw new ProtocolError('ATTEMPT_MISMATCH', 'local attempt is already different');
      }
      return;
    }
    if (right.journalId !== this.journalId) {
      throw new ProtocolError('JOURNAL_MISMATCH', 'attempt belongs to another journal');
    }
    await this.#put(right.messageId, {
      ...existing,
      attempt: { attemptId: right.attemptId, ownerPid: right.ownerPid, journalId: right.journalId },
    });
  }

  async recordReceipt(right, observation) {
    const existing = this.entry(right.messageId);
    if (!existing?.attempt || existing.attempt.attemptId !== right.attemptId
      || existing.attempt.ownerPid !== right.ownerPid || existing.attempt.journalId !== right.journalId) {
      throw new ProtocolError('ATTEMPT_MISMATCH', 'receipt has no matching local attempt');
    }
    const receipt = checkedObservation(observation, existing);
    if (existing.receipt) {
      if (!isDeepStrictEqual(existing.receipt, receipt)) {
        throw new ProtocolError('IDEMPOTENCY_CONFLICT', 'local receipt is already different');
      }
      return receipt;
    }
    await this.#put(right.messageId, { ...existing, receipt });
    return receipt;
  }
}

export class TestBridge {
  #artel;
  #journal;
  #ownerPid;
  #transmit;
  #hooks;

  constructor({ artel, journal, ownerPid = process.pid, transmit, hooks = {} }) {
    assert.equal(typeof transmit, 'function');
    this.#artel = artel;
    this.#journal = journal;
    this.#ownerPid = ownerPid;
    this.#transmit = transmit;
    this.#hooks = hooks;
  }

  async deliver(identityInput, payloadInput) {
    const identity = checkedIdentity(identityInput);
    const payload = checkedPayload(payloadInput);
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
    const spent = await this.#artel.spend({
      ...identity,
      ownerPid: this.#ownerPid,
      journalId: this.#journal.journalId,
    });
    await this.#hooks.afterSpendResponse?.(spent);
    if (!spent.right) {
      return { status: spent.status, attemptId: spent.attemptId, transmitted: false };
    }
    try {
      await this.#journal.recordAttempt(spent.right);
    } catch (cause) {
      throw new ProtocolError('ATTEMPT_WRITE_FAILED', 'attempt was not committed; transmission is forbidden', { cause });
    }
    const valid = await this.#artel.verify(spent.right);
    await this.#hooks.afterFinalVerification?.(spent.right, valid);
    if (!valid) {
      return { status: 'unknown', attemptId: spent.right.attemptId, transmitted: false };
    }
    // No lock can remove this residual verify-to-call window. Revocation after the
    // final check is not promised to cancel a call which is already being issued.
    await this.#transmit({
      ...spent.right,
      payload: clone(payload),
    });
    return { status: 'unknown', attemptId: spent.right.attemptId, transmitted: true };
  }

  async recordObservation(messageId, observation) {
    const entry = this.#journal.entry(messageId);
    if (!entry?.attempt) {
      throw new ProtocolError('ATTEMPT_MISMATCH', 'observation has no local spent attempt');
    }
    const right = { ...entry.attempt, ...entry.identity };
    const receipt = await this.#journal.recordReceipt(right, observation);
    // Artel remains unknown until this atomic server receipt write commits.
    return this.#artel.publishReceipt(right, receipt);
  }
}

export function observationFor(identity, payload) {
  return {
    type: 'before_agent_start',
    messageType: 'custom',
    sessionId: identity.sessionId,
    details: clone(payload.details),
  };
}
