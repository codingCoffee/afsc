# afsc-wp.json

Everything site-specific lives here. Unknown keys are errors. Relative paths are resolved against the directory that
holds the config file. `testdata/afsc-wp.fixture.json` is a complete example.

| Key | Default | Meaning |
|---|---|---|
| `source` | `"wp"` | Legacy-key prefix and AFSC prune scope, `[a-z0-9]{1,16}`. |
| `work_dir`, `reports_dir`, `baseline_dir`, `bundle_dir` | `work`, `reports`, `baseline`, `bundle` | Output locations. They hold site data: keep them on encrypted local disk and out of git. |
| `decisions_file` | `decisions.json` | The decision table ([decisions.md](decisions.md)). |
| `mysql.dsn_env` | `AFSC_WP_MYSQL_DSN` | Environment variable holding the go-sql-driver DSN of the **local restore**. Use a read-only user. |
| `mysql.dsn` | — | DSN in the file. Prefer `dsn_env`, so credentials stay out of the config. |
| `mysql.table_prefix` | `wp_` | Checked against `^[A-Za-z0-9_]+$` before use. |
| `uploads_dir` | — | Local copy of the **whole** `wp-content/uploads`. |
| `uploads_url_path` | `/wp-content/uploads` | Public path uploads were served under (`upload_url_path` if the site changed it). |
| `site.base_url` | — | Production URL, e.g. `https://blog.example.com`. Its host, its `www.` twin and `siteurl`/`home` are site hosts. |
| `site.old_hosts` | `[]` | Other domains that ever served the site. Links on them become internal; the proxy handles redirects. |
| `site.language` | WPLANG, else `en` | BCP 47 tag for `site.language`. |
| `site.timezone` | from options | IANA zone. Required when WordPress uses a fractional `gmt_offset`. |
| `site.post_types.<type>` | — | Custom post types imported as posts: `wp_path` (their WordPress permalink, e.g. `/updates/%postname%/`), `menu_order_meta` (numeric meta copied into `menu_order`) and `category` (a category slug added to every post of the type, created when missing; with a `{category}` post pattern it keeps `/updates/…` URLs, and the type's archive redirects to the category). |
| `site.taxonomy_bases` | `{}` | Archive base of custom taxonomies, e.g. `{"modules": "module"}`. A custom taxonomy mapped to `category` or `tag` merges into an existing term with the same slug. |
| `site.permalinks` | derived | `post_pattern` and `category_base` overrides for AFSC; posts whose URL changes get a 301. |
| `site.robots_txt_file` | — | The site's customized `robots.txt` (physical file or Yoast editor). Empty keeps AFSC's default. |
| `site.indexnow_key` | — | The IndexNow key the site already serves, if any. |
| `site.page_templates` | derived | `_wp_page_template` value → AFSC template name, e.g. `{"tpl/wide.php": "wide"}`. Unlisted files become their base name. |
| `content_selector` | `.entry-content` | CSS selector of the theme's article body (crawl and verify). Supports tags, `#id`, `.class`, `[attr]`, `[attr=v]`, descendants and `,` groups. |
| `wp_code_dirs` | `[]` | Theme, child theme and mu-plugins directories, scanned for hooks, shortcodes, menus, image sizes and hosts. Read only. |
| `theme_dir` | — | The AFSC theme (scaffolded or ported). Its `content.allowed_classes` decides which content classes are kept; without it every class is dropped. |
| `filter.command` | — | argv that runs `tools/wp-filter.php` ([tools/README.md](../tools/README.md)). afsc-wp appends mode flags. |
| `filter.replay_file` | — | Recorded wp-filter output to use instead of PHP (tests, fixture runs). |
| `filter.timeout_seconds`, `filter.batch_size` | 600, 500 | Per batch. |
| `afsc.command` | `["afsc"]` | argv prefix for AFSC; `all` appends `import --bundle … --dry-run --report …`. |
| `afsc.target_base_url` | — | AFSC base URL `verify` checks: local or staging, **never production**. |
| `crawl.base_url` | `site.base_url` | Where `crawl` starts. |
| `crawl.delay_ms` | 500 | Pause after every request, per worker. Concurrency is fixed at 2. |
| `crawl.user_agent` | `afsc-wp-crawler/1.0 (…)` | Identifying User-Agent. |
| `crawl.timeout_seconds`, `crawl.max_depth`, `crawl.max_urls` | 20, 2, 50000 | Per request; link depth from the seeds; hard cap. |
| `crawl.extra_url_files` | `[]` | Analytics and Search Console exports (CSV or text); the first URL-looking column is used. |
| `verify.afsc_content_selector` | `content_selector` | Article body selector in the AFSC theme. |
| `verify.approved_404` | `[]` | Globs (`*` spans `/`) of paths allowed to 404 after cutover. |
| `verify.import_report` | — | `afsc import --report` output for the counts check. |
| `verify.screenshots_dir` | — | Holds `wp/*.png` and `afsc/*.png` for the visual check. |
| `verify.visual_threshold` | 0.005 | Share of differing pixels allowed. |
| `verify.dynamic_selectors` | `[]` | Regions to hide before capturing screenshots (dates, ads, counters). |
| `verify.top_urls_file` | — | Top URLs by traffic; the first 150 join the visual sample. |
| `plugins.<id>.enabled` | auto | Force an adapter on or off. |
| `plugins.<id>.keys` | defaults below | Override meta keys and option names. |
| `plugins.guest_author.mode` | `join` | `replace`: the guest author is shown instead of the WordPress author. `join`: both, WordPress author first. Match what the theme displays. |
| `generated_at` | latest modification | RFC 3339 `manifest.generated_at`. The default is derived from the data, so runs are reproducible. |

## Plugin adapters

Adapters are on when their plugin is in `active_plugins` or their meta keys appear in the inventory. `core` is always
on; `guest_author` is on only when `keys.name` is configured and present.

**Every default below is the plugin's publicly known key. Verify each one against the meta-key and option tables in
`reports/inventory.md` before transforming, and override what differs.**

| Adapter | Detected by | Keys (default) |
|---|---|---|
| `core` | always | `thumbnail` `_thumbnail_id`, `page_template` `_wp_page_template`, `old_slug` `_wp_old_slug`, `old_date` `_wp_old_date`, `attached_file` `_wp_attached_file`, `attachment_metadata` `_wp_attachment_metadata`, `image_alt` `_wp_attachment_image_alt`, `user_bio` `description`, `first_name`, `last_name` |
| `yoast` | `wordpress-seo/`, `wordpress-seo-premium/`, or `_yoast_wpseo_*` meta | Post: `title` `_yoast_wpseo_title`, `description` `_yoast_wpseo_metadesc`, `focus_keyphrase` `_yoast_wpseo_focuskw`, `canonical`, `noindex` `_yoast_wpseo_meta-robots-noindex` (1 noindex, 2 index), `nofollow`, `robots_advanced` `_yoast_wpseo_meta-robots-adv`, `breadcrumb_title` `_yoast_wpseo_bctitle`, `primary_category`, `cornerstone` `_yoast_wpseo_is_cornerstone`, `og_title/og_description/og_image/og_image_id` `_yoast_wpseo_opengraph-*`, `twitter_*` `_yoast_wpseo_twitter-*`, `redirect` `_yoast_wpseo_redirect`. Options: `opt_titles` `wpseo_titles`, `opt_social` `wpseo_social`, `opt_main` `wpseo`, `opt_taxonomy_meta` `wpseo_taxonomy_meta`. Usermeta: `author_title` `wpseo_title`, `author_description` `wpseo_metadesc`, `author_noindex` `wpseo_noindex_author`, `author_social` (comma list) |
| `yoast_premium` | `wordpress-seo-premium/` | `opt_redirects` `wpseo-premium-redirects-base` (list of origin/url/type/format), `opt_export_plain`, `opt_export_regex` |
| `yoast_video` | `wpseo-video/` or `_yoast_wpseo_video*` meta | `video_meta` `_yoast_wpseo_video_meta`, `not_family_friendly` `_yoast_wpseo_videositemap-not-family-friendly`, `disable` `_yoast_wpseo_videositemap-disable`, `opt_video` `wpseo_video` |
| `jetpack` | `jetpack/` | `opt_modules` `jetpack_active_modules`, `cdn_hosts` `i0.wp.com,…,i3.wp.com`, `shortcodes` (Jetpack's shortcode names) |
| `user_profile_picture` | `metronet-profile-picture/` or `metronet_*` meta | `avatar` `metronet_image_id` (an attachment id) |
| `guest_author` | `keys.name` configured and present in postmeta | `name`, `email`, `url`, `bio`, `slug`, `avatar`: postmeta keys, all empty by default |

Other active plugins are listed in the inventory as `plugin` decisions and are not converted.

Only usermeta keys the adapters name are extracted; the rest (session tokens, dashboard settings, IP-bearing keys) are
skipped and listed in the inventory. Options outside the core list and the adapters' options are recorded by name only.
Option names that look like credentials are listed under "Credentials to rotate"; their values are never read into a
report.
