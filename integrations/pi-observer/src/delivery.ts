import { randomUUID } from "node:crypto";
import { existsSync, rmSync } from "node:fs";
import { createConnection, createServer, type Server } from "node:net";
import { dirname, join } from "node:path";
import type { DeliveryConfig } from "./config.ts";
import { DeliveryJournal } from "./delivery-journal.ts";
import { ObserverFailure, unixTransport, type Transport } from "./client.ts";

const UUID = /^(?!00000000-0000-0000-0000-000000000000$)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export interface DeliveryMessage {
  id: string; task_id: string; delivery_binding_id: string; text: string;
  kind?: "answer"; question_id?: string; question_text?: string;
  status: string; blocked: boolean; created_at: string;
}

interface DeliveryContext {
  database_instance_id: string;
  delivery_binding_id: string;
  status: string;
  runtime_id: string;
  project_id: string;
  pi_session_id: string;
  journal_id: string;
  generation: number;
  writer_id: string;
  ready: boolean;
  allow_owner_questions?: boolean;
  owner_questions_capability?: string;
}

// Test-only seam, driven by the PTY fixture over the control socket. The
// shipping extension never reads these knobs outside the test harness.
interface DeliveryTestHooks {
  holdReadyMs: number;
  sends: number;
  pollMs?: number;
  readyMs?: number;
}

const TEST_HOOK = Symbol.for("artel.delivery.test");

export interface ActiveAttempt {
  messageId: string;
  attemptId: string;
  receiptKey: string;
  text: string;
  right: boolean;
  kind?: "answer";
  questionId?: string;
  questionText?: string;
}

// DeliveryWriter owns one binding: kernel lock by exclusive unix bind, CAS
// attach, bounded readiness, single spend/verify, synchronous transmit and
// asynchronous receipt publication. No long polling; one bounded cycle at a time.
export class DeliveryWriter {
  private readonly config: DeliveryConfig;
  private readonly transport: Transport;
  private readonly ctx: { isIdle(): boolean };
  private readonly sessionId: string;
  private readonly runtimeId: string;
  private readonly status: (text: string) => void;
  private readonly send: (attempt: ActiveAttempt) => void;
  private readonly hooks: DeliveryTestHooks;
  private readonly pollMs: number;
  private readonly readyMs: number;

  private lock: Server | undefined;
  private journal: DeliveryJournal | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private controller: AbortController | undefined;
  private receiptController = new AbortController();
  private closed = false;
  private stopped = false;
  private writerId = randomUUID();
  private attached = false;
  private lastReady = -Infinity;
  private readonly attempts = new Map<string, ActiveAttempt>();

  constructor(config: DeliveryConfig, token: string, ctx: { isIdle(): boolean },
    sessionId: string, runtimeId: string, status: (text: string) => void, send: (attempt: ActiveAttempt) => void,
    transport?: Transport) {
    this.config = config;
    this.transport = transport ?? unixTransport({ observer_socket: config.observer_socket } as unknown as Parameters<typeof unixTransport>[0], token, 65536);
    this.ctx = ctx;
    this.sessionId = sessionId;
    this.runtimeId = runtimeId;
    this.status = status;
    this.send = send;
    const hooks = (globalThis as Record<symbol, unknown>)[TEST_HOOK] as DeliveryTestHooks | undefined;
    this.hooks = hooks ?? { holdReadyMs: 0, sends: 0 };
    this.pollMs = this.hooks.pollMs ?? 1000;
    this.readyMs = this.hooks.readyMs ?? 2000;
  }

  activeAttempt(messageId: string): ActiveAttempt | undefined {
    return this.attempts.get(messageId);
  }

  // Terminal writers (revoked, conflicting or stale assignment) never restart
  // on their own; the host drops the reference so a fresh grant can activate.
  terminated(): boolean {
    return this.stopped;
  }

  bindingId(): string {
    return this.config.delivery_binding_id;
  }

