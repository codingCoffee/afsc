CREATE TABLE users (
  id            uuid        PRIMARY KEY DEFAULT gen_random_uuid(),
  username      text        NOT NULL UNIQUE CHECK (username ~ '^[a-z0-9_.-]{3,32}$'),
  email         text        NOT NULL UNIQUE CHECK (char_length(email) <= 254),
  password_hash text        NOT NULL,
  first_name    text        NOT NULL DEFAULT '' CHECK (char_length(first_name) <= 100),
  last_name     text        NOT NULL DEFAULT '' CHECK (char_length(last_name) <= 100),
  user_type     text        NOT NULL DEFAULT 'user' CHECK (user_type IN ('admin', 'user')),
  created_at    timestamptz NOT NULL DEFAULT now(),
  updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE FUNCTION set_updated_at() RETURNS trigger AS $$
BEGIN
  NEW.updated_at = now();
  RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER users_set_updated_at
  BEFORE UPDATE ON users
  FOR EACH ROW EXECUTE FUNCTION set_updated_at();
