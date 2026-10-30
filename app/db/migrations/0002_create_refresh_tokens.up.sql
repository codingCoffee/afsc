CREATE TABLE refresh_tokens (
  id          uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id     uuid        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
  token_hash  bytea       NOT NULL UNIQUE,
  user_agent  text        NOT NULL DEFAULT '',
  ip          text        NOT NULL DEFAULT '',
  created_at  timestamptz NOT NULL DEFAULT now(),
  expires_at  timestamptz NOT NULL,
  revoked_at  timestamptz,
  replaced_by uuid        REFERENCES refresh_tokens (id) ON DELETE SET NULL
);

CREATE INDEX refresh_tokens_user_id_idx ON refresh_tokens (user_id);