  // One explicit tool call performs one bounded store request. It never waits
  // for the owner and never retries automatically after an uncertain outcome.
  async askOwner(taskId: string, text: string, signal: AbortSignal,
    idempotencyKey = randomUUID()): Promise<{ id: string; status: string }> {
    if (this.config.allow_owner_questions !== true || this.closed || this.stopped || !this.attached)
      throw new ObserverFailure("capability_required");
    if (!UUID.test(taskId) || !text || Buffer.byteLength(text) > 4096 || text.includes("\0") || !UUID.test(idempotencyKey))
      throw new ObserverFailure("invalid_request");
    const result = await this.transport("POST", "/delivery/v1/questions", {
      instance_id: this.config.database_instance_id, task_id: taskId, writer_id: this.writerId, text,
    }, signal, idempotencyKey) as { id?: unknown; status?: unknown };
    if (typeof result.id !== "string" || !UUID.test(result.id) || result.status !== "stored")
      throw new ObserverFailure("protocol_error");
    return { id: result.id, status: result.status };
  }

  // before_agent_start observation: only the exact custom start of a spent
  // attempt becomes a receipt; provider and completion events cannot.
  observeStart(event: { messageType?: string; message?: { customType?: string; details?: unknown } },
    sessionId: string, publish: (attempt: ActiveAttempt) => Promise<void>): void {
    if (event.messageType !== "custom" || !event.message?.details || typeof event.message.details !== "object")
      return;
    const details = event.message.details as Record<string, unknown>;
    const messageId = details.message_id;
    const attemptId = details.attempt_id;
    const attempt = typeof messageId === "string" ? this.attempts.get(messageId) : undefined;
    if (!attempt || attempt.attemptId !== attemptId || sessionId !== this.sessionId) return;
    if (attempt.kind === "answer" && (event.message?.customType !== "artel-owner-answer" ||
        details.custom_type !== "artel-owner-answer" || details.question_id !== attempt.questionId)) return;
    // A receipt already confirmed published is never queued again: a duplicated
    // callback of one writer must not multiply republication.
    if (this.journal?.entry(attempt.messageId)?.receipt?.published) return;
    queueMicrotask(() => { void publish(attempt); });
  }

  async start(): Promise<void> {
    try {
      await this.acquireLock();
      if (this.closed) throw new Error("stopped during lock acquisition");
      this.journal = this.openJournal();
    } catch (error) {
      // A failed startup is terminal for this writer object: the pickup loop
      // drops it and can activate a fresh grant for the same runtime.
      this.stopped = true;
      this.status(`Artel: delivery unavailable — ${error instanceof Error ? error.message : "stopped"}`);
      this.releaseLock();
      return;
    }
    this.status("Artel: delivery connecting");
    this.schedule(0);
  }

  // Kernel-exclusive writer lock: a second bind fails while this process owns
  // the stable path, and the kernel releases ownership on process death. A
  // leftover socket file from a killed writer is probed: if nothing listens,
  // the stale path is removed once and the bind retried.
  private async acquireLock(): Promise<void> {
    const lockPath = join(dirname(this.config.journal_file), "writer.lock");
    for (let attempt = 0; ; attempt++) {
      const server = createServer();
      this.lock = server;
      try {
        await new Promise<void>((resolve, reject) => {
          server.once("error", reject);
          server.once("listening", resolve);
          server.listen(lockPath);
        });
        return;
      } catch (error) {
        this.lock = undefined;
        server.close();
        if (attempt > 0 || this.closed || !(await this.lockPathIsStale(lockPath))) throw new Error("another writer owns this assignment");
        if (this.closed) throw new Error("stopped during lock recovery");
        rmSync(lockPath, { force: true });
      }
    }
  }

  // A stale path refuses connections; a live writer accepts or holds them.
  private lockPathIsStale(lockPath: string): Promise<boolean> {
    return new Promise(resolve => {
      const probe = createConnection(lockPath, () => { probe.destroy(); resolve(false); });
      probe.once("error", () => resolve(true));
    });
  }

  private openJournal(): DeliveryJournal {
    // The journal is created exactly once, by the owner bootstrap. A missing
    // journal under a confirmed config is never recreated silently: the grant
    // may be stale and re-creating would fabricate recovery evidence.
    if (!existsSync(this.config.journal_file)) throw new Error("journal is missing");
    try {
      return DeliveryJournal.open(this.config.journal_file, this.config.journal_id);
    } catch {
      throw new Error("journal exists but cannot be opened safely");
    }
  }

  private schedule(delay: number): void {
    if (this.closed || this.stopped) return;
    this.timer = setTimeout(() => {
      this.timer = undefined;
      this.controller = new AbortController();
      void this.cycle(this.controller.signal).finally(() => {
        if (!this.closed && !this.stopped) this.schedule(this.pollMs);
      });
    }, delay);
  }

