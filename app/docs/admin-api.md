# Admin API (M3, M4)

Every route is under `/admin/api/v1`, registered through `route()` with one capability (see `internal/authz`).
Requests carry `Authorization: Bearer <access token>`. Every non-GET request must send
`Origin: AFSC_PUBLIC_BASE_URL` and, when it has a body, `Content-Type: application/json`. The only exceptions are
`POST /redirects/import`, `POST /media` and `PUT /media/:id/file`, which take raw bodies.

Errors are `{"message": "...", "field"?: "...", "code"?: "..."}`. A missing capability is 403 with
`code: "capability"`. An ownership refusal is 403 with `code: "ownership"`. Lists are
`{"items": [...], "page": 1, "page_size": 20, "total": 0}` and accept `?page=&page_size=` (max 100).

TypeScript mirrors of every shape are in `client/app/types/api.ts`. Keep the two in sync.

## Auth, users, audit (M3a)
| Method | Path | Cap |
|---|---|---|
| POST | /signin, /token_refresh, /signout | public |
| GET, POST | /setup/status, /setup | public (404 once a user exists) |
| GET, POST | /invites/:token | public |
| GET, PATCH | /me | self |
| POST | /me/password, /me/sessions/revoke-others | self |
| GET | /me/sessions | self |
| GET, POST | /users | users.manage |
| GET, PATCH, DELETE | /users/:id | users.manage |
| POST | /users/:id/disable, /users/:id/enable, /users/:id/invite, /users/:id/sessions/revoke | users.manage |
| GET | /users/:id/sessions | users.manage |
| GET | /audit | audit.read |

## Posts and pages (M3b)
`type` is `post` or `page`. Page routes require `pages.edit` in the service layer. The route cap is
`posts.edit_own`; ownership and status rules are enforced by `internal/posts`.

| Method | Path | Cap | Notes |
|---|---|---|---|
| GET | /posts | posts.edit_own | `?type=post|page&status=&author=<author id>&term=<term id>&q=&from=&to=&sort=date|-date|title|modified&mine=1`. Returns `PostListItem`. |
| POST | /posts | posts.edit_own | `PostInput` → 201 `PostDetail` |
| GET | /posts/:id | posts.edit_own | `PostDetail`. Includes `autosave` when the caller has one newer than the post. |
| PUT | /posts/:id | posts.edit_own | `PostInput` with `version`; `?force=1` overwrites on a conflict (editor+). 409 `{code:"conflict", current_version, modified_by, modified_at}`. 422 `{code:"invalid_content", errors:[{path,message}]}` |
| PATCH | /posts/:id | posts.edit_own | quick edit: `{version, title?, slug?, status?, published_at?, category_ids?, tag_ids?}` |
| POST | /posts/:id/autosave | posts.edit_own | `{title, excerpt, content, seo?}` → `{revision_id, saved_at}` |
| POST | /posts/:id/preview | posts.edit_own | `{revision_id?}` → `{url, expires_at}` (url = `{base}/?preview=<token>`) |
| GET | /posts/:id/revisions | posts.edit_own | `RevisionItem[]` (no content) |
| GET | /revisions/:id | posts.edit_own | `Revision` (with content) |
| GET | /revisions/:id/diff | posts.edit_own | `?against=<revision id>|current` (default current) → `RevisionDiff`; the ops turn revision `:id` into `against` |
| POST | /posts/:id/trash | posts.edit_own | `{gone: "410"|"301"|"none", target?}`. `gone` is required only when the post is published. → `PostDetail` |
| POST | /posts/:id/restore | posts.edit_own | back to draft → `PostDetail` |
| DELETE | /posts/:id | posts.edit_others | only from trash |
| POST | /posts/bulk | posts.edit_own | `{ids, action: "publish"|"trash"|"restore"|"set_terms"|"add_terms"|"set_authors", term_ids?, author_ids?, gone?}` → `{results:[{id, ok, message?}]}` |
| GET | /posts/slug-check | posts.edit_own | `?type&slug&parent_id&exclude=` → `{available, suggestion}` |
| GET | /templates | posts.edit_own | theme templates usable as overrides and the theme's container class allowlist: `{post: string[], page: string[], allowed_classes: string[]}` |

