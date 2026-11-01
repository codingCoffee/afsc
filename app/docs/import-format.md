# Import bundle format (v1)

This is the contract between AFSC and the WordPress migration project (build plan §17). Changing it needs a
`format_version` bump. `testdata/bundle-sample/` is a complete, valid example that covers every record type and
content node; CI imports it twice.

```
afsc import --bundle <dir> [--dry-run] [--report out.json] [--prune [--yes]] [--theme <dir>]
```

| Flag | Meaning |
|---|---|
| `--bundle` | Bundle directory (required). |
| `--dry-run` | Validate, then run the whole import inside one transaction and roll it back. No files are uploaded. Counts in the report are exact. |
| `--report` | Write the JSON report (below) to this file. |
| `--prune` | Delete records whose `legacy_key` starts with `<source>:` but are no longer in the bundle. Prints the plan and asks you to type `yes`. Admin users are never pruned. Files of pruned media stay in object storage. |
| `--yes` | Skip the prune confirmation. |
| `--theme` | Theme directory (default `AFSC_THEME_DIR`). Its `content.allowed_classes` decides which content classes survive. |

Environment: `AFSC_DATABASE_URL`, and unless `--dry-run`, `AFSC_S3_BUCKET`, `AFSC_S3_REGION` (plus
`AFSC_S3_ENDPOINT`/`AFSC_S3_PATH_STYLE` for S3-compatible servers). S3 credentials come from the default AWS chain.

**Exit codes:** `0` success, `1` validation failed (nothing written), `2` the import failed while writing.

## Behaviour

- **Validate first.** The whole bundle is read and checked before anything is written: JSON shape (unknown fields are
  errors), references, content against the schema, slug uniqueness, legacy-path uniqueness, file hashes and file types.
- **Idempotent.** Every record is upserted by `legacy_key`, and a row is only written when a value changed. Importing
  the same bundle twice reports everything as `unchanged` on the second run. A newer bundle updates changed records.
- **Order.** users → media (+ files, + legacy paths) → authors → terms → posts → post authors, post terms, SEO,
  videos → comments → menus → redirects → legacy records → settings. Settings come last because they reference
  pages and media. Each step is one transaction; a failure stops the import, and a re-run continues safely.
- **Concurrency.** Imports take a database advisory lock, so two imports never run at once.
- **Side effects** are suppressed during import. Webhooks, IndexNow pings and emails don't exist yet. Auto redirects
  exist as a core (M2c) but only editor actions will call them, so an import that changes slugs never creates
  `slug_change` redirects. The IndexNow key file is served as soon as `seo.indexnow` is enabled; it isn't a side
  effect.
- **Users** are created without a password and can't sign in until invited. The report lists one invite command per
  user. *The `afsc user invite` command ships with the admin milestone (M3).* An imported user whose email matches an
  existing AFSC account is linked to that account instead; its role and password are kept. A re-import never changes a
  user's role.

### Not done yet

§17 asks the importer to enqueue `content.rerender_all`, `search.reindex`, `related.compute` and `spam.retrain` after
an import. None of these jobs exist yet. Content HTML and the search vector are already up to date after import, so
nothing is lost; the hooks will be added with the job queue.

The page cache *is* purged. After any import that may have written rows (every run except `--dry-run` and one that
failed validation), `afsc import` sends `NOTIFY afsc_cache`. Every running `afsc serve` then reloads its site data
(settings, menus, pages, terms, authors and redirects) and drops every cached page. See `docs/ops.md`.

## Layout

```
bundle/
  manifest.json
  settings.json            optional
  users.ndjson  media.ndjson  authors.ndjson  terms.ndjson  posts.ndjson
  comments.ndjson  menus.ndjson  redirects.ndjson  legacy.ndjson     each optional; missing = empty
  files/<sha256[:2]>/<sha256>.<ext>
```

`.ndjson` files hold one JSON object per line. Blank lines are ignored.

## Legacy keys

`"<source>:<kind>:<id>"`, for example `wp:post:123`, `wp:term:45`, `wp:user:7`, `wp:guest:<hash>`,
`wp:comment:999`, `wp:menu:3`, `wp:menu_item:88`, `wp:redirect:<hash>`, `wp:file:<hash>`.

