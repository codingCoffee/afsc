# tools/wp-filter.php

`afsc-wp transform` needs post content exactly as WordPress shows it: `wpautop` paragraphs, `wptexturize` quotes and
dashes, smilies and `capital_P_dangit`. Rather than port those to Go, afsc-wp runs WordPress's own code (migration
plan §6.2, the preferred approach) through this script.

- WordPress core of the **same version as production**, in a local PHP container that you start.
- Connected to the **local restore** with a **read-only** MySQL user. No plugins and no theme are loaded: the script
  points `WP_CONTENT_DIR` at an empty directory.
- **Filter mode** keeps only `wptexturize`, `convert_smilies`, `wpautop`, `shortcode_unautop`, `capital_P_dangit` and
  `convert_chars` on `the_content`. Every shortcode tag afsc-wp converts is registered as a no-op, so `wptexturize`
  and `shortcode_unautop` treat them as production does, but `do_shortcode` never runs. For block content, `wpautop`
  is skipped, as `do_blocks()` does.
- **Render mode** (`--render-shortcode=tag --include=…`) loads the PHP files that register one shortcode, unregisters
  every other shortcode and runs `do_shortcode`. It is used for shortcodes whose decision is `render_php`.

Input is NDJSON `{"id","content"}` on stdin; output is NDJSON `{"id","html"}` (or `{"id","error"}`) on stdout.

## Run it

Pinned images: `wordpress:6.9.0-php8.1-fpm-alpine` (core in `/usr/src/wordpress`) and `mariadb:10.1.48`.

1. Create a read-only user in the local restore (run in your MariaDB container; pick your own password):

   ```sql
   CREATE USER 'wp_readonly'@'%' IDENTIFIED BY 'change-me';
   GRANT SELECT ON wordpress.* TO 'wp_readonly'@'%';
   ```

2. Point `filter.command` in `afsc-wp.json` at the container. afsc-wp appends `--shortcodes=…` or
   `--render-shortcode=…` itself:

   ```json
   "filter": {
     "command": ["docker", "run", "--rm", "-i", "--network", "afsc-wp",
                 "-e", "WP_DB_HOST=wp-db", "-e", "WP_DB_NAME=wordpress", "-e", "WP_DB_USER=wp_readonly",
                 "-e", "WP_DB_PASSWORD", "-e", "WP_TABLE_PREFIX=wp_",
                 "-v", "/abs/path/to/migrators/afsc-wp/tools:/tools:ro",
                 "wordpress:6.9.0-php8.1-fpm-alpine", "php", "/tools/wp-filter.php"]
   }
   ```

   `-e WP_DB_PASSWORD` with no value passes the variable from your shell, so the password stays out of the config.
   For render mode, also mount the plugin or theme code and list its files in the decision's `options.include`,
   using paths inside the container (for example `/wp-code/themes/acme/inc/shortcodes.php`).

Results are cached in `work/filtered/` by content hash, so re-runs only send changed posts.

## Golden output for tests

Tests never run PHP. `testdata/filter-golden.ndjson` holds this script's expected output for every post in
`testdata/wp-fixture.sql`, and the fixture config replays it (`filter.replay_file`). After changing the fixture,
regenerate it with real WordPress:

```sh
# with the fixture loaded into the wp-db container (see README.md, "Fixture")
go run ./cmd/afsc-wp -c testdata/afsc-wp.fixture.json extract
go run ./tools/golden -c testdata/afsc-wp.fixture.json > testdata/filter-golden.ndjson
```

`tools/golden` sends every fixture post through `filter.command` (so set it in a copy of the fixture config) and
prints the replay file, including render-mode records.
