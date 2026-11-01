package site

import (
	"context"
	"fmt"
	"net/http/httptest"
	"regexp"
	"slices"
	"testing"
	"time"

	"afsc/internal/settings"
	"afsc/internal/theme"
)

var (
	testNow = time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	testPub = time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)
	future  = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
)

type fakeLookup struct {
	posts []*PostRef
	exact []*Redirect
	media map[string]string
}

func (f *fakeLookup) ExactRedirect(_ context.Context, paths []string) (*Redirect, error) {
	for _, p := range paths {
		for _, r := range f.exact {
			if r.Source == p {
				return r, nil
			}
		}
	}
	return nil, nil
}

func (f *fakeLookup) Post(_ context.Context, slug string, id int64) (*PostRef, error) {
	for _, p := range f.posts {
		if p.Type == "post" && ((id > 0 && p.ID == id) || (id == 0 && p.Slug == slug)) {
			return p, nil
		}
	}
	return nil, nil
}

func (f *fakeLookup) LegacyPost(_ context.Context, key string) (*PostRef, error) {
	for _, p := range f.posts {
		if p.LegacyKey == key {
			return p, nil
		}
	}
	return nil, nil
}

func (f *fakeLookup) LegacyMedia(_ context.Context, key string) (string, error) {
	return f.media[key], nil
}

func testSite(t *testing.T, mod func(*settings.All)) *Site {
	t.Helper()
	s := newSnapshot("https://blog.example.com")
	all := settings.Defaults()
	if mod != nil {
		mod(&all)
	}
	if err := s.setSettings(all); err != nil {
		t.Fatal(err)
	}
	page := func(id, parent int64, slug, status string) *PostRef {
		s.pages[id] = &pageRow{PostRef: PostRef{ID: id, LegacyKey: fmt.Sprintf("wp:post:%d", id), Type: "page", Slug: slug,
			Status: status, PublishedAt: &testPub, ParentID: parent}, Title: slug}
		return &s.pages[id].PostRef
	}
	pages := []*PostRef{
		page(2, 0, "about", "published"), page(3, 2, "team", "published"), page(5, 2, "intro", "published"),
		page(6, 3, "intro", "published"), page(4, 0, "blog", "published"), page(7, 0, "secret", "draft"),
	}
	term := func(id, parent int64, tax, slug string) {
		s.terms[id] = &termRow{ID: id, ParentID: parent, LegacyKey: fmt.Sprintf("wp:term:%d", id), Taxonomy: tax, Slug: slug, Name: slug}
	}
	term(5, 0, "category", "markets")
	term(6, 5, "category", "stocks")
	term(9, 0, "category", "bonds")
	term(10, 9, "category", "stocks")
	term(8, 0, "tag", "example")
	s.authors[2] = &authorRow{ID: 2, LegacyKey: "wp:user:2", Slug: "author-a", Name: "Author A"}
	s.index()
	s.prefix = []*Redirect{{ID: 2, Source: "/archive/2015/", Match: "prefix", Target: "/", Status: 302}}
	s.regex = []*Redirect{{ID: 3, Source: `^/topics/([a-z-]+)/?$`, Match: "regex", Target: "/category/$1/", Status: 301,
		re: regexp.MustCompile(`^/topics/([a-z-]+)/?$`)}}

	posts := []*PostRef{
		{ID: 123, LegacyKey: "wp:post:123", Type: "post", Slug: "example-post", Status: "published", PublishedAt: &testPub, CategoryID: 6},
		{ID: 124, LegacyKey: "wp:post:124", Type: "post", Slug: "draft-post", Status: "draft"},
		{ID: 125, LegacyKey: "wp:post:125", Type: "post", Slug: "scheduled-post", Status: "scheduled", PublishedAt: &future},
		{ID: 126, LegacyKey: "wp:post:126", Type: "post", Slug: "future-post", Status: "published", PublishedAt: &future},
		{ID: 127, LegacyKey: "wp:post:127", Type: "post", Slug: "private-post", Status: "private", PublishedAt: &testPub},
	}
	lk := &fakeLookup{
		posts: slices.Concat(posts, pages),
		exact: []*Redirect{
			{ID: 1, Source: "/old-path/", Match: "exact", Target: "/example-post/", Status: 301},
			{ID: 4, Source: "/deleted-post/", Match: "exact", Status: 410},
			{ID: 5, Source: "/Legacy.html", Match: "exact", Target: "/about/", Status: 308},
		},
		media: map[string]string{"wp:post:456": "/m/1/ab12cd34/orig/chart.png"},
	}
	st := &Site{cfg: Config{BaseURL: "https://blog.example.com"}, lk: lk, now: func() time.Time { return testNow }}
	st.snap.Store(s)
	return st
}

