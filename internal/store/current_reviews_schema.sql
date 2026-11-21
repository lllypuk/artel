-- SQLite appends the rowid to this index, preserving page order within a candidate.
CREATE INDEX IF NOT EXISTS task_reviews_current_candidate ON task_reviews(task_id,result_id);
PRAGMA user_version=6;
