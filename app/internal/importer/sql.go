package importer

// Every upsert stages one jsonb document per record, then writes only rows whose values changed.
// RETURNING (xmax = 0) is true for inserted rows and false for updated ones; unchanged rows return nothing.

const sqlLinkUsers = `
UPDATE users u SET legacy_key = s.doc->>'legacy_key'
FROM stage_users s
WHERE u.legacy_key IS NULL AND u.email = s.doc->>'email'
  AND NOT EXISTS (SELECT 1 FROM users x WHERE x.legacy_key = s.doc->>'legacy_key')
RETURNING u.legacy_key`

const sqlUpsertUsers = `
INSERT INTO users (legacy_key, username, email, display_name, role, disabled_at)
SELECT doc->>'legacy_key', doc->>'username', doc->>'email', doc->>'display_name', doc->>'role',
       CASE WHEN (doc->>'disabled')::bool THEN now() END
FROM stage_users
ON CONFLICT (legacy_key) DO UPDATE SET
  email = EXCLUDED.email,
  display_name = EXCLUDED.display_name,
  disabled_at = CASE WHEN EXCLUDED.disabled_at IS NULL THEN NULL ELSE COALESCE(users.disabled_at, EXCLUDED.disabled_at) END
WHERE users.email IS DISTINCT FROM EXCLUDED.email
   OR users.display_name IS DISTINCT FROM EXCLUDED.display_name
   OR (users.disabled_at IS NULL) <> (EXCLUDED.disabled_at IS NULL)
RETURNING (xmax = 0)`

const sqlInvites = `
SELECT legacy_key, email FROM users
WHERE legacy_key LIKE $1 AND password_hash IS NULL AND disabled_at IS NULL
ORDER BY legacy_key`

const sqlUpsertMedia = `
INSERT INTO media (legacy_key, storage_key, filename, mime, bytes, sha256, width, height, duration_seconds,
                   title, alt, caption, description, library_visible, created_at, updated_at)
SELECT doc->>'legacy_key', doc->>'storage_key', doc->>'filename', doc->>'mime', (doc->>'bytes')::bigint,
       decode(doc->>'sha256', 'hex'), (doc->>'width')::int, (doc->>'height')::int, (doc->>'duration_seconds')::int,
       doc->>'title', doc->>'alt', doc->>'caption', doc->>'description', (doc->>'library_visible')::bool,
       (doc->>'created_at')::timestamptz, (doc->>'created_at')::timestamptz
FROM stage_media
ON CONFLICT (legacy_key) DO UPDATE SET
  storage_key = EXCLUDED.storage_key, filename = EXCLUDED.filename, mime = EXCLUDED.mime, bytes = EXCLUDED.bytes,
  sha256 = EXCLUDED.sha256, width = EXCLUDED.width, height = EXCLUDED.height,
  duration_seconds = EXCLUDED.duration_seconds, title = EXCLUDED.title, alt = EXCLUDED.alt,
  caption = EXCLUDED.caption, description = EXCLUDED.description, library_visible = EXCLUDED.library_visible,
  updated_at = now()
WHERE (media.storage_key, media.filename, media.mime, media.bytes, media.sha256, media.width, media.height,
       media.duration_seconds, media.title, media.alt, media.caption, media.description, media.library_visible)
  IS DISTINCT FROM
      (EXCLUDED.storage_key, EXCLUDED.filename, EXCLUDED.mime, EXCLUDED.bytes, EXCLUDED.sha256, EXCLUDED.width,
       EXCLUDED.height, EXCLUDED.duration_seconds, EXCLUDED.title, EXCLUDED.alt, EXCLUDED.caption,
       EXCLUDED.description, EXCLUDED.library_visible)
RETURNING (xmax = 0)`

const sqlDeleteStalePaths = `
DELETE FROM media_legacy_paths m
WHERE m.media_id = ANY($1::bigint[])
  AND NOT EXISTS (SELECT 1 FROM stage_paths s WHERE s.doc->>'path' = m.path)`

