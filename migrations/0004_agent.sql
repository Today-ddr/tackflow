CREATE TABLE agent_claim (
  task_id INTEGER PRIMARY KEY REFERENCES task(id) ON DELETE CASCADE,
  claim_hash TEXT NOT NULL UNIQUE,
  owner TEXT NOT NULL,
  worker TEXT NOT NULL,
  expires_at TEXT NOT NULL,
  invalidated INTEGER NOT NULL DEFAULT 0 CHECK (invalidated IN (0, 1)),
  result_hash TEXT,
  result_json TEXT
);

CREATE INDEX agent_claim_expiry_idx ON agent_claim(expires_at)
WHERE invalidated = 0 AND result_hash IS NULL;

CREATE INDEX task_agent_queue_idx ON task(status, position, id)
WHERE deleted_at IS NULL AND agent_ready = 1;

CREATE INDEX task_trash_idx ON task(deleted_at DESC, id DESC)
WHERE deleted_at IS NOT NULL;

CREATE TRIGGER task_agent_claim_invalidate AFTER UPDATE ON task BEGIN
  UPDATE agent_claim SET invalidated = 1 WHERE task_id = NEW.id;
END;

CREATE TRIGGER task_tag_agent_claim_insert AFTER INSERT ON task_tag BEGIN
  UPDATE agent_claim SET invalidated = 1 WHERE task_id = NEW.task_id;
END;

CREATE TRIGGER task_tag_agent_claim_delete AFTER DELETE ON task_tag BEGIN
  UPDATE agent_claim SET invalidated = 1 WHERE task_id = OLD.task_id;
END;

CREATE TRIGGER task_tag_agent_claim_update AFTER UPDATE ON task_tag BEGIN
  UPDATE agent_claim SET invalidated = 1 WHERE task_id IN (OLD.task_id, NEW.task_id);
END;
