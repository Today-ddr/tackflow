ALTER TABLE session ADD COLUMN sudo_until TEXT;

CREATE INDEX task_tag_tag_idx ON task_tag(tag_id);
CREATE INDEX task_due_idx ON task(due_at) WHERE due_at IS NOT NULL AND deleted_at IS NULL;
