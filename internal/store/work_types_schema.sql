-- Rebuild tasks because the v6 state CHECK cannot represent Epic states.
-- migrate disables FK enforcement before the transaction and checks it before commit.
CREATE TABLE tasks_v7 (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id),
 kind TEXT NOT NULL CHECK(kind IN ('epic','task','bug')),
 parent_id TEXT,
 state TEXT NOT NULL,
 record TEXT NOT NULL CHECK(json_valid(record)),
 UNIQUE(id,project_id),
 CHECK(parent_id IS NULL OR parent_id!=id),
 CHECK((kind='epic' AND state IN ('open','in_progress','closed') AND parent_id IS NULL)
    OR (kind IN ('task','bug') AND state IN ('backlog','queued','in_progress','done','cancelled'))),
 FOREIGN KEY(parent_id,project_id) REFERENCES tasks_v7(id,project_id)
);
INSERT INTO tasks_v7(id,project_id,kind,parent_id,state,record)
SELECT id,project_id,'task',NULL,state,json_set(record,'$.kind','task','$.parent_id',NULL) FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_v7 RENAME TO tasks;
CREATE UNIQUE INDEX one_current ON tasks(project_id) WHERE state='in_progress' AND kind IN ('task','bug');
PRAGMA user_version=7;
