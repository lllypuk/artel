import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { dirname, join } from "node:path";
import { VERSION, type ExtensionAPI, type ExtensionContext } from "@earendil-works/pi-coding-agent";
import { readConfirmedConfig, readDeliveryConfig } from "./config.ts";
import { DeliveryWriter } from "./delivery.ts";
import { ObserverClient, UUID, unixTransport, type Activity, type Metadata, type Registration } from "./client.ts";

export function metadata(ctx: ExtensionContext): Metadata {
  const safe = (s: unknown): s is string => typeof s === "string" && !!s && [...s].length <= 256 && !/\p{Cc}/u.test(s);
  return { ...(safe(ctx.model?.provider) ? { provider: ctx.model.provider } : {}),
    ...(safe(ctx.model?.id) ? { model: ctx.model.id } : {}) };
}
export function terminalHint(env: NodeJS.ProcessEnv): Registration["terminal_hint"] {
  const window_id = env.AGTERM_WINDOW_ID, session_id = env.AGTERM_SESSION_ID, pane_id = env.AGTERM_PANE_ID;
  if (window_id && session_id && pane_id && [window_id, session_id, pane_id].every(s => UUID.test(s)))
    return { window_id, session_id, pane_id };
  return undefined;
}

// Loading the factory only registers notification hooks. No network, files or timers.
export default function observer(pi: ExtensionAPI) {
  let client: ObserverClient | undefined;
  let writer: DeliveryWriter | undefined;
  let current: ExtensionContext | undefined;
  let waiting = false;
  let working = false;
  const tools = new Set<string>();
  const status = (text: string) => {
    try { if (current?.hasUI) current.ui.setStatus("artel-observer", text); } catch { /* retired UI */ }
  };
  const latest = (): Activity => {
    if (current) working = !current.isIdle();
    return { activity: waiting ? "waiting_user" : tools.size || working ? "working" : "idle",
      tool_count: tools.size, lifecycle: "running", ...(current ? metadata(current) : {}) };
  };
  const reconcile = (ctx: ExtensionContext) => {
    if (!client) return;
    current = ctx;
    working = !ctx.isIdle();
    client?.changed();
  };
  pi.on("session_start", (event, ctx) => {
    current = ctx; tools.clear(); waiting = false;
    const path = process.env.ARTEL_OBSERVER_CONFIG;
    if (!path) return;
    const node = process.versions.node.split(".").map(Number);
    if (VERSION !== "1.5.2" || (node[0] < 22 || (node[0] === 22 && (node[1] < 23 || (node[1] === 23 && node[2] < 2)))) ||
        typeof ctx.isIdle !== "function" || typeof ctx.sessionManager?.getSessionId !== "function" ||
        (ctx.hasUI && typeof ctx.ui?.setStatus !== "function")) {
      status("Artel: disabled — unsupported Pi/Node capabilities"); return;
    }
    try {
      const { config, token } = readConfirmedConfig(path);
      const session = ctx.sessionManager.getSessionId();
      if (!UUID.test(session) || !ctx.cwd || [...ctx.cwd].length > 4096 || /\p{Cc}/u.test(ctx.cwd)) throw new Error("invalid metadata");
      working = !ctx.isIdle();
      const hint = terminalHint(process.env);
      const registration: Registration = { runtime_id: randomUUID(), pi_session_id: session,
        reason: event.reason, pi_version: VERSION, bridge_version: "0.0.0", cwd: ctx.cwd,
        ...metadata(ctx), ...(hint ? { terminal_hint: hint } : {}) };
      client = new ObserverClient(config, registration, unixTransport(config, token), latest, status);
      status("Artel: connecting");
      client.start();
      startDeliveryLoop(join(dirname(path), "delivery.json"), registration, ctx);
    } catch { status("Artel: disabled — config unavailable, pending or unsafe"); }
  });
  // Start the opt-in delivery writer only in the exactly bound runtime with a
  // confirmed private config next to the observer config. The pickup polls so
  // an owner enable after binding activates without restarting the runtime.
  let deliveryTimer: ReturnType<typeof setInterval> | undefined;
  let deliveryShutdown = false;
  // A binding that terminated terminally is not restarted for the same grant:
  // the memory clears only when the config starts naming another binding.
  let terminalBinding: string | undefined;
  let ownerQuestionsRegistered = false;
  const activateOwnerQuestions = (active: boolean) => {
    if (!ownerQuestionsRegistered) return;
    const currentTools = pi.getActiveTools();
    if (currentTools.includes("artel_ask_owner") === active) return;
    pi.setActiveTools(active ? [...currentTools, "artel_ask_owner"] : currentTools.filter(name => name !== "artel_ask_owner"));
  };
  const startDeliveryLoop = (deliveryPath: string, registration: Registration, ctx: ExtensionContext) => {
    stopDeliveryTimer();
    deliveryTimer = setInterval(() => {
      try {
        if (deliveryShutdown) return;
        if (writer?.terminated()) {
          terminalBinding = writer.bindingId();
          activateOwnerQuestions(false);
          // Close the dropped writer so its kernel lock and timers are
          // released before a fresh grant activates in this process.
          void writer.stop();
          writer = undefined;
        }
        if (writer || !existsSync(deliveryPath)) return;
        if (client?.boundRuntime() !== registration.runtime_id) return;
        const { config, token } = readDeliveryConfig(deliveryPath);
        if (config.delivery_binding_id === terminalBinding) return;
        if (config.allow_owner_questions === true && !ownerQuestionsRegistered) {
          pi.registerTool({
            name: "artel_ask_owner",
            label: "Ask Artel owner",
            defaultActive: false,
            description: "Store one explicit question for the owner of an open Artel task. Returns after storage; the answer arrives later as a separate custom message.",
            promptSnippet: "Ask the Artel owner about a specific open task UUID",
            promptGuidelines: ["Use artel_ask_owner only with the exact Artel task UUID explicitly supplied by the owner."],
            parameters: { type: "object", additionalProperties: false,
              properties: { task_id: { type: "string", description: "Exact Artel task UUID supplied by the owner" },
                text: { type: "string", description: "Question for the owner" } }, required: ["task_id", "text"] },
            async execute(_toolCallId, params: { task_id?: unknown; text?: unknown }, signal) {
              try {
                if (!writer || typeof params.task_id !== "string" || typeof params.text !== "string") throw new Error("unavailable");
                const result = await writer.askOwner(params.task_id, params.text, signal ?? new AbortController().signal);
                return { content: [{ type: "text", text: `Question ${result.id} stored; do not wait for the answer in this tool call.` }],
                  details: { question_id: result.id, status: result.status } };
              } catch {
                return { content: [{ type: "text", text: "Artel owner question was not confirmed as stored." }],
                  details: { question_id: "", status: "unconfirmed" }, isError: true };
              }
            },
          });
          ownerQuestionsRegistered = true;
        }
        writer = new DeliveryWriter(config, token, ctx, registration.pi_session_id, registration.runtime_id, status, attempt => {
          if (deliveryShutdown) return;
          const answer = attempt.kind === "answer";
          pi.sendMessage({
            customType: answer ? "artel-owner-answer" : "artel-delivery",
            content: answer ? `Artel owner answer\n${JSON.stringify({ question: attempt.questionText, answer: attempt.text })}` : attempt.text,
            display: true,
            details: { message_id: attempt.messageId, attempt_id: attempt.attemptId,
              ...(answer ? { custom_type: "artel-owner-answer", question_id: attempt.questionId } : {}) },
          }, { triggerTurn: true });
        }, undefined, activateOwnerQuestions);
        void writer.start();
      } catch { status("Artel: delivery disabled — config unavailable, pending or unsafe"); }
    }, 2000);
  };
  const stopDeliveryTimer = () => {
    if (deliveryTimer) clearInterval(deliveryTimer);
    deliveryTimer = undefined;
  };
  pi.on("session_tree", () => {
    // Pi has restored this branch's tool list. Only the live writer owns this tool's availability.
    activateOwnerQuestions(writer?.canAskOwner() === true);
  });
  pi.on("before_agent_start", (event, ctx) => {
    if (!writer) return;
    const session = ctx.sessionManager.getSessionId();
    const observedWriter = writer;
    observedWriter.observeStart(event, session, attempt => observedWriter.publishReceipt(attempt));
  });
  pi.on("agent_start", (_event, ctx) => { if (client) { current = ctx; working = true; client.changed(); } });
  // agent_end is intentionally not an idle boundary.
  pi.on("agent_settled", (_event, ctx) => { tools.clear(); reconcile(ctx); });
  pi.on("tool_execution_start", (event, ctx) => {
    if (!client) return;
    if (tools.size < 65535) tools.add(event.toolCallId);
    reconcile(ctx);
  });
  pi.on("tool_execution_end", (event, ctx) => { tools.delete(event.toolCallId); reconcile(ctx); });
  pi.on("ui_prompt_start", (_event, ctx) => { if (client) { waiting = true; reconcile(ctx); } });
  pi.on("ui_prompt_end", (_event, ctx) => { if (client) { waiting = false; reconcile(ctx); } });
  pi.on("model_select", (_event, ctx) => reconcile(ctx));
  pi.on("session_shutdown", async () => {
    const old = client; client = undefined;
    const oldWriter = writer; writer = undefined;
    deliveryShutdown = true;
    stopDeliveryTimer();
    // Stop the delivery writer first: it aborts its cycle and in-flight
    // receipt publish, so nothing can spend or transmit during teardown.
    await oldWriter?.stop();
    await old?.stop();
    deliveryShutdown = false;
    if (old) status("");
    current = undefined; tools.clear(); waiting = false;
  });
}
