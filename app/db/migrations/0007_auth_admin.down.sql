DROP TABLE audit_log;
DROP TABLE login_lockouts;
DROP TABLE login_failures;
ALTER TABLE users DROP COLUMN last_login_at;
ALTER TABLE refresh_tokens DROP COLUMN session_started_at;
DROP TABLE setup_tokens;
DROP TABLE user_invites;
