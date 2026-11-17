import { test } from "node:test";
import assert from "node:assert/strict";
import { mkdtempSync, writeFileSync, rmSync, chmodSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { randomUUID, createHash } from "node:crypto";
import { readConfirmedConfig } from "../src/config.ts";

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
