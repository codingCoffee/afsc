// Package settings defines the typed settings registry. Each top-level key is stored as one row in the settings table.
package settings

import (
  "bytes"
  "encoding/json"
  "fmt"
  "net/url"
  "slices"
  "strings"
  "time"
)

// Ref points at another entity. Bundles reference it by legacy key; AFSC stores the resolved id.
type Ref struct {
  ID        int64
  LegacyKey string
}

func (r Ref) IsZero() bool { return r.ID == 0 && r.LegacyKey == "" }

func (r Ref) MarshalJSON() ([]byte, error) {
  switch {
  case r.ID > 0:
    return json.Marshal(r.ID)
  case r.LegacyKey != "":
    return json.Marshal(r.LegacyKey)
  }
  return []byte("null"), nil
}

func (r *Ref) UnmarshalJSON(b []byte) error {
  *r = Ref{}
  switch {
  case bytes.Equal(b, []byte("null")):
    return nil
  case len(b) > 0 && b[0] == '"':
    return json.Unmarshal(b, &r.LegacyKey)
  }
  return json.Unmarshal(b, &r.ID)
}

type Site struct {
  Title      string `json:"title"`
  Tagline    string `json:"tagline"`
  Timezone   string `json:"timezone"`
  DateFormat string `json:"date_format"`
  Language   string `json:"language"`
}

type Permalinks struct {
  PostPattern           string `json:"post_pattern"`
  CategoryBase          string `json:"category_base"`
  TagBase               string `json:"tag_base"`
  AuthorBase            string `json:"author_base"`
  TrailingSlash         string `json:"trailing_slash"`
  DateArchivesEnabled   bool   `json:"date_archives_enabled"`
  AuthorArchivesEnabled bool   `json:"author_archives_enabled"`
  WPCompatEnabled       bool   `json:"wpcompat_enabled"`
}

type Reading struct {
  Home            *Ref `json:"home"`
  PostsPage       *Ref `json:"posts_page"`
  PostsPerPage    int  `json:"posts_per_page"`
  FeedItems       int  `json:"feed_items"`
  FeedExcerptOnly bool `json:"feed_excerpt_only"`
}

type KnowledgeGraph struct {
  Type string `json:"type"`
  Name string `json:"name"`
  Logo *Ref   `json:"logo_media"`
}

type SEO struct {
  Separator            string            `json:"separator"`
  TitleTemplates       map[string]string `json:"title_templates"`
  DescriptionTemplates map[string]string `json:"description_templates"`
  KnowledgeGraph       KnowledgeGraph    `json:"knowledge_graph"`
  SocialProfiles       []string          `json:"social_profiles"`
  TwitterSite          string            `json:"twitter_site"`
  DefaultImage         *Ref              `json:"default_image_media"`
  TagNoindex           bool              `json:"tag_noindex"`
  AutoDescription      bool              `json:"auto_description"`
}

type Discussion struct {
  CommentsEnabled bool `json:"comments_enabled"`
  CloseAfterDays  int  `json:"close_after_days"`
  ThreadDepth     int  `json:"thread_depth"`
}

// All is every registered settings group, keyed by its JSON name.
type All struct {
  Site       Site       `json:"site"`
  Permalinks Permalinks `json:"permalinks"`
  Reading    Reading    `json:"reading"`
  SEO        SEO        `json:"seo"`
  Discussion Discussion `json:"discussion"`
}

var Keys = []string{"site", "permalinks", "reading", "seo", "discussion"}

// TemplateTypes are the page types that have SEO title and description templates (§8.2).
var TemplateTypes = []string{"post", "page", "category", "tag", "author", "home", "search", "404", "date"}

func Defaults() All {
  return All{
    Site: Site{Timezone: "UTC", DateFormat: "January 2, 2006", Language: "en"},
    Permalinks: Permalinks{
      PostPattern: "/{slug}/", CategoryBase: "category", TagBase: "tag", AuthorBase: "author",
      TrailingSlash: "always", DateArchivesEnabled: true, AuthorArchivesEnabled: true, WPCompatEnabled: true,
    },
    Reading: Reading{PostsPerPage: 10, FeedItems: 10},
    SEO: SEO{
      Separator: "-",
      TitleTemplates: map[string]string{
        "post": "%%title%% %%page%% %%sep%% %%sitename%%", "page": "%%title%% %%page%% %%sep%% %%sitename%%",
        "category": "%%term_title%% %%page%% %%sep%% %%sitename%%", "tag": "%%term_title%% %%page%% %%sep%% %%sitename%%",
        "author": "%%name%% %%page%% %%sep%% %%sitename%%", "home": "%%sitename%% %%page%% %%sep%% %%sitedesc%%",
        "search": "You searched for %%searchphrase%% %%page%% %%sep%% %%sitename%%",
        "404":    "Page not found %%sep%% %%sitename%%", "date": "%%date%% %%page%% %%sep%% %%sitename%%",
      },
      DescriptionTemplates: map[string]string{},
      KnowledgeGraph:       KnowledgeGraph{Type: "organization"},
    },
    Discussion: Discussion{CommentsEnabled: true, ThreadDepth: 3},
  }
}

