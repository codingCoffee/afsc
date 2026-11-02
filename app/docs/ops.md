# Operations

Notes for running AFSC in production (build plan §16).

- [Deployment](#deployment): container image, read-only filesystem, environment, probes, metrics
- [Logging](#logging)
- [Migrations](#migrations)
- [Backups and restore](#backups-and-restore)
- [Secret rotation](#secret-rotation)
- [Media and imgproxy](#media-and-imgproxy)
- [Page cache](#page-cache)
- [Redirects and the 404 log](#redirects-and-the-404-log)
- [Jobs](#jobs)
- [Webhooks](#webhooks)
- [Plugins](#plugins)
- [Previews](#previews), [Admin access and sessions](#admin-access-and-sessions), [General](#general)

## Deployment

### Container image

`app/Dockerfile` builds a static binary (`CGO_ENABLED=0`, `-tags prod`, so the admin SPA is embedded) on
`gcr.io/distroless/static-debian12:nonroot`: no shell, no package manager, user `nonroot`. The themes are copied
to `/srv/afsc/themes` and `AFSC_THEME_DIR` defaults to `/srv/afsc/themes/default`. Every input is pinned:

| Build arg | What | How to look it up |
|---|---|---|
| `GO_IMAGE_DIGEST` | Digest of `golang:1.27.3-trixie` | `docker buildx imagetools inspect golang:1.27.3-trixie` |
| `DISTROLESS_DIGEST` | Digest of `gcr.io/distroless/static-debian12:nonroot` | `docker buildx imagetools inspect gcr.io/distroless/static-debian12:nonroot` |
| `BUN_SHA256` | SHA-256 of `bun-linux-x64.zip` (or `-aarch64` on an arm64 build host) for bun 1.4.4 | `SHASUMS256.txt` in the bun GitHub release; verify its signature (`SHASUMS256.txt.asc`) against bun's published key |

bun is downloaded in the build stage and checked against `BUN_SHA256`, never piped from `curl` into a shell. Check
the image digests and the bun version against the org's approved list, and refresh them for security releases.

```
docker build --build-arg GO_IMAGE_DIGEST=sha256:… --build-arg DISTROLESS_DIGEST=sha256:… \
  --build-arg BUN_SHA256=… -t afsc:dev app/
docker run --rm --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --env-file app/.env -e AFSC_METRICS_ADDR=127.0.0.1:9090 -p 1381:1381 afsc:dev serve
```

- **Read-only root filesystem.** AFSC writes nothing to disk at run time: media goes to S3, caches are in memory.
  Run with `--read-only` (Kubernetes: `readOnlyRootFilesystem: true`), no capabilities and no privilege escalation.
  The only commands that write files are `afsc import --report <file>` and `afsc content`, run as one-off jobs with
  a writable volume.
- **Processes.** `afsc serve` (site, admin, API, and jobs unless `--no-worker`) and `afsc worker` (jobs and the
  scheduler only). Run `afsc migrate up` as a one-off job before rolling out a release (see [Migrations](#migrations)).
- **Compose.** `docker-compose.yml` has an `afsc` service in the `prod` profile that builds this image and runs it
  read-only: `docker compose --profile prod up afsc` (port 1382) after exporting `AFSC_GO_IMAGE_DIGEST`,
  `AFSC_DISTROLESS_DIGEST` and `AFSC_BUN_SHA256`. The default `app` service stays the dev setup.
- **Dockerfile.dev** installs bun with `curl … | bash`. It's for local development only; don't base a production
  image on it.

### Environment

Configuration is env only. Secrets come from a secret manager, never from git or an image layer.

| Variable | Used by | Default | Meaning |
|---|---|---|---|
| `AFSC_DATABASE_URL` | all | — | Postgres URL. Use `sslmode=verify-full` outside a private network. A direct or session-pooled connection (see LISTEN below). |
| `AFSC_JWT_SECRET` | serve, worker | — | At least 32 bytes. Signs access tokens; HKDF derives the preview, comment-form and plugin keys (webhook secrets). See [rotation](#afsc_jwt_secret). |
| `AFSC_PUBLIC_BASE_URL` | serve, worker | — | The site's origin, for absolute URLs and the CSRF Origin check. Never taken from `Host`. |
| `AFSC_THEME_DIR` | serve, worker, import | — (`/srv/afsc/themes/default` in the image) | Active theme directory. |
| `AFSC_API_HOST`, `AFSC_API_PORT` | serve | `0.0.0.0`, `1381` | Public listener. |
| `AFSC_METRICS_ADDR` | serve, worker | empty (off) | Private `host:port` for `/debug/vars`, for example `127.0.0.1:9090`. Must differ from the public port. |
| `AFSC_JOB_CONCURRENCY` | serve, worker | 4 | Jobs a process runs at once (1–64). |
| `AFSC_PAGE_CACHE_MB` | serve | 128 | In-process page cache; 0 disables it. |
| `AFSC_ACCESS_TOKEN_EXPIRY_MINS` | serve | 5 | Access token lifetime. |
| `AFSC_REFRESH_TOKEN_EXPIRY_DAYS` | serve | 7 | Absolute session lifetime. |
| `AFSC_SESSION_IDLE_HOURS` | serve | 12 | Idle session timeout. |
| `AFSC_SETUP_TOKEN` | serve | generated | First-run setup token while no users exist. |
| `AFSC_TRUSTED_PROXIES` | serve | empty | CIDRs allowed to set `X-Forwarded-For`. |
| `AFSC_S3_BUCKET`, `AFSC_S3_REGION` | serve, import | — | Media bucket. Credentials come from the AWS default chain (IAM role in production). |
| `AFSC_S3_ENDPOINT`, `AFSC_S3_PATH_STYLE` | serve, import | empty, `false` | Only for S3-compatible servers in dev. |
| `AFSC_IMGPROXY_URL`, `AFSC_IMGPROXY_KEY`, `AFSC_IMGPROXY_SALT` | serve | — | Private imgproxy and its signing pair. |
| `AFSC_ENV` | serve, worker | empty | `dev` turns on theme hot reload, text logs and relaxed password rules. Never set it in production. |
| `AFSC_TEST_DATABASE_URL`, `AFSC_SITEMAP_DUMP` | tests | — | Test-only. |

### Health and readiness

| Path | Answers | Use it for |
|---|---|---|
| `GET /healthz` | 200 `{"status":"ok"}` while the process runs. Touches nothing else. | Liveness |
| `GET /readyz` | 200 when the database answers a ping within 2 s and core and every plugin migration set is current; else 503 with `{"db": "ok\|down", "migrations": "ok\|behind\|dirty\|unknown"}`. | Readiness, load balancer health checks |

Both send `Cache-Control: no-store`, never enter the page cache, and successful probes aren't logged. A database
that is *ahead* of the binary (an older replica during a rolling deploy) counts as ready. `afsc worker` has no HTTP
listener; monitor it through its metrics port or its logs.

```yaml
# Kubernetes
livenessProbe:  {httpGet: {path: /healthz, port: 1381}, periodSeconds: 10, failureThreshold: 3}
readinessProbe: {httpGet: {path: /readyz,  port: 1381}, periodSeconds: 5,  failureThreshold: 2}
```

Distroless has no curl, so compose healthchecks for `afsc` need an external checker; rely on the orchestrator's HTTP
probes instead.

### Metrics

With `AFSC_METRICS_ADDR` set, `serve` and `worker` serve stdlib `expvar` JSON at `GET /debug/vars` on that address,
a separate listener: the public port never routes `/debug/vars`. Bind it to localhost or a private interface and
never publish it; it logs a warning when bound to every interface.

| Var | Meaning |
|---|---|
| `afsc_requests` | Responses by status class (`2xx`, `4xx`, `5xx`, …) |
| `afsc_page_cache` | Page cache `hit` and `miss` |
| `afsc_page_cache_size` | `entries` and `bytes` held now |
| `afsc_jobs` | Finished runs by `<kind>.ok`, `<kind>.retry` and `<kind>.failed` (out of attempts) |
| `afsc_jobs_pending` | Unfinished jobs by kind (queried when scraped) |
| `afsc_webhook_deliveries` | Delivery attempts by `succeeded`, `retry`, `failed`, `cancelled` |
| `afsc_plugin_failures` | Plugin hook, func and action failures by hook name |
| `memstats`, `cmdline` | Go runtime defaults |

Counters are per process and reset on restart. Alert on a growing `<kind>.failed`, on `afsc_jobs_pending` that keeps
growing, and on `5xx`.

## Logging

`serve` and `worker` log JSON lines to stderr through `log/slog` (`AFSC_ENV=dev` switches to text). Every request
gets one line:

```json
{"time":"…","level":"INFO","msg":"request","request_id":"9f2c…","method":"GET","path":"/hello/","status":200,"bytes":5120,"latency_ms":3.2}
```

- **Request ID.** Each response carries `X-Request-ID`. A well-formed ID from the proxy (8–64 of `A-Z a-z 0-9 . _ -`)
  is kept, so it matches the load balancer's logs; anything else is replaced with a random one.
- **Never logged:** query strings (preview tokens, search terms), invite tokens in paths (shown as `redacted`),
  request and response bodies, cookies, `Authorization` headers, client IPs and user agents, passwords, session and
  refresh tokens, comment emails and bodies, webhook secrets and signatures.
- **One exception:** while there are no users, `afsc serve` logs the one-time setup token so the operator can create
  the first admin. It's useless once used. To keep it out of logs entirely, set `AFSC_SETUP_TOKEN` from the secret
  manager instead.
- Other lines are the existing `log.Printf` messages (jobs, plugins, cache listener), routed through slog as
  `INFO` with the text in `msg`.

## Migrations

- **Forward-only practice.** Every release's migrations must work with the previous release's code (add columns
  and tables first, drop them a release later), so a rolling deploy and a binary rollback both keep working. Down
  files exist for development, not for production rollbacks.
- **Version tables.** Core uses `schema_migrations`; each plugin with migrations has `plg_<id>_schema_migrations`
  (today `webhooks` and `related`). `afsc migrate up` applies core, then every plugin in registry order.
- **Startup check.** `afsc serve` and `afsc worker` refuse to start when any set is behind the binary or dirty, and
  say which one and what to run. `--migrate` applies pending migrations first (fine for a single instance; with
  replicas, run the migration once as a job). A database ahead of the binary only logs a warning.
- **In a deploy:** run `afsc migrate up` as a one-off job (same image, same env) before updating the deployment.
  `/readyz` stays 503 on new replicas until it has run.
- **Dirty version.** A migration failed half-way. Inspect the schema, finish or undo the change by hand, then set the
  version in the version table (for example `UPDATE schema_migrations SET version = 9, dirty = false`) after a backup,
  and run `afsc migrate up` again.

## Backups and restore

- **Database.** Use the provider's point-in-time recovery (7+ days), or `pg_dump -Fc` at least daily to storage in
  another account, encrypted, with versioning and a lifecycle rule (for example 30 daily, 12 monthly):

  ```
  pg_dump -Fc --no-owner "$AFSC_DATABASE_URL" > afsc-$(date -u +%Y%m%dT%H%MZ).dump
  ```

- **Media.** Turn on bucket versioning plus a lifecycle rule that expires noncurrent versions after, for example, 90
  days. Consider replication to a second region or account.
- **Secrets** (`AFSC_JWT_SECRET`, imgproxy key and salt) live in the secret manager with its own backup. Keep them
  apart from database backups: a dump plus the secret is enough to read webhook secrets.
- **Restore drill** (at least quarterly, and after changing the backup setup):
  1. Create an empty database and run `pg_restore --no-owner -d "$RESTORE_URL" afsc-….dump`.
  2. Start `afsc serve` against it (no `--migrate`) with the same secret, a scratch bucket copy or read-only access
     to the real one, and another `AFSC_PUBLIC_BASE_URL`.
  3. Check `/readyz`, the home page, a post, an image, admin sign-in and the webhook list (no "rotate secret"
     warnings).
  4. Write down how long it took and anything that failed, then delete the scratch environment.

## Secret rotation

### AFSC_JWT_SECRET

It signs access tokens, and HKDF derives from it the preview link key, the comment form token key and one key per
plugin (`afsc-<id>`), which webhooks use to encrypt endpoint secrets. Rotating it:

| What | Effect |
|---|---|
| Access tokens | Rejected at once. Sessions (refresh tokens are random and stored hashed) refresh silently, so editors stay signed in. To end every session too, delete `refresh_tokens` rows (after a backup). |
| Preview links | Stop working; editors open a new preview. |
| Comment forms | Pages rendered with the old key reject submissions until the page cache expires (one TTL); visitors reload. |
| Webhook endpoint secrets | Can't be decrypted. Deliveries fail with "rotate this endpoint's secret", and Admin → Webhooks marks each endpoint. Rotate every endpoint's secret there and update each receiver. |

Steps: generate a new value (`openssl rand -base64 48`), store it in the secret manager, restart every `serve` and
`worker` together (the worker needs it to sign deliveries and refuses to start without it), then rotate each webhook
endpoint's secret. Rotate on a schedule and immediately after a suspected leak.

### Others

- **imgproxy key and salt:** see [below](#rotating-the-imgproxy-key-and-salt).
- **S3 keys:** prefer IAM roles. Static keys (S3-compatible servers): create the new key, update the secret, restart,
  then delete the old key.
- **IndexNow key:** change `seo.indexnow.key` in Settings; the old key file stops being served at once.
- **Webhook endpoint secrets:** Admin → Webhooks → Rotate secret. The old secret stops working at once.
- **Database password:** add a second role or password, switch `AFSC_DATABASE_URL`, restart, then drop the old one.

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
(10 s doubling to 1 h, ±25 % jitter, unless the kind sets its own) up to `max_attempts` (8 unless set); its
`last_error` is kept. Each job has 5 minutes. Exactly one process leads the scheduler, holding a Postgres advisory
lock on its own connection; if it dies another takes over within 10 s.

| Job | When | Does |
|---|---|---|
| `posts.publish_scheduled` | every 30 s | Publishes scheduled posts whose date has passed, purges their pages and pings IndexNow |
| `not_found.prune` | daily | Drops 404 log rows past retention |
| `auth.prune` | daily | Drops expired sessions, used or expired invites and old login failures and lockouts |
| `jobs.prune` | daily | Drops jobs finished more than 7 days ago |
| `spam.retrain` | daily | Rebuilds the comment classifier |
| `comments.retention` | daily | Deletes old spam and clears commenter IPs and user agents past retention |
| `indexnow.ping` | on publish/update | Pings IndexNow with changed URLs |
| `plugins.action` | on each event, per subscribed plugin | Runs that plugin's action handlers (3 attempts, 30 s each) |
| `webhooks.deliver` | per delivery | Sends one signed webhook (8 attempts, 1 min × 4ⁿ up to 12 h) |
| `webhooks.prune` | daily | Deletes deliveries older than 30 days |
| `related.compute` | when a post is published or saved | Recomputes one post's related list |
| `related.compute_all` | daily and after `afsc import` | Recomputes every post's list, 200 per job, chaining |

Run at least one process with jobs enabled, or scheduled posts never go live. Jobs of a switched-off plugin finish
without doing anything.

## Webhooks

- **Egress.** Deliveries go only to public addresses (checked when connecting, after DNS), over https, with TLS 1.2+,
  no proxy and no redirects. Still restrict egress at the network layer: allow outbound 443 from the AFSC hosts
  only, and block `169.254.169.254` and the rest of link-local in the firewall or security group too (IMDSv2 with a
  hop limit of 1 on AWS).
- **Retention.** Delivery rows (payload, last status, first 1 KB of the response) are kept 30 days.
- **Triage.** Admin → Webhooks lists each endpoint's last status and 24 h failures; the deliveries page shows the
  status code, error and response. A 3xx means the URL moved (update it); other 4xx fail at once; 5xx, 408, 425,
  429 and network errors retry for about 30 h. "Rotate this endpoint's secret" means `AFSC_JWT_SECRET` changed.
  Watch `afsc_webhook_deliveries` and `webhooks.deliver.failed` in metrics.
- A worker crash in the middle of a send can leave a delivery `pending` after its job ran out of attempts; Redeliver
  it from the admin.

## Plugins

- Plugins are compiled in. Admins switch them on and off, and change their options, in Admin → Settings → Plugins;
  the change applies on every replica within one settings reload and purges the page cache.
- A failing filter, template func or action is skipped (comment checks fail closed to "pending") and logged at most
  once a minute per plugin and hook, as `plugins: <id>: <hook> failed: …`. `afsc_plugin_failures` counts every
  failure.
- Plugin migrations run with `afsc migrate up` whether the plugin is on or off, and count for `/readyz`.
- A theme that calls a plugin func no plugin provides, or configures an unknown plugin in theme.json, renders anyway;
  `afsc serve` logs a warning at startup and `afsc theme check` fails.

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