  private async cycle(signal: AbortSignal): Promise<void> {
    try {
      const context = await this.context(signal);
      if (!this.attached) {
        await this.transport("POST", "/delivery/v1/attach", {
          writer_id: this.writerId,
          expected_previous: context.writer_id ?? "",
          journal_id: this.config.journal_id,
          runtime_id: this.runtimeId,
          pi_session_id: this.sessionId,
          ...(this.config.allow_owner_questions === true ? { capability: "owner_questions_v1" } : {}),
        }, signal);
        this.attached = true;
        this.status("Artel: delivery attached");
      }
      if (performance.now() - this.lastReady >= this.readyMs) {
        if (this.hooks.holdReadyMs > 0) {
          const held = this.hooks.holdReadyMs;
          this.hooks.holdReadyMs = 0;
          await new Promise(resolve => setTimeout(resolve, held));
        }
        if (this.closed || this.stopped) return;
        await this.transport("POST", "/delivery/v1/ready", { writer_id: this.writerId }, signal);
        this.lastReady = performance.now();
      }
      // Republication of receipts stored before a restart never touches Pi;
      // the receipt key is replayed idempotently.
      void this.flushReceipts();
      if (this.closed || this.stopped || !this.ctx.isIdle()) return;
      const next = await this.transport("GET", `/delivery/v1/next?writer_id=${this.writerId}`, undefined, signal) as { message?: DeliveryMessage; receipt_key?: string };
      if (this.closed || this.stopped || !next.message) return;
      const message = next.message;
      if (!UUID.test(message.id) || typeof message.text !== "string" || typeof next.receipt_key !== "string" || !UUID.test(next.receipt_key) ||
          (message.kind !== undefined && message.kind !== "answer") ||
          (message.kind === "answer" && (!UUID.test(message.question_id ?? "") || typeof message.question_text !== "string")))
        throw new ObserverFailure("protocol_error");
      // Recheck idle and journal identity immediately before spending: neither
      // a manual turn nor a replaced recovery artifact may consume the right.
      if (!this.ctx.isIdle()) return;
      if (!this.journal) throw new Error("journal unavailable or unsafe");
      this.journal.assertUnchanged();
      const spend = await this.transport("POST", "/delivery/v1/spend",
        { writer_id: this.writerId, message_id: message.id }, signal) as { attempt_id?: string; right?: boolean; state?: string };
      if (!spend.attempt_id || typeof spend.attempt_id !== "string" || !UUID.test(spend.attempt_id))
        throw new ObserverFailure("protocol_error");
      const attempt: ActiveAttempt = {
        messageId: message.id, attemptId: spend.attempt_id,
        receiptKey: next.receipt_key,
        text: message.text, right: spend.right === true,
        ...(message.kind === "answer" ? { kind: "answer" as const, questionId: message.question_id, questionText: message.question_text } : {}),
      };
      if (!attempt.right || !attempt.receiptKey) {
        // Already spent elsewhere or a replayed state: never call Pi again,
        // and drop any stale in-memory right the server just disclaimed.
        this.attempts.delete(message.id);
        return;
      }
      this.attempts.set(message.id, attempt);
      this.journal.recordAttempt(message.id, spend.attempt_id, attempt.receiptKey, attempt.kind, attempt.questionId);
      const verified = await this.transport("POST", "/delivery/v1/verify",
        { writer_id: this.writerId, attempt_id: attempt.attemptId }, signal) as { verified?: boolean };
      if (!verified.verified) {
        this.attempts.delete(message.id);
        return;
      }
      this.transmit(attempt);
    } catch (error) {
      if (this.closed || this.stopped) return;
      // Journal integrity failures are terminal: after a spend consumed the
      // right, retrying would burn the next commission the same way.
      if (error instanceof Error && error.message.startsWith("journal")) {
        this.stopped = true;
        this.attempts.clear();
        this.releaseLock();
        this.status(`Artel: delivery stopped — ${error.message}`);
        return;
      }
      const code = error instanceof ObserverFailure ? error.code : "offline";
      if (code === "owner_conflict" || code === "revoked" || code === "assignment_mismatch") {
        // Terminal for this assignment: a competing or revoked ownership marker
        // never reconnects under the same writer identity. The kernel lock is
        // released so a replacement writer can take the grant in this process.
        this.stopped = true;
        this.attempts.clear();
        this.releaseLock();
        this.status(`Artel: delivery stopped — ${code}`);
        return;
      }
      if (!["offline", "unavailable", "rate_limited", "not_ready", "closed"].includes(code)) {
        this.stopped = true;
        this.attempts.clear();
        this.releaseLock();
        this.status(`Artel: delivery stopped — ${code}`);
        return;
      }
      this.status("Artel: delivery reconnecting");
    }
  }

