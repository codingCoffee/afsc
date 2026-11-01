package site

import (
  "cmp"
  "context"
  "encoding/json"
  "fmt"
  "maps"
  "net/url"
  "regexp"
  "slices"
  "strconv"
  "strings"
  "time"

  "afsc/internal/settings"
  "afsc/internal/theme"
)

// snapshot holds the small, read-mostly tables in memory. It is rebuilt on cache events and polled as a fallback.
type snapshot struct {
  base        string
  settings    settings.All
  perm        *Permalinks
  loc         *time.Location
  homeID      int64
  postsPageID int64

  pages        map[int64]*pageRow
  pageChildren map[pageKey]int64

  terms        map[int64]*termRow
  termChildren map[int64][]int64
  termPaths    map[string]int64 // taxonomy + ":" + slug path
  termLegacy   map[string]int64

  authors      map[int64]*authorRow
  authorSlugs  map[string]int64
  authorLegacy map[string]int64

  menus  map[string][]*theme.MenuItem
  prefix []*Redirect // longest source first
  regex  []*Redirect // id order
}

type pageKey struct {
  parent int64
  slug   string
}

type pageRow struct {
  PostRef
  Title    string
  Template string
}

type termRow struct {
  ID          int64
  ParentID    int64
  LegacyKey   string
  Taxonomy    string
  Slug        string
  Name        string
  Description string
}

type authorRow struct {
  ID        int64
  LegacyKey string
  Slug      string
  Name      string
  Bio       string
  Website   string
  Social    map[string]string
  Avatar    *theme.Media
}

type Redirect struct {
  ID     int64
  Source string
  Match  string
  Target string
  Status int
  re     *regexp.Regexp
}

var legacyNumRe = regexp.MustCompile(`^[a-z0-9]+:(post|term|user):(\d+)$`)

// wpID is the WordPress id behind a legacy key of the given kind, else the AFSC id.
func wpID(id int64, legacyKey, kind string) int64 {
  if m := legacyNumRe.FindStringSubmatch(legacyKey); m != nil && m[1] == kind {
    n, _ := strconv.ParseInt(m[2], 10, 64)
    return n
  }
  return id
}

func (s *snapshot) abs(path string) string {
  return s.base + (&url.URL{Path: path}).EscapedPath()
}

func (s *snapshot) pageByPath(slugs []string) *pageRow {
  var parent int64
  for _, slug := range slugs {
    id := s.pageChildren[pageKey{parent, slug}]
    if id == 0 {
      return nil
    }
    parent = id
  }
  return s.pages[parent]
}

// pageChain returns the page and its ancestors, root first.
func (s *snapshot) pageChain(id int64) []*pageRow {
  var chain []*pageRow
  for p := s.pages[id]; p != nil && len(chain) < 64; p = s.pages[p.ParentID] {
    chain = append(chain, p)
  }
  slices.Reverse(chain)
  return chain
}

func (s *snapshot) pagePath(id int64) string {
  var slugs []string
  for _, p := range s.pageChain(id) {
    slugs = append(slugs, p.Slug)
  }
  return s.perm.Page(slugs)
}

func (s *snapshot) termChain(id int64) []*termRow {
  var chain []*termRow
  for t := s.terms[id]; t != nil && len(chain) < 64; t = s.terms[t.ParentID] {
    chain = append(chain, t)
  }
  slices.Reverse(chain)
  return chain
}

func (s *snapshot) termSlugPath(id int64) string {
  var slugs []string
  for _, t := range s.termChain(id) {
    slugs = append(slugs, t.Slug)
  }
  return strings.Join(slugs, "/")
}

func (s *snapshot) termDescendants(id int64) []int64 {
  out := []int64{id}
  for i := 0; i < len(out) && i < 10000; i++ {
    out = append(out, s.termChildren[out[i]]...)
  }
  return out
}

// postPath is the canonical path of a post or page.
func (s *snapshot) postPath(r *PostRef) string {
  if r.Type == "page" {
    if r.ID == s.homeID {
      return "/"
    }
    return s.pagePath(r.ID)
  }
  return s.perm.Post(r, s.termSlugPath(r.CategoryID))
}

func (s *snapshot) termView(id int64) *theme.Term {
  chain := s.termChain(id)
  if len(chain) == 0 {
    return nil
  }
  var anc []*theme.Term
  var slugs []string
  var out *theme.Term
  for i, t := range chain {
    slugs = append(slugs, t.Slug)
    path := strings.Join(slugs, "/")
    v := &theme.Term{ID: t.ID, WPID: wpID(t.ID, t.LegacyKey, "term"), Taxonomy: t.Taxonomy, Slug: t.Slug, Name: t.Name,
      Description: t.Description, Path: path, Ancestors: slices.Clone(anc), URL: s.abs(s.perm.Term(t.Taxonomy, path))}
    if i == len(chain)-1 {
      out = v
    }
    anc = append(anc, v)
  }
  return out
}

