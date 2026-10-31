-- name: CreateRefreshToken :one
INSERT INTO refresh_tokens (user_id, session_id, token_hash, user_agent, ip, expires_at)
VALUES (@user_id, @session_id, @token_hash, @user_agent, @ip, @expires_at)
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

-- name: GetActiveSession :one
SELECT u.id, u.user_type
FROM refresh_tokens rt
JOIN users u ON u.id = rt.user_id
WHERE rt.session_id = @session_id AND rt.revoked_at IS NULL AND rt.expires_at > now()
LIMIT 1;

-- name: ListRefreshTokens :many
SELECT rt.id, rt.session_id, rt.user_id, u.username, rt.user_agent, rt.ip,
       rt.created_at, rt.expires_at, rt.revoked_at
FROM refresh_tokens rt
JOIN users u ON u.id = rt.user_id
WHERE NOT @active_only::bool OR (rt.revoked_at IS NULL AND rt.expires_at > now())
ORDER BY rt.created_at DESC, rt.id DESC
LIMIT @lim OFFSET @off;

-- name: CountRefreshTokens :one
SELECT count(*) FROM refresh_tokens
WHERE NOT @active_only::bool OR (revoked_at IS NULL AND expires_at > now());

-- name: RevokeSessionByTokenID :execrows
UPDATE refresh_tokens rt SET revoked_at = now()
WHERE rt.revoked_at IS NULL
  AND rt.session_id = (SELECT src.session_id FROM refresh_tokens src WHERE src.id = @id);

-- name: DeleteExpiredRefreshTokens :execrows
DELETE FROM refresh_tokens WHERE expires_at < now();
