package site

import (
  "context"
  "net/http"
  "net/url"
  "strconv"

  "afsc/internal/theme"
)

// compat is a WordPress query-string URL (?p=, ?cat=, …) on the home path.
type compat struct {
  kind  string // post term author media feed search invalid
  key   string // legacy key to look up
  query string // search
}

// parseCompat reads the WordPress query vars AFSC understands. ok is false when none is present.
func parseCompat(q url.Values) (compat, bool) {
  num := func(name, kind, keyKind string) (compat, bool) {
    if !q.Has(name) {
      return compat{}, false
    }
    n, err := strconv.ParseInt(q.Get(name), 10, 64)
    if err != nil || n <= 0 {
      return compat{kind: "invalid"}, true
    }
    return compat{kind: kind, key: "wp:" + keyKind + ":" + strconv.FormatInt(n, 10)}, true
  }
  for _, v := range []struct{ name, kind, keyKind string }{
    {"p", "post", "post"}, {"page_id", "post", "post"}, {"attachment_id", "media", "post"},
    {"cat", "term", "term"}, {"author", "author", "user"},
  } {
    if c, ok := num(v.name, v.kind, v.keyKind); ok {
      return c, true
    }
  }
  if q.Has("feed") {
    return compat{kind: "feed"}, true
  }
  if q.Has("s") {
    return compat{kind: "search", query: q.Get("s")}, true
  }
  return compat{}, false
}

// wpcompat answers a WordPress query-string URL with a 301 to the canonical URL, or a 404.
func (st *Site) wpcompat(ctx context.Context, snap *snapshot, q url.Values) (Result, bool, error) {
  c, ok := parseCompat(q)
  if !ok {
    return Result{}, false, nil
  }
  moved := func(path string) (Result, bool, error) {
    return Result{Status: http.StatusMovedPermanently, Location: location(path, "")}, true, nil
  }
  switch c.kind {
  case "feed":
    return moved(snap.perm.Feed("/"))
  case "search":
    q := url.Values{"q": {c.query}}.Encode()
    return Result{Status: http.StatusMovedPermanently, Location: location(snap.perm.slash("/search/"), q)}, true, nil
  case "post":
    ref, err := st.lk.LegacyPost(ctx, c.key)
    if err != nil {
      return Result{}, true, err
    }
    if ref != nil && ref.Visible(st.now()) {
      return moved(snap.postPath(ref))
    }
  case "term":
    if id := snap.termLegacy[c.key]; id != 0 {
      t := snap.terms[id]
      return moved(snap.perm.Term(t.Taxonomy, snap.termSlugPath(id)))
    }
  case "author":
    if id := snap.authorLegacy[c.key]; id != 0 {
      if !snap.settings.Permalinks.AuthorArchivesEnabled {
        return moved("/")
      }
      return moved(snap.perm.Author(snap.authors[id].Slug))
    }
  case "media":
    u, err := st.lk.LegacyMedia(ctx, c.key)
    if err != nil {
      return Result{}, true, err
    }
    if u != "" {
      return Result{Status: http.StatusMovedPermanently, Location: u}, true, nil
    }
  }
  return Result{Status: http.StatusNotFound, Kind: theme.Kind404}, true, nil
}
