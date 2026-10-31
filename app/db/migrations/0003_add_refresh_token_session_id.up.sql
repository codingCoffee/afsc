ALTER TABLE refresh_tokens ADD COLUMN session_id uuid;
UPDATE refresh_tokens SET session_id = id;
ALTER TABLE refresh_tokens ALTER COLUMN session_id SET NOT NULL;

CREATE INDEX refresh_tokens_session_id_idx ON refresh_tokens (session_id);
CREATE INDEX refresh_tokens_expires_at_idx ON refresh_tokens (expires_at);