const sqlUpsertPaths = `
INSERT INTO media_legacy_paths (path, media_id, variant, width, height, crop)
SELECT doc->>'path', (doc->>'media_id')::bigint, doc->>'variant', (doc->>'width')::int, (doc->>'height')::int,
       (doc->>'crop')::bool
FROM stage_paths
ON CONFLICT (path) DO UPDATE SET
  media_id = EXCLUDED.media_id, variant = EXCLUDED.variant, width = EXCLUDED.width, height = EXCLUDED.height,
  crop = EXCLUDED.crop, updated_at = now()
WHERE (media_legacy_paths.media_id, media_legacy_paths.variant, media_legacy_paths.width,
       media_legacy_paths.height, media_legacy_paths.crop)
  IS DISTINCT FROM (EXCLUDED.media_id, EXCLUDED.variant, EXCLUDED.width, EXCLUDED.height, EXCLUDED.crop)
RETURNING (xmax = 0)`

const sqlUpsertAuthors = `
INSERT INTO authors (legacy_key, slug, display_name, bio, website, social, avatar_media_id, user_id)
SELECT doc->>'legacy_key', doc->>'slug', doc->>'display_name', doc->>'bio', doc->>'website', doc->'social',
       (doc->>'avatar_media_id')::bigint, (doc->>'user_id')::uuid
FROM stage_authors
ON CONFLICT (legacy_key) DO UPDATE SET
  slug = EXCLUDED.slug, display_name = EXCLUDED.display_name, bio = EXCLUDED.bio, website = EXCLUDED.website,
  social = EXCLUDED.social, avatar_media_id = EXCLUDED.avatar_media_id, user_id = EXCLUDED.user_id,
  updated_at = now()
WHERE (authors.slug, authors.display_name, authors.bio, authors.website, authors.social, authors.avatar_media_id,
       authors.user_id)
  IS DISTINCT FROM (EXCLUDED.slug, EXCLUDED.display_name, EXCLUDED.bio, EXCLUDED.website, EXCLUDED.social,
       EXCLUDED.avatar_media_id, EXCLUDED.user_id)
RETURNING (xmax = 0)`

const sqlUpsertTerms = `
INSERT INTO terms (legacy_key, taxonomy, slug, name, description, parent_id)
SELECT s.doc->>'legacy_key', s.doc->>'taxonomy', s.doc->>'slug', s.doc->>'name', s.doc->>'description', p.id
FROM stage_terms s
LEFT JOIN terms p ON p.legacy_key = s.doc->>'parent'
WHERE (s.doc->>'depth')::int = $1
ON CONFLICT (legacy_key) DO UPDATE SET
  taxonomy = EXCLUDED.taxonomy, slug = EXCLUDED.slug, name = EXCLUDED.name, description = EXCLUDED.description,
  parent_id = EXCLUDED.parent_id, updated_at = now()
WHERE (terms.taxonomy, terms.slug, terms.name, terms.description, terms.parent_id)
  IS DISTINCT FROM (EXCLUDED.taxonomy, EXCLUDED.slug, EXCLUDED.name, EXCLUDED.description, EXCLUDED.parent_id)
RETURNING (xmax = 0)`

const sqlUpsertPosts = `
INSERT INTO posts (legacy_key, type, status, slug, parent_id, title, excerpt, content, content_html, content_text,
                   template, featured_media_id, published_at, sticky, menu_order, comment_status, feed_guid,
                   created_at, updated_at)
SELECT s.doc->>'legacy_key', s.doc->>'type', s.doc->>'status', s.doc->>'slug', p.id, s.doc->>'title',
       s.doc->>'excerpt', s.doc->'content', s.doc->>'content_html', s.doc->>'content_text', s.doc->>'template',
       (s.doc->>'featured_media_id')::bigint, (s.doc->>'published_at')::timestamptz, (s.doc->>'sticky')::bool,
       (s.doc->>'menu_order')::int, s.doc->>'comment_status', s.doc->>'feed_guid',
       (s.doc->>'created_at')::timestamptz, (s.doc->>'updated_at')::timestamptz
FROM stage_posts s
LEFT JOIN posts p ON p.legacy_key = s.doc->>'parent'
WHERE (s.doc->>'depth')::int = $1
ON CONFLICT (legacy_key) DO UPDATE SET
  type = EXCLUDED.type, status = EXCLUDED.status, slug = EXCLUDED.slug, parent_id = EXCLUDED.parent_id,
  title = EXCLUDED.title, excerpt = EXCLUDED.excerpt, content = EXCLUDED.content,
  content_html = EXCLUDED.content_html, content_text = EXCLUDED.content_text, template = EXCLUDED.template,
  featured_media_id = EXCLUDED.featured_media_id, published_at = EXCLUDED.published_at, sticky = EXCLUDED.sticky,
  menu_order = EXCLUDED.menu_order, comment_status = EXCLUDED.comment_status, feed_guid = EXCLUDED.feed_guid,
  created_at = EXCLUDED.created_at, updated_at = EXCLUDED.updated_at, version = posts.version + 1
WHERE (posts.type, posts.status, posts.slug, posts.parent_id, posts.title, posts.excerpt, posts.content,
       posts.content_html, posts.content_text, posts.template, posts.featured_media_id, posts.published_at,
       posts.sticky, posts.menu_order, posts.comment_status, posts.feed_guid, posts.created_at, posts.updated_at)
  IS DISTINCT FROM
      (EXCLUDED.type, EXCLUDED.status, EXCLUDED.slug, EXCLUDED.parent_id, EXCLUDED.title, EXCLUDED.excerpt,
       EXCLUDED.content, EXCLUDED.content_html, EXCLUDED.content_text, EXCLUDED.template,
       EXCLUDED.featured_media_id, EXCLUDED.published_at, EXCLUDED.sticky, EXCLUDED.menu_order,
       EXCLUDED.comment_status, EXCLUDED.feed_guid, EXCLUDED.created_at, EXCLUDED.updated_at)
RETURNING (xmax = 0)`

