CREATE TABLE passkey (
  id              TEXT PRIMARY KEY,
  user_id         INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
  name            TEXT NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
  credential_id   BLOB NOT NULL UNIQUE,
  credential_json TEXT NOT NULL,
  transports      TEXT NOT NULL,
  sign_count      INTEGER NOT NULL CHECK (sign_count BETWEEN 0 AND 4294967295),
  version         INTEGER NOT NULL DEFAULT 1,
  created_at      TEXT NOT NULL,
  last_used_at    TEXT
);

CREATE INDEX passkey_user_idx ON passkey(user_id, created_at);

CREATE TABLE passkey_challenge (
  id           TEXT PRIMARY KEY,
  purpose      TEXT NOT NULL CHECK (purpose IN ('register', 'login')),
  user_id      INTEGER NOT NULL REFERENCES user(id) ON DELETE CASCADE,
  session_id   TEXT REFERENCES session(id) ON DELETE CASCADE,
  binding      TEXT NOT NULL,
  name         TEXT NOT NULL DEFAULT '',
  session_data TEXT NOT NULL,
  expires_at   TEXT NOT NULL,
  CHECK ((purpose = 'register' AND session_id IS NOT NULL) OR (purpose = 'login' AND session_id IS NULL)),
  UNIQUE (purpose, binding)
);

CREATE INDEX passkey_challenge_expiry_idx ON passkey_challenge(expires_at);
