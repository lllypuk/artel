// Delivery writer and journal recovery proofs against stub transports only.
import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import { DeliveryJournal } from "../src/delivery-journal.ts";
import { DeliveryWriter, type ActiveAttempt } from "../src/delivery.ts";
import type { DeliveryConfig } from "../src/config.ts";
import { ObserverFailure, type Transport } from "../src/client.ts";

const TEST_HOOK = Symbol.for("artel.delivery.test");

interface Call { method: string; path: string; body: Record<string, unknown>; idempotencyKey?: string }

type Handler = (body: Record<string, unknown>, state: Record<string, unknown>) => unknown;

// Every stubbed response round-trips through JSON so escaped 8 KiB texts hit
// the same encode/decode bounds as the real unix transport.
function stub(routes: Record<string, Handler>, state: Record<string, unknown>) {
  const calls: Call[] = [];
  const transport: Transport = async (method, path, body, _signal, idempotencyKey) => {
    calls.push({ method, path, body: body as Record<string, unknown>, ...(idempotencyKey ? { idempotencyKey } : {}) });
    const [plain, query] = path.split("?");
    const handler = routes[`${method} ${plain}`];
    if (!handler) throw new ObserverFailure("protocol_error");
    const merged = { ...(body as Record<string, unknown>) };
    if (query) {
      for (const pair of query.split("&")) {
        const [key, value] = pair.split("=");
        merged[decodeURIComponent(key)] = decodeURIComponent(value);
      }
    }
    return JSON.parse(JSON.stringify(handler(merged, state) ?? {}));
  };
  return { transport, calls, state };
}

interface Env {
  dir: string; config: DeliveryConfig; session: string; runtime: string;
  sent: ActiveAttempt[]; statuses: string[]; hooks: { holdReadyMs: number; sends: number; pollMs?: number; readyMs?: number };
}

function makeEnv(options: { pollMs?: number; readyMs?: number } = {}): Env {
  const dir = mkdtempSync(join(tmpdir(), "artel-delivery-test-"));
  chmodSync(dir, 0o700);
  const config: DeliveryConfig = {
    status: "confirmed", delivery_binding_id: randomUUID(), database_instance_id: randomUUID(),
    observer_socket: join(dir, "observer.sock"), token_file: join(dir, "token"),
    journal_file: join(dir, "journal.json"), journal_id: randomUUID(),
  };
  const env: Env = {
    dir, config, session: randomUUID(), runtime: randomUUID(), sent: [], statuses: [],
    hooks: { holdReadyMs: 0, sends: 0, pollMs: options.pollMs ?? 15, readyMs: options.readyMs ?? 60 },
  };
  (globalThis as Record<symbol, unknown>)[TEST_HOOK] = env.hooks;
  // The owner bootstrap creates the journal exactly once; writers only open it.
  DeliveryJournal.create(config.journal_file, config.journal_id);
  return env;
}

function contextRoute(env: Env): Record<string, Handler> {
  return {
    "GET /delivery/v1/context": (_body, s) => ({
      database_instance_id: env.config.database_instance_id,
      delivery_binding_id: env.config.delivery_binding_id,
      status: "active", runtime_id: env.runtime, project_id: randomUUID(),
      pi_session_id: env.session, journal_id: env.config.journal_id,
      generation: 1, writer_id: (s.writer as string) ?? "", ready: true,
    }),
  };
}

function commission(text: string) {
  return {
    id: randomUUID(), task_id: randomUUID(), delivery_binding_id: randomUUID(),
    text, status: "stored", blocked: false, created_at: new Date(0).toISOString(),
    receipt_key: randomUUID(),
  };
}

function writer(env: Env, transport: Transport, idle: () => boolean = () => true,
  statuses: string[] = env.statuses): DeliveryWriter {
  return new DeliveryWriter(env.config, "ab".repeat(32), { isIdle: idle }, env.session, env.runtime,
    text => { if (text) statuses.push(text); }, attempt => env.sent.push(attempt), transport);
}

