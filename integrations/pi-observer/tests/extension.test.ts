import assert from "node:assert/strict";
import { test } from "node:test";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import observer, { metadata, terminalHint } from "../src/index.ts";
import { randomUUID } from "node:crypto";

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

test("metadata and inherited terminal hints are strictly whitelisted", () => {
  const ctx = { model: { provider: "fictional", id: "local", apiKey: "private" }, prompt: "private" } as unknown as ExtensionContext;
  assert.deepEqual(metadata(ctx), { provider: "fictional", model: "local" });
  assert.deepEqual(metadata({ model: { provider: "invalid\n", id: "x".repeat(257) } } as unknown as ExtensionContext), {});
  const env = { AGTERM_WINDOW_ID: randomUUID(), AGTERM_SESSION_ID: randomUUID(), AGTERM_PANE_ID: randomUUID(), AGTERM_SOCKET: "/never/transmit", SECRET: "private" };
  assert.deepEqual(terminalHint(env), { window_id: env.AGTERM_WINDOW_ID, session_id: env.AGTERM_SESSION_ID, pane_id: env.AGTERM_PANE_ID });
  assert.equal(terminalHint({ ...env, AGTERM_PANE_ID: "prefix" }), undefined);
  assert.equal(terminalHint({ AGTERM_SESSION_ID: env.AGTERM_SESSION_ID }), undefined);
});
