package site

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"afsc/internal/seo/sitemapcheck"
	"afsc/internal/settings"
)

var locRe = regexp.MustCompile(`<loc>([^<]+)</loc>`)

func locs(body string) []string {
	var out []string
	for _, m := range locRe.FindAllStringSubmatch(body, -1) {
		out = append(out, strings.TrimPrefix(m[1], "https://blog.example.com"))
	}
	return out
}

// TestSitemaps checks the sample bundle's sitemaps with paging forced to 2 URLs per file. With $AFSC_SITEMAP_DUMP set
// it also writes them there for scripts/check-sitemaps.sh.
func TestSitemaps(t *testing.T) {
	pool := importedPool(t)
	st := cachedSite(t, pool)
	swapSettings(t, st, func(a *settings.All) { a.SEO.Sitemaps.PageSize = 2 })
	dump := os.Getenv("AFSC_SITEMAP_DUMP")

	fetch := func(t *testing.T, path string) string {
		t.Helper()
		w := request(st, "GET", path)
		body := w.Body.String()
		if w.Code != 200 {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/xml; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", path, ct)
		}
		check := sitemapcheck.URLSet
		if path == sitemapIndexPath {
			check = sitemapcheck.Index
		}
		if err := check([]byte(body)); err != nil {
			t.Errorf("%s: %v\n%s", path, err, body)
		}
		if dump != "" {
			if err := os.WriteFile(filepath.Join(dump, "site"+strings.ReplaceAll(path, "/", "-")), []byte(body), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		return body
	}
	eq := func(t *testing.T, name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s:\n got %v\nwant %v", name, got, want)
		}
	}

	t.Run("index", func(t *testing.T) {
		body := fetch(t, sitemapIndexPath)
		eq(t, "index", locs(body), []string{"/post-sitemap.xml", "/post-sitemap2.xml", "/page-sitemap.xml", "/page-sitemap2.xml",
			"/page-sitemap3.xml", "/category-sitemap.xml", "/category-sitemap2.xml", "/author-sitemap.xml", "/video-sitemap.xml"})
		if !strings.Contains(body, "<loc>https://blog.example.com/post-sitemap.xml</loc>\n<lastmod>2021-03-21T10:00:00Z</lastmod>") {
			t.Errorf("post-sitemap.xml lastmod must be the newest updated_at on it:\n%s", body)
		}
	})

	t.Run("posts", func(t *testing.T) {
		first := fetch(t, "/post-sitemap.xml")
		eq(t, "page 1", locs(first), []string{"/example-post/", "/market-notes/"})
		eq(t, "page 2", locs(fetch(t, "/post-sitemap2.xml")), []string{"/bond-basics/", "/yearly-review/"})
		if n := strings.Count(first, "<image:image>"); n != 1 || !regexp.MustCompile(`<image:loc>https://blog\.example\.com/m/\d+/[0-9a-f]{8}/orig/chart\.png</image:loc>`).MatchString(first) {
			t.Errorf("example-post must list its featured image once (content repeats it), got %d:\n%s", n, first)
		}
		if !strings.Contains(first, "<lastmod>2021-03-05T00:00:00Z</lastmod>") {
			t.Errorf("lastmod must be updated_at:\n%s", first)
		}
		for _, p := range []string{"/post-sitemap3.xml", "/post-sitemap1.xml", "/post_tag-sitemap.xml"} {
			if w := request(st, "GET", p); w.Code != 404 {
				t.Errorf("%s: status %d, want 404", p, w.Code)
			}
		}
	})

	t.Run("pages", func(t *testing.T) {
		got := slices.Concat(locs(fetch(t, "/page-sitemap.xml")), locs(fetch(t, "/page-sitemap2.xml")), locs(fetch(t, "/page-sitemap3.xml")))
		eq(t, "pages", got, []string{"/", "/about/", "/about/team/", "/about/intro/", "/about/team/intro/", "/blog/"})
		if b := fetch(t, "/page-sitemap.xml"); !strings.Contains(b, "<loc>https://blog.example.com/</loc>\n<lastmod>2022-02-15T12:00:00Z</lastmod>") {
			t.Errorf("the home entry's lastmod must be the newest post's:\n%s", b)
		}
	})

	t.Run("archives", func(t *testing.T) {
		cats := slices.Concat(locs(fetch(t, "/category-sitemap.xml")), locs(fetch(t, "/category-sitemap2.xml")))
		slices.Sort(cats)
		eq(t, "categories", cats, []string{"/category/bonds/", "/category/bonds/stocks/", "/category/markets/", "/category/markets/stocks/"})
		authors := fetch(t, "/author-sitemap.xml")
		eq(t, "authors", locs(authors), []string{"/author/author-a/", "/author/guest-author-b/"})
		if !strings.Contains(authors, "<lastmod>2022-02-15T12:00:00Z</lastmod>") {
			t.Errorf("author lastmod must be their newest post's:\n%s", authors)
		}
	})

	t.Run("video", func(t *testing.T) {
		body := fetch(t, "/video-sitemap.xml")
		eq(t, "video", locs(body), []string{"/example-post/"})
		for _, want := range []string{
			"<video:thumbnail_loc>https://example.com/thumb.jpg</video:thumbnail_loc>",
			"<video:title>Example video</video:title>",
			"<video:player_loc>https://www.youtube.com/embed/XXXXXXXXXXX</video:player_loc>",
			"<video:duration>312</video:duration>",
			"<video:publication_date>2021-03-01T00:00:00Z</video:publication_date>",
			"<video:family_friendly>yes</video:family_friendly>",
		} {
			if !strings.Contains(body, want) {
				t.Errorf("video sitemap missing %q:\n%s", want, body)
			}
		}
		page := request(st, "GET", "/example-post/").Body.String()
		if !strings.Contains(page, `"thumbnailUrl":"https://example.com/thumb.jpg"`) {
			t.Error("the JSON-LD must use the same thumbnail URL as the video sitemap")
		}
	})

	t.Run("exclusions", func(t *testing.T) {
		swapSettings(t, st, func(a *settings.All) {
			a.SEO.Sitemaps.PageSize = 1000
			a.SEO.TagNoindex = false
			a.Permalinks.AuthorArchivesEnabled = false
		})
		st.purgeAll()
		body := fetch(t, sitemapIndexPath)
		if strings.Contains(body, "author-sitemap") || strings.Contains(body, "post_tag-sitemap") {
			t.Errorf("disabled author archives and a noindex tag must leave no sitemap:\n%s", body)
		}
		if w := request(st, "GET", "/author-sitemap.xml"); w.Code != 404 {
			t.Errorf("author sitemap with author archives off: %d", w.Code)
		}
	})

	t.Run("static front page", func(t *testing.T) {
		snap := st.snap.Load()
		swapSettings(t, st, func(a *settings.All) {
			a.SEO.Sitemaps.PageSize = 1000
			a.Reading.Home = &settings.Ref{ID: snap.pageByPath([]string{"about"}).ID}
		})
		st.purgeAll()
		eq(t, "pages", locs(fetch(t, "/page-sitemap.xml")), []string{"/", "/about/team/", "/about/intro/", "/about/team/intro/", "/blog/"})
	})

	t.Run("cache and purge", func(t *testing.T) {
		st.purgeAll()
		for i, want := range []string{"MISS", "HIT"} {
			if w := request(st, "GET", "/post-sitemap2.xml"); w.Header().Get("X-Cache") != want {
				t.Errorf("request %d: X-Cache %q, want %s", i, w.Header().Get("X-Cache"), want)
			}
		}
		request(st, "GET", sitemapIndexPath)
		st.apply(t.Context(), Event{Paths: []string{"/post-sitemap.xml"}})
		if w := request(st, "GET", "/post-sitemap2.xml"); w.Header().Get("X-Cache") != "MISS" {
			t.Error("purging the post sitemap group must drop every page of it")
		}
		if w := request(st, "GET", sitemapIndexPath); w.Header().Get("X-Cache") != "HIT" {
			t.Error("the index is its own group")
		}
		if w := request(st, "GET", "/sitemap.xml"); w.Code != 301 || w.Header().Get("Location") != sitemapIndexPath {
			t.Errorf("/sitemap.xml: %d %q", w.Code, w.Header().Get("Location"))
		}
	})
}

func TestSEOTextFiles(t *testing.T) {
	pool := importedPool(t)
	st := cachedSite(t, pool)
	text := func(t *testing.T, path string, status int) string {
		t.Helper()
		w := request(st, "GET", path)
		if w.Code != status {
			t.Fatalf("%s: status %d, want %d", path, w.Code, status)
		}
		if status == 200 && w.Header().Get("Content-Type") != "text/plain; charset=utf-8" {
			t.Errorf("%s: Content-Type %q", path, w.Header().Get("Content-Type"))
		}
		return w.Body.String()
	}

	if got := text(t, "/robots.txt", 200); got != "User-agent: *\nDisallow:\n\nSitemap: https://blog.example.com/sitemap_index.xml\n" {
		t.Errorf("default robots.txt:\n%s", got)
	}
	text(t, "/llms.txt", 404)
	text(t, "/0123abcd-ef.txt", 404)

	swapSettings(t, st, func(a *settings.All) {
		a.SEO.RobotsTxt = "User-agent: *\nDisallow: /private/\n"
		a.SEO.LLMsTxt.Mode = "auto"
		a.SEO.IndexNow = settings.IndexNow{Enabled: true, Key: "0123abcd-ef"}
	})
	st.purgeAll()
	if got := text(t, "/robots.txt", 200); got != "User-agent: *\nDisallow: /private/\n" {
		t.Errorf("custom robots.txt must be served verbatim:\n%s", got)
	}
	if got := text(t, "/0123abcd-ef.txt", 200); got != "0123abcd-ef" {
		t.Errorf("IndexNow key file = %q", got)
	}
	llms := text(t, "/llms.txt", 200)
	for _, want := range []string{
		"# Example Blog\n\n> Just another example site\n\n## Pages\n\n" +
			"- [About](https://blog.example.com/about/)\n- [Blog](https://blog.example.com/blog/)\n\n## Categories\n\n",
		"- [Markets](https://blog.example.com/category/markets/): Market news\n",
		"- [Bonds](https://blog.example.com/category/bonds/)\n",
	} {
		if !strings.Contains(llms, want) {
			t.Errorf("llms.txt missing %q:\n%s", want, llms)
		}
	}
	for _, absent := range []string{"/about/team/", "/category/markets/stocks/", "](https://blog.example.com/)"} {
		if strings.Contains(llms, absent) {
			t.Errorf("llms.txt lists only top-level pages and categories, found %q:\n%s", absent, llms)
		}
	}
	if w := request(st, "GET", "/llms.txt"); w.Header().Get("X-Cache") != "HIT" {
		t.Errorf("llms.txt is page-cached: X-Cache %q", w.Header().Get("X-Cache"))
	}

	swapSettings(t, st, func(a *settings.All) { a.SEO.LLMsTxt = settings.LLMsTxt{Mode: "custom", Body: "# Custom\n"} })
	st.purgeAll()
	if got := text(t, "/llms.txt", 200); got != "# Custom\n" {
		t.Errorf("custom llms.txt = %q", got)
	}
}