const sqlDeleteStalePostAuthors = `
DELETE FROM post_authors pa
WHERE pa.post_id = ANY($1::bigint[])
  AND NOT EXISTS (SELECT 1 FROM stage_post_authors s
                  WHERE (s.doc->>'post_id')::bigint = pa.post_id AND (s.doc->>'author_id')::bigint = pa.author_id)`

const sqlUpsertPostAuthors = `
INSERT INTO post_authors (post_id, author_id, position)
SELECT (doc->>'post_id')::bigint, (doc->>'author_id')::bigint, (doc->>'position')::int FROM stage_post_authors
ON CONFLICT (post_id, author_id) DO UPDATE SET position = EXCLUDED.position
WHERE post_authors.position IS DISTINCT FROM EXCLUDED.position
RETURNING (xmax = 0)`

const sqlDeleteStalePostTerms = `
DELETE FROM post_terms pt
WHERE pt.post_id = ANY($1::bigint[])
  AND NOT EXISTS (SELECT 1 FROM stage_post_terms s
                  WHERE (s.doc->>'post_id')::bigint = pt.post_id AND (s.doc->>'term_id')::bigint = pt.term_id)`

// clears primaries that are about to move, so the one-primary-per-post index never sees two at once
const sqlClearMovedPrimaries = `
UPDATE post_terms pt SET is_primary = false
WHERE pt.post_id = ANY($1::bigint[]) AND pt.is_primary
  AND NOT EXISTS (SELECT 1 FROM stage_post_terms s
                  WHERE (s.doc->>'post_id')::bigint = pt.post_id AND (s.doc->>'term_id')::bigint = pt.term_id
                    AND (s.doc->>'is_primary')::bool)`

const sqlUpsertPostTerms = `
INSERT INTO post_terms (post_id, term_id, is_primary)
SELECT (doc->>'post_id')::bigint, (doc->>'term_id')::bigint, (doc->>'is_primary')::bool FROM stage_post_terms
ON CONFLICT (post_id, term_id) DO UPDATE SET is_primary = EXCLUDED.is_primary
WHERE post_terms.is_primary IS DISTINCT FROM EXCLUDED.is_primary
RETURNING (xmax = 0)`

const sqlDeleteStaleSEO = `
DELETE FROM seo x
WHERE ((x.entity_type = 'post' AND x.entity_id = ANY($1::bigint[]))
    OR (x.entity_type = 'term' AND x.entity_id = ANY($2::bigint[]))
    OR (x.entity_type = 'author' AND x.entity_id = ANY($3::bigint[])))
  AND NOT EXISTS (SELECT 1 FROM stage_seo s
                  WHERE s.doc->>'entity_type' = x.entity_type AND (s.doc->>'entity_id')::bigint = x.entity_id)`