## Media (M3c)
| Method | Path | Cap | Notes |
|---|---|---|---|
| GET | /media | posts.edit_own | `?q=&type=image|video|audio|pdf&legacy=1&page=` → `MediaItem` page |
| POST | /media | media.upload | raw body, `Content-Type` = file type, `X-Filename` header (URL-encoded). 201 new, 200 duplicate (same sha256) → `MediaItem` |
| GET | /media/:id | posts.edit_own | `MediaItem` + `used_in: [{id, type, title, status}]` + `legacy_paths: [{path, hits, last_hit_at, variant}]` |
| PATCH | /media/:id | media.upload | `{title?, alt?, caption?, description?, focal_x?, focal_y?}` (uploader or editor+) |
| PUT | /media/:id/file | media.upload | raw body; replace the file and keep the id; `?confirm=1` needed when legacy paths exist (else 409 `{code:"legacy_paths", legacy_paths}`) |
| DELETE | /media/:id | content.manage | `?confirm=1` needed when legacy paths exist |
| GET | /media/legacy | content.manage | legacy upload paths by hits: `{path, media_id, hits, last_hit_at}` page |

Uploads are sniffed, never trusted by name or `Content-Type`; a mismatched extension is replaced with the sniffed
type's. Refusals: 411 without `Content-Length`, 413 `code:"too_large"` over the `media` settings limit for the
class, 415 `code:"type"` outside the allowlist (or a replace with another class of file), 403 `code:"capability"`
for SVG without `content.raw_html`, 403 `code:"ownership"` when editing someone else's upload without
`content.manage`, 409 `code:"duplicate"` (`media_id`) when a replacement is already another item.

## Terms, authors, menus (M3c)
| Method | Path | Cap | Notes |
|---|---|---|---|
| GET | /terms | posts.edit_own | `?taxonomy=category|tag&q=&page=&all=1` (`all` returns every term, for trees and pickers) → `Term` |
| POST | /terms | posts.edit_own for tags (editor picker creates tags), content.manage for categories | `TermInput` → 201 `Term` |
| GET, PUT, DELETE | /terms/:id | content.manage (GET: posts.edit_own) | |
| POST | /terms/:id/merge | content.manage | `{into: <term id>}` |
| GET | /authors | posts.edit_own | `?q=&page=&all=1` → `Author` |
| POST | /authors | content.manage | `AuthorInput` |
| GET, PUT, DELETE | /authors/:id | content.manage (GET: posts.edit_own) | DELETE refuses while posts reference the author unless `?reassign=<author id>` |
| GET | /menus | content.manage | `{menus: Menu[], locations: [{location, label, menu_id}]}` |
| POST | /menus | content.manage | `{name, slug}` |
| GET, PUT, DELETE | /menus/:id | content.manage | PUT `{name, slug, items: MenuItemInput[]}` (a whole tree) |
| PUT | /menu-locations | content.manage | `{locations: {location: menu_id|null}}` → same shape as GET /menus |

Slug, parent and merge changes record 301s for every moved archive (and post URLs under `{category}`). Deleting a
category moves its subcategories up a level. Author DELETE with posts and no `reassign` is 409 `code:"has_posts"`
(`post_count`). Only `users.manage` may set `user_id`; for others it is ignored. Menu items: `label` required,
`url` items take `http(s)://…`, `/path` or `#anchor`; at most 5 levels and 500 items; errors name the item as
`field: "items.0.children.1.url"`.

## Settings and dashboard (M3e)
| Method | Path | Cap | Notes |
|---|---|---|---|
| GET | /settings | settings.manage | `{values: {group: value}, theme: {name, settings: ThemeSettingDecl[]}, csp_report_only}` |
| PUT | /settings/:group | settings.manage | body = the whole group value; 400 `{message, field}` from settings.Validate; returns `{group, value}` |
| GET | /dashboard | self | `Dashboard` |

`:group` is one of `site permalinks reading seo discussion cache media security snippets theme_settings`.
`snippets` and `security` are admin-only like every settings group. Every save writes an audit diff and purges the
whole page cache.

The body replaces the group; omitted fields take their defaults and unknown keys are a 400. `field` is relative to the
group (`timezone`, `extra_csp_hosts.script-src`, `knowledge_graph.type`; `<theme name>.<key>` for theme_settings).
References (`reading.home`, `reading.posts_page`, the seo media refs) must be ids of an existing page or media item.
Only the active theme's values are checked against its theme.json declarations; other themes' values are kept.

