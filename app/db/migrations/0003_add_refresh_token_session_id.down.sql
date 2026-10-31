DROP INDEX IF EXISTS refresh_tokens_expires_at_idx;
DROP INDEX IF EXISTS refresh_tokens_session_id_idx;
ALTER TABLE refresh_tokens DROP COLUMN IF EXISTS session_id;
