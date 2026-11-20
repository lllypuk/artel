-- Owner task messages bound to one explicitly enabled Pi session.
-- Assignment text lives only here, never in observer telemetry or domain events.
CREATE TABLE delivery_bindings (
 id TEXT PRIMARY KEY,
 verifier TEXT NOT NULL UNIQUE CHECK(length(verifier)=64),
 record TEXT NOT NULL CHECK(json_valid(record))
);
CREATE TABLE delivery_messages (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL,
 binding_id TEXT NOT NULL,
 receipt_key TEXT NOT NULL UNIQUE,
 blocked INTEGER NOT NULL DEFAULT 0 CHECK(blocked IN (0,1)),
 block_reason TEXT,
 record TEXT NOT NULL CHECK(json_valid(record))
);
CREATE INDEX delivery_messages_task ON delivery_messages(task_id, seq);
-- One spend per message: the unique index is the single-writer admission gate.
CREATE TABLE delivery_attempts (
 attempt_id TEXT PRIMARY KEY,
 message_seq INTEGER NOT NULL UNIQUE,
 binding_id TEXT NOT NULL,
 writer_id TEXT NOT NULL,
 state TEXT NOT NULL CHECK(state IN ('spent','verified','observed')),
 record TEXT NOT NULL CHECK(json_valid(record))
);
CREATE TABLE delivery_receipts (
 principal TEXT NOT NULL,
 key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(principal,key)
);
PRAGMA user_version=3;
