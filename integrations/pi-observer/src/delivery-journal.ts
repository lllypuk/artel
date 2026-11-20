import { closeSync, constants, fstatSync, fsyncSync, openSync, readSync, renameSync, writeSync } from "node:fs";
import { basename, dirname, join } from "node:path";

const UUID_PATTERN = /^(?!00000000-0000-0000-0000-000000000000$)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export interface JournalEntry {
  attempt_id?: string;
  receipt_key?: string;
  receipt?: { type: string; message_type: string; published: boolean };
}

// Private single-document journal next to the delivery config. All writes go
// through a temporary file + atomic rename + directory fsync; the journal ID is
// pinned at creation and verified at every open.
export class DeliveryJournal {
  readonly journalId: string;
  private readonly path: string;
  private entries = new Map<string, JournalEntry>();
  private lastMtimeMs = -1;
  private lastIno = -1;

  private constructor(path: string, journalId: string, entries: Map<string, JournalEntry>) {
    this.path = path;
    this.journalId = journalId;
    this.entries = entries;
  }

  private stamp(): void {
    const fd = openSync(this.path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
    try {
      const info = fstatSync(fd);
      this.lastMtimeMs = info.mtimeMs;
      this.lastIno = info.ino;
    } finally {
      closeSync(fd);
    }
  }

  static create(path: string, journalId: string): DeliveryJournal {
    const journal = new DeliveryJournal(path, journalId, new Map());
    journal.persist();
    return journal;
  }
  static open(path: string, journalId: string): DeliveryJournal {
    // Content and identity come from one open session: validating one file
    // while recording another file's timestamps would admit a swap.
    const { content, mtimeMs, ino } = readPrivateStamped(path);
    const raw = JSON.parse(content) as { journal_id?: string; entries?: Record<string, JournalEntry> };
    if (raw.journal_id !== journalId || typeof raw.entries !== "object" || raw.entries === null || Array.isArray(raw.entries))
      throw new Error("journal mismatch");
    for (const [id, entry] of Object.entries(raw.entries)) {
      // A malformed entry must disable delivery, never crash or derail Pi.
      if (!UUID_PATTERN.test(id) || typeof entry !== "object" || entry === null || Array.isArray(entry)) {
        throw new Error("journal mismatch");
      }
      const record = entry as Record<string, unknown>;
      if ((record.attempt_id !== undefined && !UUID_PATTERN.test(String(record.attempt_id))) ||
          (record.receipt_key !== undefined && !UUID_PATTERN.test(String(record.receipt_key))) ||
          (record.receipt !== undefined && (typeof record.receipt !== "object" || record.receipt === null ||
            typeof (record.receipt as { published?: unknown }).published !== "boolean"))) {
        throw new Error("journal mismatch");
      }
    }
    const journal = new DeliveryJournal(path, journalId, new Map(Object.entries(raw.entries)));
    journal.lastMtimeMs = mtimeMs;
    journal.lastIno = ino;
    return journal;
  }

  entry(messageId: string): JournalEntry | undefined {
    return this.entries.get(messageId);
  }

  entriesView(): ReadonlyMap<string, JournalEntry> {
    return new Map(this.entries);
  }

  recordAttempt(messageId: string, attemptId: string, receiptKey: string): void {
    this.verifyUnchanged();
    const entry = this.entries.get(messageId) ?? {};
    entry.attempt_id = attemptId;
    entry.receipt_key = receiptKey;
    this.entries.set(messageId, entry);
    this.persist();
  }

  recordReceipt(messageId: string, receipt: JournalEntry["receipt"]): void {
    this.verifyUnchanged();
    const entry = this.entries.get(messageId) ?? {};
    entry.receipt = receipt;
    this.entries.set(messageId, entry);
    this.persist();
  }

  // External replacement or deletion of the journal between open and write
  // must stop the writer, not silently overwrite the new file.
  private verifyUnchanged(): void {
    try {
      const fd = openSync(this.path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
      try {
        const info = fstatSync(fd);
        if (!info.isFile() || info.mtimeMs !== this.lastMtimeMs || info.ino !== this.lastIno) {
          throw new Error("journal changed on disk");
        }
      } finally {
        closeSync(fd);
      }
    } catch (error) {
      if (error instanceof Error && error.message === "journal changed on disk") throw error;
      throw new Error("journal unavailable or unsafe");
    }
  }

  private persist(): void {
    const data = JSON.stringify({ journal_id: this.journalId, entries: Object.fromEntries(this.entries) }) + "\n";
    const tmp = join(dirname(this.path), `.${basename(this.path)}.${process.pid}.tmp`);
    const fd = openSync(tmp, constants.O_WRONLY | constants.O_CREAT | constants.O_TRUNC, 0o600);
    try {
      writeSync(fd, data);
      fsyncSync(fd);
    } finally {
      closeSync(fd);
    }
    renameSync(tmp, this.path);
    const dir = openSync(dirname(this.path), constants.O_RDONLY);
    try {
      fsyncSync(dir);
    } finally {
      closeSync(dir);
    }
    this.stamp();
  }
}

function readPrivateStamped(path: string): { content: string; mtimeMs: number; ino: number } {
  // O_NONBLOCK keeps a FIFO or device from blocking the Pi event loop; the
  // fstat check refuses anything but a regular owned file.
  const fd = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const info = fstatSync(fd);
    if (!info.isFile() || info.uid !== process.getuid?.() || (info.mode & 0o7777) !== 0o600)
      throw new Error("journal unavailable or unsafe");
    const chunks: Buffer[] = [];
    const buffer = Buffer.alloc(65536);
    for (;;) {
      const n = readSync(fd, buffer, 0, buffer.length, null);
      if (n === 0) break;
      chunks.push(Buffer.from(buffer.subarray(0, n)));
    }
    return { content: Buffer.concat(chunks).toString("utf8"), mtimeMs: info.mtimeMs, ino: info.ino };
  } finally {
    closeSync(fd);
  }
}
