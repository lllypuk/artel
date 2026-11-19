import { randomUUID } from "node:crypto";
import { existsSync } from "node:fs";
import { createServer, type Server } from "node:net";
import { dirname, join } from "node:path";
import type { DeliveryConfig } from "./config.ts";
import { DeliveryJournal } from "./delivery-journal.ts";
import { ObserverFailure, unixTransport, type Transport } from "./client.ts";

const UUID = /^(?!00000000-0000-0000-0000-000000000000$)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export interface DeliveryMessage {
  id: string; task_id: string; delivery_binding_id: string; text: string;
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
}

// Test-only seam, driven by the PTY fixture over the control socket. The
// shipping extension never reads these knobs outside the test harness.
interface DeliveryTestHooks {
  holdReadyMs: number;
  sends: number;
}

const TEST_HOOK = Symbol.for("artel.delivery.test");

interface ActiveAttempt {
  messageId: string;
  attemptId: string;
  receiptKey: string;
  text: string;
  right: boolean;
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

  private lock: Server | undefined;
  private journal: DeliveryJournal | undefined;
  private timer: ReturnType<typeof setTimeout> | undefined;
  private controller: AbortController | undefined;
  private closed = false;
  private stopped = false;
  private writerId = randomUUID();
  private attached = false;
  private lastReady = -Infinity;
  private readonly attempts = new Map<string, ActiveAttempt>();

  constructor(config: DeliveryConfig, token: string, ctx: { isIdle(): boolean },
    sessionId: string, runtimeId: string, status: (text: string) => void, send: (attempt: ActiveAttempt) => void) {
    this.config = config;
    this.transport = unixTransport({ observer_socket: config.observer_socket } as unknown as Parameters<typeof unixTransport>[0], token, 65536);
    this.ctx = ctx;
    this.sessionId = sessionId;
    this.runtimeId = runtimeId;
    this.status = status;
    this.send = send;
    const hooks = (globalThis as Record<symbol, unknown>)[TEST_HOOK] as DeliveryTestHooks | undefined;
    this.hooks = hooks ?? { holdReadyMs: 0, sends: 0 };
  }

  activeAttempt(messageId: string): ActiveAttempt | undefined {
    return this.attempts.get(messageId);
  }

  // before_agent_start observation: only the exact custom start of a spent
  // attempt becomes a receipt; provider and completion events cannot.
  observeStart(event: { messageType?: string; message?: { details?: unknown } },
    sessionId: string, publish: (attempt: ActiveAttempt) => Promise<void>): void {
    if (event.messageType !== "custom" || !event.message?.details || typeof event.message.details !== "object")
      return;
    const details = event.message.details as Record<string, unknown>;
    const messageId = details.message_id;
    const attemptId = details.attempt_id;
    const attempt = typeof messageId === "string" ? this.attempts.get(messageId) : undefined;
    if (!attempt || attempt.attemptId !== attemptId || sessionId !== this.sessionId) return;
    queueMicrotask(() => { void publish(attempt); });
  }

  async start(): Promise<void> {
    try {
      await this.acquireLock();
      this.journal = this.openJournal();
    } catch (error) {
      this.status(`Artel: delivery unavailable — ${error instanceof Error ? error.message : "stopped"}`);
      this.releaseLock();
      return;
    }
    this.status("Artel: delivery connecting");
    this.schedule(0);
  }

  // Kernel-exclusive writer lock: a second bind fails while this process owns
  // the stable path, and the kernel releases ownership on process death.
  private async acquireLock(): Promise<void> {
    const lockPath = join(dirname(this.config.journal_file), "writer.lock");
    const server = createServer();
    this.lock = server;
    try {
      await new Promise<void>((resolve, reject) => {
        server.once("error", reject);
        server.once("listening", resolve);
        server.listen(lockPath);
      });
    } catch {
      this.lock = undefined;
      server.close();
      throw new Error("another writer owns this assignment");
    }
  }