  private async context(signal: AbortSignal): Promise<DeliveryContext> {
    const value = await this.transport("GET", "/delivery/v1/context", undefined, signal) as DeliveryContext;
    if (value.database_instance_id !== this.config.database_instance_id ||
        value.delivery_binding_id !== this.config.delivery_binding_id ||
        value.journal_id !== this.config.journal_id)
      throw new ObserverFailure("database_mismatch");
    if (value.status === "revoked") throw new ObserverFailure("revoked");
    if ((this.config.allow_owner_questions === true) !== (value.allow_owner_questions === true) ||
        (this.attached && this.config.allow_owner_questions === true && value.owner_questions_capability !== "owner_questions_v1"))
      throw new ObserverFailure("capability_required");
    if (this.attached && value.writer_id !== this.writerId) throw new ObserverFailure("owner_conflict");
    return value;
  }

  // Final synchronous section: check idle, extinguish the in-memory right and
  // transmit without an await between the check and the call.
  private transmit(attempt: ActiveAttempt): void {
    const current = this.attempts.get(attempt.messageId);
    if (current !== attempt || !attempt.right || this.closed || this.stopped) return;
    // Busy after spend: extinguish the right without a call; the attempt stays
    // unknown on the server and is never retried automatically.
    if (!this.ctx.isIdle()) { attempt.right = false; return; }
    // No await is permitted between this final integrity/idle check and Pi.
    if (!this.journal) throw new Error("journal unavailable or unsafe");
    this.journal.assertUnchanged();
    if (!this.ctx.isIdle()) { attempt.right = false; return; }
    attempt.right = false;
    this.hooks.sends += 1;
    this.send(attempt);
  }

  private flushing = false;

  // Retry every receipt the journal holds unpublished: a failed or lost ACK
  // republishes with the original key and never re-acquires a Pi-call right.
  private async flushReceipts(): Promise<void> {
    if (this.flushing || !this.journal || this.closed || this.stopped) return;
    this.flushing = true;
    try {
      for (const [messageId, entry] of this.journal.entriesView()) {
        if (this.closed || this.stopped) return;
        if (!entry.attempt_id || !entry.receipt_key || !entry.receipt || entry.receipt.published) continue;
        await this.publishReceipt({ messageId, attemptId: entry.attempt_id, receiptKey: entry.receipt_key, text: "", right: false,
          ...(entry.kind === "answer" ? { kind: "answer" as const, questionId: entry.question_id } : {}) });
      }
    } finally { this.flushing = false; }
  }

  private readonly receiptInflight = new Set<string>();

  async publishReceipt(attempt: ActiveAttempt): Promise<void> {
    if (this.closed || this.stopped) return;
    if (this.receiptInflight.has(attempt.messageId) || this.journal?.entry(attempt.messageId)?.receipt?.published) return;
    this.receiptInflight.add(attempt.messageId);
    try {
      this.journal?.recordReceipt(attempt.messageId, { type: "before_agent_start", message_type: "custom", published: false });
      await this.transport("POST", "/delivery/v1/receipt", {
        key: attempt.receiptKey,
        message_id: attempt.messageId,
        attempt_id: attempt.attemptId,
        type: "before_agent_start",
        message_type: "custom",
        pi_session_id: this.sessionId,
        details: { message_id: attempt.messageId, attempt_id: attempt.attemptId,
          ...(attempt.kind === "answer" ? { custom_type: "artel-owner-answer", question_id: attempt.questionId } : {}) },
      }, this.receiptController.signal);
      if (this.closed || this.stopped) return;
      this.journal?.recordReceipt(attempt.messageId, { type: "before_agent_start", message_type: "custom", published: true });
    } catch {
      // The journal keeps the receipt; the next cycle republishes idempotently.
    } finally { this.receiptInflight.delete(attempt.messageId); }
  }

  async stop(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    this.stopped = true;
    clearTimeout(this.timer);
    this.attempts.clear();
    this.controller?.abort();
    this.receiptController?.abort();
    this.releaseLock();
    this.status("");
  }

  private releaseLock(): void {
    const lock = this.lock;
    this.lock = undefined;
    if (lock) lock.close();
  }
}
