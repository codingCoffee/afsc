ALTER TABLE users ADD COLUMN user_type text NOT NULL DEFAULT 'user' CHECK (user_type IN ('admin', 'user'));
UPDATE users SET user_type = CASE role WHEN 'admin' THEN 'admin' ELSE 'user' END;
DELETE FROM users WHERE password_hash IS NULL;
ALTER TABLE users
  ALTER COLUMN password_hash SET NOT NULL,
  DROP COLUMN role,
  DROP COLUMN legacy_key,
  DROP COLUMN display_name;
