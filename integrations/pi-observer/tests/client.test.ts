import assert from "node:assert/strict";
import { test } from "node:test";
import { randomUUID } from "node:crypto";
import { createServer } from "node:http";
import { mkdtempSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { ObserverClient, ObserverFailure, unixTransport, type Activity, type Registration, type Transport } from "../src/client.ts";
import type { ObserverConfig } from "../src/config.ts";

const sleep = (n: number) => new Promise(resolve => setTimeout(resolve, n));
async function until(predicate: () => boolean) {
  for (let i = 0; i < 200; i++) { if (predicate()) return; await sleep(5); }
  assert.fail("observer condition timed out");
}
function fixture() {
  const config: ObserverConfig = { status: "confirmed", observer_id: randomUUID(), database_instance_id: randomUUID(),
    observer_socket: "/unused", token_file: "/unused", provisioning_key: randomUUID(), payload: { label: "Fictional", verifier: "0".repeat(64) } };
  const registration: Registration = { runtime_id: randomUUID(), pi_session_id: randomUUID(), reason: "startup", pi_version: "1.5.2", bridge_version: "0.0.0", cwd: "/fictional" };
  let activity: Activity = { activity: "idle", tool_count: 0, lifecycle: "running" };
  let generation = 0;
  let disposition = "candidate";
  let online = true;
  const calls: { method: string; path: string; body: unknown; time: number }[] = [];
  const statuses: string[] = [];
  const context = () => ({ database_instance_id: config.database_instance_id, observer_id: config.observer_id,
    runtime_id: registration.runtime_id, generation, disposition,
    ...(disposition === "bound" ? { binding: { runtime_id: registration.runtime_id, project_id: config.provisioning_key } } : {}) });
  const good: Transport = async (method, path, body) => {
    calls.push({ method, path, body: structuredClone(body), time: performance.now() });
    if (!online) throw new ObserverFailure("offline");
    if (method === "PUT") return { ...(body as object), result: "accepted" };
    return context();
  };
  let transport = good;
  const client = new ObserverClient(config, registration, (...args) => transport(...args), () => activity,
    text => statuses.push(text), { heartbeat: 30, coalesce: 10, retryMin: 20, retryMax: 80, shutdown: 30 });
  return { config, registration, client, calls, statuses, good, context,
    setActivity: (v: Activity) => { activity = v; client.changed(); },
    bind: () => { generation++; disposition = "bound"; },
    online: (v: boolean) => { online = v; }, transport: (t: Transport) => { transport = t; } };
}

test("lost ACK retains exact packet; latest pending is bounded, fenced after bind and restart", async () => {
  const f = fixture(); let lose = true;
  f.transport(async (...args) => {
    const result = await f.good(...args);
    if (args[0] === "PUT" && lose) { lose = false; throw new ObserverFailure("offline"); }
    if (args[0] === "PUT" && (args[2] as { report_seq: number }).report_seq === 1)
      return { ...(result as object), result: "duplicate" };
    return result;
  });
  try {
    f.client.start();
    await until(() => f.statuses.some(s => s.includes("offline")));
    for (let i = 0; i < 1000; i++) f.setActivity({ activity: "working", tool_count: 2, lifecycle: "running" });
    await until(() => f.calls.filter(c => c.method === "PUT").length >= 3);
    const reports = f.calls.filter(c => c.method === "PUT");
    assert.deepEqual(reports[0].body, reports[1].body);
    assert.equal((reports[2].body as Activity).tool_count, 2);
    f.bind();
    await until(() => f.calls.some(c => c.method === "PUT" && (c.body as { generation: number }).generation === 1));
    const bound = f.calls.find(c => c.method === "PUT" && (c.body as { generation: number }).generation === 1)!;
    assert.equal((bound.body as { report_seq: number }).report_seq, 1);
    f.online(false);
    await until(() => f.statuses.at(-1)?.includes("offline") === true);
    const n = f.calls.length;
    for (let i = 0; i < 1000; i++) f.client.changed();
    await sleep(180);
    assert.ok(f.calls.length - n < 7, "offline event storms cannot bypass backoff");
    f.online(true);
    await until(() => f.statuses.at(-1) === "Artel: bound");
    assert.equal(f.calls.filter(c => c.method === "POST").length, 1, "same runtime restart never reprovisions");
  } finally { await f.client.stop(); }
});

test("lost registration response reuses immutable identity; stopped delivery is best effort", async () => {
  const f = fixture(); let lost = true;
  f.transport(async (...args) => {
    const result = await f.good(...args);
    if (args[0] === "POST" && lost) { lost = false; throw new ObserverFailure("offline"); }
    return result;
  });
  f.client.start();
  await until(() => f.statuses.includes("Artel: candidate"));
  const registrations = f.calls.filter(c => c.method === "POST");
  assert.equal(registrations.length, 2);
  assert.deepEqual(registrations[0].body, registrations[1].body);
  await sleep(12);
  await f.client.stop();
  assert.equal((f.calls.at(-1)!.body as Activity).lifecycle, "stopped");
  const n = f.calls.length;
  await sleep(45); assert.equal(f.calls.length, n);
});

test("terminal failures stop retries and expose only fixed diagnostics", async () => {
  for (const code of ["unauthorized", "runtime_retired", "runtime_superseded", "capacity_runtime_payloads", "capacity_runtime_identities", "capacity_observers", "database_mismatch", "sequence_conflict"]) {
    const f = fixture();
    f.transport(async (...args) => { await f.good(...args); throw new ObserverFailure(code); });
    f.client.start();
    await until(() => f.statuses.length > 0);
    const count = f.calls.length;
    f.client.changed(); await sleep(40);
    assert.equal(f.calls.length, count);
    assert.match(f.statuses[0], new RegExp(code));
    await f.client.stop();
  }
});

test("pin checked before registration, foreign identity rejected", async () => {
  for (const mismatch of ["database_instance_id", "observer_id", "runtime_id"]) {
    const f = fixture();
    f.transport(async (...args) => { await f.good(...args); return { ...f.context(), [mismatch]: randomUUID() }; });
    f.client.start(); await until(() => f.statuses.length > 0);
    assert.equal(f.calls.length, 1); assert.equal(f.calls[0].method, "GET");
    await f.client.stop();
  }
});

test("generation conflict reads own context before fresh seq; slow requests never overlap", async () => {
  const f = fixture(); let conflict = true; let active = 0; let max = 0;
  f.transport(async (...args) => {
    active++; max = Math.max(max, active);
    try {
      await sleep(15);
      const result = await f.good(...args);
      if (args[0] === "PUT" && conflict) { conflict = false; f.bind(); throw new ObserverFailure("generation_conflict"); }
      return result;
    } finally { active--; }
  });
  try {
    f.client.start();
    const burst = setInterval(() => f.client.changed(), 1);
    try { await until(() => f.statuses.includes("Artel: bound")); } finally { clearInterval(burst); }
    assert.equal(max, 1);
    const puts = f.calls.filter(c => c.method === "PUT");
    assert.equal((puts[0].body as { generation: number }).generation, 0);
    assert.equal((puts[1].body as { generation: number }).generation, 1);
    assert.equal((puts[1].body as { report_seq: number }).report_seq, 1);
  } finally { await f.client.stop(); }
});

test("shutdown aborts requests, is idempotent and bounded even for a stuck transport", async () => {
  const f = fixture(); let aborted = false;
  f.transport(async (_method, _path, _body, signal) => {
    signal.addEventListener("abort", () => { aborted = true; });
    return new Promise(() => {});
  });
  f.client.start(); await sleep(5);
  const before = performance.now(); await f.client.stop(); await f.client.stop();
  assert.ok(aborted); assert.ok(performance.now() - before < 250);
});

test("Unix HTTP fixes authority/auth, bounds output and redacts server diagnostics", async () => {
  const dir = mkdtempSync(join(tmpdir(), "artel-observer-transport-"));
  const f = fixture(); f.config.observer_socket = join(dir, "socket");
  const token = "a".repeat(64);
  let mode = "ok";
  const server = createServer((req, res) => {
    assert.equal(req.headers.host, "artel-observer");
    assert.equal(req.headers.authorization, `Bearer ${token}`);
    if (mode === "oversize") { res.end("x".repeat(20000)); return; }
    if (mode === "capacity") { res.writeHead(409); res.end(JSON.stringify({ error: "capacity_exceeded", capacity: "runtime_identities", secret: token })); return; }
    res.end(JSON.stringify(f.context()));
  });
  await new Promise<void>(resolve => server.listen(f.config.observer_socket, resolve));
  try {
    const transport = unixTransport(f.config, token);
    assert.deepEqual(await transport("GET", "/observer/v1/context", undefined, new AbortController().signal), f.context());
    mode = "capacity";
    await assert.rejects(transport("GET", "/observer/v1/context", undefined, new AbortController().signal), (e: unknown) => e instanceof ObserverFailure && e.code === "capacity_runtime_identities" && !String(e).includes(token));
    mode = "oversize";
    await assert.rejects(transport("GET", "/observer/v1/context", undefined, new AbortController().signal));
  } finally { await new Promise<void>(resolve => server.close(() => resolve())); rmSync(dir, { recursive: true }); }
});