- Every key must start with `<manifest.source>:`, have no whitespace and be at most 255 characters.
- Keys are unique per record type. The same key may appear in two types (a user and its author record both use
  `wp:user:2`).
- **All cross-references use legacy keys**, including media inside post content.
- Media keyed `<source>:post:<N>` keep `N` as their WordPress id in rendered markup (`wp-image-N`,
  `attachment_N`). Posts keyed `<source>:post:<N>` use `N` in `more-N` and `galleryid-N`.

## manifest.json

| Field | Type | Notes |
|---|---|---|
| `format_version` | int | Must be `1`. |
| `source` | string | `[a-z0-9]{1,16}`, the legacy-key prefix and the prune scope. |
| `generated_at` | RFC 3339 time | Required. Also the `created_at` of media that don't give one. |
| `counts` | object | Informational. |
| `theme` | string | Theme name the bundle was checked against. A mismatch with `--theme` is a warning. |

## Record shapes

Optional fields may be omitted or `null`. Times are RFC 3339.

### users.ndjson

| Field | Type | Notes |
|---|---|---|
| `legacy_key` | string | |
| `email` | string | Required, unique in the bundle, lowercased. |
| `display_name` | string | Required. |
| `role` | `admin` \| `editor` \| `author` \| `contributor` | Applied on first import only. |
| `disabled` | bool | Disabled users can't sign in and get no invite. |

AFSC derives a username from the email's local part (`-2`, `-3`… on collisions).

### media.ndjson

| Field | Type | Notes |
|---|---|---|
| `legacy_key` | string | |
| `file` | string | Relative path under `files/`; no `..`. |
| `sha256` | string | 64 lowercase hex chars; must match the file. |
| `filename` | string | Display name, no path separators. Never used as a storage key. |
| `mime` | string | One of `image/jpeg`, `image/png`, `image/gif`, `image/webp`, `image/avif`, `image/svg+xml`, `video/mp4`, `video/webm`, `application/pdf`, `audio/mpeg`. Must match the sniffed content. |
| `width`, `height`, `duration_seconds` | int | Optional, ≥ 0. |
| `title`, `alt`, `caption`, `description` | string | Optional. |
| `library_visible` | bool | Default `true`. `false` keeps the file only so its legacy paths work. |
| `created_at` | time | Optional. |
| `legacy_paths` | array | See below. |

`legacy_paths[]`: `{path, variant, width?, height?, crop?}`.

- `path` is the exact public path (`/wp-content/uploads/...`), absolute, without query, fragment or `..`. It may not
  start with `/admin` or `/m/`.
- Every path is unique across the whole bundle.
- `variant` is `orig` (the original bytes) or `resize` (needs `width` and `height`; `crop` defaults to `false`).
- Re-importing a media item replaces its set of paths. Hit counts are kept for paths that remain.

**Duplicate files.** Two records with the same `sha256` are stored once. The first record wins. Later ones become
aliases: references to them point at the first record, their legacy paths attach to it, and their metadata is ignored
(with a warning). The same applies when the file already exists in AFSC under a different key.

### authors.ndjson

| Field | Type | Notes |
|---|---|---|
| `legacy_key` | string | |
| `slug` | string | Unique. Lowercase letters (any script), digits, `-`, `_`. |
| `display_name` | string | Required. |
| `bio`, `website` | string | `website` must be http(s). |
| `social` | object | Name → https URL. |
| `avatar_media` | media key | |
| `user` | user key | `null` = guest author. A user can be linked to one author only. |
| `seo` | SEO object | |

### terms.ndjson

| Field | Type | Notes |
|---|---|---|
| `legacy_key` | string | |
| `taxonomy` | `category` \| `tag` | |
| `slug` | string | Unique per taxonomy and parent. |
| `name` | string | Required. |
| `description` | string | |
| `parent` | term key | Categories only; the parent must be a category. No cycles. |
| `seo` | SEO object | |

### posts.ndjson

