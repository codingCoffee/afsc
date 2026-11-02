# afsc-wp

`afsc-wp` migrates any WordPress site to AFSC. It reads a **local** MySQL/MariaDB restore and a copy of
`wp-content/uploads`, writes an AFSC import bundle (`docs/import-format.md` in AFSC), and verifies the imported site
against the old one. It implements `AFSC_Wordpress_Migration_Plan.md`. Site specifics live in two files:

- `afsc-wp.json`: connection, paths, hosts, selectors and plugin key overrides ([docs/config.md](docs/config.md)).
- `decisions.json`: the per-site decision table that `inventory` proposes and a human confirms
  ([docs/decisions.md](docs/decisions.md)).

Porting the site's theme is per-site work. `theme-scaffold` writes a starter theme to port it into.

## Pipeline

```
 production WordPress                local copies (you restore them)              AFSC
 ───────────────────                 ───────────────────────────────              ────
   site ──(human runs)── crawl ──▶ baseline/ (urls, feed.xml, sitemaps)
                                                                   │
   MySQL dump ─▶ local MariaDB ──┐                                 │
   uploads/ copy ────────────────┼─▶ inventory ─▶ reports/inventory.md + decisions.json (human confirms)
   theme + mu-plugins code ──────┘        │
                                          ▼
                     extract ─▶ work/extract/        (decoded rows, charset fixes, uploads scan)
                                          │
              tools/wp-filter.php ◀───────┤          (real WordPress: wpautop, wptexturize; shortcodes via PHP)
                                          ▼
                     transform ─▶ work/transform/ + work/url-map.ndjson + reports/transform.md
                                          │
                     bundle ─▶ bundle/  ──────────────────────────▶ afsc import --bundle … ─▶ AFSC (local/staging)
                                                                                                   │
                     verify ◀───────────── baseline/ + work/ + bundle/ ◀───────────────────────────┘
                       └─▶ reports/url-parity.md, seo-diff.md, content-fidelity.md, counts.md,
                           media.md, feeds.md, sitemaps.md, visual/

   all = extract → transform → bundle → afsc import --dry-run   (deterministic: same input, byte-identical bundle)
```

## Quick start

All commands run from `migrators/afsc-wp`. The tool never connects to production. You run `crawl` against
production yourself, and `verify` only against a local or staging AFSC.

```sh
cp testdata/afsc-wp.fixture.json afsc-wp.json   # then edit it for your site (docs/config.md)
export AFSC_WP_MYSQL_DSN='wp_readonly:…@tcp(127.0.0.1:3306)/wordpress'

go run ./cmd/afsc-wp inventory          # reports/inventory.md + decisions.json
$EDITOR decisions.json                  # confirm every item (status: "confirmed")
go run ./cmd/afsc-wp crawl              # against production, polite; writes baseline/
go run ./cmd/afsc-wp extract
go run ./cmd/afsc-wp theme-scaffold     # starter theme; set theme_dir to it
go run ./cmd/afsc-wp all                # extract → transform → bundle → afsc import --dry-run
# import into a local AFSC (see AFSC docs/import-format.md), then:
go run ./cmd/afsc-wp verify --import-report import.json
```

`transform` (and therefore `all`) refuses to run while any decision is unconfirmed, or when the data contains an item
the decision table has never seen. It fails closed.

## Reports

