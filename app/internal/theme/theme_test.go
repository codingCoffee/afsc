package theme

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"afsc/internal/content"
)

func TestCandidates(t *testing.T) {
	post := &Post{Slug: "hello", Type: "post"}
	page := &Post{Slug: "about", Type: "page"}
	tmplPage := &Post{Slug: "team", Type: "page", Template: "full-width"}
	term := &Term{Slug: "markets"}
	cases := []struct {
		d    *PageData
		want string
	}{
		{&PageData{Kind: KindHome}, "front-page home index"},
		{&PageData{Kind: KindFrontPage, Post: page}, "front-page page-about page index"},
		{&PageData{Kind: KindFrontPage, Post: tmplPage}, "front-page page-full-width page-team page index"},
		{&PageData{Kind: KindPostsPage, Post: page}, "home index"},
		{&PageData{Kind: KindSingle, Post: post}, "single-hello single index"},
		{&PageData{Kind: KindPage, Post: page}, "page-about page index"},
		{&PageData{Kind: KindPage, Post: tmplPage}, "page-full-width page-team page index"},
		{&PageData{Kind: KindCategory, Term: term}, "category-markets category archive index"},
		{&PageData{Kind: KindTag, Term: &Term{Slug: "go"}}, "tag-go tag archive index"},
		{&PageData{Kind: KindAuthor, Author: &Author{Slug: "author-a"}}, "author-author-a author archive index"},
		{&PageData{Kind: KindDate}, "date archive index"},
		{&PageData{Kind: KindSearch}, "search index"},
		{&PageData{Kind: Kind404}, "404 index"},
		{&PageData{Kind: Kind410}, "410 404 index"},
	}
	for _, c := range cases {
		got := strings.ReplaceAll(strings.Join(Candidates(c.d), " "), ".html", "")
		if got != c.want {
			t.Errorf("%s: got %q, want %q", c.d.Kind, got, c.want)
		}
	}
}

func TestDefaultThemePassesCheck(t *testing.T) {
	results, err := Check("../../themes/default", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s %s: %v", r.Template, r.Fixture, r.Err)
		}
	}
	if len(results) == 0 {
		t.Fatal("no checks ran")
	}
}

func writeTheme(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	if _, ok := files["theme.json"]; !ok {
		files["theme.json"] = `{"name":"t","menu_locations":["primary"],"strings":{"hello":"Hello"}}`
	}
	if _, ok := files["templates/index.html"]; !ok {
		files["templates/index.html"] = `<p>{{.Site.Title}}</p>`
	}
	for name, body := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestCheckFailures(t *testing.T) {
	loadErr := []struct {
		name  string
		files map[string]string
		want  string
	}{
		{"unknown func", map[string]string{"templates/single.html": `{{nope .}}`}, `function "nope" not defined`},
		{"missing index", map[string]string{"templates/index.html": "", "templates/single.html": `x`}, ""},
		{"unknown node partial", map[string]string{"templates/nodes/marquee.html": `x`}, "not an overridable content node"},
		{"unknown theme.json field", map[string]string{"theme.json": `{"name":"t","colour":"red"}`}, "unknown field"},
		{"missing asset", map[string]string{"theme.json": `{"name":"t","assets":[{"path":"app.css"}]}`}, "not found"},
		{"crop without height", map[string]string{"theme.json": `{"name":"t","image_sizes":{"sq":{"width":10,"crop":true}}}`}, "needs both"},
	}
	for _, c := range loadErr {
		t.Run(c.name, func(t *testing.T) {
			if c.name == "missing index" {
				dir := writeTheme(t, c.files)
				_ = os.Remove(filepath.Join(dir, "templates", "index.html"))
				if _, err := Check(dir, nil); err == nil || !strings.Contains(err.Error(), "index.html is required") {
					t.Fatalf("want missing index error, got %v", err)
				}
				return
			}
			_, err := Check(writeTheme(t, c.files), nil)
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("want error containing %q, got %v", c.want, err)
			}
		})
	}

	renderErr := []struct {
		name, file, body, want string
	}{
		{"missing field", "templates/single.html", `{{.Post.Nope}}`, "can't evaluate field Nope"},
		{"unguarded nil", "templates/single.html", `{{.Post.FeaturedMedia.URL}}`, "nil pointer"},
		{"missing string", "templates/single.html", `{{t "nope"}}`, `no string "nope"`},
		{"undeclared menu", "templates/single.html", `{{range menu . "sidebar"}}{{end}}`, "not declared"},
		{"undeclared theme setting", "templates/single.html", `{{theme_setting "x"}}`, "not declared"},
		{"undeclared image size", "templates/single.html", `{{image .Post.FeaturedMedia "huge"}}`, "not declared"},
		{"unknown template", "templates/sidebar.html", `x`, "not part of the template hierarchy"},
		{"bad node partial", "templates/nodes/paragraph.html", `{{.Nope}}`, "can't evaluate field Nope"},
	}
	for _, c := range renderErr {
		t.Run(c.name, func(t *testing.T) {
			results, err := Check(writeTheme(t, map[string]string{c.file: c.body}), nil)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range results {
				if r.Err != nil && strings.Contains(r.Err.Error(), c.want) {
					return
				}
			}
			t.Fatalf("want a failure containing %q, got %+v", c.want, results)
		})
	}
}

