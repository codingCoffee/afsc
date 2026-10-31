-- name: LockUserSignup :exec
SELECT pg_advisory_xact_lock(hashtext('afsc_user_signup'));

-- name: CreateUser :one
INSERT INTO users (username, email, password_hash, first_name, last_name, user_type)
VALUES (
  @username, @email, @password_hash, @first_name, @last_name,
  CASE WHEN EXISTS (SELECT 1 FROM users) THEN 'user' ELSE 'admin' END
)
RETURNING *;

-- name: GetUserByLogin :one
SELECT * FROM users WHERE username = @login OR email = @login;

-- name: GetUserByID :one
SELECT * FROM users WHERE id = @id;

-- name: CreateUserWithType :one
INSERT INTO users (username, email, password_hash, first_name, last_name, user_type)
VALUES (@username, @email, @password_hash, @first_name, @last_name, @user_type)
RETURNING *;

-- name: ListUsers :many
SELECT * FROM users ORDER BY created_at ASC, id ASC LIMIT @lim OFFSET @off;

-- name: CountUsers :one
SELECT count(*) FROM users;

-- name: CountAdmins :one
SELECT count(*) FROM users WHERE user_type = 'admin';

-- name: DeleteUser :execrows
DELETE FROM users WHERE id = @id;
