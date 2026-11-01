package site

import (
  "net/http"
  "strconv"
  "time"

  "afsc/internal/pagecache"
  "afsc/internal/theme"
)

// cacheableKinds are the page kinds stored in the page cache and sent with public cache headers (§6.3).
var cacheableKinds = map[string]bool{
  theme.KindSingle: true, theme.KindPage: true, theme.KindFrontPage: true, theme.KindHome: true, theme.KindPostsPage: true,
  theme.KindCategory: true, theme.KindTag: true, theme.KindAuthor: true, theme.KindDate: true, kindFeed: true,
}

// servePublic runs the resolver behind the page cache. Only 200s of cacheable kinds are stored; 404, 410 and
// redirects get the public TTL headers so a CDN can hold them, but stay out of the in-process cache.
func (st *Site) servePublic(w http.ResponseWriter, r *http.Request, snap *snapshot) {
  if pagecache.Bypass(r) {
    res, err := st.resolve(r.Context(), snap, r)
    var resp *response
    if err == nil {
      resp, err = st.render(snap, r, res, r.URL.RawQuery)
    }
    st.finish(w, r, resp, err)
    return
  }

  ttl := snap.settings.Cache.PageTTLSeconds
  key, query := pagecache.Key(st.host, r.URL)
  store := st.cache != nil && ttl > 0
  var gen uint64
  if store {
    if e, ok := st.cache.Get(key); ok {
      writeEntry(w, r, e, ttl, "HIT")
      return
    }
    gen = st.cache.Gen()
  }

  res, err := st.resolve(r.Context(), snap, r)
  var resp *response
  if err == nil {
    resp, err = st.render(snap, r, res, query)
  }
  if err != nil {
    st.finish(w, r, nil, err)
    return
  }
  switch resp.status {
  case http.StatusOK:
    if !cacheableKinds[res.Kind] {
      break
    }
    now := time.Now()
    e := &pagecache.Entry{ContentType: resp.header.Get("Content-Type"), Body: resp.body, ETag: pagecache.ETag(resp.body),
      Group: snap.group(res, r.URL.Path), Stored: now, Expires: now.Add(time.Duration(ttl) * time.Second)}
    if store {
      st.cache.Put(key, e, gen)
    }
    writeEntry(w, r, e, ttl, "MISS")
    return
  case http.StatusNotFound, http.StatusGone, http.StatusMovedPermanently, http.StatusFound,
    http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
    if resp.header.Get("Cache-Control") == "" {
      resp.header.Set("Cache-Control", pagecache.CacheControl(ttl))
    }
  }
  st.finish(w, r, resp, nil)
}

func writeEntry(w http.ResponseWriter, r *http.Request, e *pagecache.Entry, ttl int, status string) {
  h := w.Header()
  h.Set("Cache-Control", pagecache.CacheControl(ttl))
  h.Set("ETag", e.ETag)
  h.Set("X-Cache", status)
  if age := int(time.Since(e.Stored).Seconds()); status == "HIT" && age > 0 {
    h.Set("Age", strconv.Itoa(age))
  }
  if pagecache.Match(r.Header.Get("If-None-Match"), e.ETag) {
    w.WriteHeader(http.StatusNotModified)
    return
  }
  h.Set("Content-Type", e.ContentType)
  h.Set("Content-Length", strconv.Itoa(len(e.Body)))
  w.WriteHeader(http.StatusOK)
  if r.Method != http.MethodHead {
    _, _ = w.Write(e.Body)
  }
}

func (st *Site) purgeAll() {
  if st.cache != nil {
    st.cache.PurgeAll()
  }
}
