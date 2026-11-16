CREATE TABLE user (
  id            INTEGER PRIMARY KEY CHECK (id = 1),
  username      TEXT NOT NULL,
  password_hash TEXT NOT NULL,
  created_at    TEXT NOT NULL,
  updated_at    TEXT NOT NULL
);

CREATE TABLE session (
  id           TEXT PRIMARY KEY,
  user_id      INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
  created_at   TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  last_seen_at TEXT NOT NULL,
  user_agent   TEXT,
  ip           TEXT
);

CREATE INDEX session_expires_idx ON session(expires_at);

CREATE TABLE api_token (
  id           INTEGER PRIMARY KEY,
  name         TEXT NOT NULL,
  prefix       TEXT NOT NULL,
  hash         TEXT NOT NULL UNIQUE,
  scopes       TEXT NOT NULL,
  created_at   TEXT NOT NULL,
  last_used_at TEXT,
  revoked_at   TEXT
);

CREATE TABLE project (
  id          INTEGER PRIMARY KEY,
  name        TEXT NOT NULL UNIQUE,
  color       TEXT NOT NULL,
  position    REAL NOT NULL,
  archived_at TEXT,
  created_at  TEXT NOT NULL
);

CREATE TABLE task (
  id           INTEGER PRIMARY KEY,
  project_id   INTEGER REFERENCES project(id),
  title        TEXT NOT NULL,
  notes        TEXT NOT NULL DEFAULT '',
  status       TEXT NOT NULL CHECK (status IN ('inbox', 'todo', 'doing', 'review', 'done')),
  priority     INTEGER NOT NULL DEFAULT 0,
  position     REAL NOT NULL,
  due_at       TEXT,
  agent_ready  INTEGER NOT NULL DEFAULT 0,
  created_at   TEXT NOT NULL,
  updated_at   TEXT NOT NULL,
  completed_at TEXT,
  deleted_at   TEXT
);

CREATE INDEX task_board_idx ON task(deleted_at, status, position);
CREATE INDEX task_project_idx ON task(project_id, status);
CREATE INDEX task_updated_idx ON task(updated_at);

CREATE TABLE tag (
  id         INTEGER PRIMARY KEY,
  name       TEXT NOT NULL UNIQUE,
  color      TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE task_tag (
  task_id INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  tag_id  INTEGER NOT NULL REFERENCES tag(id) ON DELETE CASCADE,
  PRIMARY KEY (task_id, tag_id)
);

CREATE TABLE task_event (
  id         INTEGER PRIMARY KEY,
  task_id    INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  kind       TEXT NOT NULL,
  payload    TEXT NOT NULL DEFAULT '{}',
  created_at TEXT NOT NULL
);

CREATE INDEX task_event_task_idx ON task_event(task_id, created_at);

CREATE TABLE task_link (
  id          INTEGER PRIMARY KEY,
  task_id     INTEGER NOT NULL REFERENCES task(id) ON DELETE CASCADE,
  kind        TEXT NOT NULL,
  external_id TEXT NOT NULL,
  url         TEXT,
  created_at  TEXT NOT NULL,
  UNIQUE (kind, external_id)
);

CREATE INDEX task_link_task_idx ON task_link(task_id);

CREATE TABLE setting (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE idempotency (
  key        TEXT PRIMARY KEY,
  status     INTEGER NOT NULL,
  body       TEXT NOT NULL,
  created_at TEXT NOT NULL
);
