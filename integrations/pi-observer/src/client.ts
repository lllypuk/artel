import { request } from "node:http";
import { performance } from "node:perf_hooks";
import type { ObserverConfig } from "./config.ts";

export const UUID = /^(?!00000000-0000-0000-0000-000000000000$)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
export interface Metadata { provider?: string; model?: string }
export interface Activity extends Metadata {
  activity: "unknown" | "idle" | "working" | "waiting_user";
  tool_count: number;
  lifecycle: "running" | "stopped";
}
export interface Registration extends Metadata {
  runtime_id: string; pi_session_id: string; reason: string;
  pi_version: string; bridge_version: string; cwd: string;
  terminal_hint?: { window_id: string; session_id: string; pane_id: string };
}
interface Context {
  database_instance_id: string; observer_id: string; runtime_id: string;
  generation: number; disposition: string;
  binding?: { runtime_id: string; project_id: string };
}
interface Report extends Activity { runtime_id: string; generation: number; report_seq: number }
export class ObserverFailure extends Error {
  readonly code: string;
  constructor(code: string) { super("observer unavailable"); this.code = code; }
}
export type Transport = (method: string, path: string, body: unknown, signal: AbortSignal, idempotencyKey?: string) => Promise<unknown>;

// No proxy, redirects, TCP fallback, or response/error text logging. Absolute deadline
// also bounds peers which keep a connection alive by trickling individual bytes.
export function unixTransport(config: ObserverConfig, token: string, maxResponse = 16384): Transport {
  return (method, path, body, signal, idempotencyKey) => new Promise((resolve, reject) => {
    const data = body === undefined ? undefined : JSON.stringify(body);
    const requestLimit = path === "/delivery/v1/questions" ? 32768 : 16384;
    if (data && Buffer.byteLength(data) > requestLimit) { reject(new ObserverFailure("invalid_metadata")); return; }
    const req = request({ socketPath: config.observer_socket, method, path, signal,
      agent: false, maxHeaderSize: 8192,
      headers: { Host: "artel-observer", Authorization: `Bearer ${token}`,
        ...(data ? { "Content-Type": "application/json", "Content-Length": Buffer.byteLength(data) } : {}),
        ...(idempotencyKey ? { "Idempotency-Key": idempotencyKey } : {}) },
    }, res => {
      const chunks: Buffer[] = [];
      let size = 0;
      res.on("data", (chunk: Buffer) => {
        size += chunk.length;
        if (size > maxResponse) {
          reject(new ObserverFailure("protocol_error")); req.destroy();
        } else chunks.push(chunk);
      });
      res.on("error", () => reject(new ObserverFailure("offline")));
      res.on("end", () => {
        try {
          const value = JSON.parse(Buffer.concat(chunks).toString("utf8"));
          if (res.statusCode !== 200) {
            const codes = ["unauthorized", "runtime_retired", "runtime_superseded", "generation_conflict", "sequence_conflict", "capacity_exceeded", "invalid_request", "rate_limited", "unavailable",
              "not_ready", "owner_conflict", "closed", "revoked", "assignment_mismatch", "instance_mismatch", "slot_occupied", "version_conflict", "idempotency_conflict", "not_found", "task_closed", "capability_required"];
            const code = codes.includes(value.error) ? value.error : "protocol_error";
            const quota = ["runtime_payloads", "runtime_identities", "observers"].includes(value.capacity) ? value.capacity : "unknown";
            reject(new ObserverFailure(code === "capacity_exceeded" ? `capacity_${quota}` : code));
          } else resolve(value);
        } catch { reject(new ObserverFailure("protocol_error")); }
      });
    });
    const deadline = setTimeout(() => req.destroy(new ObserverFailure("offline")), 2000);
    req.on("close", () => clearTimeout(deadline));
    req.on("error", () => reject(new ObserverFailure("offline")));
    req.end(data);
  });
}

const defaults = { heartbeat: 5000, coalesce: 260, retryMin: 1000, retryMax: 30000, shutdown: 240 };
export class ObserverClient {
  private timer?: ReturnType<typeof setTimeout>;
  private timerDue = Infinity;
  private controller?: AbortController;
  private flight?: Promise<void>;
  private closed = false;
  private terminal = false;
  private registered = false;
  private generation = -1;
  private conflictGeneration?: number;
  private seq = 0;
  private packet?: Report;
  private dirty = true;
  private lastReport = -Infinity;
  private retry = 0;
  private timing: typeof defaults;
  private bound: Context | undefined;