func TestBaseLayoutAndPartials(t *testing.T) {
	dir := writeTheme(t, map[string]string{
		"templates/base.html":            `<html><title>{{block "title" .}}{{.Site.Title}}{{end}}</title><body class="{{body_class .}}">{{block "content" .}}{{end}}</body></html>`,
		"templates/single.html":          `{{define "title"}}{{.Post.Title}}{{end}}{{define "content"}}{{template "parts/hi.html" .}}{{.Post.ContentHTML}}{{end}}`,
		"templates/parts/hi.html":        `<p>{{t "hello"}}</p>`,
		"templates/nodes/paragraph.html": `<div class="p">{{.Children}}</div>`,
	})
	th, err := Load(dir, Options{Backend: FixtureBackend(&Manifest{})})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	name, err := th.Page(&b, Fixture(KindSingle, false))
	if err != nil {
		t.Fatal(err)
	}
	want := `<html><title>Example post 1</title><body class="post-template-default single single-post postid-101 single-format-standard"><p>Hello</p><p>Body <strong>text</strong>.</p></body></html>`
	if name != "single.html" || b.String() != want {
		t.Errorf("got %s\n%s\nwant\n%s", name, b.String(), want)
	}

	doc := &content.Node{Type: "doc", Content: []*content.Node{{Type: "paragraph", Content: []*content.Node{{Type: "text", Text: "x"}}}}}
	h, _, err := content.Render(doc, content.RenderContext{Partials: th})
	if err != nil || h != `<div class="p">x</div>` {
		t.Errorf("partial render: %q, %v", h, err)
	}
}

