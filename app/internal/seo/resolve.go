package seo

import (
	"cmp"
	"html/template"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"afsc/internal/settings"
	"afsc/internal/theme"
)

// Override is an entity's row in the seo table. Empty strings and nil pointers mean "not set".
type Override struct {
	Title              string
	Description        string
	Canonical          string
	NoIndex            *bool
	NoFollow           *bool
	Advanced           []string
	OGTitle            string
	OGDescription      string
	OGImage            *theme.Media
	TwitterTitle       string
	TwitterDescription string
	TwitterImage       *theme.Media
}

// Video is one post_videos row, with the thumbnail already resolved to an absolute URL.
type Video struct {
	Name           string
	Description    string
	ThumbnailURL   string
	EmbedURL       string
	ContentURL     string
	Duration       int // seconds, 0 when unknown
	UploadDate     *time.Time
	FamilyFriendly *bool
}

// Input is everything Build needs about one page.
type Input struct {
	Page         *theme.PageData
	Settings     *settings.All
	Override     *Override // the post, term or author on this page; nil when it has none
	Crumbs       []theme.Crumb
	Videos       []Video
	FirstImage   *theme.Media // first image in the post content
	DefaultImage *theme.Media // seo.default_image_media
	Logo         *theme.Media // seo.knowledge_graph.logo_media
	Location     *time.Location
	Now          time.Time
	// MetaFilter and GraphFilter are the seo.meta and seo.schema_graph plugin hooks; nil leaves the output alone.
	MetaFilter  func(*Meta) *Meta
	GraphFilter func([]map[string]any) []map[string]any
}

// Tag is one head tag after <title>: Kind "name" or "property" prints <meta Kind=Key content=Value>, "rel"
// prints <link rel=Key href=Value>.
type Tag struct {
	Kind  string
	Key   string
	Value string
}

// Meta is the head output the seo.meta filter sees: the <title> text and every tag after it, in order. The
// JSON-LD graph goes through its own filter.
type Meta struct {
	Title string
	Tags  []Tag
}

const (
	defaultRobots  = "max-snippet:-1, max-image-preview:large, max-video-preview:-1"
	maxDescription = 156
)

// Build resolves the page's head (§8.1). unknown lists %%variables%% the templates used but AFSC doesn't know.
func Build(in Input) (h *theme.Head, unknown []string) {
	d, s := in.Page, in.Settings
	if in.Location == nil {
		in.Location = time.UTC
	}
	ov := in.Override
	if ov == nil {
		ov = &Override{}
	}
	v := in.vars()
	typ := templateType(d)

	expand := func(tpl string) string {
		out, u := Expand(tpl, v)
		unknown = append(unknown, u...)
		return out
	}
	title := expand(ov.Title)
	if title == "" {
		title = expand(s.SEO.TitleTemplates[typ])
	}
	if title == "" {
		title = expand(settings.Defaults().SEO.TitleTemplates[typ])
	}
	title = cmp.Or(title, d.Site.Title)

	desc := expand(ov.Description)
	if desc == "" {
		desc = expand(s.SEO.DescriptionTemplates[typ])
	}
	if desc == "" && s.SEO.AutoDescription {
		desc = in.autoDescription()
	}
	desc = truncate(desc, maxDescription)

	h = &theme.Head{Title: title, Description: desc, Canonical: in.canonical(ov), Robots: in.robots(ov)}
	m := &tags{}
	m.meta("description", desc)
	m.meta("robots", h.Robots)
	m.link("canonical", h.Canonical)
	if p := d.Pagination; p != nil && d.Kind != theme.KindSearch {
		m.link("prev", p.Prev)
		m.link("next", p.Next)
	}
	in.openGraph(m, ov, title, desc, h.Canonical)
	in.twitter(m, ov)
	meta := &Meta{Title: title, Tags: m.list}
	if in.MetaFilter != nil {
		if out := in.MetaFilter(meta); out != nil {
			meta = out
		}
	}
	h.Title = meta.Title
	var b strings.Builder
	for _, t := range meta.Tags {
		t.write(&b)
	}
	g := in.graph(title, desc, h.Canonical)
	if in.GraphFilter != nil {
		g = in.GraphFilter(g)
	}
	b.WriteString(script(g))
	h.Tags = template.HTML(b.String())
	return h, unknown
}

// templateType maps a page kind to its §8.2 template key.
func templateType(d *theme.PageData) string {
	switch d.Kind {
	case theme.KindSingle:
		return "post"
	case theme.KindPage, theme.KindFrontPage, theme.KindPostsPage:
		return "page"
	case theme.KindCategory, theme.KindTag, theme.KindAuthor, theme.KindDate, theme.KindSearch, theme.KindHome:
		return d.Kind
	}
	return "404"
}

