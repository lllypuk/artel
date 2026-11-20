-- Explicit Pi owner questions and their single answer. Text stays out of
-- observer telemetry and domain events; delivery remains in the 4a queue.
CREATE TABLE IF NOT EXISTS owner_questions (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL REFERENCES tasks(id),
 binding_id TEXT NOT NULL REFERENCES delivery_bindings(id),
 status TEXT NOT NULL CHECK(status IN ('open','answered','closed')),
 answer_message_id TEXT UNIQUE REFERENCES delivery_messages(id),
 record TEXT NOT NULL CHECK(json_valid(record)),
 CHECK((status='answered') = (answer_message_id IS NOT NULL))
);
CREATE INDEX IF NOT EXISTS owner_questions_task ON owner_questions(task_id, seq);
-- Terminal history is immutable. Only open rows can acquire one answer or close.
CREATE TRIGGER IF NOT EXISTS owner_questions_terminal_immutable
BEFORE UPDATE ON owner_questions WHEN OLD.status!='open'
BEGIN SELECT RAISE(ABORT,'terminal owner question is immutable'); END;
PRAGMA user_version=4;
