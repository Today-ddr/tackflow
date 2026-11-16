DROP TRIGGER task_agent_claim_invalidate;

CREATE TRIGGER task_agent_claim_invalidate AFTER UPDATE ON task
WHEN NEW.title IS NOT OLD.title OR NEW.notes IS NOT OLD.notes
  OR NEW.status IS NOT OLD.status OR NEW.project_id IS NOT OLD.project_id
  OR NEW.priority IS NOT OLD.priority OR NEW.due_at IS NOT OLD.due_at
  OR NEW.agent_ready IS NOT OLD.agent_ready OR NEW.completed_at IS NOT OLD.completed_at
  OR NEW.deleted_at IS NOT OLD.deleted_at BEGIN
  UPDATE agent_claim SET invalidated = 1 WHERE task_id = NEW.id;
END;

CREATE TABLE agent_claim_replay (
  key TEXT PRIMARY KEY,
  owner TEXT NOT NULL,
  body BLOB NOT NULL,
  expires_at TEXT NOT NULL
);
