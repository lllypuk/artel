CREATE TABLE observers (
 id TEXT PRIMARY KEY,
 verifier TEXT NOT NULL UNIQUE CHECK(length(verifier)=64),
 record TEXT NOT NULL CHECK(json_valid(record))
);
-- Identities survive payload pruning, without retaining registration metadata.
CREATE TABLE observer_identities (
 observer_id TEXT NOT NULL REFERENCES observers(id),
 runtime_id TEXT NOT NULL,
 retired INTEGER NOT NULL DEFAULT 0 CHECK(retired IN (0,1)),
 PRIMARY KEY(observer_id,runtime_id)
);
CREATE TABLE observer_runtimes (
 observer_id TEXT NOT NULL,
 runtime_id TEXT NOT NULL,
 record TEXT NOT NULL CHECK(json_valid(record)),
 PRIMARY KEY(observer_id,runtime_id),
 FOREIGN KEY(observer_id,runtime_id) REFERENCES observer_identities(observer_id,runtime_id)
);
CREATE TABLE observer_receipts (
 principal TEXT NOT NULL,
 key TEXT NOT NULL,
 request_hash TEXT NOT NULL,
 result TEXT NOT NULL CHECK(json_valid(result)),
 PRIMARY KEY(principal,key)
);
CREATE TABLE observer_audit (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 observer_id TEXT NOT NULL REFERENCES observers(id),
 action TEXT NOT NULL,
 timestamp TEXT NOT NULL
);
PRAGMA user_version=2;