func (s *snapshot) authorView(id int64) *theme.Author {
  a := s.authors[id]
  if a == nil {
    return nil
  }
  return &theme.Author{ID: a.ID, WPID: wpID(a.ID, a.LegacyKey, "user"), Slug: a.Slug, Name: a.Name, Bio: a.Bio,
    Website: a.Website, Social: a.Social, Avatar: a.Avatar, URL: s.abs(s.perm.Author(a.Slug))}
}

// matchRedirect tries prefix redirects, longest source first, then regex redirects in id order.
func (s *snapshot) matchRedirect(path string) (*Redirect, string) {
  for _, r := range s.prefix {
    if strings.HasPrefix(path, strings.ToLower(r.Source)) {
      return r, r.Target
    }
  }
  for _, r := range s.regex {
    if m := r.re.FindStringSubmatchIndex(path); m != nil {
      return r, string(r.re.ExpandString(nil, r.Target, path, m))
    }
  }
  return nil, ""
}

const (
  sqlSettings = `SELECT coalesce(jsonb_object_agg(key, value), '{}'::jsonb) FROM settings`
  sqlPages    = `
SELECT id, coalesce(legacy_key, ''), slug, status, published_at, coalesce(parent_id, 0), title, coalesce(template, '')
FROM posts WHERE type = 'page' AND status <> 'trash'`
  sqlTerms = `
SELECT id, coalesce(parent_id, 0), coalesce(legacy_key, ''), taxonomy, slug, name, description FROM terms`
  sqlAuthors = `
SELECT id, coalesce(legacy_key, ''), slug, display_name, bio, website, social, coalesce(avatar_media_id, 0) FROM authors`
  sqlRedirects = `
SELECT id, source, match, coalesce(target, ''), status FROM redirects WHERE match <> 'exact' ORDER BY id`
  sqlMenuItems = `
SELECT ml.location, mi.id, coalesce(mi.parent_id, 0), mi.label, mi.kind, coalesce(mi.target_id, 0), coalesce(mi.url, ''),
       mi.new_tab, mi.css_classes
FROM menu_locations ml JOIN menu_items mi ON mi.menu_id = ml.menu_id
ORDER BY ml.location, mi.position, mi.id`
)

func newSnapshot(base string) *snapshot {
  return &snapshot{base: base, pages: map[int64]*pageRow{}, terms: map[int64]*termRow{}, authors: map[int64]*authorRow{},
    menus: map[string][]*theme.MenuItem{}}
}

// index builds the lookup maps from pages, terms and authors.
func (s *snapshot) index() {
  s.pageChildren, s.termChildren = map[pageKey]int64{}, map[int64][]int64{}
  s.termPaths, s.termLegacy = map[string]int64{}, map[string]int64{}
  s.authorSlugs, s.authorLegacy = map[string]int64{}, map[string]int64{}
  for _, p := range s.pages {
    s.pageChildren[pageKey{p.ParentID, p.Slug}] = p.ID
  }
  for id, t := range s.terms {
    if t.ParentID != 0 {
      s.termChildren[t.ParentID] = append(s.termChildren[t.ParentID], id)
    }
    if t.LegacyKey != "" {
      s.termLegacy[t.LegacyKey] = id
    }
    s.termPaths[t.Taxonomy+":"+s.termSlugPath(id)] = id
  }
  for id, a := range s.authors {
    s.authorSlugs[a.Slug] = id
    if a.LegacyKey != "" {
      s.authorLegacy[a.LegacyKey] = id
    }
  }
}

// setSettings applies a settings value and derives the timezone, permalinks and home/posts page ids.
func (s *snapshot) setSettings(all settings.All) error {
  loc, err := time.LoadLocation(all.Site.Timezone)
  if err != nil {
    return err
  }
  perm, err := NewPermalinks(all.Permalinks, loc)
  if err != nil {
    return err
  }
  s.settings, s.loc, s.perm, s.homeID, s.postsPageID = all, loc, perm, 0, 0
  if r := all.Reading.Home; r != nil {
    s.homeID = r.ID
  }
  if r := all.Reading.PostsPage; r != nil {
    s.postsPageID = r.ID
  }
  return nil
}

