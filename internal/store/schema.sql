CREATE TABLE metadata (id INTEGER PRIMARY KEY CHECK(id=1), instance_id TEXT NOT NULL);
CREATE TABLE projects (
 id TEXT PRIMARY KEY,
 root_path TEXT NOT NULL,
 archived INTEGER NOT NULL CHECK(archived IN (0,1)),
 record TEXT NOT NULL CHECK(json_valid(record))
);
CREATE UNIQUE INDEX active_root ON projects(root_path) WHERE archived=0;
CREATE TABLE tasks (
 id TEXT PRIMARY KEY,
 project_id TEXT NOT NULL REFERENCES projects(id),
 state TEXT NOT NULL CHECK(state IN ('backlog','queued','in_progress','done','cancelled')),
 record TEXT NOT NULL CHECK(json_valid(record)),
 UNIQUE(id,project_id)
);
CREATE UNIQUE INDEX one_current ON tasks(project_id) WHERE state='in_progress';
CREATE TABLE queue_entries (
 project_id TEXT NOT NULL REFERENCES projects(id),
 position INTEGER NOT NULL CHECK(position>=0),
 task_id TEXT NOT NULL UNIQUE,
 PRIMARY KEY(project_id,position),
 FOREIGN KEY(task_id,project_id) REFERENCES tasks(id,project_id)
);
CREATE TABLE events (
 seq INTEGER PRIMARY KEY AUTOINCREMENT,
 action TEXT NOT NULL,
 project_id TEXT NOT NULL REFERENCES projects(id),
 task_id TEXT REFERENCES tasks(id),
 source TEXT NOT NULL CHECK(source='owner_manual'),
 timestamp TEXT NOT NULL,
 changes TEXT NOT NULL CHECK(json_valid(changes))
);
CREATE TABLE mutation_receipts (
 key TEXT PRIMARY KEY,
 method TEXT NOT NULL,
 path TEXT NOT NULL,
 payload_hash TEXT NOT NULL,
 status INTEGER NOT NULL,
 body BLOB NOT NULL
);
PRAGMA application_id=1095914546;
PRAGMA user_version=1;
