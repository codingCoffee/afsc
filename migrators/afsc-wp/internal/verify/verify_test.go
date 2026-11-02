package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"afsc/migrators/afsc-wp/internal/crawl"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/htmlmeta"
	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/transform"
)

func pngOf(w, h int, c color.Color) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, c)
		}
	}
	var b bytes.Buffer
	png.Encode(&b, img)
	return b.Bytes()
}

const head = `<title>Hello | Site</title><meta name="description" content="D"><meta name="robots" content="index, follow">` +
	`<link rel="canonical" href="%s/hello/"><meta property="og:title" content="Hello">` +
	`<script type="application/ld+json">{"@graph":[{"@type":"WebPage"},{"@type":"BlogPosting","headline":"Hello"}]}</script>`

// afscServer imitates an imported AFSC: pages, redirects, legacy upload paths, feed and sitemaps.
func afscServer(t *testing.T, orig []byte, sha string) *httptest.Server {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/hello/":
			w.Header().Set("Content-Type", "text/html")
			fmt.Fprintf(w, `<html><head>`+head+`</head><body><h1>Hello</h1><div class="entry-content"><p>Hi “there”.</p>`+
				`<img class="wp-image-5" src="/m/1/%s/w300/a.png"></div></body></html>`, srv.URL, sha[:8])
		case "/old/":
			http.Redirect(w, r, "/hello/", http.StatusMovedPermanently)
		case "/chained/":
			http.Redirect(w, r, "/old/", http.StatusMovedPermanently)
		case "/wp-content/uploads/2021/03/a.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(orig)
		case "/wp-content/uploads/2021/03/a-2x2.png":
			w.Header().Set("Content-Type", "image/png")
			w.Write(pngOf(2, 2, color.White))
		case "/m/1/" + sha[:8] + "/w300/a.png":
			w.Header().Set("Content-Type", "image/png")
		case "/feed/":
			fmt.Fprintf(w, `<rss version="2.0"><channel><title>S</title><link>%[1]s/</link><description>d</description>`+
				`<item><title>Hello</title><link>%[1]s/hello/</link><guid>https://blog.example.com/?p=5</guid></item></channel></rss>`, srv.URL)
		case "/sitemap_index.xml":
			fmt.Fprintf(w, `<sitemapindex><sitemap><loc>%s/post-sitemap.xml</loc></sitemap></sitemapindex>`, srv.URL)
		case "/post-sitemap.xml":
			fmt.Fprintf(w, `<urlset><url><loc>%s/hello/</loc></url></urlset>`, srv.URL)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func setup(t *testing.T) (Options, *httptest.Server) {
	dir := t.TempDir()
	orig := pngOf(4, 4, color.Black)
	h := sha256.Sum256(orig)
	sha := hex.EncodeToString(h[:])
	srv := afscServer(t, orig, sha)
	o := Options{Source: "wp", Target: srv.URL, WPBase: "https://blog.example.com", Hosts: map[string]bool{"blog.example.com": true},
		ContentSelector: ".entry-content", WorkDir: filepath.Join(dir, "work"), BaselineDir: filepath.Join(dir, "baseline"),
		ReportsDir: filepath.Join(dir, "reports"), BundleDir: filepath.Join(dir, "bundle"), Threshold: 0.005, Timeout: 5 * time.Second}
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(extract.WriteNDJSON(filepath.Join(o.WorkDir, "url-map.ndjson"), []transform.URLExpect{
		{URL: "https://blog.example.com/hello/", Path: "/hello/", Expect: "200"},
		{URL: "https://blog.example.com/old/", Path: "/old/", Expect: "301", Target: "/hello/"},
		{URL: "https://blog.example.com/chained/", Path: "/chained/", Expect: "301", Target: "/old/"},
		{URL: "https://blog.example.com/mystery/", Path: "/mystery/", Expect: "unknown"},
	}))
	wpMeta, _ := htmlmeta.Parse([]byte(`<html><head>`+fmt.Sprintf(head, "https://blog.example.com")+`</head><body><h1>Hello</h1>`+
		`<div class="entry-content"><p>Hi “there”.</p><p><img class="alignnone wp-image-5" src="https://blog.example.com/wp-content/uploads/2021/03/a-2x2.png"></p></div></body></html>`), ".entry-content")
	must(extract.WriteNDJSON(filepath.Join(o.BaselineDir, "urls.ndjson"), []crawl.Record{{URL: "https://blog.example.com/hello/", Status: 200, Meta: wpMeta}}))
	must(os.WriteFile(filepath.Join(o.BaselineDir, "feed.xml"), []byte(`<rss version="2.0"><channel><item><link>https://blog.example.com/hello/</link><guid>https://blog.example.com/?p=5</guid></item></channel></rss>`), 0o600))
	must(os.MkdirAll(filepath.Join(o.BaselineDir, "sitemaps"), 0o700))
	must(os.WriteFile(filepath.Join(o.BaselineDir, "sitemaps", "post-sitemap.xml"), []byte(`<urlset><url><loc>https://blog.example.com/hello/</loc></url><url><loc>https://blog.example.com/old/</loc></url></urlset>`), 0o600))
	must(extract.WriteNDJSON(filepath.Join(o.BundleDir, "media.ndjson"), []*records.Media{{LegacyKey: "wp:post:5", SHA256: sha, Filename: "a.png", Mime: "image/png",
		LegacyPaths: []records.LegacyPath{{Path: "/wp-content/uploads/2021/03/a.png", Variant: "orig"},
			{Path: "/wp-content/uploads/2021/03/a-2x2.png", Variant: "resize", Width: records.Ptr(2), Height: records.Ptr(2)}}}}))
	must(extract.WriteNDJSON(filepath.Join(o.WorkDir, "paths.ndjson"), []transform.PathRec{{Key: "wp:post:5", Type: "post", Status: "published", WPPath: "/hello/", AFSCPath: "/hello/"}}))
	must(extract.WriteNDJSON(filepath.Join(o.WorkDir, "filtered", "reference.ndjson"), []transform.FilteredPost{{ID: 5, HTML: "<p>unused</p>"}}))
	must(extract.WriteJSON(filepath.Join(o.ReportsDir, "transform.json"), transform.Report{Counts: []transform.CountRow{{Entity: "posts (x)", Source: 2, Bundle: 1, Skipped: map[string]int{"status draft": 1}}}}))
	return o, srv
}

func TestVerify(t *testing.T) {
	o, _ := setup(t)
	imp := filepath.Join(t.TempDir(), "import.json")
	os.WriteFile(imp, []byte(`{"counts":{"posts":{"created":1}}}`), 0o600)
	o.ImportReport = imp
	res, err := Run(context.Background(), o, []string{"urls", "seo", "content", "counts", "media", "feeds", "sitemaps"})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]Result{}
	for _, r := range res {
		got[r.Check] = r
	}
	want := map[string]bool{"urls": false, "seo": true, "content": true, "counts": true, "media": true, "feeds": true, "sitemaps": true}
	for k, v := range want {
		if got[k].Pass != v {
			b, _ := os.ReadFile(filepath.Join(o.ReportsDir, map[string]string{"urls": "url-parity.md", "seo": "seo-diff.md", "content": "content-fidelity.md",
				"counts": "counts.md", "media": "media.md", "feeds": "feeds.md", "sitemaps": "sitemaps.md"}[k]))
			t.Errorf("%s: pass=%v want %v (%s)\n%s", k, got[k].Pass, v, got[k].Summary, b)
		}
	}
	b, _ := os.ReadFile(filepath.Join(o.ReportsDir, "url-parity.md"))
	for _, s := range []string{"/chained/", "redirect chain", "/mystery/", "no expected outcome"} {
		if !strings.Contains(string(b), s) {
			t.Errorf("url-parity.md lacks %q:\n%s", s, b)
		}
	}
	if strings.Contains(string(b), "| /old/") || strings.Contains(string(b), "| /hello/") {
		t.Errorf("good URLs reported as mismatches:\n%s", b)
	}
}