| Field | Type | Notes |
|---|---|---|
| `legacy_key` | string | |
| `type` | `post` \| `page` | |
| `status` | `draft` \| `pending` \| `scheduled` \| `published` \| `private` \| `trash` | Password-protected posts map to `private`. |
| `slug` | string | Unique per type and parent. |
| `parent` | post key | Pages only, must be a page. No cycles. |
| `title`, `excerpt` | string | |
| `content` | ProseMirror JSON | See [Content](#content). |
| `created_at` | time | Required. |
| `updated_at` | time | Default `created_at`. |
| `published_at` | time | Required for `published` and `scheduled`. |
| `authors` | array of author keys | Ordered. |
| `terms` | array of `{term, primary?}` | At most one `primary`, and it must be a category. |
| `featured_media` | media key | |
| `template` | string | `[a-z0-9_-]{1,64}`. |
| `sticky` | bool | |
| `menu_order` | int | |
| `comment_status` | `open` \| `closed` | Default `open`. |
| `feed_guid` | string | Preserved RSS guid. |
| `seo` | SEO object | |
| `videos` | array | `{provider, external_id, url, title, description, thumbnail_url, thumbnail_media, duration_seconds, upload_date, family_friendly}`. `url` is required, http(s), unique per post. Stored as `source = 'import'`. |

### SEO object

Used by posts, terms and authors. Every field is optional. A missing or all-null object removes the entity's SEO row.

`title`, `description`, `canonical`, `robots_noindex`, `robots_nofollow`, `robots_advanced` (array of strings),
`og_title`, `og_description`, `og_image_media` (media key), `twitter_title`, `twitter_description`,
`twitter_image_media` (media key), `focus_keyphrase`, `breadcrumb_title`, `is_cornerstone` (bool).

Titles and descriptions may use Yoast `%%variables%%`; they are stored as given and expanded per request.

How each field is used on the public site (see `docs/themes.md`, `head`, for the full resolution order):

| Field | Effect |
|---|---|
| `title`, `description` | Override the type template. Variables are expanded. |
| `canonical` | Absolute URL or root-relative path. Replaces the self canonical. |
| `robots_noindex`, `robots_nofollow` | `null` falls back to the type default (`seo.tag_noindex` for tags; otherwise index, follow). |
| `robots_advanced` | Values such as `noarchive`, `noimageindex`, `nosnippet`. They replace the default `max-snippet:-1, max-image-preview:large, max-video-preview:-1`. |
| `og_title`, `og_description`, `og_image_media` | Open Graph overrides. Variables are expanded. |
| `twitter_title`, `twitter_description`, `twitter_image_media` | Emitted as `twitter:*` tags only when set. |
| `breadcrumb_title` | Replaces the entity's name in breadcrumbs and in the JSON-LD `BreadcrumbList`. |
| `focus_keyphrase`, `is_cornerstone` | Stored for the editor's SEO panel; not printed. |

**Supported variables:** `%%title%%` `%%sitename%%` `%%sitedesc%%` `%%sep%%` `%%page%%` `%%pagenumber%%`
`%%pagetotal%%` `%%primary_category%%` `%%category%%` `%%tag%%` `%%term_title%%` `%%term_description%%`
(`%%category_description%%` and `%%tag_description%%` are aliases) `%%name%%` `%%author_first_name%%`
`%%author_last_name%%` `%%excerpt%%` `%%excerpt_only%%` `%%date%%` `%%modified%%` `%%currentyear%%`
`%%currentmonth%%` `%%searchphrase%%` `%%pt_single%%` `%%pt_plural%%`.

- `%%page%%` renders `{sep} Page N of M` only from page 2 on.
- `%%excerpt%%` is the explicit excerpt. It falls back to the start of the content only when `seo.auto_description`
  is on. `%%excerpt_only%%` never falls back.
- `%%name%%`, `%%author_first_name%%` and `%%author_last_name%%` use the first author; first and last name are the
  display name split at the first space.
- Unknown variables (for example `%%focuskw%%` or `%%cf_*%%`) render as nothing. After expansion, whitespace is
  collapsed and leading, trailing or repeated separators are removed.

Videos with a `title`, a thumbnail (`thumbnail_media` or `thumbnail_url`) and an `upload_date` become JSON-LD
`VideoObject`s. YouTube and Vimeo videos get an `embedUrl` built from `external_id`; others use `url` as
`contentUrl`. When several rows share a URL, a manually edited one wins over an imported one.

### comments.ndjson

| Field | Type | Notes |
|---|---|---|
| `legacy_key` | string | |
| `post` | post key | Required. |
| `parent` | comment key | Must belong to the same post. |
| `author_name`, `author_email`, `author_url` | string | `author_url` must be http(s). An invalid email is a warning. |
| `user` | user key | |
| `ip` | string | IPv4 or IPv6, or empty. |
| `user_agent` | string | |
| `body` | string | Plain text, required. |
| `status` | `pending` \| `approved` \| `spam` \| `trash` | Labelled spam/approved comments will seed the spam classifier (M4). |
| `created_at` | time | Required. |

### menus.ndjson

`{legacy_key, slug, name, location?, items}`.

- `slug` is `[a-z0-9_-]{1,64}`, unique.
- `location` is a theme menu location. It must be unique across menus. An undeclared location is a warning.
- `items[]` is `{legacy_key, parent?, position, label, kind, target?, url?, new_tab, css_classes}`:
  - `kind` is `post`, `term`, `author` or `url`.
  - `target` is the matching legacy key; required unless `kind` is `url`.
  - `url` is required for `url` items: http(s), `mailto:`, `tel:` or relative.
  - `parent` must be an item of the same menu.

Re-importing a menu replaces its items. Items added in AFSC to an imported menu are removed.

### redirects.ndjson

`{legacy_key, source, match, target?, status, note?}`.

- `match` is `exact`, `prefix` (source starts with `/`) or `regex`. Regexes use Go's RE2 syntax; PCRE-only features
  (look-arounds, backreferences in the pattern) fail validation.
- `status` is one of `301 302 307 308 410 451`. `target` is required except for 410 and 451.
- Sources under `/admin` are rejected.
- An exact source already used by a redirect from outside the bundle is skipped with a warning.
- **No chain or loop checks on import.** Imported redirects are written as given (`origin: "import"`), so a bundle
  can bring in chains it had in WordPress. The checks run when an editor saves one of them through the admin API,
  which keeps its `origin` and `legacy_key`; a re-import overwrites that edit. Flatten chains in the bundle when
  you can: point each redirect at the final URL rather than at another redirect's source.
- Exact sources are stored as given. The site matches them against both the raw and the canonical request path,
  so give the canonical form (lowercase, with the trailing slash policy applied) where you can.

### legacy.ndjson

`{legacy_key, kind, source}`, where `source` is any JSON object. These are raw source rows kept for audit and
re-conversion, stored in `legacy_records`.

## settings.json

An object with any of the keys below. Every nested key is optional and defaults as shown. Unknown keys are errors.
Reference fields take legacy keys.

| Key | Fields (default) |
|---|---|
| `site` | `title` (""), `tagline` (""), `timezone` ("UTC", IANA name), `date_format` ("January 2, 2006", Go layout), `language` ("en"; a BCP 47 tag such as `en-IN`, also used for `og:locale`, where a bare `en` becomes `en_US`) |
| `permalinks` | `post_pattern` ("/{slug}/"; tokens `{slug} {year} {month} {day} {id} {category}`, must contain `{slug}` or `{id}`), `category_base` ("category"), `tag_base` ("tag"), `author_base` ("author"), `trailing_slash` ("always" \| "never"), `date_archives_enabled` (true), `author_archives_enabled` (true), `wpcompat_enabled` (true; the `/?p=` style WordPress URL layer) |
| `reading` | `home` (null = latest posts, or a page key), `posts_page` (page key or null), `posts_per_page` (10, 1–100), `feed_items` (10, 1–100), `feed_excerpt_only` (false) |
| `seo` | `separator` ("-"), `title_templates` and `description_templates` (type → template; types `post page category tag author home search 404 date`; given entries override the defaults), `knowledge_graph` (`{type: "organization" \| "person", name, logo_media}`), `social_profiles` (https URLs), `twitter_site`, `default_image_media` (media key), `tag_noindex` (false), `auto_description` (false), `robots_txt` ("" = allow all plus the `Sitemap:` line; otherwise served verbatim as `/robots.txt`), `llms_txt` (`{mode: "off" \| "auto" \| "custom", body: ""}`; `custom` needs a `body`), `sitemaps` (`{page_size: 1000}`, 1–50000 URLs per sitemap file), `indexnow` (`{enabled: false, key: ""}`; the key is 8–128 letters, digits or dashes and is required when enabled), `not_found` (`{noise_patterns: ["*.php", "/.env*", "/.git/*", "/wp-admin/*", "*.asp*", "/cgi-bin/*"], retention_days: 90}`; see below) |
| `discussion` | `comments_enabled` (true), `close_after_days` (0 = never), `thread_depth` (3, 1–10) |
| `cache` | `page_ttl_seconds` (60, 0–86400; how long browsers, CDNs and the in-process cache keep public pages; 0 = `no-cache`, nothing kept in-process) |

The `seo` keys `robots_txt`, `llms_txt`, `sitemaps`, `indexnow` and `not_found` were added in M2. Bundles without
them stay valid. When migrating from WordPress:

- `robots_txt`: the site's current `/robots.txt` text when it was customised (a physical file or Yoast's file
  editor). Leave it empty for WordPress's virtual default; AFSC's default is equivalent and points at the AFSC
  sitemap index.
