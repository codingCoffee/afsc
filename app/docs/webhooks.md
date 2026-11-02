# Webhooks

The built-in `webhooks` plugin POSTs a signed JSON body to your endpoints when something changes. Manage endpoints
under **Admin → Webhooks**; you need the `settings.manage` capability (admins). The plugin is on by default and can
be switched off under **Settings → Plugins**.

## Events

Each endpoint subscribes to one or more events. The full data schemas are in
[plugins.md → Actions](plugins.md#actions).

| Event | When | `data` |
|---|---|---|
| `post.saved` | A post or page is created, saved or restored from trash | `id, type, status, title, slug, url, modified_at` |
| `post.published` | A post or page goes live (a save or its schedule) | as `post.saved`, plus `published_at` |
| `post.unpublished` | A published post or page moves to another status or to trash | as `post.saved`, plus `previous_status` |
| `post.deleted` | A post or page is deleted from trash for good | `id, type, slug` |
| `term.saved` | A category or tag is created or edited | `id, taxonomy, slug, name, url` |
| `comment.created` | A comment is posted (not spam), or staff reply | `id, post_id, post_url, parent_id, status, author_name` |
| `comment.status_changed` | A comment is moderated; `to` is `deleted` for a permanent delete | `id, post_id, from, to` |
| `media.uploaded` | A file is added to the library | `id, url, mime, width, height, filename` |
| `user.login` | Someone signs in | `user_id, username, role` |
| `settings.changed` | A settings group, or the spam blocklist, is saved with changes | `group, fields` (names only) |
| `ping` | You press **Send test** | `endpoint_id` |

Payloads never contain commenter emails, IPs, user agents, comment bodies or setting values. Call the
[admin API](admin-api.md) if you need more.

## The request

```http
POST /your/endpoint HTTP/1.1
Content-Type: application/json
User-Agent: AFSC-Webhooks/1
X-AFSC-Event: post.published
X-AFSC-Event-Id: 6f1c2a8e-4b0d-4f7e-9a51-2d3c4b5a6e7f
X-AFSC-Delivery-Id: 1842
X-AFSC-Signature: t=1793613600,v1=5d41402abc4b2a76b9719d911017c592…

{"event":"post.published","occurred_at":"2026-11-02T10:00:00Z","data":{"id":42,"type":"post","status":"published",…}}
```

- `X-AFSC-Event-Id` identifies the event. It's the same on every retry and on a manual redelivery, so **dedupe on
  it**.
- `X-AFSC-Delivery-Id` identifies one delivery row in the admin log.
- The body is the same bytes on every attempt. The signature is recomputed per attempt, with a fresh `t`.

## Verifying the signature

`X-AFSC-Signature` is `t=<unix seconds>,v1=<hex HMAC-SHA256>`. The HMAC key is the endpoint's whole secret
(`whsec_…`), and the signed message is `<t>.<raw body>`. To verify:

1. Read the **raw** body bytes. Don't parse and re-encode the JSON first.
2. Compute `HMAC-SHA256(secret, t + "." + body)` and compare it to `v1` in constant time.
3. Reject a `t` more than 5 minutes from your clock, to stop replays.
4. Skip events whose `X-AFSC-Event-Id` you've already processed.

Go (this function is compiled and tested in `plugins/webhooks/example_verify_test.go`):

```go
func Verify(secret, header string, body []byte, now time.Time) error {
	var ts, sig string
	for _, part := range strings.Split(header, ",") {
		k, v, _ := strings.Cut(part, "=")
		switch k {
		case "t":
			ts = v
		case "v1":
			sig = v
		}
	}
	t, err := strconv.ParseInt(ts, 10, 64)
	if err != nil || sig == "" {
		return errors.New("malformed X-AFSC-Signature")
	}
	if d := now.Sub(time.Unix(t, 0)); d > 5*time.Minute || d < -5*time.Minute {
		return errors.New("timestamp outside the 5 minute tolerance")
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(ts + "."))
	mac.Write(body)
	if !hmac.Equal([]byte(sig), []byte(hex.EncodeToString(mac.Sum(nil)))) {
		return errors.New("signature mismatch")
	}
	return nil
}

// in a handler: body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
// err := Verify(os.Getenv("AFSC_WEBHOOK_SECRET"), r.Header.Get("X-AFSC-Signature"), body, time.Now())
```

Python (standard library):

```python
import hashlib, hmac, time

def verify(secret: str, header: str, body: bytes, now: float | None = None) -> bool:
    parts = dict(p.split("=", 1) for p in header.split(",") if "=" in p)
    ts, sig = parts.get("t", ""), parts.get("v1", "")
    if not ts.isdigit() or not sig:
        return False
    if abs((now or time.time()) - int(ts)) > 300:
        return False
    want = hmac.new(secret.encode(), ts.encode() + b"." + body, hashlib.sha256).hexdigest()
    return hmac.compare_digest(sig, want)
```

Node (built-in `crypto`):

```js
const crypto = require('node:crypto')

function verify(secret, header, body /* Buffer */, now = Date.now() / 1000) {
  const parts = Object.fromEntries(header.split(',').map(p => p.split('=', 2)))
  const ts = parts.t ?? ''
  const sig = parts.v1 ?? ''
  if (!/^\d+$/.test(ts) || !sig || Math.abs(now - Number(ts)) > 300)
    return false
  const want = crypto.createHmac('sha256', secret).update(`${ts}.`).update(body).digest('hex')
  return sig.length === want.length && crypto.timingSafeEqual(Buffer.from(sig), Buffer.from(want))
}
// Express: app.post('/hook', express.raw({ type: 'application/json' }), (req, res) => verify(secret, req.get('X-AFSC-Signature'), req.body))
```

## Responses, retries and ordering

| Your response | Result |
|---|---|
| 2xx | Succeeded. |
| Network error, timeout (10 s), 408, 425, 429 or 5xx | Retried. |
| 3xx | Failed at once. Redirects aren't followed, so set the endpoint's final URL. |
| Any other 4xx | Failed at once. |

- Retries back off 1 min × 4ⁿ, capped at 12 h, with ±25 % jitter: about 1 m, 4 m, 16 m, 1 h, 4 h, 12 h, 12 h.
  A delivery fails for good after 8 attempts (about 30 hours).
- Answer quickly with a 2xx and do slow work afterwards.
- Events aren't ordered. Retries, and several workers, can deliver a later event before an earlier one. Use
  `occurred_at`, or fetch the current state from the admin API, when order matters.
- Delivery is at least once. The same event can arrive twice: dedupe on `X-AFSC-Event-Id`.
- An endpoint that's switched off gets nothing new, and its pending deliveries are cancelled. Deleting an endpoint
  deletes its log.

The admin keeps 30 days of deliveries, with the last attempt's status code, error, duration and the first 1 KB of
the response. **Redeliver** sends a stored delivery again as a new row, with the same event ID.

## Endpoint URLs

- `https://` only, with no username, password or `#fragment`.
- AFSC connects only to public addresses. It refuses loopback, private, link-local (including cloud metadata at
  `169.254.169.254`), CGNAT, documentation, benchmarking, multicast and reserved ranges, IPv6 ULA and link-local,
  and the NAT64, 6to4 and Teredo prefixes. The check runs when the connection is made, so a hostname that resolves
  to an internal address is refused too. Proxy environment variables are ignored.
- For local development, expose your receiver through a public HTTPS tunnel.

## Secrets

- AFSC generates each endpoint's secret (`whsec_` followed by 43 characters). It's shown **once**, when you create
  the endpoint or rotate its secret. The list shows only its last 4 characters.
- **Rotate secret** replaces it at once; there's no overlap window. Update your receiver right after rotating, and
  expect signature failures for deliveries already in flight.
- Secrets are stored encrypted (AES-256-GCM) with a key derived from `AFSC_JWT_SECRET`. If you change
  `AFSC_JWT_SECRET`, stored secrets can't be read: deliveries fail with "rotate this endpoint's secret", and the
  admin flags each affected endpoint. Rotate each one, then update its receiver.
- `afsc worker` needs `AFSC_JWT_SECRET` too, because it signs the deliveries.

## Admin API

All under `/admin/api/v1/plugins/webhooks/`, capability `settings.manage`. Create, update, delete and rotate are
written to the audit log as `webhook.*`, never with the secret.

| Route | Notes |
|---|---|
| `GET events` | `{events: [{name, description}]}` |
| `GET endpoints` | `{items: [Endpoint]}`, each with `secret_hint`, `secret_unreadable`, `last_status` and `failures_24h` |
| `POST endpoints` | `{url, description, events, enabled}` → 201 `{endpoint, secret}`. At most 50 endpoints and 20 events each. |
| `PUT endpoints/:id` | Same body → the endpoint |
| `DELETE endpoints/:id` | 204 |
| `POST endpoints/:id/rotate-secret` | `{endpoint, secret}` |
| `POST endpoints/:id/ping` | 202 `{delivery_id}` |
| `GET deliveries?endpoint_id=&status=&event=&page=` | `{items, total, page, page_size}`, newest first, 50 a page |
| `GET deliveries/:id` | Adds `payload`, `request_headers` (signature redacted) and `response_snippet` |
| `POST deliveries/:id/redeliver` | 202 `{delivery_id}` |

Validation errors are 400 `{message, field}`, with `field` one of `url`, `description` or `events`.