const sqlUpsertSEO = `
INSERT INTO seo (entity_type, entity_id, title, description, canonical, robots_noindex, robots_nofollow,
                 robots_advanced, og_title, og_description, og_image_media_id, twitter_title, twitter_description,
                 twitter_image_media_id, focus_keyphrase, breadcrumb_title, is_cornerstone)
SELECT doc->>'entity_type', (doc->>'entity_id')::bigint, doc->>'title', doc->>'description', doc->>'canonical',
       (doc->>'robots_noindex')::bool, (doc->>'robots_nofollow')::bool,
       CASE WHEN jsonb_typeof(doc->'robots_advanced') = 'array'
            THEN ARRAY(SELECT jsonb_array_elements_text(doc->'robots_advanced')) END,
       doc->>'og_title', doc->>'og_description', (doc->>'og_image_media_id')::bigint, doc->>'twitter_title',
       doc->>'twitter_description', (doc->>'twitter_image_media_id')::bigint, doc->>'focus_keyphrase',
       doc->>'breadcrumb_title', (doc->>'is_cornerstone')::bool
FROM stage_seo
ON CONFLICT (entity_type, entity_id) DO UPDATE SET
  title = EXCLUDED.title, description = EXCLUDED.description, canonical = EXCLUDED.canonical,
  robots_noindex = EXCLUDED.robots_noindex, robots_nofollow = EXCLUDED.robots_nofollow,
  robots_advanced = EXCLUDED.robots_advanced, og_title = EXCLUDED.og_title, og_description = EXCLUDED.og_description,
  og_image_media_id = EXCLUDED.og_image_media_id, twitter_title = EXCLUDED.twitter_title,
  twitter_description = EXCLUDED.twitter_description, twitter_image_media_id = EXCLUDED.twitter_image_media_id,
  focus_keyphrase = EXCLUDED.focus_keyphrase, breadcrumb_title = EXCLUDED.breadcrumb_title,
  is_cornerstone = EXCLUDED.is_cornerstone, updated_at = now()
WHERE (seo.title, seo.description, seo.canonical, seo.robots_noindex, seo.robots_nofollow, seo.robots_advanced,
       seo.og_title, seo.og_description, seo.og_image_media_id, seo.twitter_title, seo.twitter_description,
       seo.twitter_image_media_id, seo.focus_keyphrase, seo.breadcrumb_title, seo.is_cornerstone)
  IS DISTINCT FROM
      (EXCLUDED.title, EXCLUDED.description, EXCLUDED.canonical, EXCLUDED.robots_noindex, EXCLUDED.robots_nofollow,
       EXCLUDED.robots_advanced, EXCLUDED.og_title, EXCLUDED.og_description, EXCLUDED.og_image_media_id,
       EXCLUDED.twitter_title, EXCLUDED.twitter_description, EXCLUDED.twitter_image_media_id,
       EXCLUDED.focus_keyphrase, EXCLUDED.breadcrumb_title, EXCLUDED.is_cornerstone)
RETURNING (xmax = 0)`

// sqlSyncPostMedia rebuilds post_media (Q14) for the imported posts, like migration 0008's backfill.
const sqlSyncPostMedia = `
WITH want AS (
  SELECT DISTINCT p.id AS post_id, m.id AS media_id
  FROM posts p
  CROSS JOIN LATERAL (
    SELECT v FROM jsonb_path_query(p.content, '$.**.mediaId') v
    UNION ALL SELECT v FROM jsonb_path_query(p.content, '$.**.posterMediaId') v
    UNION ALL SELECT to_jsonb(p.featured_media_id) WHERE p.featured_media_id IS NOT NULL
  ) x
  JOIN media m ON m.id = CASE WHEN jsonb_typeof(x.v) = 'number' THEN trunc(x.v::numeric) END
  WHERE p.id = ANY($1::bigint[])
), gone AS (
  DELETE FROM post_media pm
  WHERE pm.post_id = ANY($1::bigint[])
    AND NOT EXISTS (SELECT 1 FROM want w WHERE w.post_id = pm.post_id AND w.media_id = pm.media_id)
)
INSERT INTO post_media (post_id, media_id) SELECT post_id, media_id FROM want
ON CONFLICT DO NOTHING`

const sqlDeleteStaleVideos = `
DELETE FROM post_videos v
WHERE v.source = 'import' AND v.post_id = ANY($1::bigint[])
  AND NOT EXISTS (SELECT 1 FROM stage_videos s
                  WHERE (s.doc->>'post_id')::bigint = v.post_id AND s.doc->>'url' = v.url)`

