# Operations

Notes for running AFSC in production. This page covers media serving, imgproxy and the page cache for now; later
milestones add to it.

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
setting; the old file stops being served at once. AFSC doesn't send pings yet; they arrive with publishing (M3).

## General

- **MFA.** Turn on MFA for every account with access to infrastructure: the cloud console, the registry, DNS and the
  CDN.
- **Audit logging.** Turn on audit logs for the cloud account (for example CloudTrail, including S3 data events for
  the media bucket) and keep them where application credentials can't delete them.
- **Secrets.** Keep secrets in a secret manager, never in git. Rotate `AFSC_JWT_SECRET`, the imgproxy key and salt, and
  any static storage keys on a schedule, and at once after a suspected leak.
- **Incidents.** For a suspected breach or exposed credential, contact the security team before changing anything that
  could destroy evidence.
