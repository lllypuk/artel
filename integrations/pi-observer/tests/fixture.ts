// Test driver only. Commands/provider/tools here must never ship as an observer.
import { readFileSync, writeFileSync, existsSync } from "node:fs";
import type { ExtensionAPI } from "@earendil-works/pi-coding-agent";

export default function fixture(pi: ExtensionAPI) {
  const endpoint = new URL(process.env.ARTEL_FIXTURE_URL!);
  if (endpoint.protocol !== "http:" || endpoint.hostname !== "127.0.0.1" || endpoint.pathname !== "/v1")
    throw new Error("fixture provider must be numeric loopback /v1");
  const delay = Number(process.env.ARTEL_FIXTURE_DELAY || "0");
  if (!Number.isFinite(delay) || delay < 0 || delay > 5000) throw new Error("invalid fixture delay");
  pi.registerProvider("artel-fixture", {
    baseUrl: process.env.ARTEL_FIXTURE_URL!, api: "openai-completions", apiKey: "fictional",
    models: [{ id: "local", name: "Local fixture", reasoning: false, input: ["text"],
      cost: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0 },
      contextWindow: 32768, maxTokens: 256 }],
  });
  pi.on("session_start", (_event, ctx) => {
    const path = process.env.ARTEL_FIXTURE_SESSION!;
    if (!existsSync(path)) writeFileSync(path, ctx.sessionManager.getSessionFile()!, { mode: 0o600 });
  });
  for (const name of ["fixture_a", "fixture_b"]) {
    pi.registerTool({ name, label: name, description: "Deterministic fixture",
      parameters: { type: "object", properties: {}, additionalProperties: false },
      async execute() {
        await new Promise(resolve => setTimeout(resolve, delay || (name === "fixture_a" ? 900 : 650)));
        return { content: [{ type: "text", text: "fictional" }], details: {} };
      },
    });
  }
  pi.registerCommand("fixture-wait", { handler: async (_args, ctx) => {
    await ctx.ui.confirm("Fictional private title", "Fictional private body", { timeout: delay || 800 });
  } });
  pi.registerCommand("fixture-new", { handler: async (_args, ctx) => { await ctx.newSession(); } });
  pi.registerCommand("fixture-reload", { handler: async (_args, ctx) => { await ctx.reload(); } });
  pi.registerCommand("fixture-resume", { handler: async (_args, ctx) => {
    await ctx.switchSession(readFileSync(process.env.ARTEL_FIXTURE_SESSION!, "utf8"));
  } });
  pi.registerCommand("fixture-fork", { handler: async (_args, ctx) => {
    const entry = ctx.sessionManager.getEntries().find(e => e.type === "message" && e.message.role === "user");
    if (!entry) throw new Error("missing fixture entry");
    await ctx.fork(entry.id, { position: "at" });
  } });
  pi.registerCommand("fixture-quit", { handler: async (_args, ctx) => { ctx.shutdown(); } });
}