const sqlUpsertVideos = `
INSERT INTO post_videos (post_id, provider, external_id, url, title, description, thumbnail_url, thumbnail_media_id,
                         duration_seconds, upload_date, family_friendly, source)
SELECT (doc->>'post_id')::bigint, doc->>'provider', doc->>'external_id', doc->>'url', doc->>'title',
       doc->>'description', doc->>'thumbnail_url', (doc->>'thumbnail_media_id')::bigint,
       (doc->>'duration_seconds')::int, (doc->>'upload_date')::timestamptz, (doc->>'family_friendly')::bool, 'import'
FROM stage_videos
ON CONFLICT (post_id, source, url) DO UPDATE SET
  provider = EXCLUDED.provider, external_id = EXCLUDED.external_id, title = EXCLUDED.title,
  description = EXCLUDED.description, thumbnail_url = EXCLUDED.thumbnail_url,
  thumbnail_media_id = EXCLUDED.thumbnail_media_id, duration_seconds = EXCLUDED.duration_seconds,
  upload_date = EXCLUDED.upload_date, family_friendly = EXCLUDED.family_friendly, updated_at = now()
WHERE (post_videos.provider, post_videos.external_id, post_videos.title, post_videos.description,
       post_videos.thumbnail_url, post_videos.thumbnail_media_id, post_videos.duration_seconds,
       post_videos.upload_date, post_videos.family_friendly)
  IS DISTINCT FROM
      (EXCLUDED.provider, EXCLUDED.external_id, EXCLUDED.title, EXCLUDED.description, EXCLUDED.thumbnail_url,
       EXCLUDED.thumbnail_media_id, EXCLUDED.duration_seconds, EXCLUDED.upload_date, EXCLUDED.family_friendly)
RETURNING (xmax = 0)`

const sqlUpsertComments = `
INSERT INTO comments (legacy_key, post_id, parent_id, author_name, author_email, author_url, user_id, ip,
                      user_agent, body, status, created_at, updated_at)
SELECT s.doc->>'legacy_key', (s.doc->>'post_id')::bigint, p.id, s.doc->>'author_name', s.doc->>'author_email',
       s.doc->>'author_url', (s.doc->>'user_id')::uuid, (s.doc->>'ip')::inet, s.doc->>'user_agent',
       s.doc->>'body', s.doc->>'status', (s.doc->>'created_at')::timestamptz, (s.doc->>'created_at')::timestamptz
FROM stage_comments s
LEFT JOIN comments p ON p.legacy_key = s.doc->>'parent'
WHERE (s.doc->>'depth')::int = $1
ON CONFLICT (legacy_key) DO UPDATE SET
  post_id = EXCLUDED.post_id, parent_id = EXCLUDED.parent_id, author_name = EXCLUDED.author_name,
  author_email = EXCLUDED.author_email, author_url = EXCLUDED.author_url, user_id = EXCLUDED.user_id,
  ip = EXCLUDED.ip, user_agent = EXCLUDED.user_agent, body = EXCLUDED.body, status = EXCLUDED.status,
  created_at = EXCLUDED.created_at, updated_at = now()
WHERE (comments.post_id, comments.parent_id, comments.author_name, comments.author_email, comments.author_url,
       comments.user_id, comments.ip, comments.user_agent, comments.body, comments.status, comments.created_at)
  IS DISTINCT FROM
      (EXCLUDED.post_id, EXCLUDED.parent_id, EXCLUDED.author_name, EXCLUDED.author_email, EXCLUDED.author_url,
       EXCLUDED.user_id, EXCLUDED.ip, EXCLUDED.user_agent, EXCLUDED.body, EXCLUDED.status, EXCLUDED.created_at)
RETURNING (xmax = 0)`

const sqlUpsertMenus = `
INSERT INTO menus (legacy_key, slug, name)
SELECT doc->>'legacy_key', doc->>'slug', doc->>'name' FROM stage_menus
ON CONFLICT (legacy_key) DO UPDATE SET slug = EXCLUDED.slug, name = EXCLUDED.name, updated_at = now()
WHERE (menus.slug, menus.name) IS DISTINCT FROM (EXCLUDED.slug, EXCLUDED.name)
RETURNING (xmax = 0)`

const sqlUpsertMenuLocation = `
INSERT INTO menu_locations (location, menu_id) VALUES ($1, $2)
ON CONFLICT (location) DO UPDATE SET menu_id = EXCLUDED.menu_id, updated_at = now()
WHERE menu_locations.menu_id IS DISTINCT FROM EXCLUDED.menu_id`