  private openJournal(): DeliveryJournal {
    if (existsSync(this.config.journal_file)) {
      return DeliveryJournal.open(this.config.journal_file, this.config.journal_id);
    }
    try {
      return DeliveryJournal.create(this.config.journal_file, this.config.journal_id);
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
        if (!this.closed && !this.stopped) this.schedule(1000);
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
        }, signal);
        this.attached = true;
        this.status("Artel: delivery attached");
      }
      if (performance.now() - this.lastReady >= 2000) {
        if (this.hooks.holdReadyMs > 0) {
          const held = this.hooks.holdReadyMs;
          this.hooks.holdReadyMs = 0;
          await new Promise(resolve => setTimeout(resolve, held));
        }
        if (this.closed || this.stopped) return;
        await this.transport("POST", "/delivery/v1/ready", { writer_id: this.writerId }, signal);
        this.lastReady = performance.now();
      }
      if (this.closed || this.stopped || !this.ctx.isIdle()) return;
      const next = await this.transport("GET", "/delivery/v1/next", undefined, signal) as { message?: DeliveryMessage; receipt_key?: string };
      if (this.closed || this.stopped || !next.message) return;
      const message = next.message;
      if (!UUID.test(message.id) || typeof message.text !== "string" || typeof next.receipt_key !== "string" || !UUID.test(next.receipt_key))
        throw new ObserverFailure("protocol_error");
      const spend = await this.transport("POST", "/delivery/v1/spend",
        { writer_id: this.writerId, message_id: message.id }, signal) as { attempt_id?: string; right?: boolean; state?: string };
      if (!spend.attempt_id || typeof spend.attempt_id !== "string") throw new ObserverFailure("protocol_error");
      const attempt: ActiveAttempt = {
        messageId: message.id, attemptId: spend.attempt_id,
        receiptKey: next.receipt_key,
        text: message.text, right: spend.right === true,
      };
      if (!attempt.right || !attempt.receiptKey) {
        // Already spent elsewhere or a replayed state: never call Pi again.
        return;
      }
      this.attempts.set(message.id, attempt);
      this.journal?.recordAttempt(message.id, spend.attempt_id);
      const verified = await this.transport("POST", "/delivery/v1/verify",
        { writer_id: this.writerId, attempt_id: attempt.attemptId }, signal) as { verified?: boolean };
      if (!verified.verified) {
        this.attempts.delete(message.id);
        return;
      }
      this.transmit(attempt);
    } catch (error) {
      if (this.closed || this.stopped) return;
      const code = error instanceof ObserverFailure ? error.code : "offline";
      if (!["offline", "unavailable", "rate_limited", "not_ready", "owner_conflict", "closed", "revoked", "assignment_mismatch"].includes(code)) {
        this.stopped = true;
        this.status(`Artel: delivery stopped — ${code}`);
        return;
      }
      if (code === "owner_conflict" || code === "revoked" || code === "assignment_mismatch") {
        this.attempts.clear();
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
    if (this.attached && value.writer_id !== this.writerId) throw new ObserverFailure("owner_conflict");
    return value;
  }

  // Final synchronous section: check idle, extinguish the in-memory right and
  // transmit without an await between the check and the call.
  private transmit(attempt: ActiveAttempt): void {
    const current = this.attempts.get(attempt.messageId);
    if (current !== attempt || !attempt.right || this.closed || this.stopped) return;
    if (!this.ctx.isIdle()) return;
    attempt.right = false;
    this.hooks.sends += 1;
    this.send(attempt);
  }

  async publishReceipt(attempt: ActiveAttempt): Promise<void> {
    this.journal?.recordReceipt(attempt.messageId, { type: "before_agent_start", message_type: "custom", published: false });
    try {
      await this.transport("POST", "/delivery/v1/receipt", {
        key: attempt.receiptKey,
        message_id: attempt.messageId,
        attempt_id: attempt.attemptId,
        type: "before_agent_start",
        message_type: "custom",
        pi_session_id: this.sessionId,
        details: { message_id: attempt.messageId, attempt_id: attempt.attemptId },
      }, AbortSignal.timeout(4000));
      this.journal?.recordReceipt(attempt.messageId, { type: "before_agent_start", message_type: "custom", published: true });
    } catch {
      // The journal keeps the receipt; the next cycle republishes idempotently.
    }
  }

  async stop(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    this.stopped = true;
    clearTimeout(this.timer);
    this.attempts.clear();
    this.controller?.abort();
    this.releaseLock();
    this.status("");
  }

  private releaseLock(): void {
    const lock = this.lock;
    this.lock = undefined;
    if (lock) lock.close();
  }
}
