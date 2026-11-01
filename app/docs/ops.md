# Operations

Notes for running AFSC in production. This page covers media serving, imgproxy, the page cache, redirects and the
404 log for now; later milestones add to it.

## Media and imgproxy

AFSC serves every media URL itself: `/m/…` and legacy `/wp-content/uploads/…` paths. For images it fetches a
processed copy from [imgproxy](https://imgproxy.net) over the internal network, and for other files it streams the
original from S3. Visitors never talk to imgproxy or the bucket directly.

```
visitor ──▶ CDN (optional) ──▶ AFSC ──signed URL──▶ imgproxy ──s3:GetObject──▶ bucket
                                 └──────────── s3:GetObject (PDF, video, SVG, legacy originals) ──▶
```

### Keep imgproxy private

- **Network.** Run imgproxy on a private network only: no public IP, no published ports and no load balancer listener.
  Only the AFSC hosts may reach its port; firewall or security-group rules should allow AFSC's subnet or security group,
  never `0.0.0.0/0`. The local `docker-compose.yml` publishes no ports for it.
- **Signing.** AFSC signs every imgproxy URL with HMAC-SHA256. imgproxy rejects unsigned URLs as long as
  `IMGPROXY_KEY` and `IMGPROXY_SALT` are set; never run it without them.
- **Configuration.**
  - Set `IMGPROXY_ALLOWED_SOURCES=s3://<bucket>/` so imgproxy can't be used to fetch anything else.
  - Set `IMGPROXY_MAX_SRC_RESOLUTION` (megapixels) to bound memory use.
  - Leave `IMGPROXY_STRIP_METADATA` on (AFSC also asks for it on every request), so EXIF and GPS data never reach
    visitors.
- **Settings in AFSC.**

  | Variable | Value |
  |---|---|
  | `AFSC_IMGPROXY_URL` | The private URL, for example `http://imgproxy:8080` |
  | `AFSC_IMGPROXY_KEY`, `AFSC_IMGPROXY_SALT` | The same hex values as imgproxy's `IMGPROXY_KEY` and `IMGPROXY_SALT`, each at least 32 bytes (`openssl rand -hex 32`) |

  `afsc serve` refuses to start without all three, and without a reachable bucket.
- **Image version.** The compose file pins an imgproxy image tag. Check it against the org's approved list, and
  update it when security releases come out.

### Rotating the imgproxy key and salt

imgproxy accepts a single key/salt pair, so a rotation takes a short window where image variants fail:

1. Generate a new pair and store it in the secret manager.
2. Update imgproxy and AFSC together, restarting imgproxy first.
3. Media URLs themselves don't contain signatures, so nothing cached in a CDN or a browser breaks.

Rotate on a schedule, and immediately if the key may have leaked: anyone holding the key can make imgproxy do
expensive work.

### Bucket and IAM (least privilege)

- **Block all public access** on the bucket. Originals are private; every public byte goes through AFSC.
- **Encryption.** Enable default encryption (SSE-S3 or SSE-KMS).
- **Versioning.** Turn it on, so deleted or overwritten originals can be restored (see backups).
- **Credentials.** Use separate principals with no wildcards. Prefer instance or workload IAM roles to static keys.

AFSC (uploads, imports, streaming originals and the startup bucket check):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {"Effect": "Allow", "Action": ["s3:GetObject", "s3:PutObject"], "Resource": "arn:aws:s3:::BUCKET_NAME/*"},
    {"Effect": "Allow", "Action": ["s3:ListBucket"], "Resource": "arn:aws:s3:::BUCKET_NAME"}
  ]
}
```

imgproxy (reads only):

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {"Effect": "Allow", "Action": ["s3:GetObject"], "Resource": "arn:aws:s3:::BUCKET_NAME/*"}
  ]
}
```

Leave out `s3:DeleteObject` until a feature needs it. With S3-compatible servers (for example RustFS in local dev),
create the same two users with equivalent policies rather than sharing the root keys.

### Caching and load

- **No stored variants.** AFSC doesn't store resized copies. Each `/m/` URL is immutable and cached for a year, so a
  CDN in front absorbs repeat requests.
- **Without a CDN**, every image request reaches imgproxy. The page cache only holds HTML. A CDN is strongly
  recommended for image-heavy traffic.
- **Allowed sizes.** Widths and crops are limited to the theme's `image_sizes` and `srcset_widths`, so nobody can
  make imgproxy generate arbitrary sizes.
- **Legacy upload paths** are cached for a day. Their hit counts are kept in memory and written to the database every
  60 seconds and on shutdown, so a crash loses at most a minute of counts.

## Page cache

Each `afsc serve` process keeps rendered public pages (posts, pages, archives, home, feeds, sitemaps and the SEO text
files) in an in-process LRU, and sends cache headers so a CDN in front can hold them too.
AFSC never calls a CDN purge API.