func resolveStr(t *testing.T, st *Site, target string) string {
	t.Helper()
	res, err := st.resolve(context.Background(), st.snap.Load(), httptest.NewRequest("GET", target, nil))
	if err != nil {
		t.Fatalf("%s: %v", target, err)
	}
	if res.Location != "" {
		return fmt.Sprintf("%d %s", res.Status, res.Location)
	}
	s := fmt.Sprintf("%d %s", res.Status, res.Kind)
	if res.ID != 0 {
		s += fmt.Sprintf(" id=%d", res.ID)
	}
	if res.FeedOf != "" {
		s += " of=" + res.FeedOf
	}
	if res.Paged > 1 {
		s += fmt.Sprintf(" page=%d", res.Paged)
	}
	if res.Year != 0 {
		s += fmt.Sprintf(" date=%d/%d", res.Year, res.Month)
	}
	if res.Query != "" {
		s += " q=" + res.Query
	}
	if res.File != "" {
		s += " file=" + res.File
	}
	return s
}

func TestResolve(t *testing.T) {
	type tc struct{ target, want string }
	run := func(t *testing.T, st *Site, cases []tc) {
		t.Helper()
		for _, c := range cases {
			if got := resolveStr(t, st, c.target); got != c.want {
				t.Errorf("%s:\n got %s\nwant %s", c.target, got, c.want)
			}
		}
	}

	t.Run("defaults", func(t *testing.T) {
		run(t, testSite(t, nil), []tc{
			{"/", "200 home"},
			{"/page/2/", "200 home page=2"},
			{"/page/1/", "301 /"},
			{"/feed/", "200 feed of=home"},
			{"/feed/rss2/", "301 /feed/"},
			{"/example-post/", "200 single id=123"},
			{"/example-post/feed/", "301 /example-post/"},
			{"/example-post/page/2/", "404 404"},
			{"/Example-Post", "301 /example-post/"},
			{"/example-post", "301 /example-post/"},
			{"/example-post/?utm_source=x", "200 single id=123"},
			{"/Example-Post?utm_source=x", "301 /example-post/?utm_source=x"},
			{"/index.php/example-post/", "301 /example-post/"},
			{"/about//team/", "301 /about/team/"},
			{"/draft-post/", "404 404"},
			{"/scheduled-post/", "404 404"},
			{"/future-post/", "404 404"},
			{"/private-post/", "404 404"},
			{"/about/", "200 page id=2"},
			{"/about/team/", "200 page id=3"},
			{"/about/intro/", "200 page id=5"},
			{"/about/team/intro/", "200 page id=6"},
			{"/about/feed/", "301 /about/"},
			{"/about/page/2/", "404 404"},
			{"/secret/", "404 404"},
			{"/blog/", "200 page id=4"},
			{"/category/markets/", "200 category id=5"},
			{"/category/markets/stocks/", "200 category id=6"},
			{"/category/bonds/stocks/", "200 category id=10"},
			{"/category/stocks/", "404 404"},
			{"/category/markets/feed/", "200 feed id=5 of=category"},
			{"/category/markets/page/2/", "200 category id=5 page=2"},
			{"/tag/example/", "200 tag id=8"},
			{"/tag/example/feed/", "200 feed id=8 of=tag"},
			{"/author/author-a/", "200 author id=2"},
			{"/author/author-a/feed/", "200 feed id=2 of=author"},
			{"/author/nobody/", "404 404"},
			{"/2021/", "200 date date=2021/0"},
			{"/2021/03/", "200 date date=2021/3"},
			{"/2021/03/page/2/", "200 date page=2 date=2021/3"},
			{"/search/?q=bonds", "200 search q=bonds"},
			{"/search/?q=bonds&page=3", "200 search page=3 q=bonds"},
			{"/old-path/", "301 /example-post/"},
			{"/deleted-post/", "410 410"},
			{"/Legacy.html", "308 /about/"},
			{"/archive/2015/anything/", "302 /"},
			{"/archive/2015/anything", "301 /archive/2015/anything/"},
			{"/topics/markets/", "301 /category/markets/"},
			{"/nope/", "404 404"},
			{"/admin/anything", "404 404"},
		})
	})

	t.Run("wpcompat", func(t *testing.T) {
		run(t, testSite(t, nil), []tc{
			{"/?p=123", "301 /example-post/"},
			{"/index.php?p=123", "301 /example-post/"},
			{"/?page_id=3", "301 /about/team/"},
			{"/?p=3", "301 /about/team/"},
			{"/?p=124", "404 404"},
			{"/?p=999", "404 404"},
			{"/?p=abc", "404 404"},
			{"/?cat=6", "301 /category/markets/stocks/"},
			{"/?cat=8", "301 /tag/example/"},
			{"/?author=2", "301 /author/author-a/"},
			{"/?attachment_id=456", "301 /m/1/ab12cd34/orig/chart.png"},
			{"/?feed=rss2", "301 /feed/"},
			{"/?s=bond+basics", "301 /search/?q=bond+basics"},
			{"/?utm_source=x", "200 home"},
			{"/about/?p=123", "200 page id=2"},
		})
		run(t, testSite(t, func(a *settings.All) { a.Permalinks.WPCompatEnabled = false }), []tc{
			{"/?p=123", "200 home"},
		})
	})

	t.Run("static front page", func(t *testing.T) {
		run(t, testSite(t, func(a *settings.All) {
			a.Reading.Home, a.Reading.PostsPage = &settings.Ref{ID: 2}, &settings.Ref{ID: 4}
		}), []tc{
			{"/", "200 front_page id=2"},
			{"/page/2/", "404 404"},
			{"/about/", "301 /"},
			{"/about/team/", "200 page id=3"},
			{"/blog/", "200 posts_page id=4"},
			{"/blog/page/2/", "200 posts_page id=4 page=2"},
			{"/feed/", "200 feed of=home"},
			{"/?page_id=2", "301 /"},
		})
	})

	t.Run("post patterns", func(t *testing.T) {
		run(t, testSite(t, func(a *settings.All) { a.Permalinks.PostPattern = "/{year}/{month}/{slug}/" }), []tc{
			{"/2021/03/example-post/", "200 single id=123"},
			{"/2020/01/example-post/", "301 /2021/03/example-post/"},
			{"/example-post/", "404 404"},
			{"/?p=123", "301 /2021/03/example-post/"},
			{"/2021/03/", "200 date date=2021/3"},
		})
		run(t, testSite(t, func(a *settings.All) { a.Permalinks.PostPattern = "/{category}/{slug}/" }), []tc{
			{"/markets/stocks/example-post/", "200 single id=123"},
			{"/bonds/example-post/", "301 /markets/stocks/example-post/"},
		})
		run(t, testSite(t, func(a *settings.All) { a.Permalinks.PostPattern = "/archives/{id}/" }), []tc{
			{"/archives/123/", "200 single id=123"},
			{"/archives/124/", "404 404"},
		})
	})

	t.Run("seo files", func(t *testing.T) {
		seoFiles := []tc{
			{"/sitemap.xml", "301 /sitemap_index.xml"},
			{"/sitemap_index.xml", "200 sitemap file=index"},
			{"/post-sitemap.xml", "200 sitemap file=post"},
			{"/post-sitemap2.xml", "200 sitemap page=2 file=post"},
			{"/page-sitemap.xml", "200 sitemap file=page"},
			{"/category-sitemap.xml", "200 sitemap file=category"},
			{"/post_tag-sitemap12.xml", "200 sitemap page=12 file=post_tag"},
			{"/author-sitemap.xml", "200 sitemap file=author"},
			{"/video-sitemap.xml", "200 sitemap file=video"},
			{"/robots.txt", "200 text file=/robots.txt"},
			{"/post-sitemap1.xml", "404 404"},
			{"/post-sitemap0.xml", "404 404"},
			{"/post-sitemap01.xml", "404 404"},
			{"/tag-sitemap.xml", "404 404"},
			{"/Sitemap_Index.xml", "404 404"},
			{"/sitemap_index.xml/", "404 404"},
			{"/llms.txt", "404 404"},
			{"/0123abcd-ef.txt", "404 404"},
		}
		run(t, testSite(t, nil), seoFiles)
		// sitemap paths win over redirects and the trailing-slash policy
		st := testSite(t, func(a *settings.All) { a.Permalinks.TrailingSlash = "never" })
		st.lk.(*fakeLookup).exact = append(st.lk.(*fakeLookup).exact,
			&Redirect{ID: 9, Source: "/robots.txt", Match: "exact", Target: "/about/", Status: 301})
		run(t, st, seoFiles[:10])
		run(t, testSite(t, func(a *settings.All) {
			a.SEO.LLMsTxt.Mode = "auto"
			a.SEO.IndexNow = settings.IndexNow{Enabled: true, Key: "0123abcd-ef"}
		}), []tc{
			{"/llms.txt", "200 text file=/llms.txt"},
			{"/0123abcd-ef.txt", "200 text file=/0123abcd-ef.txt"},
			{"/0123ABCD-EF.txt", "404 404"},
		})
		run(t, testSite(t, func(a *settings.All) { a.SEO.IndexNow = settings.IndexNow{Key: "0123abcd-ef"} }), []tc{
			{"/0123abcd-ef.txt", "404 404"},
		})
	})

	t.Run("toggles and trailing slash", func(t *testing.T) {
		run(t, testSite(t, func(a *settings.All) {
			a.Permalinks.DateArchivesEnabled, a.Permalinks.AuthorArchivesEnabled = false, false
		}), []tc{
			{"/2021/", "301 /"},
			{"/author/author-a/", "301 /"},
			{"/?author=2", "301 /"},
		})
		run(t, testSite(t, func(a *settings.All) { a.Permalinks.TrailingSlash = "never" }), []tc{
			{"/about/", "301 /about"},
			{"/about", "200 page id=2"},
			{"/example-post", "200 single id=123"},
			{"/feed", "200 feed of=home"},
			{"/page/2", "200 home page=2"},
			{"/?s=x", "301 /search?q=x"},
		})
	})
}

