CREATE TABLE media (
  id              bigserial   PRIMARY KEY,
  storage_key     text        NOT NULL UNIQUE,
  filename        text        NOT NULL,
  mime            text        NOT NULL,
  bytes           bigint      NOT NULL CHECK (bytes >= 0),
  sha256          bytea       NOT NULL UNIQUE CHECK (octet_length(sha256) = 32),
  width           int         CHECK (width > 0),
  height          int         CHECK (height > 0),
  duration_seconds int        CHECK (duration_seconds >= 0),
  title           text        NOT NULL DEFAULT '',
  alt             text        NOT NULL DEFAULT '',
  caption         text        NOT NULL DEFAULT '',
  description     text        NOT NULL DEFAULT '',
  focal_x         real        CHECK (focal_x BETWEEN 0 AND 1),
  focal_y         real        CHECK (focal_y BETWEEN 0 AND 1),
  uploaded_by     uuid        REFERENCES users (id) ON DELETE SET NULL,
  library_visible bool        NOT NULL DEFAULT true,
  legacy_key      text        UNIQUE,
  created_at      timestamptz NOT NULL DEFAULT now(),
  updated_at      timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE media_legacy_paths (
  path        text        PRIMARY KEY CHECK (path LIKE '/%'),
  media_id    bigint      NOT NULL REFERENCES media (id) ON DELETE CASCADE,
  variant     text        NOT NULL CHECK (variant IN ('orig', 'resize')),
  width       int         CHECK (width > 0),
  height      int         CHECK (height > 0),
  crop        bool        NOT NULL DEFAULT false,
  hits        bigint      NOT NULL DEFAULT 0,
  last_hit_at timestamptz,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CHECK (variant = 'orig' OR (width IS NOT NULL AND height IS NOT NULL))
);
CREATE INDEX media_legacy_paths_media_id_idx ON media_legacy_paths (media_id);

CREATE TABLE authors (
  id             bigserial   PRIMARY KEY,
  slug           text        NOT NULL UNIQUE,
  display_name   text        NOT NULL,
  bio            text        NOT NULL DEFAULT '',
  website        text        NOT NULL DEFAULT '',
  social         jsonb       NOT NULL DEFAULT '{}',
  avatar_media_id bigint     REFERENCES media (id) ON DELETE SET NULL,
  user_id        uuid        UNIQUE REFERENCES users (id) ON DELETE SET NULL,
  legacy_key     text        UNIQUE,
  created_at     timestamptz NOT NULL DEFAULT now(),
  updated_at     timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE posts (
  id                bigserial   PRIMARY KEY,
  type              text        NOT NULL CHECK (type IN ('post', 'page')),
  status            text        NOT NULL CHECK (status IN ('draft', 'pending', 'scheduled', 'published', 'private', 'trash')),
  slug              text        NOT NULL,
  parent_id         bigint      REFERENCES posts (id) ON DELETE SET NULL,
  title             text        NOT NULL,
  excerpt           text        NOT NULL DEFAULT '',
  content           jsonb       NOT NULL,
  content_html      text        NOT NULL,
  content_text      text        NOT NULL,
  template          text,
  featured_media_id bigint      REFERENCES media (id) ON DELETE SET NULL,
  published_at      timestamptz,
  sticky            bool        NOT NULL DEFAULT false,
  menu_order        int         NOT NULL DEFAULT 0,
  comment_status    text        NOT NULL DEFAULT 'open' CHECK (comment_status IN ('open', 'closed')),
  feed_guid         text,
  version           int         NOT NULL DEFAULT 1,
  search            tsvector    GENERATED ALWAYS AS (
    setweight(to_tsvector('english'::regconfig, title), 'A') ||
    setweight(to_tsvector('english'::regconfig, excerpt), 'B') ||
    setweight(to_tsvector('english'::regconfig, content_text), 'C')
  ) STORED,
  legacy_key        text        UNIQUE,
  created_at        timestamptz NOT NULL DEFAULT now(),
  updated_at        timestamptz NOT NULL DEFAULT now(),
  CHECK (type = 'page' OR parent_id IS NULL),
  UNIQUE NULLS NOT DISTINCT (type, parent_id, slug)
);
CREATE INDEX posts_search_idx ON posts USING gin (search);
CREATE INDEX posts_listing_idx ON posts (type, status, published_at DESC);

CREATE TABLE post_authors (
  post_id   bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  author_id bigint NOT NULL REFERENCES authors (id) ON DELETE CASCADE,
  position  int    NOT NULL DEFAULT 0,
  PRIMARY KEY (post_id, author_id)
);
CREATE INDEX post_authors_author_id_idx ON post_authors (author_id);

CREATE TABLE terms (
  id          bigserial   PRIMARY KEY,
  taxonomy    text        NOT NULL CHECK (taxonomy IN ('category', 'tag')),
  slug        text        NOT NULL,
  name        text        NOT NULL,
  description text        NOT NULL DEFAULT '',
  parent_id   bigint      REFERENCES terms (id) ON DELETE SET NULL,
  legacy_key  text        UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CHECK (taxonomy = 'category' OR parent_id IS NULL),
  UNIQUE NULLS NOT DISTINCT (taxonomy, parent_id, slug)
);

CREATE TABLE post_terms (
  post_id    bigint NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  term_id    bigint NOT NULL REFERENCES terms (id) ON DELETE CASCADE,
  is_primary bool   NOT NULL DEFAULT false,
  PRIMARY KEY (post_id, term_id)
);
CREATE INDEX post_terms_term_id_idx ON post_terms (term_id);
CREATE UNIQUE INDEX post_terms_one_primary_idx ON post_terms (post_id) WHERE is_primary;

CREATE TABLE seo (
  entity_type            text   NOT NULL CHECK (entity_type IN ('post', 'term', 'author', 'home', 'static')),
  entity_id              bigint NOT NULL,
  title                  text,
  description            text,
  canonical              text,
  robots_noindex         bool,
  robots_nofollow        bool,
  robots_advanced        text[],
  og_title               text,
  og_description         text,
  og_image_media_id      bigint REFERENCES media (id) ON DELETE SET NULL,
  twitter_title          text,
  twitter_description    text,
  twitter_image_media_id bigint REFERENCES media (id) ON DELETE SET NULL,
  focus_keyphrase        text,
  breadcrumb_title       text,
  is_cornerstone         bool   NOT NULL DEFAULT false,
  created_at             timestamptz NOT NULL DEFAULT now(),
  updated_at             timestamptz NOT NULL DEFAULT now(),
  PRIMARY KEY (entity_type, entity_id)
);

CREATE TABLE post_videos (
  id                 bigserial   PRIMARY KEY,
  post_id            bigint      NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  provider           text        NOT NULL DEFAULT '',
  external_id        text        NOT NULL DEFAULT '',
  url                text        NOT NULL,
  title              text        NOT NULL DEFAULT '',
  description        text        NOT NULL DEFAULT '',
  thumbnail_url      text        NOT NULL DEFAULT '',
  thumbnail_media_id bigint      REFERENCES media (id) ON DELETE SET NULL,
  duration_seconds   int         CHECK (duration_seconds >= 0),
  upload_date        timestamptz,
  family_friendly    bool,
  source             text        NOT NULL CHECK (source IN ('auto', 'manual', 'import')),
  created_at         timestamptz NOT NULL DEFAULT now(),
  updated_at         timestamptz NOT NULL DEFAULT now(),
  UNIQUE (post_id, source, url)
);

CREATE TABLE comments (
  id           bigserial   PRIMARY KEY,
  post_id      bigint      NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
  parent_id    bigint      REFERENCES comments (id) ON DELETE SET NULL,
  author_name  text        NOT NULL DEFAULT '',
  author_email citext      NOT NULL DEFAULT '',
  author_url   text        NOT NULL DEFAULT '',
  user_id      uuid        REFERENCES users (id) ON DELETE SET NULL,
  ip           inet,
  user_agent   text,
  body         text        NOT NULL,
  status       text        NOT NULL CHECK (status IN ('pending', 'approved', 'spam', 'trash')),
  spam_score   real,
  spam_reasons jsonb,
  legacy_key   text        UNIQUE,
  created_at   timestamptz NOT NULL DEFAULT now(),
  updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX comments_post_id_idx ON comments (post_id, status, created_at);

CREATE TABLE menus (
  id         bigserial   PRIMARY KEY,
  slug       text        NOT NULL UNIQUE,
  name       text        NOT NULL,
  legacy_key text        UNIQUE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE menu_items (
  id          bigserial   PRIMARY KEY,
  menu_id     bigint      NOT NULL REFERENCES menus (id) ON DELETE CASCADE,
  parent_id   bigint      REFERENCES menu_items (id) ON DELETE CASCADE,
  position    int         NOT NULL DEFAULT 0,
  label       text        NOT NULL DEFAULT '',
  kind        text        NOT NULL CHECK (kind IN ('post', 'term', 'author', 'url')),
  target_id   bigint,
  url         text,
  new_tab     bool        NOT NULL DEFAULT false,
  css_classes text[]      NOT NULL DEFAULT '{}',
  legacy_key  text        UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CHECK ((kind = 'url') = (url IS NOT NULL AND target_id IS NULL))
);
CREATE INDEX menu_items_menu_id_idx ON menu_items (menu_id, position);

CREATE TABLE menu_locations (
  location   text        PRIMARY KEY,
  menu_id    bigint      NOT NULL REFERENCES menus (id) ON DELETE CASCADE,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE redirects (
  id          bigserial   PRIMARY KEY,
  source      text        NOT NULL,
  match       text        NOT NULL CHECK (match IN ('exact', 'prefix', 'regex')),
  target      text,
  status      int         NOT NULL CHECK (status IN (301, 302, 307, 308, 410, 451)),
  hits        bigint      NOT NULL DEFAULT 0,
  last_hit_at timestamptz,
  note        text        NOT NULL DEFAULT '',
  created_by  uuid        REFERENCES users (id) ON DELETE SET NULL,
  origin      text        NOT NULL CHECK (origin IN ('manual', 'slug_change', 'import')),
  legacy_key  text        UNIQUE,
  created_at  timestamptz NOT NULL DEFAULT now(),
  updated_at  timestamptz NOT NULL DEFAULT now(),
  CHECK (status IN (410, 451) OR target IS NOT NULL)
);
CREATE UNIQUE INDEX redirects_exact_source_idx ON redirects (source) WHERE match = 'exact';

CREATE TABLE not_found_log (
  path          text        PRIMARY KEY,
  hits          bigint      NOT NULL DEFAULT 0,
  first_seen    timestamptz NOT NULL DEFAULT now(),
  last_seen     timestamptz NOT NULL DEFAULT now(),
  last_referrer text        NOT NULL DEFAULT ''
);

CREATE TABLE settings (
  key        text        PRIMARY KEY,
  value      jsonb       NOT NULL,
  created_at timestamptz NOT NULL DEFAULT now(),
  updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE legacy_records (
  legacy_key  text        PRIMARY KEY,
  kind        text        NOT NULL,
  source      jsonb       NOT NULL,
  imported_at timestamptz NOT NULL DEFAULT now()
);