| Report | Written by | What it holds |
|---|---|---|
| `reports/inventory.md`, `inventory.json` | `inventory` | Shape of the site, plugins and adapter keys, shortcodes, blocks, HTML, images, links, hooks, uploads, and the decision table. Secrets appear as option names only. |
| `decisions.json` | `inventory` (proposes), you (confirm) | One item per decision ([docs/decisions.md](docs/decisions.md)). |
| `baseline/urls.ndjson`, `feed.xml`, `sitemaps/`, `robots.txt` | `crawl` | Per URL: status, redirect chain, head SEO fields, JSON-LD, H1, body by `content_selector`. |
| `reports/transform.md`, `transform.json` | `transform` | Counts and skips, conversion flags per kind, media problems (missing, unservable, not imported), redirects, settings notes, credentials to rotate. |
| `reports/content-fidelity-pre.md` | `transform` | Each published post rendered with AFSC's renderer and compared with WordPress's filtered HTML, before import. |
| `work/url-map.ndjson` | `transform` | Expected AFSC outcome for every baseline and WordPress URL. |
| `reports/all-timing.md` | `all` | Time per step (the cutover budget is one hour). |
| `reports/url-parity.md` | `verify --checks urls` | Status, single hop and final status for every url-map entry. |
| `reports/seo-diff.md` | `verify --checks seo` | Title, description, canonical, robots, OG, JSON-LD types and headline, H1, grouped by route and field. |
| `reports/content-fidelity.md` | `verify --checks content` | exact / minor / fallback / lossy per published post, against the crawl body or the filtered reference. |
| `reports/counts.md` | `verify --checks counts` | Source rows → skipped → bundle → AFSC, per entity. Needs `--import-report`. |
| `reports/media.md` | `verify --checks media` | Every legacy upload path: 200, no redirect, Content-Type, bytes or dimensions; rendered images. |
| `reports/feeds.md` | `verify --checks feeds` | RSS 2.0 validity and guid parity with `baseline/feed.xml`. |
| `reports/sitemaps.md` | `verify --checks sitemaps` | AFSC sitemap URL set against the Yoast set, minus intentional removals. |
| `reports/visual/` | `verify --checks visual` | Pixel diffs of screenshot pairs, `index.html`, and `sample-urls.txt` listing what to capture. |
| `reports/issues-for-afsc.md` | written by hand | Gaps in AFSC found while building the migrator. |

**Visual parity.** `chromedp` was declined, so afsc-wp doesn't drive a browser. Capture full-page PNGs of the URLs in
`reports/visual/sample-urls.txt` at 375, 768 and 1440 px with any tool, into `<screenshots_dir>/wp/` and
`<screenshots_dir>/afsc/` under the same names. Freeze animations, hide the `verify.dynamic_selectors`, and wait for
fonts first. `verify --checks visual` then diffs each pair with a YIQ perceptual threshold.

## Fixture and tests

`testdata/wp-fixture.sql` is a synthetic WordPress 6.9.0 database covering the migration plan's §12 gotchas: latin1
double encoding, percent-encoded slugs, `-scaled` originals, all-zero GMT dates, serialized options with multibyte
strings, Yoast, Yoast Premium (including a PCRE redirect RE2 can't compile), Yoast Video, Jetpack, a guest-author
plugin, User Profile Picture, menus, widgets, a gallery without `ids`, `<!--nextpage-->`, and block content.
`testdata/uploads/` matches it, including non-media files and a file name with a space. All data is placeholder data.

```sh
go test ./...                                              # unit tests; MySQL and Postgres tests skip

docker run -d --name afsc-wp-db --network afsc-wp -e MYSQL_ROOT_PASSWORD=change-me \
  -e MYSQL_DATABASE=wordpress -p 3307:3306 mariadb:10.1.48
docker exec -i afsc-wp-db mysql --default-character-set=utf8mb4 -uroot -pchange-me wordpress < testdata/wp-fixture.sql

AFSC_WP_TEST_MYSQL_DSN='root:change-me@tcp(127.0.0.1:3307)/wordpress' \
AFSC_TEST_DATABASE_URL='postgres://…/afsc_test' go test ./internal/wpdb ./internal/e2e -v
```

The end-to-end test imports into the AFSC test database (source `wpfx`, so keys don't collide with AFSC's own
sample bundle). Run it on its own, not in parallel with AFSC's integration tests.

`testdata/filter-golden.ndjson` stands in for `tools/wp-filter.php` in tests; [tools/README.md](tools/README.md)
explains how to regenerate it with real WordPress.

## Layout

```
cmd/afsc-wp/        subcommands
internal/           config, phpser (unserialize), wpdb (read-only MySQL), extract, uploads, plugins (adapters),
                    decisions, inventory, filter, shortcode, blocks, mediaidx, links, permalink, convert (HTML →
                    ProseMirror), wpseo, wpsettings, redirects, transform, records, bundle, crawl, htmlmeta,
                    fidelity, verify, scaffold, report, e2e
tools/              wp-filter.php (+ README), golden (regenerates the filter golden file)
testdata/           synthetic fixture
docs/               config.md, decisions.md, cutover.md
reports/            produced by runs (only issues-for-afsc.md is checked in)
```

Dependencies are listed in [DEPENDENCIES.md](DEPENDENCIES.md).
