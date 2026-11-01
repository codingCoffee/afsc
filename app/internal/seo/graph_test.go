package seo

import (
	"encoding/json"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"afsc/internal/settings"
	"afsc/internal/theme"
)

// vocab lists the schema.org types AFSC emits, their parent and their own properties (from schema.org).
var vocab = map[string]struct {
	parent string
	props  []string
}{
	"Thing":              {"", []string{"name", "url", "description", "image", "sameAs", "mainEntityOfPage", "potentialAction"}},
	"CreativeWork":       {"Thing", []string{"inLanguage", "isPartOf", "datePublished", "dateModified", "author", "publisher", "headline", "keywords", "thumbnailUrl", "video", "about", "mainEntity"}},
	"WebPage":            {"CreativeWork", []string{"breadcrumb", "primaryImageOfPage"}},
	"CollectionPage":     {"WebPage", nil},
	"ProfilePage":        {"WebPage", nil},
	"SearchResultsPage":  {"WebPage", nil},
	"WebSite":            {"CreativeWork", nil},
	"Article":            {"CreativeWork", []string{"articleSection", "wordCount"}},
	"SocialMediaPosting": {"Article", nil},
	"BlogPosting":        {"SocialMediaPosting", nil},
	"MediaObject":        {"CreativeWork", []string{"contentUrl", "embedUrl", "uploadDate", "duration", "width", "height"}},
	"ImageObject":        {"MediaObject", []string{"caption"}},
	"VideoObject":        {"MediaObject", []string{"caption"}},
	"Organization":       {"Thing", []string{"logo"}},
	"Person":             {"Thing", nil},
	"Action":             {"Thing", []string{"target"}},
	"SearchAction":       {"Action", []string{"query-input"}},
	"EntryPoint":         {"Thing", []string{"urlTemplate"}},
	"ItemList":           {"Thing", []string{"itemListElement"}},
	"BreadcrumbList":     {"ItemList", nil},
	"ListItem":           {"Thing", []string{"position", "item"}},
}

// required are the properties Google's structured data docs require (or that AFSC always promises).
var required = map[string][]string{
	"WebSite":           {"url", "name"},
	"Organization":      {"name", "url"},
	"Person":            {"name"},
	"WebPage":           {"url", "name", "isPartOf"},
	"CollectionPage":    {"url", "name", "isPartOf"},
	"ProfilePage":       {"url", "name", "isPartOf", "mainEntity"},
	"SearchResultsPage": {"url", "name", "isPartOf"},
	"BlogPosting":       {"headline", "datePublished", "dateModified", "author", "publisher", "mainEntityOfPage"},
	"ImageObject":       {"url"},
	"VideoObject":       {"name", "thumbnailUrl", "uploadDate"},
	"BreadcrumbList":    {"itemListElement"},
	"ListItem":          {"position", "name"},
	"SearchAction":      {"target", "query-input"},
}

