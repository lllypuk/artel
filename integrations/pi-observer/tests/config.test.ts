import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync, chmodSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomUUID, createHash } from "node:crypto";
import { readConfirmedConfig, readDeliveryConfig } from "../src/config.ts";

test("delivery config preserves 4a absence and accepts only an explicit boolean 4b opt-in", () => {
  const dir = mkdtempSync(join(tmpdir(), "artel-delivery-config-"));
  chmodSync(dir, 0o700);
  const path = join(dir, "delivery.json"), tokenPath = join(dir, "token");
  const config: { status: string; delivery_binding_id: string; database_instance_id: string; observer_socket: string;
    token_file: string; journal_file: string; journal_id: string; allow_owner_questions?: unknown } = {
    status: "confirmed", delivery_binding_id: randomUUID(), database_instance_id: randomUUID(),
    observer_socket: join(dir, "observer.sock"), token_file: tokenPath,
    journal_file: join(dir, "journal.json"), journal_id: randomUUID(),
  };
  try {
    writeFileSync(tokenPath, "ab".repeat(32) + "\n", { mode: 0o600 });
    writeFileSync(path, JSON.stringify(config), { mode: 0o600 });
    assert.equal(readDeliveryConfig(path).config.allow_owner_questions, undefined);
    config.allow_owner_questions = false;
    writeFileSync(path, JSON.stringify(config), { mode: 0o600 });
    assert.equal(readDeliveryConfig(path).config.allow_owner_questions, false);
    config.allow_owner_questions = true;
    writeFileSync(path, JSON.stringify(config), { mode: 0o600 });
    assert.equal(readDeliveryConfig(path).config.allow_owner_questions, true);
    config.allow_owner_questions = "true";
    writeFileSync(path, JSON.stringify(config), { mode: 0o600 });
    assert.throws(() => readDeliveryConfig(path), /pending or unsafe/);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});

test("consumer rejects pending/unsafe provisioning artifacts before runtime startup", () => {
  const dir = mkdtempSync(join(tmpdir(), "artel-config-"));
  const path = join(dir, "config.json"), tokenPath = join(dir, "observer.token");
  const token = "ab".repeat(32);
  const config = { status: "pending", observer_id: randomUUID(), database_instance_id: randomUUID(),
    observer_socket: join(dir, "observer.sock"), token_file: tokenPath, provisioning_key: randomUUID(),
    payload: { label: "Fictional", verifier: createHash("sha256").update(token).digest("hex") } };
  const save = () => writeFileSync(path, JSON.stringify(config), { mode: 0o600 });
  try {
    writeFileSync(tokenPath, token + "\n", { mode: 0o600 }); save();
    assert.throws(() => readConfirmedConfig(path), /pending or unsafe/);
    config.status = "confirmed"; save();
    assert.equal(readConfirmedConfig(path).config.observer_id, config.observer_id);
    chmodSync(tokenPath, 0o644);
    assert.throws(() => readConfirmedConfig(path), /pending or unsafe/);
    chmodSync(tokenPath, 0o600);
    config.payload.verifier = "cd".repeat(32); save();
    assert.throws(() => readConfirmedConfig(path), /pending or unsafe/);
    config.payload.verifier = createHash("sha256").update(token).digest("hex"); save();
    const link = join(dir, "link.json"); symlinkSync(path, link);
    assert.throws(() => readConfirmedConfig(link), /pending or unsafe/);
    chmodSync(dir, 0o755);
    assert.throws(() => readConfirmedConfig(path), /pending or unsafe/);
  } finally { rmSync(dir, { recursive: true, force: true }); }
});