func TestDevReload(t *testing.T) {
	dir := writeTheme(t, map[string]string{})
	th, err := Load(dir, Options{Dev: true, Backend: FixtureBackend(&Manifest{})})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "templates", "index.html"), []byte(`<p>changed {{.Kind}}</p>`), 0o644); err != nil {
		t.Fatal(err)
	}
	th.checked = th.checked.Add(-time.Second)
	var b strings.Builder
	if _, err := th.Page(&b, Fixture(Kind404, false)); err != nil {
		t.Fatal(err)
	}
	if b.String() != "<p>changed 404</p>" {
		t.Errorf("not reloaded: %q", b.String())
	}

	if err := os.WriteFile(filepath.Join(dir, "templates", "index.html"), []byte(`{{nope}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	th.checked = th.checked.Add(-time.Second)
	if _, err := th.Page(&strings.Builder{}, Fixture(Kind404, false)); err == nil {
		t.Errorf("want the reload error in dev mode")
	}
}

func TestAssets(t *testing.T) {
	dir := writeTheme(t, map[string]string{"assets/css/site.css": "body{}", "templates/index.html": `{{asset "css/site.css"}}`})
	th, err := Load(dir, Options{Backend: FixtureBackend(&Manifest{})})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if _, err := th.Page(&b, Fixture(Kind404, false)); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(b.String(), AssetPrefix) || !strings.HasSuffix(b.String(), "/css/site.css") {
		t.Errorf("asset url: %q", b.String())
	}
}

func TestImageFuncs(t *testing.T) {
	dir := writeTheme(t, map[string]string{
		"theme.json": `{"name":"t","image_sizes":{"large":{"width":1024,"height":1024},"thumb":{"width":150,"height":150,"crop":true}},
      "srcset_widths":[640]}`,
		"templates/single.html": `{{image .Post.FeaturedMedia "large"}}|{{image .Post.FeaturedMedia "large" "50vw"}}|` +
			`{{image_url .Post.FeaturedMedia "thumb"}}|{{image_url .Post.FeaturedMedia}}|{{image nil "large"}}`,
	})
	th, err := Load(dir, Options{Backend: FixtureBackend(&Manifest{})})
	if err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	if _, err := th.Page(&b, Fixture(KindSingle, true)); err != nil {
		t.Fatal(err)
	}
	srcset := `/m/7/ab12cd34/w640/chart.png 640w, /m/7/ab12cd34/w1024/chart.png 1024w, /m/7/ab12cd34/orig/chart.png 1600w`
	img := `<img src="/m/7/ab12cd34/w1024/chart.png" alt="Bar chart" width="1024" height="576" srcset="` + srcset + `" sizes="%s" class="attachment-large size-large">`
	want := strings.Join([]string{
		strings.Replace(img, "%s", "(max-width: 1024px) 100vw, 1024px", 1),
		strings.Replace(img, "%s", "50vw", 1),
		fixtureBase + "/m/7/ab12cd34/c150x150/chart.png",
		fixtureBase + "/m/7/ab12cd34/orig/chart.png",
		"",
	}, "|")
	if b.String() != want {
		t.Errorf("\n got %s\nwant %s", b.String(), want)
	}
}

func TestPagination(t *testing.T) {
	p := NewPagination(5, 10, func(n int) string { return "/page/" + string(rune('0'+n%10)) + "/" })
	var got []string
	for _, l := range p.Links {
		switch {
		case l.Dots:
			got = append(got, "…")
		case l.Current:
			got = append(got, "["+string(rune('0'+l.N%10))+"]")
		default:
			got = append(got, string(rune('0'+l.N%10)))
		}
	}
	if s := strings.Join(got, " "); s != "1 … 3 4 [5] 6 7 … 0" {
		t.Errorf("links: %s", s)
	}
	if p.Prev != "/page/4/" || p.Next != "/page/6/" {
		t.Errorf("prev/next: %s %s", p.Prev, p.Next)
	}

	st := &state{strings: defaultStrings}
	html := string(st.pagination(&PageData{Pagination: NewPagination(1, 2, func(n int) string { return "/p" + string(rune('0'+n)) })}))
	want := `<span aria-current="page" class="page-numbers current">1</span>` + "\n" +
		`<a class="page-numbers" href="/p2">2</a>` + "\n" + `<a class="next page-numbers" href="/p2">Next »</a>`
	if html != want {
		t.Errorf("markup:\n%s\nwant\n%s", html, want)
	}
	if st.pagination(&PageData{Pagination: NewPagination(1, 1, func(int) string { return "/" })}) != "" {
		t.Errorf("single page should render nothing")
	}
}

func TestClasses(t *testing.T) {
	d := Fixture(KindSingle, true)
	if got := PostClass(d.Post); got != "post-101 post type-post status-publish format-standard has-post-thumbnail sticky hentry category-markets category-stocks tag-example" {
		t.Errorf("post_class: %s", got)
	}
	cases := map[string]string{
		KindHome:     "home blog paged paged-2",
		KindPage:     "page-template page-template-full-width page page-id-105 page-child parent-pageid-2",
		KindCategory: "archive category category-stocks category-6 paged paged-2",
		KindSearch:   "search search-results paged paged-2",
		Kind404:      "error404",
	}
	for kind, want := range cases {
		if got := BodyClass(Fixture(kind, true)); got != want {
			t.Errorf("%s: %q, want %q", kind, got, want)
		}
	}
}

func TestExcerptAndReadingTime(t *testing.T) {
	st := &state{strings: defaultStrings}
	p := &Post{ContentText: strings.Repeat("word ", 60)}
	if got, _ := st.excerpt(p); got != strings.TrimSpace(strings.Repeat("word ", 55))+" […]" {
		t.Errorf("trimmed: %q", got)
	}
	p.HasMore, p.TeaserText = true, "Before the fold."
	if got, _ := st.excerpt(p); got != "Before the fold." {
		t.Errorf("teaser: %q", got)
	}
	p.Excerpt = "Explicit."
	if got, _ := st.excerpt(p, 3); got != "Explicit." {
		t.Errorf("explicit: %q", got)
	}
	if ReadingTime(&Post{ContentText: strings.Repeat("w ", 451)}) != 3 || ReadingTime(&Post{}) != 1 {
		t.Errorf("reading time")
	}
}

func TestMenuCurrent(t *testing.T) {
	items := FixtureBackend(&Manifest{MenuLocations: []string{"primary"}}).Menus["primary"]
	out, _ := markCurrent(items, fixtureBase+"/example-post/")
	if !out[0].CurrentAncestor || !out[0].Children[0].Current || out[1].Current {
		t.Errorf("flags: %+v %+v", out[0], out[0].Children[0])
	}
	if items[0].CurrentAncestor || items[0].Children[0].Current {
		t.Errorf("markCurrent mutated shared items")
	}
	if got := out[0].Class(); got != "menu-item menu-item-type-term menu-item-1 menu-item-has-children current-menu-ancestor current-menu-parent" {
		t.Errorf("class: %s", got)
	}
}

func TestFixtureKindsCoverHierarchy(t *testing.T) {
	for _, k := range FixtureKinds("index.html") {
		for _, name := range Candidates(Fixture(k, true)) {
			if FixtureKinds(name) == nil {
				t.Errorf("hierarchy name %s has no fixture kinds", name)
			} else if !slices.Contains(FixtureKinds(name), k) && name != "index.html" {
				t.Errorf("%s can render %s but theme check doesn't test it", name, k)
			}
		}
	}
}