func allowed(typ, prop string) bool {
	for t := typ; t != ""; t = vocab[t].parent {
		if slices.Contains(vocab[t].props, prop) {
			return true
		}
	}
	return false
}

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}(Z|[+-]\d{2}:\d{2})$`)

// validate checks the graph against the vocabulary, required properties and @id references.
func validate(t *testing.T, raw string) map[string]int {
	t.Helper()
	var doc struct {
		Context string           `json:"@context"`
		Graph   []map[string]any `json:"@graph"`
	}
	if err := json.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatalf("invalid JSON-LD: %v", err)
	}
	if doc.Context != "https://schema.org" {
		t.Errorf("@context = %q", doc.Context)
	}
	ids, types := map[string]bool{}, map[string]int{}
	var refs []string
	var walk func(path string, n map[string]any, top bool)
	walk = func(path string, n map[string]any, top bool) {
		typ, _ := n["@type"].(string)
		id, _ := n["@id"].(string)
		if typ == "" {
			if id == "" || len(n) > 2 {
				t.Errorf("%s: node without @type must be a reference: %v", path, n)
			}
			refs = append(refs, id)
			return
		}
		if _, ok := vocab[typ]; !ok {
			t.Errorf("%s: unknown type %q", path, typ)
			return
		}
		types[typ]++
		if id != "" {
			if ids[id] {
				t.Errorf("%s: duplicate @id %q", path, id)
			}
			ids[id] = true
		} else if top {
			t.Errorf("%s: top-level %s without @id", path, typ)
		}
		for _, p := range required[typ] {
			if v, ok := n[p]; !ok || v == "" {
				t.Errorf("%s: %s missing required %q", path, typ, p)
			}
		}
		for k, v := range n {
			if strings.HasPrefix(k, "@") {
				continue
			}
			if !allowed(typ, k) {
				t.Errorf("%s: %s has no property %q", path, typ, k)
			}
			if strings.HasPrefix(k, "date") || k == "uploadDate" {
				if s, _ := v.(string); !dateRe.MatchString(s) {
					t.Errorf("%s: %s.%s = %q is not an ISO 8601 datetime", path, typ, k, s)
				}
			}
			visit(v, func(c map[string]any) { walk(path+"."+k, c, false) })
		}
	}
	for i, n := range doc.Graph {
		walk("@graph["+string(rune('0'+i))+"]", n, true)
	}
	for _, r := range refs {
		if !ids[r] {
			t.Errorf("@id reference %q does not resolve inside the graph", r)
		}
	}
	return types
}

func visit(v any, fn func(map[string]any)) {
	switch x := v.(type) {
	case map[string]any:
		fn(x)
	case []any:
		for _, e := range x {
			visit(e, fn)
		}
	}
}

var ldRe = regexp.MustCompile(`<script type="application/ld\+json"[^>]*>(.*?)</script>`)

func fixtureInput(t *testing.T, th *theme.Theme, kind string, full bool) Input {
	t.Helper()
	s := settings.Defaults()
	s.Site.Title = "Example Blog"
	s.SEO.SocialProfiles = []string{"https://x.com/example"}
	d := theme.Fixture(kind, full)
	in := Input{Page: d, Settings: &s, Now: time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC), Crumbs: th.Breadcrumbs(d)}
	if full {
		img := &theme.Media{ID: 9, URL: "https://blog.example.com/m/9/ab12cd34/orig/logo.png", Mime: "image/png", Width: 600, Height: 60}
		in.Logo, in.DefaultImage = img, img
		up := time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
		in.Videos = []Video{
			{Name: "Example video", Description: "A video.", ThumbnailURL: "https://example.com/thumb.jpg", EmbedURL: EmbedURL("youtube", "XXXXXXXXXXX"), Duration: 312, UploadDate: &up},
			{Name: "No thumbnail", UploadDate: &up, ContentURL: "https://example.com/v.mp4"},
		}
	}
	return in
}

func loadTheme(t *testing.T) *theme.Theme {
	t.Helper()
	m, err := theme.LoadManifest("../../themes/default")
	if err != nil {
		t.Fatal(err)
	}
	th, err := theme.Load("../../themes/default", theme.Options{Backend: theme.FixtureBackend(m)})
	if err != nil {
		t.Fatal(err)
	}
	return th
}

func TestGraphPerPageType(t *testing.T) {
	th := loadTheme(t)
	want := map[string][]string{
		theme.KindHome:      {"CollectionPage", "WebSite", "Organization", "BreadcrumbList"},
		theme.KindFrontPage: {"WebPage", "WebSite", "Organization"},
		theme.KindPostsPage: {"CollectionPage", "BreadcrumbList"},
		theme.KindSingle:    {"BlogPosting", "WebPage", "ImageObject", "BreadcrumbList", "Person", "VideoObject"},
		theme.KindPage:      {"WebPage", "ImageObject", "BreadcrumbList"},
		theme.KindCategory:  {"CollectionPage", "BreadcrumbList"},
		theme.KindTag:       {"CollectionPage", "BreadcrumbList"},
		theme.KindAuthor:    {"ProfilePage", "Person", "BreadcrumbList"},
		theme.KindDate:      {"CollectionPage", "BreadcrumbList"},
		theme.KindSearch:    {"SearchResultsPage", "BreadcrumbList"},
		theme.Kind404:       {"WebSite", "Organization"},
	}
	for kind, types := range want {
		for _, full := range []bool{true, false} {
			name := kind + map[bool]string{true: "/full", false: "/sparse"}[full]
			t.Run(name, func(t *testing.T) {
				h, _ := Build(fixtureInput(t, th, kind, full))
				m := ldRe.FindStringSubmatch(string(h.Tags))
				if m == nil {
					t.Fatal("no JSON-LD script")
				}
				got := validate(t, m[1])
				for _, typ := range types {
					if typ == "BreadcrumbList" && kind == theme.KindHome {
						continue // home's trail is just "Home"
					}
					if (typ == "VideoObject" || typ == "ImageObject" || typ == "Person") && !full && kind != theme.KindAuthor {
						continue
					}
					if got[typ] == 0 {
						t.Errorf("missing %s; got %v", typ, got)
					}
				}
				if kind == theme.KindSingle && full && got["VideoObject"] != 1 {
					t.Errorf("want one VideoObject (the other lacks a thumbnail), got %d", got["VideoObject"])
				}
			})
		}
	}
}

func TestGraphStableIDs(t *testing.T) {
	th := loadTheme(t)
	h1, _ := Build(fixtureInput(t, th, theme.KindSingle, true))
	h2, _ := Build(fixtureInput(t, th, theme.KindSingle, true))
	if h1.Tags != h2.Tags {
		t.Fatal("output differs between identical builds")
	}
	a := &theme.Author{Key: "wp:user:2"}
	if PersonID("https://x.test", a) != PersonID("https://x.test", &theme.Author{ID: 99, Key: "wp:user:2"}) {
		t.Error("Person @id must depend only on the key")
	}
	for _, id := range []string{`"@id":"https://blog.example.com/#website"`, `"@id":"https://blog.example.com/#organization"`,
		`"@id":"https://blog.example.com/example-post-1/#article"`, `"@id":"https://blog.example.com/example-post-1/#primaryimage"`,
		`"@id":"https://blog.example.com/example-post-1/#breadcrumb"`, `"@id":"https://blog.example.com/example-post-1/#video-1"`} {
		if !strings.Contains(string(h1.Tags), id) {
			t.Errorf("missing %s", id)
		}
	}
}

func TestGraphEscapesScript(t *testing.T) {
	th := loadTheme(t)
	in := fixtureInput(t, th, theme.KindSingle, true)
	in.Page.Post.Title = "</script><script>alert(1)</script>"
	h, _ := Build(in)
	if strings.Count(string(h.Tags), "</script>") != 1 {
		t.Fatalf("title broke out of the JSON-LD script: %s", h.Tags)
	}
}

func TestBuildMeta(t *testing.T) {
	th := loadTheme(t)
	yes, no := true, false
	cases := []struct {
		name     string
		kind     string
		full     bool
		mod      func(*Input)
		contains []string
		absent   []string
	}{
		{"single defaults", theme.KindSingle, true, nil, []string{
			"<meta name=\"robots\" content=\"index, follow, max-snippet:-1, max-image-preview:large, max-video-preview:-1\">",
			`<link rel="canonical" href="https://blog.example.com/example-post-1/">`,
			`<meta property="og:type" content="article">`, `<meta property="article:author" content="https://blog.example.com/author/author-a/">`,
			`<meta property="og:image" content="https://blog.example.com/m/7/ab12cd34/orig/chart.png">`,
			`<meta property="og:image:width" content="1600">`, `<meta name="twitter:card" content="summary_large_image">`,
		}, []string{"twitter:title", "twitter:image", `name="description"`}},
		{"override wins", theme.KindSingle, true, func(in *Input) {
			in.Override = &Override{Title: "Custom %%sep%% %%sitename%%", Description: "Custom description", Canonical: "/elsewhere/",
				NoIndex: &yes, NoFollow: &yes, OGTitle: "OG %%title%%", TwitterTitle: "Tweet", TwitterImage: &theme.Media{URL: "https://cdn.test/t.png"}}
		}, []string{`<meta name="description" content="Custom description">`, `<meta name="robots" content="noindex, nofollow">`,
			`<link rel="canonical" href="https://blog.example.com/elsewhere/">`, `<meta property="og:title" content="OG Example post 1">`,
			`<meta name="twitter:title" content="Tweet">`, `<meta name="twitter:image" content="https://cdn.test/t.png">`}, nil},
		{"advanced robots", theme.KindSingle, true, func(in *Input) {
			in.Override = &Override{NoIndex: &no, Advanced: []string{"noarchive", "noimageindex"}}
		}, []string{`<meta name="robots" content="index, follow, noarchive, noimageindex">`}, nil},
		{"first content image", theme.KindSingle, false, func(in *Input) {
			in.FirstImage = &theme.Media{URL: "https://blog.example.com/m/3/aa/orig/first.jpg", Mime: "image/jpeg"}
		}, []string{`<meta property="og:image" content="https://blog.example.com/m/3/aa/orig/first.jpg">`}, nil},
		{"default image", theme.KindCategory, false, func(in *Input) {
			in.DefaultImage = &theme.Media{URL: "https://blog.example.com/m/4/bb/orig/default.png", Mime: "image/png"}
		}, []string{`<meta property="og:image" content="https://blog.example.com/m/4/bb/orig/default.png">`}, nil},
		{"paged archive", theme.KindCategory, true, nil, []string{
			`<link rel="canonical" href="https://blog.example.com/category/markets/stocks/">`,
			`<link rel="prev" href="https://blog.example.com/category/markets/stocks/">`,
			`<link rel="next" href="https://blog.example.com/category/markets/stocks/page/3/">`}, nil},
		{"tag noindex setting", theme.KindTag, false, func(in *Input) { in.Settings.SEO.TagNoindex = true },
			[]string{`<meta name="robots" content="noindex, follow">`}, nil},
		{"tag override beats setting", theme.KindTag, false, func(in *Input) {
			in.Settings.SEO.TagNoindex = true
			in.Override = &Override{NoIndex: &no}
		}, []string{`content="index, follow, max-snippet`}, nil},
		{"search", theme.KindSearch, true, nil, []string{`<meta name="robots" content="noindex, follow">`}, []string{`rel="canonical"`, `rel="next"`}},
		{"404", theme.Kind404, false, nil, []string{`<meta name="robots" content="noindex, follow">`}, []string{`rel="canonical"`}},
		{"auto description off", theme.KindSingle, false, nil, nil, []string{`name="description"`}},
		{"auto description on", theme.KindSingle, false, func(in *Input) { in.Settings.SEO.AutoDescription = true },
			[]string{`<meta name="description" content="Body text.">`}, nil},
		{"description template", theme.KindCategory, true, func(in *Input) {
			in.Settings.SEO.DescriptionTemplates = map[string]string{"category": "%%term_title%%: %%term_description%%"}
		}, []string{`<meta name="description" content="Stocks:">`}, nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			in := fixtureInput(t, th, c.kind, c.full)
			if c.mod != nil {
				c.mod(&in)
			}
			h, _ := Build(in)
			out := string(h.Tags)
			for _, s := range c.contains {
				if !strings.Contains(out, s) {
					t.Errorf("missing %s\n%s", s, out)
				}
			}
			for _, s := range c.absent {
				if strings.Contains(out, s) {
					t.Errorf("unexpected %s", s)
				}
			}
		})
	}
}

func TestBuildTitles(t *testing.T) {
	th := loadTheme(t)
	cases := map[string]string{
		theme.KindSingle:   "Example post 1 - Example Blog",
		theme.KindPage:     "Example post 5 - Example Blog",
		theme.KindCategory: "Stocks - Page 2 of 7 - Example Blog",
		theme.KindAuthor:   "Author A - Page 2 of 7 - Example Blog",
		theme.KindDate:     "March 2021 - Page 2 of 7 - Example Blog",
		theme.KindSearch:   "You searched for example <b> - Page 2 of 7 - Example Blog",
		theme.Kind404:      "Page not found - Example Blog",
	}
	for kind, want := range cases {
		h, unknown := Build(fixtureInput(t, th, kind, true))
		if h.Title != want || len(unknown) > 0 {
			t.Errorf("%s: title %q (unknown %v), want %q", kind, h.Title, unknown, want)
		}
	}
}
