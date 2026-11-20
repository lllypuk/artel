-- Owner-submitted Git candidates and normalized review evidence. These records
-- are immutable; only the separate per-task current-result pointer changes.
CREATE TABLE IF NOT EXISTS task_results (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL,
 repo TEXT NOT NULL,
 base_sha TEXT NOT NULL,
 head_sha TEXT NOT NULL,
 record TEXT NOT NULL CHECK(json_valid(record)),
 UNIQUE(task_id,id),
 FOREIGN KEY(task_id) REFERENCES tasks(id)
);
CREATE INDEX IF NOT EXISTS task_results_task ON task_results(task_id,seq);
CREATE TABLE IF NOT EXISTS current_task_results (
 task_id TEXT PRIMARY KEY,
 result_id TEXT NOT NULL,
 FOREIGN KEY(task_id,result_id) REFERENCES task_results(task_id,id)
);
CREATE TABLE IF NOT EXISTS task_reviews (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 id TEXT NOT NULL UNIQUE,
 task_id TEXT NOT NULL,
 result_id TEXT NOT NULL,
 repo TEXT NOT NULL,
 revmux_task TEXT NOT NULL,
 run TEXT NOT NULL,
 payload_hash TEXT NOT NULL,
 record TEXT NOT NULL CHECK(json_valid(record)),
 UNIQUE(repo,revmux_task,run),
 FOREIGN KEY(task_id,result_id) REFERENCES task_results(task_id,id)
);
CREATE INDEX IF NOT EXISTS task_reviews_result ON task_reviews(task_id,result_id,seq);
CREATE TRIGGER IF NOT EXISTS task_results_immutable_update
BEFORE UPDATE ON task_results BEGIN SELECT RAISE(ABORT,'task result is immutable'); END;
CREATE TRIGGER IF NOT EXISTS task_results_immutable_delete
BEFORE DELETE ON task_results BEGIN SELECT RAISE(ABORT,'task result is immutable'); END;
CREATE TRIGGER IF NOT EXISTS task_reviews_immutable_update
BEFORE UPDATE ON task_reviews BEGIN SELECT RAISE(ABORT,'task review is immutable'); END;
CREATE TRIGGER IF NOT EXISTS task_reviews_immutable_delete
BEFORE DELETE ON task_reviews BEGIN SELECT RAISE(ABORT,'task review is immutable'); END;
PRAGMA user_version=5;
