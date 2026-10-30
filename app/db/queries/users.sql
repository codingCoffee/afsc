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