const sqlDeleteStaleMenuItems = `
DELETE FROM menu_items m
WHERE m.menu_id = ANY($1::bigint[])
  AND (m.legacy_key IS NULL
       OR NOT EXISTS (SELECT 1 FROM stage_menu_items s WHERE s.doc->>'legacy_key' = m.legacy_key))`

const sqlUpsertMenuItems = `
INSERT INTO menu_items (legacy_key, menu_id, parent_id, position, label, kind, target_id, url, new_tab, css_classes)
SELECT s.doc->>'legacy_key', (s.doc->>'menu_id')::bigint, p.id, (s.doc->>'position')::int, s.doc->>'label',
       s.doc->>'kind', (s.doc->>'target_id')::bigint, s.doc->>'url', (s.doc->>'new_tab')::bool,
       ARRAY(SELECT jsonb_array_elements_text(s.doc->'css_classes'))
FROM stage_menu_items s
LEFT JOIN menu_items p ON p.legacy_key = s.doc->>'parent'
WHERE (s.doc->>'depth')::int = $1
ON CONFLICT (legacy_key) DO UPDATE SET
  menu_id = EXCLUDED.menu_id, parent_id = EXCLUDED.parent_id, position = EXCLUDED.position, label = EXCLUDED.label,
  kind = EXCLUDED.kind, target_id = EXCLUDED.target_id, url = EXCLUDED.url, new_tab = EXCLUDED.new_tab,
  css_classes = EXCLUDED.css_classes, updated_at = now()
WHERE (menu_items.menu_id, menu_items.parent_id, menu_items.position, menu_items.label, menu_items.kind,
       menu_items.target_id, menu_items.url, menu_items.new_tab, menu_items.css_classes)
  IS DISTINCT FROM
      (EXCLUDED.menu_id, EXCLUDED.parent_id, EXCLUDED.position, EXCLUDED.label, EXCLUDED.kind, EXCLUDED.target_id,
       EXCLUDED.url, EXCLUDED.new_tab, EXCLUDED.css_classes)
RETURNING (xmax = 0)`

const sqlForeignExactRedirects = `
SELECT source FROM redirects
WHERE match = 'exact' AND (legacy_key IS NULL OR legacy_key NOT LIKE $1)`

const sqlUpsertRedirects = `
INSERT INTO redirects (legacy_key, source, match, target, status, note, origin)
SELECT doc->>'legacy_key', doc->>'source', doc->>'match', doc->>'target', (doc->>'status')::int, doc->>'note', 'import'
FROM stage_redirects
ON CONFLICT (legacy_key) DO UPDATE SET
  source = EXCLUDED.source, match = EXCLUDED.match, target = EXCLUDED.target, status = EXCLUDED.status,
  note = EXCLUDED.note, updated_at = now()
WHERE (redirects.source, redirects.match, redirects.target, redirects.status, redirects.note)
  IS DISTINCT FROM (EXCLUDED.source, EXCLUDED.match, EXCLUDED.target, EXCLUDED.status, EXCLUDED.note)
RETURNING (xmax = 0)`

const sqlUpsertLegacy = `
INSERT INTO legacy_records (legacy_key, kind, source)
SELECT doc->>'legacy_key', doc->>'kind', doc->'source' FROM stage_legacy
ON CONFLICT (legacy_key) DO UPDATE SET kind = EXCLUDED.kind, source = EXCLUDED.source, imported_at = now()
WHERE (legacy_records.kind, legacy_records.source) IS DISTINCT FROM (EXCLUDED.kind, EXCLUDED.source)
RETURNING (xmax = 0)`

const sqlUpsertSetting = `
INSERT INTO settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()
WHERE settings.value IS DISTINCT FROM EXCLUDED.value
RETURNING (xmax = 0)`

// keyQueries load legacy_key → id maps after each phase. Table names are constants, never input.
var keyQueries = map[string]string{
	"authors": `SELECT legacy_key, id FROM authors WHERE legacy_key LIKE $1`,
	"terms":   `SELECT legacy_key, id FROM terms WHERE legacy_key LIKE $1`,
	"posts":   `SELECT legacy_key, id FROM posts WHERE legacy_key LIKE $1`,
	"menus":   `SELECT legacy_key, id FROM menus WHERE legacy_key LIKE $1`,
}