func (st *Site) loadSnapshot(ctx context.Context) (*snapshot, error) {
  s := newSnapshot(st.cfg.BaseURL)

  var raw []byte
  if err := st.db.QueryRow(ctx, sqlSettings).Scan(&raw); err != nil {
    return nil, fmt.Errorf("settings: %w", err)
  }
  all, _, err := settings.Decode(raw)
  if err != nil {
    return nil, err
  }
  if err := s.setSettings(all); err != nil {
    return nil, err
  }

  rows, err := st.db.Query(ctx, sqlPages)
  if err != nil {
    return nil, err
  }
  for rows.Next() {
    p := &pageRow{PostRef: PostRef{Type: "page"}}
    if err := rows.Scan(&p.ID, &p.LegacyKey, &p.Slug, &p.Status, &p.PublishedAt, &p.ParentID, &p.Title, &p.Template); err != nil {
      rows.Close()
      return nil, err
    }
    s.pages[p.ID] = p
  }
  if err := rows.Err(); err != nil {
    return nil, err
  }

  rows, err = st.db.Query(ctx, sqlTerms)
  if err != nil {
    return nil, err
  }
  for rows.Next() {
    t := &termRow{}
    if err := rows.Scan(&t.ID, &t.ParentID, &t.LegacyKey, &t.Taxonomy, &t.Slug, &t.Name, &t.Description); err != nil {
      rows.Close()
      return nil, err
    }
    s.terms[t.ID] = t
  }
  if err := rows.Err(); err != nil {
    return nil, err
  }

  avatars := map[int64][]*authorRow{}
  rows, err = st.db.Query(ctx, sqlAuthors)
  if err != nil {
    return nil, err
  }
  for rows.Next() {
    a := &authorRow{}
    var social []byte
    var avatar int64
    if err := rows.Scan(&a.ID, &a.LegacyKey, &a.Slug, &a.Name, &a.Bio, &a.Website, &social, &avatar); err != nil {
      rows.Close()
      return nil, err
    }
    _ = json.Unmarshal(social, &a.Social)
    s.authors[a.ID] = a
    if avatar != 0 {
      avatars[avatar] = append(avatars[avatar], a)
    }
  }
  if err := rows.Err(); err != nil {
    return nil, err
  }
  if len(avatars) > 0 {
    media, err := st.loadMedia(ctx, slices.Collect(maps.Keys(avatars)))
    if err != nil {
      return nil, err
    }
    for id, list := range avatars {
      if m, ok := media[id]; ok {
        for _, a := range list {
          a.Avatar = s.mediaView(m)
        }
      }
    }
  }

  s.index()
  if err := st.loadRedirects(ctx, s); err != nil {
    return nil, err
  }
  if err := st.loadMenus(ctx, s); err != nil {
    return nil, err
  }
  return s, nil
}

func (st *Site) loadRedirects(ctx context.Context, s *snapshot) error {
  rows, err := st.db.Query(ctx, sqlRedirects)
  if err != nil {
    return err
  }
  defer rows.Close()
  for rows.Next() {
    r := &Redirect{}
    if err := rows.Scan(&r.ID, &r.Source, &r.Match, &r.Target, &r.Status); err != nil {
      return err
    }
    if r.Match == "regex" {
      if r.re, err = regexp.Compile(r.Source); err != nil {
        st.cfg.Logf("redirect %d: invalid regex skipped: %v", r.ID, err)
        continue
      }
      s.regex = append(s.regex, r)
    } else {
      s.prefix = append(s.prefix, r)
    }
  }
  slices.SortStableFunc(s.prefix, func(a, b *Redirect) int { return cmp.Compare(len(b.Source), len(a.Source)) })
  return rows.Err()
}

type menuRow struct {
  location string
  item     *theme.MenuItem
  parent   int64
  target   int64
}

func (st *Site) loadMenus(ctx context.Context, s *snapshot) error {
  rows, err := st.db.Query(ctx, sqlMenuItems)
  if err != nil {
    return err
  }
  var items []menuRow
  var postIDs []int64
  for rows.Next() {
    var r menuRow
    it := &theme.MenuItem{}
    if err := rows.Scan(&r.location, &it.ID, &r.parent, &it.Label, &it.Kind, &r.target, &it.URL, &it.NewTab, &it.Classes); err != nil {
      rows.Close()
      return err
    }
    r.item = it
    items = append(items, r)
    if it.Kind == "post" {
      postIDs = append(postIDs, r.target)
    }
  }
  if err := rows.Err(); err != nil {
    return err
  }
  refs, err := st.postRefs(ctx, postIDs)
  if err != nil {
    return err
  }
  now := st.now()
  byID := map[int64]*theme.MenuItem{}
  var kept []menuRow
  for _, r := range items {
    it := r.item
    switch it.Kind {
    case "post":
      ref := refs[r.target]
      if ref == nil || !ref.Visible(now) {
        continue
      }
      it.URL = s.abs(s.postPath(ref))
    case "term":
      t := s.termView(r.target)
      if t == nil {
        continue
      }
      it.URL = t.URL
    case "author":
      a := s.authorView(r.target)
      if a == nil {
        continue
      }
      it.URL = a.URL
    case "url":
      if strings.HasPrefix(it.URL, "/") && !strings.HasPrefix(it.URL, "//") {
        it.URL = s.base + it.URL
      }
    }
    byID[it.ID] = it
    kept = append(kept, r)
  }
  for _, r := range kept {
    if r.parent == 0 {
      s.menus[r.location] = append(s.menus[r.location], r.item)
    } else if p := byID[r.parent]; p != nil {
      p.Children = append(p.Children, r.item)
    }
  }
  return nil
}
