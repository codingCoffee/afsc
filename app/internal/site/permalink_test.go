package site

import (
	"fmt"
	"net/url"
	"testing"
	"time"

	"afsc/internal/settings"
)

func perm(t *testing.T, mod func(*settings.Permalinks)) *Permalinks {
	t.Helper()
	p := settings.Defaults().Permalinks
	if mod != nil {
		mod(&p)
	}
	loc, _ := time.LoadLocation("Asia/Kolkata")
	pl, err := NewPermalinks(p, loc)
	if err != nil {
		t.Fatal(err)
	}
	return pl
}

func TestPermalinkBuild(t *testing.T) {
	pub := time.Date(2021, 3, 31, 20, 0, 0, 0, time.UTC) // 1 April in Asia/Kolkata
	ref := &PostRef{ID: 123, Slug: "hello", PublishedAt: &pub}
	cases := []struct {
		pattern, cat, want string
	}{
		{"/{slug}/", "", "/hello/"},
		{"/{year}/{month}/{day}/{slug}/", "", "/2021/04/01/hello/"},
		{"/archives/{id}", "", "/archives/123/"},
		{"/{category}/{slug}/", "markets/stocks", "/markets/stocks/hello/"},
		{"/{category}/{slug}/", "", "/uncategorized/hello/"},
	}
	for _, c := range cases {
		p := perm(t, func(s *settings.Permalinks) { s.PostPattern = c.pattern })
		if got := p.Post(ref, c.cat); got != c.want {
			t.Errorf("%s: got %s, want %s", c.pattern, got, c.want)
		}
	}

	p := perm(t, nil)
	for got, want := range map[string]string{
		p.Page([]string{"about", "team"}):    "/about/team/",
		p.Term("category", "markets/stocks"): "/category/markets/stocks/",
		p.Term("tag", "go"):                  "/tag/go/",
		p.Author("author-a"):                 "/author/author-a/",
		p.Year(2021):                         "/2021/",
		p.Month(2021, 3):                     "/2021/03/",
		p.Feed("/"):                          "/feed/",
		p.Feed("/tag/go/"):                   "/tag/go/feed/",
		p.Paged("/", 2):                      "/page/2/",
		p.Paged("/tag/go/", 1):               "/tag/go/",
		p.Paged("/tag/go/", 3):               "/tag/go/page/3/",
	} {
		if got != want {
			t.Errorf("got %s, want %s", got, want)
		}
	}

	never := perm(t, func(s *settings.Permalinks) { s.TrailingSlash = "never" })
	for got, want := range map[string]string{
		never.Page([]string{"about"}): "/about",
		never.Feed("/about"):          "/about/feed",
		never.Paged("/about", 2):      "/about/page/2",
		never.Page(nil):               "/",
	} {
		if got != want {
			t.Errorf("never: got %s, want %s", got, want)
		}
	}
}

func TestNewPermalinksRejectsBadPatterns(t *testing.T) {
	for _, pat := range []string{"/{slug}/{slug}/", "/{nope}/", "/{slug"} {
		p := settings.Defaults().Permalinks
		p.PostPattern = pat
		if _, err := NewPermalinks(p, time.UTC); err == nil {
			t.Errorf("%s: want an error", pat)
		}
	}
}

func routeStr(rs []Route) string {
	s := ""
	for _, r := range rs {
		s += fmt.Sprintf("%s%v%s%d/%d/%d%s|p%d", r.Kind, r.Slugs, r.Slug, r.ID, r.Year, r.Month, r.Category, r.Paged)
		if r.Feed {
			s += "|feed"
		}
		s += " "
	}
	return s
}

