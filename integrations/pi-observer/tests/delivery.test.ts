// Delivery writer and journal recovery proofs against stub transports only.
import { test } from "node:test";
import assert from "node:assert/strict";
import { chmodSync, existsSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomUUID } from "node:crypto";
import { DeliveryJournal } from "../src/delivery-journal.ts";
import { DeliveryWriter, type ActiveAttempt } from "../src/delivery.ts";
import type { DeliveryConfig } from "../src/config.ts";
import { ObserverFailure, type Transport } from "../src/client.ts";

const TEST_HOOK = Symbol.for("artel.delivery.test");

interface Call { method: string; path: string; body: Record<string, unknown> }

type Handler = (body: Record<string, unknown>, state: Record<string, unknown>) => unknown;

// Every stubbed response round-trips through JSON so escaped 8 KiB texts hit
// the same encode/decode bounds as the real unix transport.
function stub(routes: Record<string, Handler>, state: Record<string, unknown>) {
  const calls: Call[] = [];
  const transport: Transport = async (method, path, body) => {
    calls.push({ method, path, body: body as Record<string, unknown> });
    const handler = routes[`${method} ${path}`];
    if (!handler) throw new ObserverFailure("protocol_error");
    return JSON.parse(JSON.stringify(handler(body as Record<string, unknown>, state) ?? {}));
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

function writer(env: Env, transport: Transport, idle: () => boolean = () => true): DeliveryWriter {
  return new DeliveryWriter(env.config, "ab".repeat(32), { isIdle: idle }, env.session, env.runtime,
    text => { if (text) env.statuses.push(text); }, attempt => env.sent.push(attempt), transport);
}

async function until(cond: () => boolean, label: string, timeoutMs = 4000): Promise<void> {
  const deadline = Date.now() + timeoutMs;
  for (;;) {
    if (cond()) return;
    if (Date.now() > deadline) throw new Error(`timeout waiting for ${label}; statuses follow`);
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

    // Missing at first bootstrap: created under the lock and attached.
    rmSync(env.config.journal_file);
    const fresh = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
    }, {});
    const w3 = writer(env, fresh.transport);
    await w3.start();
    await until(() => fresh.state.writer !== undefined, "attach after bootstrap");
    const journal = DeliveryJournal.open(env.config.journal_file, env.config.journal_id);
    assert.equal(journal.journalId, env.config.journal_id);
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

test("journal write failure after spend keeps the right from Pi", async () => {
  const env = makeEnv();
  try {
    const message = commission("Fictional commission");
    const attemptId = randomUUID();
    const { transport, calls, state } = stub({
      ...contextRoute(env),
      "POST /delivery/v1/attach": (body, s) => { s.writer = body.writer_id; return {}; },
      "POST /delivery/v1/ready": () => ({}),
      "GET /delivery/v1/next": (_b, s) => s.allow && !s.spent ? { message, receipt_key: (message as { receipt_key: string }).receipt_key } : { message: null },
      "POST /delivery/v1/spend": (_b, s) => s.spent ? { attempt_id: attemptId, right: false } : (s.spent = true, { attempt_id: attemptId, right: true }),
      "POST /delivery/v1/verify": () => ({ verified: true }),
    }, {});
    const w = writer(env, transport);
    await w.start();
    await until(() => calls.some(c => c.path === "/delivery/v1/attach"), "attached before the write failure");
    chmodSync(env.dir, 0o500);
    state.allow = true;
    await until(() => calls.some(c => c.path === "/delivery/v1/spend"), "spend attempted against the failed journal");
    await sleep(200);
    assert.equal(env.hooks.sends, 0, "write failure prevents the Pi call");
    assert.equal(calls.filter(c => c.path === "/delivery/v1/verify").length, 0);
    chmodSync(env.dir, 0o700);
    await w.stop();
  } finally {
    try { chmodSync(env.dir, 0o700); } catch { /* cleaned up */ }
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
    const second = stub({
      "GET /delivery/v1/context": () => ({
        database_instance_id: env.config.database_instance_id,
        delivery_binding_id: env.config.delivery_binding_id,
        status: "active", runtime_id: env.runtime, project_id: randomUUID(),
        pi_session_id: env.session, journal_id: env.config.journal_id,
        generation: 1, writer_id: randomUUID(), ready: true,
      }),
      "POST /delivery/v1/attach": () => ({}),
    }, {});
    const w2 = writer(env, second.transport);
    await w2.start();
    await until(() => env.statuses.filter(s => s.includes("stopped")).length >= 1 || env.statuses.some(s => s.includes("conflict")), "conflict surfaced");
    await w2.stop();
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
    journal.recordAttempt("msg-1", "attempt-1", "key-1");
    assert.equal(DeliveryJournal.open(path, journalId).entry("msg-1")?.receipt_key, "key-1");
    assert.throws(() => DeliveryJournal.open(path, randomUUID()), /journal mismatch/);
    writeFileSync(path, JSON.stringify({ journal_id: journalId, entries: null }) + "\n");
    assert.throws(() => DeliveryJournal.open(path, journalId), /journal mismatch/);
    assert.ok(existsSync(path));
    assert.ok(readFileSync(path, "utf8").endsWith("\n"));
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
