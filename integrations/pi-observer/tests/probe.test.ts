import assert from "node:assert/strict";
import { existsSync, mkdtempSync, readFileSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { test } from "node:test";
import type { ExtensionAPI, ExtensionContext } from "@earendil-works/pi-coding-agent";
import probe from "./probe.ts";

test("probe factory inert, whitelist only, tool set and bounded idempotent cleanup", () => {
  const root = mkdtempSync(join(tmpdir(), "artel-probe-unit-"));
  const previous = process.env.ARTEL_PROBE_TRACE;
  process.env.ARTEL_PROBE_TRACE = join(root, "trace");
  try {
    const handlers = new Map<string, (event: unknown, ctx: ExtensionContext) => void>();
    // Any attempted control API access fails: this observer can only subscribe.
    const api = new Proxy({}, { get(_target, key) {
      assert.equal(key, "on");
      return (event: string, handler: (event: unknown, ctx: ExtensionContext) => void) => { handlers.set(event, handler); return () => {}; };
    } }) as ExtensionAPI;
    probe(api);
    assert.equal(existsSync(process.env.ARTEL_PROBE_TRACE), false);
    const ctx = { sessionManager: { getSessionId: () => "fictional" }, isIdle: () => true } as ExtensionContext;
    const emit = (type: string, rest = {}) => handlers.get(type)!({ type, ...rest }, ctx);
    emit("session_start", { reason: "startup" });
    emit("tool_execution_start", { toolCallId: "a", args: "PRIVATE" });
    emit("tool_execution_start", { toolCallId: "b" });
    emit("tool_execution_end", { toolCallId: "a", result: "PRIVATE" });
    emit("ui_prompt_start", { title: "PRIVATE" });
    const start = performance.now();
    emit("session_shutdown", { reason: "quit" });
    emit("session_shutdown", { reason: "quit" });
    assert.ok(performance.now() - start < 250);
    const rows = readFileSync(process.env.ARTEL_PROBE_TRACE, "utf8");
    assert.ok(!rows.includes("PRIVATE"));
    const states = rows.trim().split("\n").map(line => JSON.parse(line));
    assert.deepEqual(states.map(s => s.tools), [0, 1, 2, 1, 1, 0, 0]);
    assert.equal(states.at(-1).waiting, false);
  } finally {
    if (previous === undefined) delete process.env.ARTEL_PROBE_TRACE;
    else process.env.ARTEL_PROBE_TRACE = previous;
    rmSync(root, { recursive: true });
  }
});
