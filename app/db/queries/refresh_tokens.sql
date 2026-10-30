-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, token_hash, user_agent, ip, expires_at)
VALUES (@user_id, @token_hash, @user_agent, @ip, @expires_at)
RETURNING id;

-- name: GetRefreshTokenByHashForUpdate :one
SELECT * FROM refresh_tokens WHERE token_hash = @token_hash FOR UPDATE;

-- name: RevokeRefreshToken :exec
UPDATE refresh_tokens
SET revoked_at = now(), replaced_by = @replaced_by
WHERE id = @id AND revoked_at IS NULL;

-- name: RevokeRefreshTokenByHash :exec
UPDATE refresh_tokens SET revoked_at = now()
WHERE token_hash = @token_hash AND revoked_at IS NULL;

-- name: RevokeUserRefreshTokens :exec
UPDATE refresh_tokens SET revoked_at = now()
WHERE user_id = @user_id AND revoked_at IS NULL;