func TestVisual(t *testing.T) {
	o, _ := setup(t)
	shots := t.TempDir()
	os.MkdirAll(filepath.Join(shots, "wp"), 0o700)
	os.MkdirAll(filepath.Join(shots, "afsc"), 0o700)
	os.WriteFile(filepath.Join(shots, "wp", "home-1440.png"), pngOf(10, 10, color.White), 0o600)
	os.WriteFile(filepath.Join(shots, "afsc", "home-1440.png"), pngOf(10, 10, color.White), 0o600)
	os.WriteFile(filepath.Join(shots, "wp", "post-1440.png"), pngOf(10, 10, color.White), 0o600)
	os.WriteFile(filepath.Join(shots, "afsc", "post-1440.png"), pngOf(10, 12, color.White), 0o600)
	o.ScreenshotsDir = shots
	res, err := Run(context.Background(), o, []string{"visual"})
	if err != nil {
		t.Fatal(err)
	}
	if res[0].Pass {
		t.Fatal("a taller page must fail the threshold")
	}
	for _, f := range []string{"index.html", "visual.md", "sample-urls.txt", "home-1440-diff.png"} {
		if _, err := os.Stat(filepath.Join(o.ReportsDir, "visual", f)); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	r, _, _ := Diff(image.NewRGBA(image.Rect(0, 0, 2, 2)), image.NewRGBA(image.Rect(0, 0, 2, 2)))
	if r != 0 {
		t.Errorf("identical images differ: %v", r)
	}
}
