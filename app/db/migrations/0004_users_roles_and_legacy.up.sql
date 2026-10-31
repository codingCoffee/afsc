CREATE EXTENSION IF NOT EXISTS citext;

ALTER TABLE users
  ADD COLUMN display_name text NOT NULL DEFAULT '' CHECK (char_length(display_name) <= 200),
  ADD COLUMN legacy_key   text UNIQUE,
  ADD COLUMN role         text NOT NULL DEFAULT 'contributor'
    CHECK (role IN ('admin', 'editor', 'author', 'contributor')),
  ALTER COLUMN password_hash DROP NOT NULL;

UPDATE users SET role = CASE user_type WHEN 'admin' THEN 'admin' ELSE 'contributor' END;

ALTER TABLE users DROP COLUMN user_type;
