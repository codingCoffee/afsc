CREATE TABLE jobs (
  id           bigserial   PRIMARY KEY,
  kind         text        NOT NULL,
  payload      jsonb       NOT NULL DEFAULT '{}',
  run_at       timestamptz NOT NULL DEFAULT now(),
  attempts     int         NOT NULL DEFAULT 0,
  max_attempts int         NOT NULL DEFAULT 8 CHECK (max_attempts > 0),
  locked_at    timestamptz,
  locked_by    text,
  last_error   text,
  done_at      timestamptz,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX jobs_due_idx ON jobs (run_at) WHERE done_at IS NULL;

CREATE TABLE post_revisions (
  id           bigserial   PRIMARY KEY,
  post_id      bigint      NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  kind         text        NOT NULL CHECK (kind IN ('autosave', 'manual', 'publish')),
  title        text        NOT NULL,
  excerpt      text        NOT NULL DEFAULT '',
  content      jsonb       NOT NULL,
  content_text text        NOT NULL DEFAULT '',
  seo          jsonb,
  user_id      uuid        REFERENCES users (id) ON DELETE SET NULL,
  created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX post_revisions_post_id_idx ON post_revisions (post_id, created_at DESC);
CREATE UNIQUE INDEX post_revisions_autosave_idx ON post_revisions (post_id, user_id) WHERE kind = 'autosave';

CREATE TABLE post_media (
  post_id  bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  media_id bigint NOT NULL REFERENCES media (id) ON DELETE CASCADE,
  PRIMARY KEY (post_id, media_id)
);
CREATE INDEX post_media_media_id_idx ON post_media (media_id);

-- lax $.** can reach a node twice through array unwrapping, hence DISTINCT
INSERT INTO post_media (post_id, media_id)
SELECT DISTINCT p.id, m.id
FROM posts p
CROSS JOIN LATERAL (
  SELECT v FROM jsonb_path_query(p.content, '$.**.mediaId') v
  UNION ALL SELECT v FROM jsonb_path_query(p.content, '$.**.posterMediaId') v
  UNION ALL SELECT to_jsonb(p.featured_media_id) WHERE p.featured_media_id IS NOT NULL
) x
JOIN media m ON m.id = CASE WHEN jsonb_typeof(x.v) = 'number' THEN trunc(x.v::numeric) END;
