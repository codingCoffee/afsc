# Issues for AFSC

Gaps found while building `afsc-wp`. AFSC was not modified; each item says how the migrator works around it today.
This file is written by hand. The tool also lists run-time gaps under "Gaps in AFSC found during this run" in
`reports/transform.md`.

## 1. Slugs can't contain combining marks (blocks Indic-script URLs)

`importer/validate.go` accepts slugs matching `^[\p{Ll}\p{Lo}\p{Nd}_-]{1,200}$`. Devanagari and most Indic scripts
spell words with vowel signs and viramas (`\p{Mn}`, `\p{Mc}`), so a WordPress slug like `नमस्ते` is rejected.

- **Workaround:** afsc-wp rewrites such slugs (`नमस्ते` → `नमस-त`), emits a 301 from the old URL and notes it in
  `reports/transform.md`. The URL still changes, which is what the migration tries to avoid.
- **Ask:** allow `\p{M}` in post, term and author slugs (validator, admin API and router).

## 2. `{id}` in `post_pattern` is AFSC's id, not the WordPress id

`site.Permalinks.Post` fills `{id}` with the AFSC post id. Sites using `%post_id%` permalinks would change every URL.

- **Workaround:** afsc-wp treats `%post_id%` as unsupported. The `permalink_structure` decision must be `change`,
  and every post gets a 301.
- **Ask:** use `WPID` (the numeric id from a `wp:post:N` legacy key) for `{id}` when present, as the class helpers do.

## 3. Legacy upload paths can't contain spaces

The importer rejects legacy paths containing a space, `?` or `#`. FTP-uploaded files often have spaces
(`/wp-content/uploads/2019/06/logo final.png`, requested as `logo%20final.png`), and they are exactly the email-signature
files §9.1 exists for.

- **Workaround:** afsc-wp skips those paths and lists them as "Paths AFSC can't serve" in `reports/transform.md`.
- **Ask:** accept spaces in decoded legacy paths (the lookup already compares decoded paths), or document the
  encoded form to use.

## 4. Media types outside the allowlist can't keep their URLs

Only ten MIME types are importable. Uploads such as `.mov`, `.m4a`, `.ogg`, `.wav`, `.ico` and `.bmp` can't be imported,
so their `/wp-content/uploads/...` URLs will 404 after cutover.

- **Workaround:** they are reported ("media type AFSC does not accept") and not imported.
- **Ask:** a passthrough "legacy file" kind for safe non-HTML types, served with the extension's Content-Type and
  `Content-Disposition: attachment`, or a wider allowlist.

## 5. A menu can have only one location

WordPress lets one menu fill several theme locations. The bundle's `menus[].location` is a single string.

- **Workaround:** afsc-wp keeps the first location (sorted by name) and reports the others.
- **Ask:** `locations: []` on menus, or allow the same menu to be referenced by several locations.

## 6. No date-archive noindex setting

Yoast's `noindex-archive-wpseo` has no AFSC equivalent. Posts, pages, categories and authors can be noindexed per
entity (afsc-wp copies the Yoast type default onto each entity), but date archives have no entity.

- **Workaround:** reported in `reports/transform.md`. The SEO diff will show `robots` differences on date archives.
- **Ask:** `seo.date_noindex` (and, for symmetry, type-level noindex for categories and authors).

## 7. Category and tag base must be one path segment

WordPress allows `blog/topics` or the `.` trick (no base). AFSC requires a single segment.

- **Workaround:** a `category_base` decision with value `change`, which changes the archive URLs and adds 301s.
- **Ask:** multi-segment bases.

## 8. No day archives

WordPress serves `/YYYY/MM/DD/`. AFSC serves year and month archives only.

- **Workaround:** every day archive found in the baseline crawl gets a 301 to its month archive.

## 9. Bundles can't be validated without Postgres

`importer.validate` is unexported, and `afsc import --dry-run` needs a database. The migrator therefore re-implements
part of the validation (content against `content.Validate`, settings through `settings.Decode`, record shapes through
strict decoding in tests) and runs the real dry run only in `afsc-wp all`.

- **Ask:** an exported `importer.Validate(dir, theme)`, or `afsc import --validate-only` that needs no database.

## 10. Dynamic blocks have no saved HTML

Third-party dynamic blocks (`<!-- wp:acme/thing /-->`) are rendered by PHP at request time, so there is nothing to
import.

- **Workaround:** flagged as `dynamic_block`. The `block` decision can't recover them; they need a theme partial or a
  plugin.

## 11. `docs/import-format.md` lists fewer settings keys than the decoder accepts

`settings.Decode` also accepts `media`, `security`, `snippets` and `theme_settings`. The format doc lists only `site`,
`permalinks`, `reading`, `seo`, `discussion`, `cache` and `plugins`.

- **Workaround:** afsc-wp emits only the documented groups. Theme options from `theme_mods_*` become `theme.json`
  defaults (via `theme-scaffold`), not `theme_settings`.
- **Ask:** document whether bundles may carry `theme_settings` and `security.extra_csp_hosts`.
