# Cutover runbook

For any site migrated with afsc-wp (migration plan §11). **A human runs every step.** afsc-wp never touches production.
`<site>` is the production host, `<staging>` the staging AFSC, `<afsc-prod-prehost>` production AFSC on its
pre-switch hostname.

## Gates

All of these must pass on staging before cutover is scheduled (plan §1). Each has a report:

| Gate | Command | Report |
|---|---|---|
| URL parity | `afsc-wp verify --target https://<staging> --checks urls` | `reports/url-parity.md` |
| SEO parity | `--checks seo` | `reports/seo-diff.md` (every difference fixed or signed off) |
| Content fidelity | `--checks content` | `reports/content-fidelity.md` (zero `lossy` unless signed off) |
| Counts | `--checks counts --import-report import.json` | `reports/counts.md` |
| Visual | `--checks visual` (screenshots captured first) | `reports/visual/` |
| Media | `--checks media,seo` (seo collects rendered images) | `reports/media.md` |
| Feeds | `--checks feeds` | `reports/feeds.md` |
| Sitemaps | `--checks sitemaps` | `reports/sitemaps.md` |

## T-7 days

1. Full rehearsal on staging from a fresh production dump and uploads sync:
   - Restore the dump into the local MariaDB; sync uploads; refresh theme and mu-plugin code.
   - `afsc-wp inventory`: confirm any new decision items (the transform refuses to run until you do).
   - `afsc-wp crawl` against production (polite; expect roughly `URLs × (delay + response time) / 2`).
   - `afsc-wp all`: record `reports/all-timing.md`. The budget is one hour.
   - Import into staging (`afsc import --bundle bundle --report import.json --theme <theme>`), then run every
     verify check. All gates green.
2. Lower the DNS TTL if the cutover uses DNS.
3. Invite every editor to staging (`afsc user invite …`, listed in the import report). Accounts carry over: the
   import is idempotent by `legacy_key`.
4. Prepare the proxy/CDN rule set for old domains (`site.old_hosts`, listed in `reports/transform.md`): a 301 to the
   same path on the main domain.

## T-1 day

Announce the content-freeze window.

## T-0

1. Freeze WordPress: disable logins for everyone except one admin and close comments.
2. Take the final dump (`mysqldump --single-transaction --routines=false --default-character-set=utf8mb4 …`) and
   sync the whole `wp-content/uploads` directory.
3. Restore both locally and run the final baseline crawl against production.
4. `afsc-wp inventory` (expect no new items), then `afsc-wp all`.
5. Import into production AFSC (empty or reset). Any `--prune` is your decision; review its plan before typing
   `yes`.
6. Run every verify check against `<afsc-prod-prehost>`. The gates must pass.
7. Switch traffic at the proxy or CDN. Apply the old-domain rules.
8. Purge the CDN.
9. Smoke-test: the top 20 URLs, `/feed/`, `/sitemap_index.xml`, `/robots.txt` (it must not disallow everything), and a
   sample of `/wp-content/uploads/...` paths, including known email-signature images (200, no redirect, same bytes).

## T+0 to T+1 day

- Submit the sitemap index in Search Console.
- Watch AFSC's 404 log and error logs hourly. Fix 404s with redirects in the admin.
- Re-enable comments.

## T+1 to T+30 days

- Monitor Search Console coverage, crawl stats and Core Web Vitals.
- Keep the WordPress stack **offline from the public internet** but intact (private hostname or snapshot), for
  rollback and reference.
- **Rollback:** switch the proxy back to WordPress. Content edited in AFSC after cutover must be re-entered in
  WordPress by hand, which is why the freeze window should be short.

## Decommission (after 30+ days)

1. Take a final backup of the WordPress database and files to cold storage.
2. Rotate every credential that existed in `wp_options`, the theme and the plugins: the option names are listed under
   "Credentials to rotate" in `reports/inventory.md` and `reports/transform.md` (Jetpack and Akismet keys, SMTP, API
   keys). Turn on MFA and audit logging for the accounts that replace them.
3. Review and delete the non-media files listed under "Files that will NOT be imported"; some may have been publicly
   reachable.
4. Tear down the infrastructure.

If you find a security incident at any point (exposed credentials, unauthorized access), contact
cybersec@zerodha.com before changing anything that could destroy evidence.
