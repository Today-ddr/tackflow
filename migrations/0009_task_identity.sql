CREATE TABLE resource_sequence (
  resource TEXT PRIMARY KEY,
  last_id INTEGER NOT NULL CHECK (typeof(last_id) = 'integer' AND last_id >= 0)
);

INSERT INTO resource_sequence(resource, last_id)
SELECT 'task', MAX(
  COALESCE((SELECT MAX(id) FROM task), 0),
  COALESCE((SELECT MAX(resource_id) FROM change_event WHERE resource = 'task'), 0)
);