async function until(cond: () => boolean, label: string, timeoutMs = 4000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (cond()) return;
    if (Date.now() > deadline) throw new Error(`timeout waiting for ${label}`);
    await new Promise(resolve => setTimeout(resolve, 10));
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise(resolve => setTimeout(resolve, ms));
}

function observe(writer: DeliveryWriter, env: Env, messageId: string, attemptId: string, session = env.session): void {
  writer.observeStart({ messageType: "custom", message: { details: { message_id: messageId, attempt_id: attemptId } } },
    session, async () => { throw new Error("old callback fired"); });
}

test("lost attach response reconciles under the lock with one writer id", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    const { transport, calls, state } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => {
        if (!s.writer) { s.writer = body.writer_id; throw new ObserverFailure("offline"); }
        s.reattached = { expected_previous: body.expected_previous, writer_id: body.writer_id };
        return {};
      },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => env.hooks.sends === 1, "one delivery after attach loss");
    const attaches = calls.filter(c => c.path === "/delivery/v1/attach");
    assert.equal(attaches.length, 2);
    assert.equal((state.reattached as { writer_id: string }).writer_id, state.writer as string);
    assert.equal((state.reattached as { expected_previous: string }).expected_previous, state.writer as string);
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("ready refreshes on a bound and polling stops on shutdown", async () => {
  const env = makeEnv({ readyMs: 80 });
  try {
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": () => ({ message: null }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => calls.filter(c => c.path === "/delivery/v1/ready").length >= 3, "ready refresh");
    const total = calls.length;
    await w.stop();
    await sleep(150);
    assert.equal(calls.length, total, "no transport activity after shutdown");
    assert.ok(env.statuses.some(s => s.includes("attached")));
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("CAS ownership transfers to a new writer after a clean stop", async () => {
  const env = makeEnv();
  const shared: Record<string, unknown> = {};
  try {
    const first = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": () => ({ message: null }),
    }, shared);
    const a = writer(env, first.transport);
    await a.start();
    await until(() => shared.writer !== undefined, "first attach");
    const firstWriter = shared.writer as string;
    await a.stop();

    const message = commission("Second fictional commission");
    const second = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.transfer = { expected_previous: body.expected_previous }; s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, shared);
    const b = writer(env, second.transport);
    await b.start();
    await until(() => env.hooks.sends === 1, "new writer delivers");
    assert.equal((shared.transfer as { expected_previous: string }).expected_previous, firstWriter);
    await b.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("old callbacks stay neutral after the lock is released", async () => {
  const env = makeEnv();
  let released = false;
  try {
    const message = commission("Fictional commission");
    const attemptId = randomUUID();
    const first = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: attemptId, right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
      "POST /delivery/v1/receipt": () => ({}),
    }, {});
    const a = writer(env, first.transport);
    await a.start();
    await until(() => env.hooks.sends === 1, "delivery");
    const delivered = env.sent[0];
    await a.stop();
    released = true;
    // A callback queued before shutdown must not publish after the lock is gone.
    let published = 0;
    a.observeStart({ messageType: "custom", message: { details: { message_id: delivered.messageId, attempt_id: delivered.attemptId } } },
      env.session, async () => { published += 1; });
    await sleep(50);
    assert.equal(published, 0);
    assert.equal(first.calls.filter(c => c.path === "/delivery/v1/receipt").length, 0);
    // The released lock admits a fresh writer bootstrap.
    const probe = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
    }, {});
    const b = writer(env, probe.transport);
    await b.start();
    await until(() => probe.state.writer !== undefined, "lock reacquired");
    await b.stop();
  } finally {
    assert.ok(released);
    delete (globalThis as Record<symbol, unknown>)[TEST_HOOK];
    rmSync(env.dir, { recursive: true, force: true });
  }
});

