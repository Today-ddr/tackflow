CREATE TABLE change_event (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  resource TEXT NOT NULL CHECK (resource IN ('task', 'project', 'tag')),
  resource_id INTEGER NOT NULL,
  kind TEXT NOT NULL,
  created_at TEXT NOT NULL
);

CREATE TABLE webhook (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  url TEXT NOT NULL,
  events TEXT NOT NULL CHECK (json_valid(events)),
  secret TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
  created_at TEXT NOT NULL
);

CREATE TABLE webhook_delivery (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  webhook_id INTEGER NOT NULL REFERENCES webhook(id) ON DELETE CASCADE,
  event_id INTEGER NOT NULL REFERENCES change_event(id),
  status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'in_flight', 'delivered', 'failed', 'cancelled')),
  attempts INTEGER NOT NULL DEFAULT 0,
  next_attempt_at TEXT NOT NULL,
  last_error TEXT NOT NULL DEFAULT '',
  delivered_at TEXT,
  lease_until TEXT,
  lease_token TEXT,
  UNIQUE(webhook_id, event_id)
);

CREATE INDEX webhook_delivery_due_idx ON webhook_delivery(status, next_attempt_at, id);
CREATE INDEX webhook_delivery_hook_idx ON webhook_delivery(webhook_id, id);

CREATE TRIGGER change_event_enqueue AFTER INSERT ON change_event BEGIN
  INSERT INTO webhook_delivery(webhook_id, event_id, next_attempt_at)
  SELECT webhook.id, NEW.id, NEW.created_at FROM webhook
  WHERE enabled = 1 AND EXISTS (
    SELECT 1 FROM json_each(webhook.events)
    WHERE value IN ('*', NEW.resource || '.*')
  );
END;

CREATE TRIGGER webhook_cancel_pending AFTER UPDATE OF enabled, url, events ON webhook
WHEN NEW.enabled = 0 OR NEW.url IS NOT OLD.url OR NEW.events IS NOT OLD.events BEGIN
  UPDATE webhook_delivery SET status = 'cancelled', lease_until = NULL, lease_token = NULL
  WHERE webhook_id = NEW.id AND status IN ('pending', 'in_flight');
END;

CREATE TRIGGER task_event_change AFTER INSERT ON task_event
WHEN NEW.kind NOT IN ('deleted', 'restored') BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('task', NEW.task_id, NEW.kind, NEW.created_at);
END;

CREATE TRIGGER task_deleted_change AFTER UPDATE OF deleted_at ON task
WHEN NEW.deleted_at IS NOT OLD.deleted_at BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('task', NEW.id, CASE WHEN NEW.deleted_at IS NULL THEN 'restored' ELSE 'deleted' END,
          strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
END;

CREATE TRIGGER task_purged_change AFTER DELETE ON task BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('task', OLD.id, 'purged', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
END;

CREATE TRIGGER project_created_change AFTER INSERT ON project BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('project', NEW.id, 'created', NEW.created_at);
END;

CREATE TRIGGER project_updated_change AFTER UPDATE ON project
WHEN NEW.name IS NOT OLD.name OR NEW.color IS NOT OLD.color OR NEW.position IS NOT OLD.position
  OR NEW.archived_at IS NOT OLD.archived_at BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('project', NEW.id,
    CASE WHEN NEW.archived_at IS NOT OLD.archived_at
      THEN CASE WHEN NEW.archived_at IS NULL THEN 'restored' ELSE 'archived' END
      ELSE 'updated' END,
    strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
END;

CREATE TRIGGER project_deleted_change AFTER DELETE ON project BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('project', OLD.id, 'deleted', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
END;

CREATE TRIGGER tag_created_change AFTER INSERT ON tag BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('tag', NEW.id, 'created', NEW.created_at);
END;

CREATE TRIGGER tag_updated_change AFTER UPDATE ON tag
WHEN NEW.name IS NOT OLD.name OR NEW.color IS NOT OLD.color BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('tag', NEW.id, 'updated', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
END;

CREATE TRIGGER tag_deleted_change AFTER DELETE ON tag BEGIN
  INSERT INTO change_event(resource, resource_id, kind, created_at)
  VALUES ('tag', OLD.id, 'deleted', strftime('%Y-%m-%dT%H:%M:%SZ', 'now'));
END;