- **Size.** `AFSC_PAGE_CACHE_MB` (default 128) bounds the memory per process; least recently used pages are evicted
  first, and a single page over 2 MB is never stored. Set it to 0 to turn the in-process cache off. Count it in the
  container's memory limit.
- **TTL.** The `cache.page_ttl_seconds` setting (default 60, 0–86400) sets `max-age`, `s-maxage` and
  `stale-while-revalidate`, and also how long a page lives in memory. With 0, responses say `no-cache` and nothing
  is kept in memory. Search is always `private, no-store`; `/m/`, legacy upload paths and theme assets keep their
  own headers and never enter the page cache.
- **Checking it.** Cacheable responses carry `X-Cache: HIT` or `MISS`, an `ETag`, and `Age` on hits:

  ```
  curl -sI https://blog.example.com/ | grep -iE 'x-cache|cache-control|etag|age'
  ```

### Invalidation across replicas

A change purges the affected URLs (or everything, for settings, menus, themes, imports and `--rerender`) and is
broadcast with Postgres `NOTIFY afsc_cache`. Every `afsc serve` holds one extra database connection with
`LISTEN afsc_cache`, reloads its site data (settings, menus, pages, terms, authors, prefix and regex redirects) and
purges its cache when a message arrives.

- **Connection.** The listener needs a direct or session-pooled connection. PgBouncer in transaction mode drops
  `LISTEN`, so point `AFSC_DATABASE_URL` at Postgres directly or at a session-mode pool. Allow one connection per
  replica on top of the pool size.
- **Reconnects.** A dropped listener reconnects with backoff (1 to 30 seconds). Messages sent while it was down are
  lost, so on reconnect it reloads site data and purges everything.
- **Fallback.** Site data is also reloaded every 30 seconds, and every cached page expires after one TTL, so even a
  missed message corrects itself within a TTL. Scheduled posts that go live also show up within one TTL.
- **CDN.** A CDN in front keeps serving its copy until the TTL runs out (plus `stale-while-revalidate` while it
  refetches), so it catches up within one TTL of a change. Lower the TTL if that's too long, and don't let the CDN
  override AFSC's `Cache-Control`.

### Sitemaps, robots.txt and llms.txt

Sitemaps, `robots.txt`, `llms.txt` and the IndexNow key file are built on request and stored in the page cache with
the page TTL, like pages. Purges are per sitemap type: every page of one type (`post-sitemap.xml`,
`post-sitemap2.xml`, …) shares one cache group.