- `sitemaps.page_size`: Yoast's `wpseo_sitemap_entries_per_page` filter value when the site changed it; otherwise
  leave the default of 1000, which is also Yoast's default and keeps the same sitemap file names.
- `indexnow`: the key the site already serves, if any, so search engines don't need to verify a new one.
- `llms_txt`: the current `/llms.txt` as `{mode: "custom", body}` when the site has one.
- `not_found`: leave it out to keep the defaults. If the Redirection plugin's 404 log was configured to ignore
  paths, add those as patterns.

`seo.not_found` controls the 404 log:

- `noise_patterns` are globs matched against the whole request path, ignoring case: `*` matches any run of
  characters including `/`, `?` matches one character, everything else is literal. Each pattern starts with `/` or
  `*` and is at most 256 bytes; at most 200 patterns. Matching 404s are never logged. Giving the key replaces the
  default list; `[]` logs every 404.
- `retention_days` (1–3650) is how long a path stays in the log after it was last seen.

## Content

`posts[].content` is a ProseMirror document validated against the AFSC schema (`internal/content/schema.go`).
`internal/content/testdata/schema.json` is the exported schema, and `internal/content/testdata/all-nodes.json`
uses every node and mark.

- **Media references:** `image.attrs.mediaId`, `video.attrs.mediaId` and `video.attrs.posterMediaId` must be
  **media legacy keys** in a bundle (`"mediaId": "wp:post:456"`). AFSC replaces them with its own ids.
