package crawl

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// wpServer imitates a small WordPress site with Yoast sitemaps and JSON-LD.
func wpServer(t *testing.T) (*httptest.Server, *[]string) {
	var mu sync.Mutex
	var hits []string
	mux := http.NewServeMux()
	var srv *httptest.Server
	page := func(title, body string) string {
		return `<html><head><title>` + title + `</title><link rel="canonical" href="` + srv.URL + `/hello-world/">` +
			`<script type="application/ld+json">{"@graph":[{"@type":"Article","headline":"` + title + `"}]}</script></head>` +
			`<body><h1>` + title + `</h1><div class="entry-content">` + body + `</div></body></html>`
	}
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits = append(hits, r.URL.Path)
		mu.Unlock()
		if ua := r.Header.Get("User-Agent"); !strings.HasPrefix(ua, "afsc-wp-test") {
			t.Errorf("missing user agent: %q", ua)
		}
		switch r.URL.Path {
		case "/robots.txt":
			fmt.Fprint(w, "User-agent: *\nDisallow: /wp-admin/\nDisallow: /private-area/\nSitemap: "+srv.URL+"/sitemap_index.xml\n")
		case "/sitemap_index.xml":
			fmt.Fprint(w, `<?xml version="1.0"?><sitemapindex><sitemap><loc>`+srv.URL+`/post-sitemap.xml</loc></sitemap></sitemapindex>`)
		case "/post-sitemap.xml":
			fmt.Fprint(w, `<?xml version="1.0"?><urlset><url><loc>`+srv.URL+`/hello-world/</loc></url></urlset>`)
		case "/feed/":
			w.Header().Set("Content-Type", "application/rss+xml")
			fmt.Fprint(w, `<rss version="2.0"><channel><title>T</title></channel></rss>`)
		case "/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page("Home", `<a href="/hello-world/">post</a> <a href="/chain/">chain</a> <a href="/off/">off</a>
				<a href="https://other.example.org/x">ext</a> <a href="/wp-admin/edit.php">admin</a> <a href="/private-area/">p</a>`))
		case "/hello-world/":
			w.Header().Set("Content-Type", "text/html; charset=UTF-8")
			fmt.Fprint(w, page("Hello", `<p>Body text here</p>`))
		case "/chain/":
			http.Redirect(w, r, "/old/", http.StatusMovedPermanently)
		case "/old/":
			http.Redirect(w, r, "/hello-world/", http.StatusFound)
		case "/off/":
			http.Redirect(w, r, "https://other.example.org/landing", http.StatusMovedPermanently)
		case "/private-area/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprint(w, page("Private", ""))
		default:
			http.NotFound(w, r)
		}
	})
	srv = httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, &hits
}

func TestCrawl(t *testing.T) {
	srv, hits := wpServer(t)
	out := t.TempDir()
	recs, err := Run(context.Background(), Options{Base: srv.URL, UserAgent: "afsc-wp-test", Delay: time.Millisecond,
		Timeout: 5 * time.Second, MaxDepth: 2, MaxURLs: 100, Selector: ".entry-content", OutDir: out, Seeds: []string{"/does-not-exist/"}})
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Record{}
	for _, r := range recs {
		by[strings.TrimPrefix(r.URL, srv.URL)] = r
	}
	if r := by["/hello-world/"]; r.Status != 200 || r.Meta == nil || r.Meta.Title != "Hello" || r.Meta.Body != "<p>Body text here</p>" || r.Meta.Headline() != "Hello" {
		t.Errorf("hello: %+v", r)
	}
	if r := by["/chain/"]; len(r.Chain) != 2 || r.Chain[0].Status != 301 || r.Chain[1].Status != 302 || r.FinalStatus != 200 {
		t.Errorf("chain: %+v", r)
	}
	if r := by["/off/"]; len(r.Chain) != 1 || r.FinalStatus != 0 || !strings.HasPrefix(r.Final, "https://other.example.org") {
		t.Errorf("off-site: %+v", r)
	}
	if r := by["/does-not-exist/"]; r.Status != 404 {
		t.Errorf("seed: %+v", r)
	}
	if _, ok := by["/private-area/"]; !ok {
		t.Errorf("non-admin disallow rules are not honoured by design")
	}
	for _, h := range *hits {
		if strings.HasPrefix(h, "/wp-admin") {
			t.Errorf("crawled an admin path: %s", h)
		}
	}
	for _, f := range []string{"urls.ndjson", "feed.xml", "robots.txt", "sitemaps/post-sitemap.xml", "sitemaps/sitemap_index.xml"} {
		if _, err := os.Stat(filepath.Join(out, f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	loaded, err := Load(out)
	if err != nil || len(loaded) != len(recs) {
		t.Fatalf("load: %v %d", err, len(loaded))
	}
}

func TestReadURLFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "top.csv")
	os.WriteFile(p, []byte("Page,Views\n/hello-world/,100\n\"https://blog.example.com/a/\",5\nnot a url,1\n"), 0o600)
	got, err := ReadURLFile(p)
	if err != nil || len(got) != 2 || got[0] != "/hello-world/" {
		t.Fatalf("got %v %v", got, err)
	}
}