## SEO (M3f)
| Method | Path | Cap | Notes |
|---|---|---|---|
| POST | /seo/resolve | posts.edit_own | `SeoResolveInput` → `SeoResolved` |
| GET, PUT | /seo/:entity_type/:entity_id | content.manage (post: posts.edit_own + ownership) | entity_type `post|term|author|home`; body `SeoFields` |
| GET | /seo/bulk | content.manage | `?type=post|page&missing=title|description&q=&page=` → `SeoBulkRow` page |
| PATCH | /seo/bulk | content.manage | `{rows: [{post_id, title?, description?}]}` (1-100 rows; absent keeps, null clears) → `{updated}` |
| GET | /seo/videos/:post_id, PUT same | posts.edit_own + ownership | `{items: VideoRow[]}` both ways. PUT replaces the manual rows; auto and imported rows are read-only, and a manual row with the same URL wins |

Home SEO is `entity_id` 0 and applies to the latest-posts home; a static front page uses its page's fields.
`SeoResolved.twitter` falls back to the OG values, as crawlers do, though `{{head}}` only prints overrides. Checks
(`internal/seo/checks.go`) target titles of 30-60 characters and descriptions of 120-156; `duplicate_title`
compares the title override (else the post title), `duplicate_description` the description override, and
`keyphrase_used` the focus keyphrase with every post that isn't trashed. Suggestions are the top 10 published posts
matching any word of the title or keyphrase.

## Comments and spam (M4c)
| Method | Path | Cap | Notes |
|---|---|---|---|
| GET | /comments | comments.moderate | `?status=pending|approved|spam|trash` (default pending)`&post_id=&q=&page=&page_size=` → `CommentList`, newest first. `q` matches body, name and email (substring) or the exact IP. `counts` is per status, honouring `post_id` only. |
| POST | /comments/bulk | comments.moderate | `{ids: number[] (1-500), action}` → `{changed: CommentChange[]}`. Actions: `approve`, `unapprove` (→ pending), `spam`, `not_spam` (→ approved), `trash`, `restore` (→ pending), `delete`. Comments already in the target status are skipped. `delete` is permanent, only for spam and trash (409 `code: "not_deletable"` otherwise), and writes one `comment.delete` audit entry per comment. |
| POST | /comments/:id/reply | comments.moderate | `{body}` (2-5000 chars) → 201 `{id, post_id, parent_id, status: "approved"}`. Posted as the signed-in user (linked author name, else display name, else username). It skips the spam pipeline and the closed check, and replies past `thread_depth` attach to the deepest allowed ancestor. |
| GET | /spam/blocklist | settings.manage | `SpamBlocklist` |
| PUT | /spam/blocklist | settings.manage | `{entries: {kind: string[]}}` replaces the whole list → `SpamBlocklist`. Kinds `keyword domain ip cidr email`; values are trimmed and normalised (lowercase, masked CIDR, unmapped IP), ≤200 bytes each, ≤5000 in total; 400 `field: "entries.<kind>[i]"`. Audit-logged as `spam.blocklist` with `{added, removed}`. |
| POST | /spam/retrain | settings.manage | Queues `spam.retrain` → 202 `{queued: true}` |

- **Training.** Every status change trains the classifier: approved is ham, spam is spam, and pending and trash are
  unlabelled.
- **Purges.** A change into or out of `approved`, and every staff reply, purges the post's cached page.
- **Delete.** Deleting spam keeps its tokens in `spam_archive`, so the classifier doesn't forget it.
- **Classifier stats.** `classifier` in `SpamBlocklist` is `{spam, ham, min: 200, active}`. The classifier only
  scores once it has `min` messages of each label.
- **Thresholds and retention** are in the `discussion` settings group: `spam_threshold` (6),
  `moderation_threshold` (2, below the spam threshold), `trusted_auto_approve` (true), `comments_per_page` (50,
  0 = all), `comment_ip_retention_days` (90) and `spam_retention_days` (30).

`GET /dashboard` adds `pending_comments: {count, latest: DashComment[5]}` for roles with `comments.moderate`.