| Change | Also purges |
|---|---|
| A post published, edited or unpublished | The post, page, category, tag and author sitemaps it affects (the page sitemap because the home entry's `lastmod` moves), the video sitemap if it has videos, and the index |
| A page | The page sitemap, `llms.txt` and the index |
| A term | Its taxonomy's sitemap, the video sitemap, the index, and `llms.txt` for categories |
| An author | The author sitemap and the index |
| Settings, imports, `--rerender` | Everything |

- **Cost.** A cache miss on any sitemap reads the whole list for its type (one small row per published post or page,
  without content), then the images of that one page. A miss on the index reads every type's list. With a 60 second TTL that's at most one rebuild per file per minute
  per replica.
- **Size.** `seo.sitemaps.page_size` (default 1000, at most 50,000) is the number of URLs per file. Keep the default
  unless a crawler struggles with the file size: each URL can carry many `image:image` entries.
- **Search Console.** Submit `https://{site}/sitemap_index.xml`. The file names match Yoast's, so a migrated
  site's existing submission keeps working.
- **Validation.** `scripts/check-sitemaps.sh` (needs `xmllint` and `AFSC_TEST_DATABASE_URL`) generates the sample
  bundle's sitemaps and validates them against the vendored XSDs in `testdata/xsd`.

### IndexNow key

Set `seo.indexnow` to `{"enabled": true, "key": "…"}` (8 to 128 letters, digits or dashes). AFSC then serves the key
at `/{key}.txt` so search engines can verify it. Generate a key with `openssl rand -hex 16`. To rotate it, change the
setting; the old file stops being served at once. While enabled, publishing, updating, unpublishing, trashing or
moving a post or page queues an `indexnow.ping` job with its old and new URLs (never archives, never noindex posts),
retried up to 8 times. Pings go only to `api.indexnow.org` over a client that refuses private addresses.

## Redirects and the 404 log

### Redirect API

The redirect manager is a JSON API under `/admin/api/v1`. Every route needs a signed-in **admin or editor** (the
same JWT as the rest of the admin API); other roles get 403. The admin screens arrive in M3.

| Route | Does |
|---|---|
| `GET /redirects?q=&match=&origin=&sort=hits\|source\|created&page=&page_size=` | Page through redirects (`q` searches source, target and note) |
| `GET /redirects/{id}` | One redirect, with `hits`, `last_hit_at`, `origin` and `created_by` |
| `POST /redirects[?flatten=1]` | Create `{source, match, target, status, note}`; 201 `{redirect, warnings, flattened_ids}` |
| `PUT /redirects/{id}[?flatten=1]` | Replace the same fields; 200 with the same body |
| `DELETE /redirects/{id}` | 204 |
| `POST /redirects/test` | `{match, source, target, paths}` (at most 100 paths) → what the rule does with each path |
| `GET /redirects/export` | Every redirect as CSV: `source,match,target,status,note` |
| `POST /redirects/import[?flatten=1]` | A CSV body with those columns (any order; only `source` is required) |
| `GET /not-found?q=&sort=hits\|last_seen&page=&page_size=` | Page through the 404 log, most hits first |
| `POST /not-found/dismiss` | `{paths: [...]}` (at most 1,000) → `{dismissed}` |

- **Fields.** `match` is `exact` (default), `prefix` or `regex` (RE2); `status` is 301 (default), 302, 307, 308,
  410 or 451, and 410/451 take no target. Targets are a path or an http(s) URL; same-site URLs are stored as paths.
  Exact sources and internal targets are stored in canonical form (lowercase, trailing-slash policy), prefix
  sources in lowercase. Sources under `/admin`, and exact sources the site answers before redirects (sitemaps,
  `robots.txt`, `/m/` and legacy upload paths), are rejected. Regexes are capped at 500 bytes and 1,000 compiled
  RE2 steps; RE2 runs in linear time, so no pattern can stall a request.
- **Chain and loop checks.** On every save the target is followed the way a visitor's browser would: exact
  redirects, the canonical 301, content, then prefix and regex redirects, up to 10 hops. The save gets a 409 when:

  | `code` | Meaning | `?flatten=1` |
  |---|---|---|
  | `chain` | The target redirects again | Saves with `flattened_target`, where the chain ends |
  | `loop` | The target leads back to this redirect | No |
  | `gone` | The target ends at a 410 or 451 | No |
  | `too_long` | More than 10 hops | No |

  The 409 body is `{message, field: "target", code, hops: [{from, to, status, via, redirect_id}], flattened_target}`.
  Redirects that already pointed at the new source are flattened in the same transaction and listed in
  `flattened_ids`. Warnings don't block a save: an exact source that hides a live page, a target that is a 404,
  and regex targets with `$` groups (not checked). Saves take an advisory lock, so two editors can't build a chain
  between them.
- **CSV import** is all or nothing: every row is checked in file order (later rows see earlier ones), and any error
  returns 422 with `{errors: [{line, field, message}]}` and writes nothing. A row replaces the redirect with the same
  exact source (or the same `match` and `source` for prefix and regex), otherwise creates one with origin `manual`.
  Files are capped at 5 MB and 10,000 rows. Exports prefix cells starting with `= + - @` with `'` so spreadsheets
  don't run them as formulas; imports strip that prefix again.
- **Imported redirects** (origin `import`) keep their origin and `legacy_key` when edited here, so the next bundle
  import overwrites the edit.
- **Caching.** Saving an exact redirect purges its source path. Saving a prefix or regex redirect reloads site data
  on every replica and purges every cached page, so bulk edits of those are best done as one CSV import. Redirects
  themselves are never stored in the in-process cache; a CDN keeps them for one TTL.
- **Hit counts** are kept in memory and written every 60 seconds and on shutdown, like legacy upload hits. A CDN
  answering cached redirects means those hits are never counted.

### Auto redirects

`internal/redirects.SlugChange` creates a 301 (origin `slug_change`) from each old URL to its new one, points
redirects that targeted the old URL at the new one, and deletes any exact redirect whose source is the new URL
(it would hide the content). `site.PostURLs`, `TermURLs` and `AuthorURLs` compute the URL sets to compare. Saving a
published post or page with a new slug, parent, date or primary category records its moves (subpages included);
imports never do. Trashing a published post asks for a 410, a 301 to a chosen URL, or nothing; the rule is an
exact `manual` redirect noted `trashed post #id`, and republishing the post at that URL deletes it.

### 404 log

- **What's logged.** Every 404 on the public site, with its hit count, first and last time seen and last referrer,
  except paths matching `seo.not_found.noise_patterns` (default `*.php`, `/.env*`, `/.git/*`, `/wp-admin/*`,
  `*.asp*`, `/cgi-bin/*`). Patterns are checked when a 404 happens, so rows logged before a pattern was added stay
  until dismissed or pruned.
- **Retention.** Rows not seen for `seo.not_found.retention_days` (default 90) are deleted by the daily
  `not_found.prune` job (see Jobs).
- **Dismiss** deletes rows. A path that is hit again comes back with a fresh count, so add a noise pattern for
  recurring junk. Creating an exact redirect also removes the 404 row for its source.

## Jobs

`afsc serve` runs a job worker and the scheduler unless started with `--no-worker`; `afsc worker` runs only those.
`AFSC_JOB_CONCURRENCY` (default 4) sets how many jobs a process runs at once. Jobs live in the `jobs` table and are
claimed with `FOR UPDATE SKIP LOCKED`, so any number of processes can work. A failed job retries with backoff
(10 s doubling to 1 h, ±25 % jitter) up to `max_attempts` (8); its `last_error` is kept. Exactly one process leads
the scheduler, holding a Postgres advisory lock on its own connection; if it dies another takes over within 10 s.

| Job | Every | Does |
|---|---|---|
| `posts.publish_scheduled` | 30 s | Publishes scheduled posts whose date has passed, purges their pages and pings IndexNow |
| `not_found.prune` | day | Drops 404 log rows past retention |
| `auth.prune` | day | Drops expired sessions, used or expired invites and old login failures and lockouts |
| `jobs.prune` | day | Drops jobs finished more than 7 days ago |

Run at least one process with jobs enabled, or scheduled posts never go live.

## Previews

The editor's preview link is `/?preview=<token>`: an HMAC (key derived from `AFSC_JWT_SECRET` with HKDF) over the
post, the revision and an expiry one hour out. Preview responses bypass the page cache and send `no-store`,
`X-Robots-Tag: noindex`, `Referrer-Policy: no-referrer` and a noindex robots meta. Rotating `AFSC_JWT_SECRET` voids
open preview links.

## Admin access and sessions

- **Restrict `/admin` at the proxy.** The admin lives on the public domain. Allow `/admin` only from a VPN or office
  range at the proxy or CDN. The app doesn't enforce IP restrictions itself. Any script on a public page runs on the
  same origin as the admin; the defenses are the public CSP, the content sanitizer and admin-only code snippets.
- **First run.** While the `users` table is empty, `afsc serve` logs a one-time setup token (or uses
  `AFSC_SETUP_TOKEN`). Only the replica that created the token logs it. Open `/admin/`, enter the token and create
  the first admin. After that, the setup endpoints return 404 and the token is deleted.
- **Users are invite-only.** Admins invite users in Admin → Users, or run `afsc user create --email … --role …` or
  `afsc user invite --email …` (for imported users). Each prints a link that is valid for 72 h and works once. AFSC
  sends no email: send the link yourself over a trusted channel.
- **Sessions.** A session ends after `AFSC_SESSION_IDLE_HOURS` (default 12) without use, and after
  `AFSC_REFRESH_TOKEN_EXPIRY_DAYS` (default 7) in any case. Role changes, disabling an account and password changes
  end that user's sessions. Access tokens last `AFSC_ACCESS_TOKEN_EXPIRY_MINS`, but every API request also checks
  that the session is still active, so a revocation takes effect on the next request.
- **Sign-in limits.** These are stored in Postgres, so they hold across replicas. After 5 failures in 15 min, an
  account is locked for 15 min; each further lockout doubles, up to 4 h. One IP may fail 50 times in 15 min. Anyone
  who knows a username can lock that account out, which is one more reason to keep `/admin` behind a VPN.
- **Client IPs.** Set `AFSC_TRUSTED_PROXIES` to your load balancer's ranges. `X-Forwarded-For` is honoured only when
  the direct peer is trusted; otherwise the peer address is used, so clients can't spoof it.
- **CSRF.** The API accepts access tokens only in the `Authorization` header. Every non-GET request must carry
  `Origin` equal to `AFSC_PUBLIC_BASE_URL` and a JSON body. The refresh cookie is `HttpOnly`, `Secure`,
  `SameSite=Strict` and scoped to `/admin/api/v1/`.
- **Admin headers.** `/admin` responses are `Cache-Control: no-store` and `X-Robots-Tag: noindex`, and can't be
  framed. The SPA's CSP allows its own inline scripts by sha256 hash, computed from the embedded `index.html` at
  startup, and Google Fonts for the admin typeface.
- **Audit log.** Admin → Audit log lists sign-ins (including failures and lockouts), setup, user, role and session
  changes, redirect changes, and, from later milestones, content and settings changes.

## General

- **MFA.** Turn on MFA for every account with access to infrastructure: the cloud console, the registry, DNS and the
  CDN.
- **Audit logging.** Turn on audit logs for the cloud account (for example CloudTrail, including S3 data events for
  the media bucket) and keep them where application credentials can't delete them.
- **Secrets.** Keep secrets in a secret manager, never in git. Rotate `AFSC_JWT_SECRET`, the imgproxy key and salt, and
  any static storage keys on a schedule, and at once after a suspected leak.
- **Incidents.** For a suspected breach or exposed credential, contact the security team before changing anything that
  could destroy evidence.
