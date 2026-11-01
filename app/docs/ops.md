# Operations

Notes for running AFSC in production. This page covers media serving and imgproxy for now; later milestones add to
it.

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

## General

- **MFA.** Turn on MFA for every account with access to infrastructure: the cloud console, the registry, DNS and the
  CDN.
- **Audit logging.** Turn on audit logs for the cloud account (for example CloudTrail, including S3 data events for
  the media bucket) and keep them where application credentials can't delete them.
- **Secrets.** Keep secrets in a secret manager, never in git. Rotate `AFSC_JWT_SECRET`, the imgproxy key and salt, and
  any static storage keys on a schedule, and at once after a suspected leak.
- **Incidents.** For a suspected breach or exposed credential, contact the security team before changing anything that
  could destroy evidence.
