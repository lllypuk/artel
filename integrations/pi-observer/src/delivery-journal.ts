import { closeSync, constants, fsyncSync, openSync, readSync, renameSync, writeSync } from "node:fs";
import { basename, dirname, join } from "node:path";

export interface JournalEntry {
  attempt_id?: string;
  receipt?: { type: string; message_type: string; published: boolean };
}

// Private single-document journal next to the delivery config. All writes go
// through a temporary file + atomic rename + directory fsync; the journal ID is
// pinned at creation and verified at every open.
export class DeliveryJournal {
  readonly journalId: string;
  private readonly path: string;
  private entries = new Map<string, JournalEntry>();

  private constructor(path: string, journalId: string, entries: Map<string, JournalEntry>) {
    this.path = path;
    this.journalId = journalId;
    this.entries = entries;
  }

  static create(path: string, journalId: string): DeliveryJournal {
    const journal = new DeliveryJournal(path, journalId, new Map());
    journal.persist();
    return journal;
  }

  static open(path: string, journalId: string): DeliveryJournal {
    const raw = JSON.parse(readPrivate(path)) as { journal_id?: string; entries?: Record<string, JournalEntry> };
    if (raw.journal_id !== journalId || typeof raw.entries !== "object" || raw.entries === null)
      throw new Error("journal mismatch");
    return new DeliveryJournal(path, journalId, new Map(Object.entries(raw.entries)));
  }

  entry(messageId: string): JournalEntry | undefined {
    return this.entries.get(messageId);
  }

  recordAttempt(messageId: string, attemptId: string): void {
    const entry = this.entries.get(messageId) ?? {};
    entry.attempt_id = attemptId;
    this.entries.set(messageId, entry);
    this.persist();
  }

  recordReceipt(messageId: string, receipt: JournalEntry["receipt"]): void {
    const entry = this.entries.get(messageId) ?? {};
    entry.receipt = receipt;
    this.entries.set(messageId, entry);
    this.persist();
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
  }
}

function readPrivate(path: string): string {
  const fd = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW);
  try {
    const chunks: Buffer[] = [];
    const buffer = Buffer.alloc(65536);
    for (;;) {
      const n = readSync(fd, buffer, 0, buffer.length, null);
      if (n === 0) break;
      chunks.push(Buffer.from(buffer.subarray(0, n)));
    }
    return Buffer.concat(chunks).toString("utf8");
  } finally {
    closeSync(fd);
  }
}