func TestNotFoundLogAggregates(t *testing.T) {
	l := newNotFoundLog()
	l.add("/a/", "", testNow)
	l.add("/a/", "https://ref.example.com/", testNow.Add(time.Minute))
	l.add("/b/\x00", "", testNow)
	l.add("/"+string(make([]byte, nfMaxPath)), "", testNow)
	got := l.take()
	if len(got) != 2 || got["/a/"].hits != 2 || got["/a/"].ref != "https://ref.example.com/" || got["/b/"] == nil {
		t.Fatalf("unexpected batch: %+v", got)
	}
	if len(l.take()) != 0 {
		t.Fatalf("take should empty the log")
	}
	for i := 0; i < nfMaxPending+5; i++ {
		l.add(fmt.Sprintf("/p%d/", i), "", testNow)
	}
	if n := len(l.take()); n != nfMaxPending {
		t.Fatalf("pending not capped: %d", n)
	}
}

func TestFeedHelpers(t *testing.T) {
	if got := cdata("a]]>b\x01c"); got != "<![CDATA[a]]]]><![CDATA[>bc]]>" {
		t.Errorf("cdata: %s", got)
	}
	if got := xmlText(`<a & "b">`); got != "&lt;a &amp; &#34;b&#34;&gt;" {
		t.Errorf("xmlText: %s", got)
	}
	p := &theme.Post{ContentText: "one two"}
	if feedExcerpt(p) != "one two" {
		t.Errorf("short excerpt")
	}
	if rssDate(testPub) != "Thu, 04 Mar 2021 05:06:07 +0000" {
		t.Errorf("rss date: %s", rssDate(testPub))
	}
}