  private readonly config: ObserverConfig;
  readonly registration: Registration;
  private readonly transport: Transport;
  private readonly latest: () => Activity;
  private readonly status: (text: string) => void;
  constructor(config: ObserverConfig, registration: Registration, transport: Transport,
    latest: () => Activity, status: (text: string) => void, timing: Partial<typeof defaults> = {}) {
    this.config = config; this.registration = registration; this.transport = transport;
    this.latest = latest; this.status = status; this.timing = { ...defaults, ...timing };
  }
  start(): void { this.schedule(0); }
  changed(): void {
    this.dirty = true;
    // Events may advance a heartbeat, never a retry delay or current request.
    if (!this.retry && !this.flight && !this.closed && !this.terminal && this.timerDue > performance.now() + this.timing.coalesce) {
      clearTimeout(this.timer); this.timer = undefined;
      this.schedule(this.timing.coalesce);
    }
  }
  private schedule(delay: number): void {
    if (this.closed || this.terminal || this.timer) return;
    this.timerDue = performance.now() + delay;
    this.timer = setTimeout(() => {
      this.timer = undefined; this.timerDue = Infinity;
      this.controller = new AbortController();
      this.flight = this.cycle(this.controller.signal).finally(() => {
        this.flight = undefined;
        if (!this.closed && !this.terminal) this.schedule(this.retry || (this.dirty ? this.timing.coalesce : this.timing.heartbeat));
      });
    }, delay);
  }
  private context(value: unknown): Context {
    const c = value as Context | null;
    if (!c || c.database_instance_id !== this.config.database_instance_id) throw new ObserverFailure("database_mismatch");
    if (c.observer_id !== this.config.observer_id || c.runtime_id !== this.registration.runtime_id ||
        !Number.isSafeInteger(c.generation) || c.generation < 0 ||
        !["candidate", "bound", "superseded", "revoked"].includes(c.disposition) ||
        (c.disposition === "bound" && (!c.binding || c.binding.runtime_id !== c.runtime_id || !UUID.test(c.binding.project_id))))
      throw new ObserverFailure("protocol_error");
    if (c.disposition === "superseded" || c.disposition === "revoked") throw new ObserverFailure(`runtime_${c.disposition}`);
    if (c.generation < this.generation || c.generation === this.conflictGeneration) throw new ObserverFailure("generation_unresolved");
    this.conflictGeneration = undefined;
    if (this.generation !== c.generation) {
      this.generation = c.generation; this.seq = 0; this.packet = undefined;
    }
    this.bound = c;
    return c;
  }
  // Bound runtime of the last accepted context; delivery starts only there.
  boundRuntime(): string | undefined {
    return this.bound?.disposition === "bound" ? this.bound.binding?.runtime_id : undefined;
  }
  private async cycle(signal: AbortSignal): Promise<void> {
    try {
      // Pin the DB before any registration or telemetry, including reconnection.
      let c = this.context(await this.transport("GET", `/observer/v1/context?runtime_id=${this.registration.runtime_id}`, undefined, signal));
      if (this.closed) return;
      if (!this.registered) {
        c = this.context(await this.transport("POST", "/observer/v1/register", this.registration, signal));
        this.registered = true;
      }
      if (this.closed) return;
      if (performance.now() - this.lastReport < this.timing.coalesce) {
        this.dirty = true; return;
      }
      if (!this.packet) {
        this.packet = { ...this.latest(), runtime_id: this.registration.runtime_id,
          generation: this.generation, report_seq: ++this.seq };
        this.dirty = false;
      }
      this.lastReport = performance.now();
      const p = this.packet;
      const result = await this.transport("PUT", "/observer/v1/report", p, signal) as Record<string, unknown>;
      if (this.closed) return;
      if (!result || result.runtime_id !== p.runtime_id || result.generation !== p.generation || result.report_seq !== p.report_seq ||
          !["accepted", "duplicate"].includes(String(result.result))) throw new ObserverFailure("protocol_error");
      this.packet = undefined; this.retry = 0;
      this.status(`Artel: ${c.disposition}`);
    } catch (error) {
      if (this.closed) return;
      const code = error instanceof ObserverFailure ? error.code : "offline";
      if (!["offline", "unavailable", "rate_limited", "generation_conflict"].includes(code)) {
        this.terminal = true;
        this.status(`Artel: ${code} — owner action required`);
        return;
      }
      if (code === "generation_conflict") this.conflictGeneration = this.generation;
      // Conflict triggers one own-context read on the next cycle, never guessed +1.
      // Uncertain packets are retained byte-identically until ACK or confirmed generation.
      const base = this.retry ? Math.min(this.timing.retryMax, this.retry * 2) : this.timing.retryMin;
      this.retry = Math.min(this.timing.retryMax, base * (1 + Math.random() * 0.2));
      this.status("Artel: offline / reconnecting");
    }
  }
  async stop(): Promise<void> {
    if (this.closed) return;
    this.closed = true;
    clearTimeout(this.timer); this.timer = undefined;
    this.controller?.abort();
    const stop = new AbortController();
    const deadline = setTimeout(() => stop.abort(), this.timing.shutdown);
    // All production transports honor abort. Race also bounds a faulty test/host transport.
    const bounded = new Promise<void>(resolve => stop.signal.addEventListener("abort", () => resolve(), { once: true }));
    try {
      await Promise.race([(async () => {
        await this.flight;
        if (!this.registered || this.terminal || stop.signal.aborted) return;
        const p = { ...this.latest(), runtime_id: this.registration.runtime_id, generation: this.generation,
          report_seq: ++this.seq, lifecycle: "stopped", tool_count: 0 };
        // Respect report rate even during teardown. Stale is safer than an extra packet.
        if (performance.now() - this.lastReport < this.timing.coalesce) return;
        await this.transport("PUT", "/observer/v1/report", p, stop.signal);
      })().catch(() => {}), bounded]);
    } finally { clearTimeout(deadline); stop.abort(); }
  }
}
