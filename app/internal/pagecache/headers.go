package pagecache

import (
  "crypto/sha256"
  "encoding/hex"
  "strconv"
  "strings"
)

// CacheControl maps the page TTL setting to a Cache-Control value. 0 means browsers and CDNs must revalidate.
func CacheControl(ttl int) string {
  if ttl <= 0 {
    return "no-cache"
  }
  t := strconv.Itoa(ttl)
  return "public, max-age=" + t + ", s-maxage=" + t + ", stale-while-revalidate=" + t
}

// ETag is a strong validator over the body.
func ETag(body []byte) string {
  sum := sha256.Sum256(body)
  return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// Match reports whether an If-None-Match header matches etag, using weak comparison (RFC 9110 §13.1.2).
func Match(ifNoneMatch, etag string) bool {
  if ifNoneMatch == "" || etag == "" {
    return false
  }
  want := strings.TrimPrefix(etag, "W/")
  for _, tag := range strings.Split(ifNoneMatch, ",") {
    tag = strings.TrimSpace(tag)
    if tag == "*" || strings.TrimPrefix(tag, "W/") == want {
      return true
    }
  }
  return false
}