test("replaced or corrupt journal is refused before any bridge call; missing one bootstraps", async () => {
  const env = makeEnv();
  try {
    // Replaced: a journal with a different pinned ID must never be adopted.
    writeFileSync(env.config.journal_file, JSON.stringify({ journal_id: randomUUID(), entries: {} }) + "\n", { mode: 0o600 });
    const refused = stub({ ...contextRoute(env) }, {});
    const w = writer(env, refused.transport);
    await w.start();
    await until(() => env.statuses.some(s => s.includes("unavailable")), "refused status");
    assert.equal(refused.calls.length, 0, "no bridge call on journal mismatch");
    await w.stop();

    // Corrupt content is equally unsafe.
    writeFileSync(env.config.journal_file, "{not json", { mode: 0o600 });
    const corrupt = stub({ ...contextRoute(env) }, {});
    const w2 = writer(env, corrupt.transport);
    await w2.start();
    await until(() => env.statuses.filter(s => s.includes("unavailable")).length >= 2, "corrupt refused");
    assert.equal(corrupt.calls.length, 0);
    await w2.stop();

    // Missing under a confirmed config: never recreated silently; the writer
    // stops before any bridge call and the owner bootstrap must recreate it.
    rmSync(env.config.journal_file);
    const missing = stub({ ...contextRoute(env) }, {});
    const w3 = writer(env, missing.transport);
    await w3.start();
    await until(() => env.statuses.filter(s => s.includes("unavailable")).length >= 3, "missing journal refused");
    assert.equal(missing.calls.length, 0, "no bridge call without the original journal");
    assert.ok(!existsSync(env.config.journal_file), "missing journal was not recreated");
    await w3.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("lost spend response never reaches Pi and keeps polling", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; throw new ObserverFailure("offline"); },
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => calls.some(c => c.path === "/delivery/v1/spend"), "spend committed");
    await sleep(100);
    assert.equal(env.hooks.sends, 0, "no Pi call after a lost spend response");
    assert.equal(calls.filter(c => c.path === "/delivery/v1/verify").length, 0, "no verify without a right");
    const contexts = calls.filter(c => c.path === "/delivery/v1/context").length;
    await until(() => calls.filter(c => c.path === "/delivery/v1/context").length >= contexts + 3, "writer keeps polling");
    assert.ok(!env.statuses.some(s => s.includes("stopped")), "writer keeps reconnecting");
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("journal persistence failure leaves the original intact and stops before a second spend", async () => {
  const env = makeEnv();
  env.config.allow_owner_questions = true;
  const message = { ...commission("Fictional answer"), kind: "answer", question_id: randomUUID(), question_text: "Fictional question" };
  const original = readFileSync(env.config.journal_file, "utf8");
  const { transport, calls } = stub({
    ...ownerQuestionsRoutes(env),
    "GET /delivery/v1/next": () => ({ message, receipt_key: message.receipt_key }),
    "POST /delivery/v1/spend": () => {
      // Fail the native temporary-file open before rename, even as root.
      mkdirSync(join(env.dir, `.journal.json.${process.pid}.tmp`), { recursive: true });
      return { attempt_id: randomUUID(), right: true };
    },
    "POST /delivery/v1/verify": () => ({ verified: true }),
  }, {});
  const w = writer(env, transport);
  try {
    await w.start();
    await until(() => w.terminated(), "terminal journal persistence failure");
    await sleep(100);
    assert.equal(readFileSync(env.config.journal_file, "utf8"), original);
    assert.equal(calls.filter(c => c.path === "/delivery/v1/spend").length, 1);
    assert.equal(calls.filter(c => c.path === "/delivery/v1/verify").length, 0);
    assert.equal(env.hooks.sends, 0);
  } finally {
    await w.stop();
    delete (globalThis as Record<symbol, unknown>)[TEST_HOOK];
    rmSync(env.dir, { recursive: true, force: true });
  }
});

test("restart after spend never retransmits and republishes the stored receipt without Pi", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    const attemptId = randomUUID();
    const receiptKey = (message as { receipt_key: string }).receipt_key;
    const first = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: attemptId, right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
      "POST /delivery/v1/receipt": () => { throw new ObserverFailure("offline"); },
    }, {});
    const a = writer(env, first.transport);
    await a.start();
    await until(() => env.hooks.sends === 1, "first delivery");
    // Receipt observed locally, publication ACK lost.
    a.observeStart({ messageType: "custom", message: { details: { message_id: message.id, attempt_id: attemptId } } },
      env.session, attempt => a.publishReceipt(attempt));
    await until(() => first.calls.filter(c => c.path === "/delivery/v1/receipt").length >= 1, "receipt attempted");
    await until(() => {
      const journal = DeliveryJournal.open(env.config.journal_file, env.config.journal_id);
      return journal.entry(message.id)?.receipt?.published === false;
    }, "receipt kept unpublished");
    await a.stop();

    // Restart: the same assignment and original journal; no second Pi call ever.
    const secondHooks = { holdReadyMs: 0, sends: 0, pollMs: 15, readyMs: 60 };
    (globalThis as Record<symbol, unknown>)[TEST_HOOK] = secondHooks;
    const second = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": () => ({ message: null }),
      "POST /delivery/v1/receipt": (_b, s) => { s.republished = (s.republished as number | undefined) ?? 0; s.republished = (s.republished as number) + 1; return {}; },
    }, {});
    const b = writer(env, second.transport);
    await b.start();
    await until(() => (second.state.republished as number | undefined) === 1, "stored receipt republished");
    await sleep(120);
    assert.equal(secondHooks.sends, 0, "restart after spend never retransmits");
    assert.equal((second.state.republished as number | undefined) ?? 0, 1, "exactly one republication");
    assert.equal(second.calls.filter(c => c.path === "/delivery/v1/spend").length, 0);
    assert.equal(second.calls.filter(c => c.path === "/delivery/v1/receipt")[0].body.key, receiptKey);
    const journal = DeliveryJournal.open(env.config.journal_file, env.config.journal_id);
    assert.equal(journal.entry(message.id)?.receipt?.published, true);
    await b.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("restart after a local receipt does not call Pi again even with no receipt stored", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    const first = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, {});
    const a = writer(env, first.transport);
    await a.start();
    await until(() => env.hooks.sends === 1, "delivery before abrupt restart");
    // No receipt observed: the journal holds the attempt only.
    await a.stop();
    const secondHooks = { holdReadyMs: 0, sends: 0, pollMs: 15, readyMs: 60 };
    (globalThis as Record<symbol, unknown>)[TEST_HOOK] = secondHooks;
    const second = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": () => ({ message: null }),
    }, {});
    const b = writer(env, second.transport);
    await b.start();
    await sleep(200);
    assert.equal(secondHooks.sends, 0);
    assert.equal(second.calls.filter(c => c.path === "/delivery/v1/receipt").length, 0, "no receipt without a stored one");
    await b.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("maximum escaped 8 KiB text round-trips through the stubbed transport", async () => {
  const env = makeEnv();
  try {
    const text = "\u0001".repeat(8192);
    assert.equal(Buffer.byteLength(text), 8192);
    const message = commission(text);
    const { transport } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => env.hooks.sends === 1, "escaped delivery");
    assert.equal(env.sent[0].text, text);
    assert.equal(Buffer.byteLength(env.sent[0].text), 8192);
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("duplicated callback and repeated observation keep one Pi call and one receipt", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    const attemptId = randomUUID();
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: attemptId, right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
      "POST /delivery/v1/receipt": () => ({}),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => env.hooks.sends === 1, "delivery");
    assert.equal(w.activeAttempt(message.id)?.right, false, "right extinguished after the call");
    // Foreign details and an old session marker must be ignored outright.
    observe(w, env, message.id, randomUUID());
    observe(w, env, message.id, attemptId, randomUUID());
    // The duplicated callback of the one writer fires at most one publication.
    w.observeStart({ messageType: "custom", message: { details: { message_id: message.id, attempt_id: attemptId } } },
      env.session, attempt => w.publishReceipt(attempt));
    w.observeStart({ messageType: "custom", message: { details: { message_id: message.id, attempt_id: attemptId } } },
      env.session, attempt => w.publishReceipt(attempt));
    await until(() => calls.filter(c => c.path === "/delivery/v1/receipt").length >= 1, "receipt published");
    await sleep(120);
    assert.equal(env.hooks.sends, 1, "exactly one Pi call");
    assert.equal(calls.filter(c => c.path === "/delivery/v1/receipt").length, 1, "exactly one receipt publication");
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("busy Pi keeps the Pi count at zero until idle, then delivers once", async () => {
  const env = makeEnv();
  try {
    let idle = false;
    const message = commission("Fictional commission");
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, {});
    const w = writer(env, transport, () => idle);
    await w.start();
    await until(() => calls.some(c => c.path === "/delivery/v1/ready"), "attached");
    await sleep(150);
    assert.equal(env.hooks.sends, 0, "busy Pi is never asked while working");
    assert.equal(calls.filter(c => c.path === "/delivery/v1/spend").length, 0, "busy Pi never asks for spend");
    idle = true;
    await until(() => env.hooks.sends === 1, "delivery once idle");
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("revoke before verify and conflicting ownership keep the Pi count at zero", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
      "POST /delivery/v1/verify": () => { throw new ObserverFailure("revoked"); },
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => calls.some(c => c.path === "/delivery/v1/verify"), "verify attempted");
    await sleep(100);
    assert.equal(env.hooks.sends, 0, "revoke before verify leaves no call");
    await until(() => env.statuses.some(s => s.includes("stopped")), "writer stops terminally");
    const total = calls.length;
    await sleep(120);
    assert.equal(calls.length, total, "terminal writer polls no further");
    await w.stop();

    // An embedded old ownership marker terminates instead of reconnecting.
    // The foreign marker is fixed for the whole run; a fresh random marker
    // per cycle would model nothing and could not prove termination.
    const foreign = randomUUID();
    const secondStatuses: string[] = [];
    const second = stub({
      "GET /delivery/v1/context": () => ({
        database_instance_id: env.config.database_instance_id,
        delivery_binding_id: env.config.delivery_binding_id,
        status: "active", runtime_id: env.runtime, project_id: randomUUID(),
        pi_session_id: env.session, journal_id: env.config.journal_id,
        generation: 1, writer_id: foreign, ready: true,
      }),
      "POST /delivery/v1/attach": () => ({}),
    }, {});
    const before = env.statuses.length;
    const w2 = writer(env, second.transport, () => true, secondStatuses);
    await w2.start();
    await until(() => secondStatuses.some(s => s.includes("stopped") || s.includes("conflict")),
      "conflict surfaced on the second writer");
    assert.ok(second.calls.some(c => c.path.startsWith("/delivery/v1/context")),
      "termination must follow real cycles, not a missing fixture route");
    const callsAfter = second.calls.length;
    await sleep(120);
    assert.equal(second.calls.length, callsAfter, "terminated second writer polls no further");
    await w2.stop();
    assert.equal(env.statuses.length, before, "second writer never reused the shared status sink");
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("shutdown cancels the in-flight request and every timer", async () => {
  const env = makeEnv();
  try {
    let aborted = 0;
    const hanging: Transport = (_method, _path, _body, signal) => new Promise((_resolve, reject) => {
      signal.addEventListener("abort", () => { aborted += 1; reject(new ObserverFailure("offline")); }, { once: true });
    });
    const w = writer(env, hanging);
    await w.start();
    await sleep(50);
    const stopped = Date.now();
    await w.stop();
    assert.ok(Date.now() - stopped < 1000, "stop does not wait for the transport deadline");
    assert.ok(aborted >= 1, "in-flight request aborted");
    await sleep(100);
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("journal pins the ID at create and refuses a changed one at open", () => {
  const dir = mkdtempSync(join(tmpdir(), "artel-delivery-journal-"));
  chmodSync(dir, 0o700);
  try {
    const path = join(dir, "journal.json");
    const journalId = randomUUID();
    const journal = DeliveryJournal.create(path, journalId);
    const messageId = randomUUID(), attemptId = randomUUID(), receiptKey = randomUUID();
    journal.recordAttempt(messageId, attemptId, receiptKey);
    assert.equal(DeliveryJournal.open(path, journalId).entry(messageId)?.receipt_key, receiptKey);
    assert.throws(() => DeliveryJournal.open(path, randomUUID()), /journal mismatch/);
    writeFileSync(path, JSON.stringify({ journal_id: journalId, entries: null }) + "\n");
    assert.throws(() => DeliveryJournal.open(path, journalId), /journal mismatch/);
    assert.ok(existsSync(path));
    assert.ok(readFileSync(path, "utf8").endsWith("\n"));
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("a stale lock path from a killed writer is probed, removed and rebound", async () => {
  const env = makeEnv();
  try {
    // A leftover regular file at the lock path: nothing listens, so the
    // writer must treat it as stale, remove it once and take ownership.
    writeFileSync(join(env.dir, "writer.lock"), "stale", { mode: 0o600 });
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": () => ({ message: null }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => calls.some(c => c.path === "/delivery/v1/attach"), "rebound after stale probe");
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("a manual turn starting while next is in flight never spends the right", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    let idle = true;
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      // The owner starts a manual turn while next is in flight.
      "GET /delivery/v1/next": (_b, s) => { if (!s.writer) return { message: null }; idle = false; return { message, receipt_key: (message as { receipt_key: string }).receipt_key }; },
      "POST /delivery/v1/spend": () => { throw new Error("spend must not be asked while busy"); },
    }, {});
    const w = writer(env, transport, () => idle);
    await w.start();
    await until(() => calls.some(c => c.path.startsWith("/delivery/v1/next")) && !idle, "manual turn during next");
    await sleep(100);
    assert.equal(env.hooks.sends, 0, "no Pi call while busy");
    assert.equal(calls.filter(c => c.path === "/delivery/v1/spend").length, 0, "the delivery right is not consumed");
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

function ownerQuestionsRoutes(env: Env): Record<string, Handler> {
  return {
    "GET /delivery/v1/context": (_body, s) => ({
      database_instance_id: env.config.database_instance_id,
      delivery_binding_id: env.config.delivery_binding_id,
      status: "active", runtime_id: env.runtime, project_id: randomUUID(),
      pi_session_id: env.session, journal_id: env.config.journal_id,
      generation: 1, writer_id: (s.writer as string) ?? "", ready: true,
      allow_owner_questions: true,
      owner_questions_capability: s.writer ? "owner_questions_v1" : "",
    }),
    "POST /delivery/v1/attach": (body, s) => {
      assert.equal(body.capability, "owner_questions_v1");
      assert.equal(body.instance_id, env.config.database_instance_id);
      s.writer = body.writer_id;
      return {};
    },
    "POST /delivery/v1/ready": () => ({}),
  };
}

test("ordinary 4a omits the 4b capability and owner-question access", async () => {
  const env = makeEnv();
  try {
    const message = commission("Ordinary fictional commission");
    const { transport, calls } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
      "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => env.hooks.sends === 1, "ordinary 4a delivery");
    assert.equal("capability" in calls.find(c => c.path === "/delivery/v1/attach")!.body, false);
    await assert.rejects(w.askOwner(randomUUID(), "Must not be sent", new AbortController().signal),
      (error: unknown) => error instanceof ObserverFailure && error.code === "capability_required");
    assert.equal(calls.some(c => c.path === "/delivery/v1/questions"), false);
    assert.equal(env.sent[0].kind, undefined);
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("question ACK recovery replays only the limited payload with the original key", async () => {
  const env = makeEnv();
  env.config.allow_owner_questions = true;
  try {
    const taskId = randomUUID(), questionId = randomUUID(), key = randomUUID();
    const { transport, calls } = stub({
      ...ownerQuestionsRoutes(env),
      "GET /delivery/v1/next": () => ({ message: null }),
      "POST /delivery/v1/questions": () => ({ id: questionId, status: "stored" }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => calls.some(c => c.path === "/delivery/v1/attach"), "capable attach");
    const signal = new AbortController().signal;
    assert.deepEqual(await w.askOwner(taskId, "Fictional question", signal, key), { id: questionId, status: "stored" });
    assert.deepEqual(await w.askOwner(taskId, "Fictional question", signal, key), { id: questionId, status: "stored" });
    const questions = calls.filter(c => c.path === "/delivery/v1/questions");
    assert.equal(questions.length, 2);
    assert.deepEqual(questions.map(c => c.idempotencyKey), [key, key]);
    for (const call of questions) {
      assert.deepEqual(call.body, { instance_id: env.config.database_instance_id, task_id: taskId,
        writer_id: call.body.writer_id, text: "Fictional question" });
      assert.deepEqual(Object.keys(call.body).sort(), ["instance_id", "task_id", "text", "writer_id"]);
    }
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("mixed queue preserves answer context and wrong-kind or question starts yield no receipt", async () => {
  const env = makeEnv();
  env.config.allow_owner_questions = true;
  try {
    const ordinary = commission("Ordinary before answer");
    const answer = { ...commission("Fictional answer"), kind: "answer", question_id: randomUUID(), question_text: "Fictional question" };
    const queue = [ordinary, answer];
    const attempts = new Map<string, string>();
    let published = 0;
    const { transport, calls } = stub({
      ...ownerQuestionsRoutes(env),
      "GET /delivery/v1/next": (_b, s) => {
        const index = (s.index as number | undefined) ?? 0;
        const message = queue[index];
        return message ? { message, receipt_key: (message as { receipt_key: string }).receipt_key } : { message: null };
      },
      "POST /delivery/v1/spend": (body, s) => {
        const attemptId = randomUUID(); attempts.set(body.message_id as string, attemptId);
        s.index = ((s.index as number | undefined) ?? 0) + 1;
        return { attempt_id: attemptId, right: true };
      },
      "POST /delivery/v1/verify": () => ({ verified: true }),
      "POST /delivery/v1/receipt": () => { published += 1; return {}; },
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => env.hooks.sends === 2, "commission then answer from mixed queue");
    assert.equal(env.sent[0].kind, undefined);
    assert.deepEqual({ kind: env.sent[1].kind, questionId: env.sent[1].questionId, questionText: env.sent[1].questionText },
      { kind: "answer", questionId: answer.question_id, questionText: answer.question_text });
    const attemptId = attempts.get(answer.id)!;
    let publications = 0;
    const publish = async () => { publications += 1; };
    w.observeStart({ messageType: "custom", message: { customType: "artel-delivery",
      details: { message_id: answer.id, attempt_id: attemptId, custom_type: "artel-owner-answer", question_id: answer.question_id } } }, env.session, publish);
    w.observeStart({ messageType: "custom", message: { customType: "artel-owner-answer",
      details: { message_id: answer.id, attempt_id: attemptId, custom_type: "artel-owner-answer", question_id: randomUUID() } } }, env.session, publish);
    await sleep(50);
    assert.equal(publications, 0);
    w.observeStart({ messageType: "custom", message: { customType: "artel-owner-answer",
      details: { message_id: answer.id, attempt_id: attemptId, custom_type: "artel-owner-answer", question_id: answer.question_id } } },
      env.session, attempt => w.publishReceipt(attempt));
    await until(() => published === 1, "matching typed answer receipt");
    const receipt = calls.find(c => c.path === "/delivery/v1/receipt")!;
    assert.equal(receipt.body.pi_session_id, env.session);
    assert.deepEqual(receipt.body.details, { message_id: answer.id, attempt_id: attemptId,
      custom_type: "artel-owner-answer", question_id: answer.question_id });
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

test("answer receipt recovery uses journal type and original session without any Pi call", async () => {
  const env = makeEnv();
  env.config.allow_owner_questions = true;
  try {
    const messageId = randomUUID(), attemptId = randomUUID(), receiptKey = randomUUID(), questionId = randomUUID();
    const journal = DeliveryJournal.open(env.config.journal_file, env.config.journal_id);
    journal.recordAttempt(messageId, attemptId, receiptKey, "answer", questionId);
    journal.recordReceipt(messageId, { type: "before_agent_start", message_type: "custom", published: false });
    const { transport, calls } = stub({
      ...ownerQuestionsRoutes(env),
      "GET /delivery/v1/next": () => ({ message: null }),
      "POST /delivery/v1/receipt": () => ({}),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => calls.some(c => c.path === "/delivery/v1/receipt"), "stored answer receipt recovered");
    await sleep(80);
    assert.equal(env.hooks.sends, 0);
    assert.equal(calls.filter(c => c.path === "/delivery/v1/spend").length, 0);
    const receipt = calls.find(c => c.path === "/delivery/v1/receipt")!;
    assert.equal(receipt.body.pi_session_id, env.session);
    assert.deepEqual(receipt.body.details, { message_id: messageId, attempt_id: attemptId,
      custom_type: "artel-owner-answer", question_id: questionId });
    assert.equal(DeliveryJournal.open(env.config.journal_file, env.config.journal_id).entry(messageId)?.receipt?.published, true);
    await w.stop();
  } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
});

for (const stage of ["before-spend", "after-spend", "after-verify", "mode-before-spend", "mode-after-verify"] as const) {
  test(`journal loss ${stage} fences the right and never calls Pi`, async () => {
    const env = makeEnv();
    try {
      const message = commission("Journal fence");
      const { transport, calls } = stub({
        ...contextRoute(env),
        "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
        "POST /delivery/v1/ready": () => ({}),
        "GET /delivery/v1/next": (_b, s) => {
          if (s.spent) return { message: null };
          if (stage === "before-spend") rmSync(env.config.journal_file);
          if (stage === "mode-before-spend") chmodSync(env.config.journal_file, 0o644);
          return { message, receipt_key: (message as { receipt_key: string }).receipt_key };
        },
        "POST /delivery/v1/spend": (_b, s) => {
          s.spent = true;
          if (stage === "after-spend") rmSync(env.config.journal_file);
          return { attempt_id: randomUUID(), right: true };
        },
        "POST /delivery/v1/verify": () => {
          if (stage === "after-verify") rmSync(env.config.journal_file);
          if (stage === "mode-after-verify") chmodSync(env.config.journal_file, 0o644);
          return { verified: true };
        },
      }, {});
      const w = writer(env, transport);
      await w.start();
      await until(() => env.statuses.some(s => s.includes("stopped") && s.includes("journal")), "terminal journal fence");
      assert.equal(env.hooks.sends, 0);
      assert.equal(calls.filter(c => c.path === "/delivery/v1/spend").length, stage.endsWith("before-spend") ? 0 : 1);
      assert.equal(calls.filter(c => c.path === "/delivery/v1/verify").length, stage.endsWith("after-verify") ? 1 : 0);
      await w.stop();
    } finally { delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true }); }
  });
}

test("busy after spend and a lost verify ACK never retransmit", async () => {
  for (const failure of ["busy", "lost-verify"] as const) {
    const env = makeEnv();
    let w: DeliveryWriter | undefined;
    try {
      const message = commission(failure);
      let idle = true;
      const { transport, calls } = stub({
        ...contextRoute(env),
        "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
        "POST /delivery/v1/ready": () => ({}),
        "GET /delivery/v1/next": (_b, s) => s.spent ? { message: null } : { message, receipt_key: (message as { receipt_key: string }).receipt_key },
        "POST /delivery/v1/spend": (_b, s) => { s.spent = true; return { attempt_id: randomUUID(), right: true }; },
        "POST /delivery/v1/verify": () => {
          if (failure === "busy") { idle = false; return { verified: true }; }
          throw new ObserverFailure("offline");
        },
      }, {});
      w = writer(env, transport, () => idle);
      await w.start();
      await until(() => calls.some(c => c.path === "/delivery/v1/verify"), failure);
      // A subsequent cycle proves the busy/failed verify cycle finished.
      const contextPolls = () => calls.filter(c => c.path === "/delivery/v1/context").length;
      const before = contextPolls();
      await until(() => contextPolls() > before, "writer polled again after verify");
      if (failure === "busy") {
        assert.equal(w.activeAttempt(message.id)?.right, false);
      }
      const pickups = () => calls.filter(c => c.path.startsWith("/delivery/v1/next?")).length;
      const beforeIdle = pickups();
      idle = true;
      await until(() => pickups() > beforeIdle + 1, "idle writer resumed pickup");
      assert.equal(env.hooks.sends, 0);
      assert.equal(calls.filter(c => c.path === "/delivery/v1/spend").length, 1);
    } finally {
      await w?.stop();
      delete (globalThis as Record<symbol, unknown>)[TEST_HOOK]; rmSync(env.dir, { recursive: true, force: true });
    }
  }
});
