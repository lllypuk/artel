// Test-only public API probe, NOT the shipping observer extension.
import { randomUUID } from "node:crypto";
import { appendFileSync } from "node:fs";
import { VERSION, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";

export default function probe(pi: ExtensionAPI) {
  if (VERSION !== "1.5.2") throw new Error("unsupported Pi version");
  let runtime = "";
  let waiting = false;
  const tools = new Set<string>();
  const record = (event: string, ctx: ExtensionContext, reason?: string) => {
    // Never serialize event/context wholesale: these contain dialog/tool text.
    appendFileSync(process.env.ARTEL_PROBE_TRACE!, JSON.stringify({
      event, runtime, session: ctx.sessionManager.getSessionId(),
      idle: ctx.isIdle(), tools: tools.size, waiting, ...(reason ? { reason } : {}),
    }) + "\n", { mode: 0o600 });
  };
  pi.on("session_start", (event, ctx) => {
    runtime = randomUUID(); tools.clear(); waiting = false;
    record(event.type, ctx, event.reason);
  });
  pi.on("session_shutdown", (event, ctx) => {
    tools.clear(); waiting = false; record(event.type, ctx, event.reason);
  });
  pi.on("agent_start", (event, ctx) => record(event.type, ctx));
  pi.on("agent_end", (event, ctx) => record(event.type, ctx));
  pi.on("agent_settled", (event, ctx) => record(event.type, ctx));
  pi.on("tool_execution_start", (event, ctx) => {
    tools.add(event.toolCallId); record(event.type, ctx);
  });
  pi.on("tool_execution_end", (event, ctx) => {
    tools.delete(event.toolCallId); record(event.type, ctx);
  });
  pi.on("ui_prompt_start", (event, ctx) => {
    waiting = true; record(event.type, ctx);
  });
  pi.on("ui_prompt_end", (event, ctx) => {
    waiting = false; record(event.type, ctx);
  });
  pi.on("model_select", (event, ctx) => record(event.type, ctx));
}
