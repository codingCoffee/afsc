CREATE TABLE user_invites (
  token_hash bytea       PRIMARY KEY,
  user_id    uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  created_by uuid        REFERENCES users (id) ON DELETE SET NULL,
  expires_at timestamptz NOT NULL,
  used_at    timestamptz,
  created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_invites_user_id_idx ON user_invites (user_id);

CREATE TABLE setup_tokens (
  id         int         PRIMARY KEY CHECK (id = 1),
  token_hash bytea       NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now()
);

ALTER TABLE refresh_tokens ADD COLUMN session_started_at timestamptz;
UPDATE refresh_tokens SET session_started_at = created_at;
ALTER TABLE refresh_tokens
  ALTER COLUMN session_started_at SET NOT NULL,
  ALTER COLUMN session_started_at SET DEFAULT now();

ALTER TABLE users ADD COLUMN last_login_at timestamptz;

CREATE TABLE login_failures (
  key text        NOT NULL,
  at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX login_failures_key_at_idx ON login_failures (key, at);

CREATE TABLE login_lockouts (
  key        text        PRIMARY KEY,
  until      timestamptz NOT NULL,
  count      int         NOT NULL DEFAULT 1,
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE audit_log (
  id            bigserial   PRIMARY KEY,
  actor_user_id uuid        REFERENCES users (id) ON DELETE SET NULL,
  action        text        NOT NULL,
  entity_type   text        NOT NULL DEFAULT '',
  entity_id     text        NOT NULL DEFAULT '',
  diff          jsonb,
  ip            inet,
  created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_log_created_at_idx ON audit_log (created_at DESC);
CREATE INDEX audit_log_entity_idx ON audit_log (entity_type, entity_id);
CREATE INDEX audit_log_actor_idx ON audit_log (actor_user_id, created_at DESC);