func TestMatch(t *testing.T) {
	def := perm(t, nil)
	dated := perm(t, func(s *settings.Permalinks) { s.PostPattern = "/{year}/{month}/{slug}/" })
	cat := perm(t, func(s *settings.Permalinks) { s.PostPattern = "/{category}/{slug}/" })
	ids := perm(t, func(s *settings.Permalinks) { s.PostPattern = "/archives/{id}/" })
	cases := []struct {
		p        *Permalinks
		path     string
		want     string
		redirect string
	}{
		{def, "/", "home[]0/0/0|p1 ", ""},
		{def, "/page/2/", "home[]0/0/0|p2 ", ""},
		{def, "/page/1/", "", "/"},
		{def, "/feed/", "home[]0/0/0|p1|feed ", ""},
		{def, "/feed/rss2/", "", "/feed/"},
		{def, "/feed/atom/", "", "/feed/"},
		{def, "/tag/go/feed/atom/", "", "/tag/go/feed/"},
		{def, "/feed/page/2/", "", ""},
		{def, "/search/", "search[]0/0/0|p1 ", ""},
		{def, "/2021/", "year[]0/2021/0|p1 page[2021]0/0/0|p1 post[]20210/0/0|p1 ", ""},
		{def, "/2021/03/", "month[]0/2021/3|p1 page[2021 03]0/0/0|p1 ", ""},
		{def, "/2021/13/", "page[2021 13]0/0/0|p1 ", ""},
		{def, "/category/markets/stocks/page/3/", "category[markets stocks]0/0/0|p3 page[category markets stocks]0/0/0|p3 ", ""},
		{def, "/category/markets/feed/", "category[markets]0/0/0|p1|feed page[category markets]0/0/0|p1|feed ", ""},
		{def, "/tag/go/", "tag[]go0/0/0|p1 page[tag go]0/0/0|p1 ", ""},
		{def, "/author/author-a/", "author[]author-a0/0/0|p1 page[author author-a]0/0/0|p1 ", ""},
		{def, "/about/team/", "page[about team]0/0/0|p1 ", ""},
		{def, "/hello/", "page[hello]0/0/0|p1 post[]hello0/0/0|p1 ", ""},
		{def, "/hello/feed/", "page[hello]0/0/0|p1|feed post[]hello0/0/0|p1|feed ", ""},
		{dated, "/2021/03/hello/", "page[2021 03 hello]0/0/0|p1 post[]hello0/2021/3|p1 ", ""},
		{cat, "/markets/stocks/hello/", "page[markets stocks hello]0/0/0|p1 post[]hello0/0/0markets/stocks|p1 ", ""},
		{ids, "/archives/123/", "page[archives 123]0/0/0|p1 post[]123/0/0|p1 ", ""},
	}
	for _, c := range cases {
		got, redirect := c.p.Match(c.path)
		if s := routeStr(got); s != c.want || redirect != c.redirect {
			t.Errorf("%s:\n got %q (redirect %q)\nwant %q (redirect %q)", c.path, s, redirect, c.want, c.redirect)
		}
	}
}

func TestCanonical(t *testing.T) {
	cases := []struct{ in, policy, want string }{
		{"/", "always", "/"},
		{"/About", "always", "/about/"},
		{"/about//team", "always", "/about/team/"},
		{"/index.php", "always", "/"},
		{"/index.php/about/", "always", "/about/"},
		{"/robots.txt", "always", "/robots.txt"},
		{"/wp-content/Uploads/2020/05/Chart.png", "always", "/wp-content/Uploads/2020/05/Chart.png"},
		{"/Ünïcode-Slug/", "always", "/ünïcode-slug/"},
		{"/about/", "never", "/about"},
		{"/", "never", "/"},
	}
	for _, c := range cases {
		if got := Canonical(c.in, c.policy); got != c.want {
			t.Errorf("Canonical(%q, %s) = %q, want %q", c.in, c.policy, got, c.want)
		}
	}
}

func TestParseCompat(t *testing.T) {
	cases := []struct {
		query string
		want  compat
		ok    bool
	}{
		{"p=123", compat{kind: "post", key: "wp:post:123"}, true},
		{"page_id=2", compat{kind: "post", key: "wp:post:2"}, true},
		{"attachment_id=456", compat{kind: "media", key: "wp:post:456"}, true},
		{"cat=5", compat{kind: "term", key: "wp:term:5"}, true},
		{"author=2", compat{kind: "author", key: "wp:user:2"}, true},
		{"feed=rss2", compat{kind: "feed"}, true},
		{"s=hello+world", compat{kind: "search", query: "hello world"}, true},
		{"p=abc", compat{kind: "invalid"}, true},
		{"p=-1", compat{kind: "invalid"}, true},
		{"cat=5&feed=rss2", compat{kind: "term", key: "wp:term:5"}, true},
		{"utm_source=x", compat{}, false},
	}
	for _, c := range cases {
		q, _ := url.ParseQuery(c.query)
		got, ok := parseCompat(q)
		if got != c.want || ok != c.ok {
			t.Errorf("%s: got %+v %v, want %+v %v", c.query, got, ok, c.want, c.ok)
		}
	}
}