func (in *Input) vars() Vars {
	d, s := in.Page, in.Settings
	v := Vars{SiteName: d.Site.Title, SiteDesc: d.Site.Tagline, Sep: s.SEO.Separator, Page: max(1, d.Request.Paged),
		PageTotal: 1, Now: in.Now.In(in.Location)}
	if d.Pagination != nil {
		v.PageTotal = d.Pagination.Total
	}
	format := func(t time.Time) string { return t.In(in.Location).Format(s.Site.DateFormat) }
	if p := d.Post; p != nil {
		v.Title, v.ExcerptOnly = p.Title, p.Excerpt
		v.Excerpt = p.Excerpt
		if v.Excerpt == "" && s.SEO.AutoDescription {
			v.Excerpt = contentExcerpt(p)
		}
		v.Date, v.Modified = format(p.PublishedAt), format(p.UpdatedAt)
		v.PTSingle, v.PTPlural = "Post", "Posts"
		if p.Type == "page" {
			v.PTSingle, v.PTPlural = "Page", "Pages"
		}
		if p.Primary != nil {
			v.PrimaryCategory = p.Primary.Name
		} else if len(p.Categories) > 0 {
			v.PrimaryCategory = p.Categories[0].Name
		}
		v.Category, v.Tag = termNames(p.Categories), termNames(p.Tags)
		if len(p.Authors) > 0 {
			v.Name = p.Authors[0].Name
		}
	}
	if t := d.Term; t != nil {
		v.Title, v.TermTitle, v.TermDescription = t.Name, t.Name, t.Description
		if t.Taxonomy == "category" {
			v.Category, v.PrimaryCategory = t.Name, t.Name
		} else {
			v.Tag = t.Name
		}
	}
	if a := d.Author; a != nil {
		v.Title, v.Name = a.Name, a.Name
	}
	if v.Name != "" {
		first, last, _ := strings.Cut(v.Name, " ")
		v.AuthorFirst, v.AuthorLast = first, last
	}
	if dt := d.Date; dt != nil {
		v.Date = strconv.Itoa(dt.Year)
		if dt.Month > 0 {
			v.Date = dt.Start.Format("January 2006")
		}
	}
	if d.Search != nil {
		v.SearchPhrase = d.Search.Query
	}
	return v
}

func termNames(ts []*theme.Term) string {
	names := make([]string, len(ts))
	for i, t := range ts {
		names[i] = t.Name
	}
	return strings.Join(names, ", ")
}

func contentExcerpt(p *theme.Post) string {
	if p.HasMore {
		return p.TeaserText
	}
	return theme.TrimWords(p.ContentText, 55, "")
}

func (in *Input) autoDescription() string {
	d := in.Page
	switch {
	case d.Post != nil && d.Kind != theme.KindPostsPage:
		return cmp.Or(d.Post.Excerpt, contentExcerpt(d.Post))
	case d.Term != nil:
		return d.Term.Description
	case d.Author != nil:
		return d.Author.Bio
	}
	return ""
}

// truncate cuts s to at most n runes on a word boundary, adding an ellipsis when it cut anything.
func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)[:n-1]
	cut := string(r)
	if i := strings.LastIndexByte(cut, ' '); i > n/2 {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:") + "…"
}

func (in *Input) canonical(ov *Override) string {
	d := in.Page
	switch d.Kind {
	case theme.KindSearch, theme.Kind404, theme.Kind410:
		return ""
	}
	return cmp.Or(OverrideCanonical(d.Site.URL, ov.Canonical), d.Request.URL)
}

// OverrideCanonical makes an entity's canonical override absolute. It returns "" when c is unset or unusable.
func OverrideCanonical(siteURL, c string) string {
	switch {
	case strings.HasPrefix(c, "/") && !strings.HasPrefix(c, "//"):
		return siteURL + c
	case strings.HasPrefix(c, "https://") || strings.HasPrefix(c, "http://"):
		return c
	}
	return ""
}

// Indexable reports whether the page may be indexed: what the robots meta says and what sitemaps include.
func (in *Input) Indexable(ov *Override) bool {
	d := in.Page
	if d.Request.NoIndex {
		return false
	}
	var noIndex *bool
	if ov != nil {
		noIndex = ov.NoIndex
	}
	return IndexableKind(d.Kind, noIndex, in.Settings)
}

// IndexableKind is the entity part of Indexable: the noindex override, else the type default.
func IndexableKind(kind string, noIndex *bool, s *settings.All) bool {
	switch kind {
	case theme.KindSearch, theme.Kind404, theme.Kind410:
		return false
	}
	if noIndex != nil {
		return !*noIndex
	}
	return !(kind == theme.KindTag && s.SEO.TagNoindex)
}