// Decode reads a settings document, applying values over defaults. Unknown keys are rejected at every level.
func Decode(b []byte) (All, map[string]bool, error) {
  var groups map[string]json.RawMessage
  if err := json.Unmarshal(b, &groups); err != nil {
    return All{}, nil, fmt.Errorf("settings must be a json object: %w", err)
  }
  all := Defaults()
  present := map[string]bool{}
  targets := map[string]any{
    "site": &all.Site, "permalinks": &all.Permalinks, "reading": &all.Reading, "seo": &all.SEO, "discussion": &all.Discussion,
  }
  for key, raw := range groups {
    target, ok := targets[key]
    if !ok {
      return All{}, nil, fmt.Errorf("unknown settings key %q", key)
    }
    dec := json.NewDecoder(bytes.NewReader(raw))
    dec.DisallowUnknownFields()
    if err := dec.Decode(target); err != nil {
      return All{}, nil, fmt.Errorf("settings.%s: %w", key, err)
    }
    present[key] = true
  }
  return all, present, all.Validate()
}

// Group returns the value stored for key.
func (a All) Group(key string) any {
  switch key {
  case "site":
    return a.Site
  case "permalinks":
    return a.Permalinks
  case "reading":
    return a.Reading
  case "seo":
    return a.SEO
  case "discussion":
    return a.Discussion
  }
  return nil
}

var postTokens = []string{"{slug}", "{year}", "{month}", "{day}", "{id}", "{category}"}

func (a All) Validate() error {
  var errs []string
  bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }

  if _, err := time.LoadLocation(a.Site.Timezone); err != nil {
    bad("site.timezone: unknown timezone %q", a.Site.Timezone)
  }
  p := a.Permalinks
  if !strings.HasPrefix(p.PostPattern, "/") || !(strings.Contains(p.PostPattern, "{slug}") || strings.Contains(p.PostPattern, "{id}")) {
    bad("permalinks.post_pattern: must start with / and contain {slug} or {id}")
  }
  rest := p.PostPattern
  for _, t := range postTokens {
    rest = strings.ReplaceAll(rest, t, "")
  }
  if strings.ContainsAny(rest, "{}") {
    bad("permalinks.post_pattern: unknown token; allowed: %s", strings.Join(postTokens, " "))
  }
  for name, base := range map[string]string{"category_base": p.CategoryBase, "tag_base": p.TagBase, "author_base": p.AuthorBase} {
    if base == "" || strings.ContainsAny(base, "/?#{} ") || base == "admin" || base == "m" {
      bad("permalinks.%s: must be a single non-reserved path segment", name)
    }
  }
  if p.TrailingSlash != "always" && p.TrailingSlash != "never" {
    bad("permalinks.trailing_slash: must be always or never")
  }
  if a.Reading.PostsPerPage < 1 || a.Reading.PostsPerPage > 100 {
    bad("reading.posts_per_page: must be 1-100")
  }
  if a.Reading.FeedItems < 1 || a.Reading.FeedItems > 100 {
    bad("reading.feed_items: must be 1-100")
  }
  for k := range a.SEO.TitleTemplates {
    if !slices.Contains(TemplateTypes, k) {
      bad("seo.title_templates: unknown type %q", k)
    }
  }
  for k := range a.SEO.DescriptionTemplates {
    if !slices.Contains(TemplateTypes, k) {
      bad("seo.description_templates: unknown type %q", k)
    }
  }
  if kg := a.SEO.KnowledgeGraph.Type; kg != "organization" && kg != "person" {
    bad("seo.knowledge_graph.type: must be organization or person")
  }
  for _, s := range a.SEO.SocialProfiles {
    if u, err := url.Parse(s); err != nil || u.Scheme != "https" || u.Host == "" {
      bad("seo.social_profiles: %q must be an https url", s)
    }
  }
  if a.Discussion.ThreadDepth < 1 || a.Discussion.ThreadDepth > 10 {
    bad("discussion.thread_depth: must be 1-10")
  }
  if a.Discussion.CloseAfterDays < 0 {
    bad("discussion.close_after_days: must be 0 (never) or more")
  }

  if len(errs) > 0 {
    slices.Sort(errs)
    return fmt.Errorf("invalid settings: %s", strings.Join(errs, "; "))
  }
  return nil
}

// Refs returns pointers to every reference in a, with the kind of entity each must point at.
func (a *All) Refs() map[string]**Ref {
  return map[string]**Ref{
    "reading.home":                   &a.Reading.Home,
    "reading.posts_page":             &a.Reading.PostsPage,
    "seo.knowledge_graph.logo_media": &a.SEO.KnowledgeGraph.Logo,
    "seo.default_image_media":        &a.SEO.DefaultImage,
  }
}

// RefKind is the entity kind each Refs() entry points at.
var RefKind = map[string]string{
  "reading.home":                   "post",
  "reading.posts_page":             "post",
  "seo.knowledge_graph.logo_media": "media",
  "seo.default_image_media":        "media",
}
