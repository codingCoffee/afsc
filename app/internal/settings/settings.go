// Package settings defines the typed settings registry. Each top-level key is stored as one row in the settings table.
package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
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
	RobotsTxt            string            `json:"robots_txt"`
	LLMsTxt              LLMsTxt           `json:"llms_txt"`
	Sitemaps             Sitemaps          `json:"sitemaps"`
	IndexNow             IndexNow          `json:"indexnow"`
	NotFound             NotFound          `json:"not_found"`
}

type LLMsTxt struct {
	Mode string `json:"mode"` // off, auto or custom
	Body string `json:"body"`
}

type Sitemaps struct {
	PageSize int `json:"page_size"`
}

type IndexNow struct {
	Enabled bool   `json:"enabled"`
	Key     string `json:"key"`
}

// NotFound controls the 404 log. Paths matching a noise pattern (globs: * spans slashes, ? is one character,
// case-insensitive, whole path) are never logged.
type NotFound struct {
	NoisePatterns []string `json:"noise_patterns"`
	RetentionDays int      `json:"retention_days"`
}

func DefaultNoisePatterns() []string {
	return []string{"*.php", "/.env*", "/.git/*", "/wp-admin/*", "*.asp*", "/cgi-bin/*"}
}

type Discussion struct {
	CommentsEnabled        bool `json:"comments_enabled"`
	CloseAfterDays         int  `json:"close_after_days"`
	ThreadDepth            int  `json:"thread_depth"`
	CommentIPRetentionDays int  `json:"comment_ip_retention_days"`
	SpamRetentionDays      int  `json:"spam_retention_days"`
	CommentsPerPage        int  `json:"comments_per_page"` // top-level threads per page; 0 shows all
	SpamThreshold          int  `json:"spam_threshold"`
	ModerationThreshold    int  `json:"moderation_threshold"`
	TrustedAutoApprove     bool `json:"trusted_auto_approve"`
}

// Media holds upload size limits in MB per file class (§7.4).
type Media struct {
	MaxImageMB int `json:"max_image_mb"`
	MaxVideoMB int `json:"max_video_mb"`
	MaxAudioMB int `json:"max_audio_mb"`
	MaxPDFMB   int `json:"max_pdf_mb"`
}

// Security holds the public CSP additions and toggles (§7.3, §7.6). ExtraCSPHosts maps a directive to origins.
type Security struct {
	ExtraCSPHosts map[string][]string `json:"extra_csp_hosts"`
	CSPReportOnly bool                `json:"csp_report_only"`
	HSTS          bool                `json:"hsts"`
}

// Snippets are admin-only, unsanitized HTML injected by {{head}} and {{footer}} (§7.6).
type Snippets struct {
	HeadHTML    string `json:"head_html"`
	BodyEndHTML string `json:"body_end_html"`
}

// CSPDirectives are the directives that accept extra hosts.
var CSPDirectives = []string{"script-src", "style-src", "img-src", "font-src", "connect-src", "frame-src"}

var cspHost = regexp.MustCompile(`^https://(\*\.)?[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+(:[0-9]{1,5})?$`)

const maxSnippetBytes = 64 << 10

type Cache struct {
	PageTTLSeconds int `json:"page_ttl_seconds"`
}

// All is every registered settings group, keyed by its JSON name.
type All struct {
	Site       Site       `json:"site"`
	Permalinks Permalinks `json:"permalinks"`
	Reading    Reading    `json:"reading"`
	SEO        SEO        `json:"seo"`
	Discussion Discussion `json:"discussion"`
	Cache      Cache      `json:"cache"`
	Media      Media      `json:"media"`
	Security   Security   `json:"security"`
	Snippets   Snippets   `json:"snippets"`
	// ThemeSettings maps a theme name to its values, so switching themes keeps each theme's settings.
	ThemeSettings map[string]map[string]any `json:"theme_settings"`
}

