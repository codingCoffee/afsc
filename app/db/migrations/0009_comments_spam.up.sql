-- convert_to is only marked stable; the database encoding never changes, so the wrapper is safe to declare immutable
CREATE FUNCTION afsc_body_hash(body text) RETURNS bytea
LANGUAGE sql IMMUTABLE PARALLEL SAFE
RETURN sha256(convert_to(lower(regexp_replace(btrim(body), '\s+', ' ', 'g')), 'UTF8'));

ALTER TABLE comments ADD COLUMN body_hash bytea GENERATED ALWAYS AS (afsc_body_hash(body)) STORED;

CREATE INDEX comments_body_hash_idx ON comments (body_hash, created_at);
CREATE INDEX comments_ip_idx ON comments (ip, created_at) WHERE ip IS NOT NULL;
CREATE INDEX comments_email_idx ON comments (author_email, created_at);
CREATE INDEX comments_trusted_idx ON comments (author_email) WHERE status = 'approved';
CREATE INDEX comments_status_idx ON comments (status, created_at DESC);
CREATE INDEX comments_retention_idx ON comments (created_at) WHERE ip IS NOT NULL OR user_agent IS NOT NULL;

CREATE TABLE spam_tokens (
  token text PRIMARY KEY,
  spam  int  NOT NULL DEFAULT 0,
  ham   int  NOT NULL DEFAULT 0
);

CREATE TABLE spam_blocklist (
  kind       text        NOT NULL CHECK (kind IN ('keyword', 'domain', 'ip', 'cidr', 'email')),
  value      text        NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (kind, value)
);

CREATE TABLE spam_archive (
  comment_key text        PRIMARY KEY,
  tokens      text[]      NOT NULL,
  archived_at timestamptz NOT NULL DEFAULT now()
);