func (in *Input) robots(ov *Override) string {
	index := in.Indexable(ov)
	parts := []string{"noindex"}
	if index {
		parts[0] = "index"
	}
	if ov.NoFollow != nil && *ov.NoFollow {
		parts = append(parts, "nofollow")
	} else {
		parts = append(parts, "follow")
	}
	if !index {
		return strings.Join(parts, ", ")
	}
	if len(ov.Advanced) > 0 {
		return strings.Join(append(parts, ov.Advanced...), ", ")
	}
	return strings.Join(parts, ", ") + ", " + defaultRobots
}

// image follows §8.1 step 5: override, featured image, first content image, site default.
func (in *Input) image(override *theme.Media) *theme.Media {
	if override != nil {
		return override
	}
	if p := in.Page.Post; p != nil {
		if p.FeaturedMedia != nil {
			return p.FeaturedMedia
		}
		if in.FirstImage != nil {
			return in.FirstImage
		}
	}
	return in.DefaultImage
}

func (in *Input) openGraph(m *tags, ov *Override, title, desc, canonical string) {
	d := in.Page
	typ := "website"
	if d.Kind == theme.KindSingle || d.Kind == theme.KindPage {
		typ = "article"
	}
	m.prop("og:locale", locale(d.Site.Language))
	m.prop("og:type", typ)
	m.prop("og:title", cmp.Or(expandPlain(ov.OGTitle, in), title))
	m.prop("og:description", cmp.Or(expandPlain(ov.OGDescription, in), desc))
	m.prop("og:url", canonical)
	m.prop("og:site_name", d.Site.Title)
	if p := d.Post; p != nil && typ == "article" {
		if p.Type == "post" {
			m.prop("article:published_time", isoTime(p.PublishedAt))
		}
		m.prop("article:modified_time", isoTime(p.UpdatedAt))
		if p.Type == "post" {
			for _, a := range p.Authors {
				m.prop("article:author", a.URL)
			}
		}
	}
	if img := in.image(ov.OGImage); img != nil {
		m.prop("og:image", img.URL)
		if img.Width > 0 && img.Height > 0 {
			m.prop("og:image:width", strconv.Itoa(img.Width))
			m.prop("og:image:height", strconv.Itoa(img.Height))
		}
		m.prop("og:image:type", img.Mime)
	}
}

func expandPlain(tpl string, in *Input) string {
	out, _ := Expand(tpl, in.vars())
	return out
}

func (in *Input) twitter(m *tags, ov *Override) {
	m.meta("twitter:card", "summary_large_image")
	if site := in.Settings.SEO.TwitterSite; site != "" {
		m.meta("twitter:site", "@"+strings.TrimPrefix(handle(site), "@"))
	}
	m.meta("twitter:title", expandPlain(ov.TwitterTitle, in))
	m.meta("twitter:description", expandPlain(ov.TwitterDescription, in))
	if ov.TwitterImage != nil {
		m.meta("twitter:image", ov.TwitterImage.URL)
	}
}

// handle turns a profile URL or handle into the bare handle.
func handle(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), "/")
	if i := strings.LastIndexByte(s, '/'); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// locale maps a BCP 47 language tag to an Open Graph locale: en-IN → en_IN, en → en_US.
func locale(lang string) string {
	lang = strings.ReplaceAll(cmp.Or(lang, "en"), "-", "_")
	l, r, ok := strings.Cut(lang, "_")
	if !ok {
		if l == "en" {
			return "en_US"
		}
		return strings.ToLower(l) + "_" + strings.ToUpper(l)
	}
	return strings.ToLower(l) + "_" + strings.ToUpper(r)
}

func isoTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// tags accumulates head tags, skipping empty values.
type tags struct{ list []Tag }

func (t *tags) add(kind, key, value string) {
	if value != "" {
		t.list = append(t.list, Tag{Kind: kind, Key: key, Value: value})
	}
}

func (t *tags) meta(name, content string) { t.add("name", name, content) }
func (t *tags) prop(prop, content string) { t.add("property", prop, content) }
func (t *tags) link(rel, href string)     { t.add("rel", rel, href) }

// write prints the tag escaped. Tags of an unknown kind or with an empty value print nothing.
func (t Tag) write(b *strings.Builder) {
	if t.Value == "" {
		return
	}
	switch t.Kind {
	case "name", "property":
		b.WriteString(`<meta ` + t.Kind + `="` + esc(t.Key) + `" content="` + esc(t.Value) + "\">\n")
	case "rel":
		b.WriteString(`<link rel="` + esc(t.Key) + `" href="` + esc(t.Value) + "\">\n")
	}
}

var esc = template.HTMLEscapeString