var Keys = []string{"site", "permalinks", "reading", "seo", "discussion", "cache", "media", "security", "snippets",
	"theme_settings"}

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
			LLMsTxt:              LLMsTxt{Mode: "off"},
			Sitemaps:             Sitemaps{PageSize: 1000},
			NotFound:             NotFound{NoisePatterns: DefaultNoisePatterns(), RetentionDays: 90},
		},
		Discussion: Discussion{CommentsEnabled: true, ThreadDepth: 3, CommentIPRetentionDays: 90, SpamRetentionDays: 30,
			CommentsPerPage: 50, SpamThreshold: 6, ModerationThreshold: 2, TrustedAutoApprove: true},
		Cache:         Cache{PageTTLSeconds: 60},
		Media:         Media{MaxImageMB: 20, MaxVideoMB: 500, MaxAudioMB: 50, MaxPDFMB: 50},
		Security:      Security{ExtraCSPHosts: map[string][]string{}, HSTS: true},
		ThemeSettings: map[string]map[string]any{},
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
	for key, raw := range groups {
		target := all.groupPtr(key)
		if target == nil {
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

func (a *All) groupPtr(key string) any {
	switch key {
	case "site":
		return &a.Site
	case "permalinks":
		return &a.Permalinks
	case "reading":
		return &a.Reading
	case "seo":
		return &a.SEO
	case "discussion":
		return &a.Discussion
	case "cache":
		return &a.Cache
	case "media":
		return &a.Media
	case "security":
		return &a.Security
	case "snippets":
		return &a.Snippets
	case "theme_settings":
		return &a.ThemeSettings
	}
	return nil
}

// ReplaceGroup sets group key to b decoded over the group's defaults, rejecting unknown fields. It doesn't validate.
func (a *All) ReplaceGroup(key string, b []byte) error {
	d := Defaults()
	src, dst := d.groupPtr(key), a.groupPtr(key)
	if src == nil {
		return fmt.Errorf("unknown settings group %q", key)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(src); err != nil {
		return err
	}
	if dec.More() {
		return fmt.Errorf("unexpected data after the %s value", key)
	}
	reflect.ValueOf(dst).Elem().Set(reflect.ValueOf(src).Elem())
	return nil
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
	case "cache":
		return a.Cache
	case "media":
		return a.Media
	case "security":
		return a.Security
	case "snippets":
		return a.Snippets
	case "theme_settings":
		return a.ThemeSettings
	}
	return nil
}

// indexNowKey is the key format the IndexNow protocol allows.
var indexNowKey = regexp.MustCompile(`^[a-zA-Z0-9-]{8,128}$`)

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
	switch l := a.SEO.LLMsTxt; {
	case l.Mode != "off" && l.Mode != "auto" && l.Mode != "custom":
		bad("seo.llms_txt.mode: must be off, auto or custom")
	case l.Mode == "custom" && strings.TrimSpace(l.Body) == "":
		bad("seo.llms_txt.body: required when mode is custom")
	}
	if n := a.SEO.Sitemaps.PageSize; n < 1 || n > 50000 {
		bad("seo.sitemaps.page_size: must be 1-50000")
	}
	if in := a.SEO.IndexNow; (in.Enabled || in.Key != "") && !indexNowKey.MatchString(in.Key) {
		bad("seo.indexnow.key: must be 8-128 letters, digits or dashes")
	}
	if nf := a.SEO.NotFound; len(nf.NoisePatterns) > 200 {
		bad("seo.not_found.noise_patterns: at most 200 patterns")
	} else {
		for _, p := range nf.NoisePatterns {
			if len(p) > 256 || !(strings.HasPrefix(p, "/") || strings.HasPrefix(p, "*")) {
				bad("seo.not_found.noise_patterns: %q must start with / or * and be at most 256 bytes", p)
			}
		}
	}
	if d := a.SEO.NotFound.RetentionDays; d < 1 || d > 3650 {
		bad("seo.not_found.retention_days: must be 1-3650")
	}
	if a.Discussion.ThreadDepth < 1 || a.Discussion.ThreadDepth > 10 {
		bad("discussion.thread_depth: must be 1-10")
	}
	if a.Discussion.CloseAfterDays < 0 {
		bad("discussion.close_after_days: must be 0 (never) or more")
	}
	if a.Cache.PageTTLSeconds < 0 || a.Cache.PageTTLSeconds > 86400 {
		bad("cache.page_ttl_seconds: must be 0-86400")
	}
	if d := a.Discussion.CommentIPRetentionDays; d < 1 || d > 3650 {
		bad("discussion.comment_ip_retention_days: must be 1-3650")
	}
	if d := a.Discussion.SpamRetentionDays; d < 1 || d > 3650 {
		bad("discussion.spam_retention_days: must be 1-3650")
	}
	if n := a.Discussion.CommentsPerPage; n < 0 || n > 500 {
		bad("discussion.comments_per_page: must be 0 (all) to 500")
	}
	if n := a.Discussion.SpamThreshold; n < 1 || n > 100 {
		bad("discussion.spam_threshold: must be 1-100")
	}
	if n := a.Discussion.ModerationThreshold; n < 0 || n >= a.Discussion.SpamThreshold {
		bad("discussion.moderation_threshold: must be 0 or more and below spam_threshold")
	}
	for name, mb := range map[string]int{"max_image_mb": a.Media.MaxImageMB, "max_video_mb": a.Media.MaxVideoMB,
		"max_audio_mb": a.Media.MaxAudioMB, "max_pdf_mb": a.Media.MaxPDFMB} {
		if mb < 1 || mb > 4096 {
			bad("media.%s: must be 1-4096", name)
		}
	}
	for dir, hosts := range a.Security.ExtraCSPHosts {
		if !slices.Contains(CSPDirectives, dir) {
			bad("security.extra_csp_hosts: unknown directive %q", dir)
			continue
		}
		if len(hosts) > 50 {
			bad("security.extra_csp_hosts.%s: at most 50 origins", dir)
		}
		for _, h := range hosts {
			if !cspHost.MatchString(h) {
				bad("security.extra_csp_hosts.%s: %q must be an https origin, optionally with a leading *.", dir, h)
			}
		}
	}
	if len(a.Snippets.HeadHTML) > maxSnippetBytes || len(a.Snippets.BodyEndHTML) > maxSnippetBytes {
		bad("snippets: each snippet must be at most 64 KB")
	}

	if len(errs) > 0 {
		slices.Sort(errs)
		return Invalid(errs)
	}
	return nil
}

// Invalid lists validation problems, each "group.field: message".
type Invalid []string

func (e Invalid) Error() string { return "invalid settings: " + strings.Join(e, "; ") }

// ValidCSPHost reports whether h is an https origin, optionally with a leading "*.".
func ValidCSPHost(h string) bool { return cspHost.MatchString(h) }

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

// OpenFor reports whether a post accepts new comments: comments are on globally, the post's comment_status is
// open and, with close_after_days set, the post was published within that many days.
func (d Discussion) OpenFor(commentStatus string, publishedAt *time.Time, now time.Time) bool {
	if !d.CommentsEnabled || commentStatus != "open" {
		return false
	}
	return d.CloseAfterDays == 0 || (publishedAt != nil && now.Before(publishedAt.AddDate(0, 0, d.CloseAfterDays)))
}
