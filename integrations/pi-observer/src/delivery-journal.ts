import { closeSync, constants, fstatSync, fsyncSync, openSync, readSync, renameSync, rmSync, writeFileSync, type Stats } from "node:fs";
import { basename, dirname, join } from "node:path";

const UUID_PATTERN = /^(?!00000000-0000-0000-0000-000000000000$)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;

export class DeliveryJournalError extends Error {}

function requirePrivateFile(info: Stats): void {
  if (!info.isFile() || info.uid !== process.getuid?.() || (info.mode & 0o7777) !== 0o600)
    throw new DeliveryJournalError("journal unavailable or unsafe");
}

export interface JournalEntry {
  attempt_id?: string;
  receipt_key?: string;
  kind?: "answer";
  question_id?: string;
  receipt?: { type: string; message_type: string; published: boolean };
}

// Private single-document journal next to the delivery config. All writes go
// through a temporary file + atomic rename + directory fsync; the journal ID is
// pinned at creation and verified at every open.
export class DeliveryJournal {
  readonly journalId: string;
  private readonly path: string;
  private entries = new Map<string, JournalEntry>();
  private stamp: Stats | undefined;

  private constructor(path: string, journalId: string, entries: Map<string, JournalEntry>) {
    this.path = path;
    this.journalId = journalId;
    this.entries = entries;
  }

  static create(path: string, journalId: string): DeliveryJournal {
    const journal = new DeliveryJournal(path, journalId, new Map());
    journal.persist(new Map());
    return journal;
  }
  static open(path: string, journalId: string): DeliveryJournal {
    // Content and identity come from one open session: validating one file
    // while recording another file's timestamps would admit a swap.
    const { content, info } = readPrivateStamped(path);
    const raw = JSON.parse(content) as { journal_id?: string; entries?: Record<string, JournalEntry> };
    if (raw.journal_id !== journalId || typeof raw.entries !== "object" || raw.entries === null || Array.isArray(raw.entries))
      throw new DeliveryJournalError("journal mismatch");
    for (const [id, entry] of Object.entries(raw.entries)) {
      // A malformed entry must disable delivery, never crash or derail Pi.
      if (!UUID_PATTERN.test(id) || typeof entry !== "object" || entry === null || Array.isArray(entry)) {
        throw new DeliveryJournalError("journal mismatch");
      }
      const record = entry as Record<string, unknown>;
      if ((record.attempt_id !== undefined && !UUID_PATTERN.test(String(record.attempt_id))) ||
          (record.receipt_key !== undefined && !UUID_PATTERN.test(String(record.receipt_key))) ||
          (record.kind !== undefined && record.kind !== "answer") ||
          (record.kind === "answer" && (typeof record.question_id !== "string" || !UUID_PATTERN.test(record.question_id))) ||
          (record.kind !== "answer" && record.question_id !== undefined) ||
          (record.receipt !== undefined && (typeof record.receipt !== "object" || record.receipt === null ||
            (record.receipt as { type?: unknown }).type !== "before_agent_start" ||
            (record.receipt as { message_type?: unknown }).message_type !== "custom" ||
            typeof (record.receipt as { published?: unknown }).published !== "boolean"))) {
        throw new DeliveryJournalError("journal mismatch");
      }
    }
    const journal = new DeliveryJournal(path, journalId, new Map(Object.entries(raw.entries)));
    journal.stamp = info;
    return journal;
  }

  entry(messageId: string): JournalEntry | undefined {
    return this.entries.get(messageId);
  }

  entriesView(): ReadonlyMap<string, JournalEntry> {
    return new Map(this.entries);
  }

  recordAttempt(messageId: string, attemptId: string, receiptKey: string,
    kind?: "answer", questionId?: string): void {
    this.assertUnchanged();
    const entries = new Map(this.entries);
    entries.set(messageId, {
      ...entries.get(messageId), attempt_id: attemptId, receipt_key: receiptKey,
      ...(kind === "answer" ? { kind, question_id: questionId } : {}),
    });
    this.persist(entries);
  }

  recordReceipt(messageId: string, receipt: JournalEntry["receipt"]): void {
    this.assertUnchanged();
    const entries = new Map(this.entries);
    entries.set(messageId, { ...entries.get(messageId), receipt });
    this.persist(entries);
  }

  // External replacement or deletion must fence both a new spend and the final
  // synchronous Pi call, not just a later journal write.
  assertUnchanged(): void {
    try {
      const fd = openSync(this.path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
      try {
        const info = fstatSync(fd);
        requirePrivateFile(info);
        if (!this.stamp || info.dev !== this.stamp.dev || info.ino !== this.stamp.ino ||
            info.mtimeMs !== this.stamp.mtimeMs || info.ctimeMs !== this.stamp.ctimeMs) {
          throw new DeliveryJournalError("journal changed on disk");
        }
      } finally {
        closeSync(fd);
      }
    } catch (error) {
      if (error instanceof DeliveryJournalError) throw error;
      throw new DeliveryJournalError("journal unavailable or unsafe");
    }
  }

  private persist(entries: Map<string, JournalEntry>): void {
    const tmp = join(dirname(this.path), `.${basename(this.path)}.${process.pid}.tmp`);
    let created = false;
    try {
      const data = JSON.stringify({ journal_id: this.journalId, entries: Object.fromEntries(entries) }) + "\n";
      const fd = openSync(tmp, constants.O_WRONLY | constants.O_CREAT | constants.O_EXCL, 0o600);
      created = true;
      try {
        writeFileSync(fd, data);
        fsyncSync(fd);
        if (this.stamp) this.assertUnchanged();
        renameSync(tmp, this.path);
        created = false;
        const dir = openSync(dirname(this.path), constants.O_RDONLY);
        try {
          fsyncSync(dir);
        } finally {
          closeSync(dir);
        }
        this.stamp = fstatSync(fd);
      } finally {
        closeSync(fd);
      }
      this.assertUnchanged();
      this.entries = entries;
    } catch (error) {
      if (error instanceof DeliveryJournalError) throw error;
      throw new DeliveryJournalError("journal could not be persisted");
    } finally {
      if (created) {
        try { rmSync(tmp, { force: true }); } catch { /* persistence already failed */ }
      }
    }
  }
}

function readPrivateStamped(path: string): { content: string; info: Stats } {
  // O_NONBLOCK keeps a FIFO or device from blocking the Pi event loop; the
  // fstat check refuses anything but a regular owned file.
  const fd = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const info = fstatSync(fd);
    requirePrivateFile(info);
    const chunks: Buffer[] = [];
    const buffer = Buffer.alloc(65536);
    for (;;) {
      const n = readSync(fd, buffer, 0, buffer.length, null);
      if (n === 0) break;
      chunks.push(Buffer.from(buffer.subarray(0, n)));
    }
    return { content: Buffer.concat(chunks).toString("utf8"), info };
  } finally {
    closeSync(fd);
  }
}
