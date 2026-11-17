import { constants, lstatSync, openSync, fstatSync, readSync, closeSync } from "node:fs";
import { dirname, isAbsolute, normalize } from "node:path";
import { createHash } from "node:crypto";

export interface ObserverConfig {
  status: "confirmed";
  observer_id: string;
  database_instance_id: string;
  observer_socket: string;
  token_file: string;
  provisioning_key: string;
  payload: { label: string; verifier: string };
}

function privateBytes(path: string, limit: number): Buffer {
  if (!isAbsolute(path) || normalize(path) !== path) throw new Error("observer config unavailable or unsafe");
  for (let part = path; ; part = dirname(part)) {
    if (lstatSync(part).isSymbolicLink()) throw new Error("observer config unavailable or unsafe");
    if (part === dirname(part)) break;
  }
  const parent = lstatSync(dirname(path));
  if (!parent.isDirectory() || (parent.mode & 0o7777) !== 0o700 || parent.uid !== process.getuid?.())
    throw new Error("observer config unavailable or unsafe");
  const fd = openSync(path, constants.O_RDONLY | constants.O_NOFOLLOW | constants.O_NONBLOCK);
  try {
    const info = fstatSync(fd);
    if (!info.isFile() || info.uid !== process.getuid?.() || (info.mode & 0o7777) !== 0o600 || info.size > limit)
      throw new Error("observer config unavailable or unsafe");
    // Positional bounded read cannot block on special files; type checked above.
    const bytes = Buffer.alloc(limit + 1);
    let length = 0;
    while (length < bytes.length) {
      const n = readSync(fd, bytes, length, bytes.length - length, length);
      if (n === 0) break;
      length += n;
    }
    if (length > limit) throw new Error("observer config unavailable or unsafe");
    return bytes.subarray(0, length);
  } finally { closeSync(fd); }
}

// Consumer boundary: pending provisioning records are never runtime config.
export function readConfirmedConfig(path: string): { config: ObserverConfig; token: string } {
  try {
    const config = JSON.parse(privateBytes(path, 16384).toString("utf8")) as ObserverConfig;
    const id = /^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$/;
    if (config.status !== "confirmed" || !id.test(config.observer_id) || !id.test(config.database_instance_id) ||
        !id.test(config.provisioning_key) || !isAbsolute(config.observer_socket) ||
        Buffer.byteLength(config.observer_socket) > 107 || !config.payload ||
        typeof config.payload.label !== "string" || !config.payload.label ||
        !/^[0-9a-f]{64}$/.test(config.payload.verifier))
      throw new Error("invalid config");
    const text = privateBytes(config.token_file, 65).toString("utf8");
    if (!/^[0-9a-f]{64}\n$/.test(text)) throw new Error("invalid token");
    const token = text.slice(0, 64);
    if (createHash("sha256").update(token).digest("hex") !== config.payload.verifier)
      throw new Error("credential mismatch");
    return { config, token };
  } catch { throw new Error("observer config unavailable, pending or unsafe"); }
}
