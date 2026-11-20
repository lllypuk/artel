-- Explicit Pi owner questions and their single answer. Text stays out of
-- observer telemetry and domain events; delivery remains in the 4a queue.
CREATE TABLE IF NOT EXISTS owner_questions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL,
 binding_id TEXT NOT NULL,
 status TEXT NOT NULL CHECK(status IN ('open','answered','closed')),
 answer_message_id TEXT UNIQUE,
 record TEXT NOT NULL CHECK(json_valid(record))
);
CREATE INDEX IF NOT EXISTS owner_questions_task ON owner_questions(task_id, seq);
PRAGMA user_version=4;
