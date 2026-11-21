import assert from "node:assert/strict";
import { createHash, randomUUID } from "node:crypto";
import { once } from "node:events";
import { mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import { setTimeout as sleep } from "node:timers/promises";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import observer, { metadata, terminalHint } from "../src/index.ts";

test("shipping factory only subscribes to notification hooks; absent config is inert", async () => {
  const hooks = new Map<string, (event: unknown, ctx: unknown) => unknown>();
  const api = new Proxy({}, { get: (_target, key) => {
    assert.equal(key, "on", "observer must not access control, provider, tool or messaging APIs");
    return (event: string, handler: (event: unknown, ctx: unknown) => unknown) => hooks.set(event, handler);
  } }) as ExtensionAPI;
  const saved = process.env.ARTEL_OBSERVER_CONFIG;
  delete process.env.ARTEL_OBSERVER_CONFIG;
  try {
    observer(api);
    assert.deepEqual([...hooks.keys()], ["session_start", "session_tree", "before_agent_start", "agent_start", "agent_settled", "tool_execution_start", "tool_execution_end", "ui_prompt_start", "ui_prompt_end", "model_select", "session_shutdown"]);
    const ctx = { hasUI: true, isIdle: () => true, ui: { setStatus: () => assert.fail("no config must be silent") } };
    await hooks.get("session_start")!({ reason: "startup" }, ctx);
    assert.equal(hooks.has("agent_end"), false);
    await hooks.get("session_tree")!({}, ctx);
    await hooks.get("session_shutdown")!({ reason: "quit" }, ctx);
  } finally {
    if (saved === undefined) delete process.env.ARTEL_OBSERVER_CONFIG;
    else process.env.ARTEL_OBSERVER_CONFIG = saved;
  }
});

test("configured host without sendMessage observes with a persistent delivery warning", async t => {
  const root = mkdtempSync(join(tmpdir(), "artel-observer-host-"));
  t.after(() => rmSync(root, { recursive: true, force: true }));
  const token = "a".repeat(64), tokenFile = join(root, "token"), configPath = join(root, "observer.json");
  const socketPath = join(root, "observer.sock");
  const observerId = randomUUID(), databaseId = randomUUID(), projectId = randomUUID(), sessionId = randomUUID();
  writeFileSync(tokenFile, `${token}\n`, { mode: 0o600 });
  writeFileSync(configPath, JSON.stringify({ status: "confirmed", observer_id: observerId, database_instance_id: databaseId,
    observer_socket: socketPath, token_file: tokenFile, provisioning_key: randomUUID(),
    payload: { label: "Fictional", verifier: createHash("sha256").update(token).digest("hex") } }), { mode: 0o600 });
  const requests: { path: string; body: Record<string, unknown> }[] = [];
  const server = createServer((req, res) => {
    const url = new URL(req.url!, "http://localhost");
    req.setEncoding("utf8");
    let data = "";
    req.on("data", chunk => { data += chunk; });
    req.on("end", () => {
      const body = data ? JSON.parse(data) : {};
      requests.push({ path: url.pathname, body });
      const runtime = body.runtime_id ?? url.searchParams.get("runtime_id");
      res.setHeader("Content-Type", "application/json");
      if (url.pathname === "/observer/v1/report") res.end(JSON.stringify({ ...body, result: "accepted" }));
      else res.end(JSON.stringify({ observer_id: observerId, database_instance_id: databaseId, runtime_id: runtime,
        disposition: "bound", generation: 1, binding: { runtime_id: runtime, project_id: projectId } }));
    });
  });
  server.listen(socketPath);
  await once(server, "listening");
  const saved = process.env.ARTEL_OBSERVER_CONFIG;
  process.env.ARTEL_OBSERVER_CONFIG = configPath;
  const hooks = new Map<string, (event: unknown, ctx: unknown) => unknown>();
  const statuses = new Map<string, string>();
  const ctx = { cwd: root, hasUI: true, isIdle: () => true, sessionManager: { getSessionId: () => sessionId },
    ui: { setStatus: (key: string, text: string) => statuses.set(key, text) } };
  const intervals = t.mock.method(globalThis, "setInterval");
  try {
    observer({ on: (event: string, handler: (event: unknown, ctx: unknown) => unknown) => hooks.set(event, handler) } as unknown as ExtensionAPI);
    await hooks.get("session_start")!({ reason: "startup" }, ctx);
    for (let i = 0; i < 200 && statuses.get("artel-observer") !== "Artel: bound"; i++) await sleep(10);
    assert.equal(statuses.get("artel-observer"), "Artel: bound");
    assert.match(statuses.get("artel-capability") ?? "", /delivery unavailable/);
    assert.deepEqual(requests.map(r => r.path), ["/observer/v1/context", "/observer/v1/register", "/observer/v1/report"]);
    assert.equal(requests[1].body.pi_session_id, sessionId);
    assert.equal(intervals.mock.callCount(), 0, "delivery polling must not start");
  } finally {
    await hooks.get("session_shutdown")?.({ reason: "quit" }, ctx);
    if (saved === undefined) delete process.env.ARTEL_OBSERVER_CONFIG;
    else process.env.ARTEL_OBSERVER_CONFIG = saved;
    await new Promise<void>(resolve => server.close(() => resolve()));
  }
  assert.equal(statuses.get("artel-capability"), "");
});

test("metadata and inherited terminal hints are strictly whitelisted", () => {
  const ctx = { model: { provider: "fictional", id: "local", apiKey: "private" }, prompt: "private" } as unknown as ExtensionContext;
  assert.deepEqual(metadata(ctx), { provider: "fictional", model: "local" });
  assert.deepEqual(metadata({ model: { provider: "invalid\n", id: "x".repeat(257) } } as unknown as ExtensionContext), {});
  const env = { AGTERM_WINDOW_ID: randomUUID(), AGTERM_SESSION_ID: randomUUID(), AGTERM_PANE_ID: randomUUID(), AGTERM_SOCKET: "/never/transmit", SECRET: "private" };
  assert.deepEqual(terminalHint(env), { window_id: env.AGTERM_WINDOW_ID, session_id: env.AGTERM_SESSION_ID, pane_id: env.AGTERM_PANE_ID });
  assert.equal(terminalHint({ ...env, AGTERM_PANE_ID: "prefix" }), undefined);
  assert.equal(terminalHint({ AGTERM_SESSION_ID: env.AGTERM_SESSION_ID }), undefined);
});