- **Classes:** `classes` attributes not in the theme's `content.allowed_classes` are dropped, with a warning.
- **rawHtml:** `rawHtml.attrs.html` is passed through the trusted sanitizer. Every removal (element, attribute,
  reason) is listed in the report.
- **Shortcodes** aren't supported; convert them to nodes (galleries, embeds, captions) before export.
- **Embeds:** providers are `youtube vimeo x instagram spotify soundcloud googlemaps`.
  - `url` must belong to the provider.
  - `youtube` and `vimeo` also need `externalId`.
  - Spotify URLs must be `open.spotify.com/<track|album|playlist|episode|show|artist>/<id>`.
  - Google Maps URLs must be `https://www.google.com/maps/embed?...`.

Validation errors name the exact node, for example
`{"legacy_key":"wp:post:123","field":"content.content[3].attrs.level","message":"must be between 1 and 6"}`.

## Report

```json
{
  "source": "wp",
  "dry_run": false,
  "counts": {"posts": {"created": 9, "updated": 0, "unchanged": 0, "skipped": 0}},
  "errors": [{"legacy_key": "wp:post:1", "field": "slug", "message": "..."}],
  "warnings": [{"legacy_key": "wp:post:126", "field": "content.content[0].attrs.classes", "message": "..."}],
  "sanitizer_removals": [{"legacy_key": "wp:post:126", "path": "content[1]", "element": "script", "reason": "element not allowed"}],
  "invites": [{"legacy_key": "wp:user:2", "email": "author.a@example.com", "command": "afsc user invite --email author.a@example.com"}],
  "prune": [{"table": "posts", "count": 1, "sample": ["wp:post:77"]}]
}
```

- `counts` keys: `users media media_legacy_paths authors terms posts post_authors post_terms seo post_videos comments
  menus menu_items redirects legacy_records settings`. Counts also include `deleted` where an import replaces a set
  (legacy paths, SEO rows, videos, menu items) or prunes.
- `skipped` covers duplicate-file media aliases and redirects that clash with existing ones.
- The report file is written with mode 0600 because it contains email addresses.
