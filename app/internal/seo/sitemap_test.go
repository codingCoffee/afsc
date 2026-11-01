package seo_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"afsc/internal/seo"
	"afsc/internal/seo/sitemapcheck"
)

// dumpSitemap writes b to $AFSC_SITEMAP_DUMP for scripts/check-sitemaps.sh, which runs xmllint on it.
func dumpSitemap(t *testing.T, name string, b []byte) {
	t.Helper()
	if dir := os.Getenv("AFSC_SITEMAP_DUMP"); dir != "" {
		if err := os.WriteFile(filepath.Join(dir, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSitemapWriters(t *testing.T) {
	up := time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
	yes := true
	long := strings.Repeat("word ", 30)
	videos := []seo.Video{
		{Name: "Clip <1> & more", Description: "A clip.", ThumbnailURL: "https://blog.example.com/m/1/ab/orig/t.jpg",
			EmbedURL: "https://www.youtube-nocookie.com/embed/abc", Duration: 312, UploadDate: &up, FamilyFriendly: &yes},
		{Name: long, Description: strings.Repeat("d", 3000), ThumbnailURL: "https://example.com/t.jpg",
			ContentURL: "https://example.com/v.mp4", Duration: 99999},
		{Name: "No thumbnail", Description: "x", ContentURL: "https://example.com/v.mp4"},
		{Name: "No description", ThumbnailURL: "https://example.com/t.jpg", ContentURL: "https://example.com/v.mp4"},
		{Name: "  ", Description: "blank title", ThumbnailURL: "https://example.com/t.jpg", ContentURL: "https://example.com/v.mp4"},
		{Name: "No player", Description: "x", ThumbnailURL: "https://example.com/t.jpg"},
	}
	images := make([]string, 1005)
	for i := range images {
		images[i] = "https://blog.example.com/m/7/ab12cd34/orig/chart.png"
	}
	urls := []seo.SitemapURL{
		{Loc: "https://blog.example.com/a/?x=1&y=2", LastMod: time.Date(2021, 3, 5, 0, 0, 0, 0, time.FixedZone("IST", 19800)),
			Images: images, Videos: videos},
		{Loc: "https://blog.example.com/caf%C3%A9/"},
	}
	set := seo.URLSet(urls)
	dumpSitemap(t, "writer-urlset.xml", set)
	if err := sitemapcheck.URLSet(set); err != nil {
		t.Fatalf("urlset: %v\n%s", err, set)
	}
	s := string(set)
	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<loc>https://blog.example.com/a/?x=1&amp;y=2</loc>`,
		`<lastmod>2021-03-04T18:30:00Z</lastmod>`,
		`<video:title>Clip &lt;1&gt; &amp; more</video:title>`,
		`<video:player_loc>https://www.youtube-nocookie.com/embed/abc</video:player_loc>`,
		`<video:duration>312</video:duration>`,
		`<video:publication_date>2021-03-01T00:00:00Z</video:publication_date>`,
		`<video:family_friendly>yes</video:family_friendly>`,
		`<video:content_loc>https://example.com/v.mp4</video:content_loc>`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("urlset missing %q", want)
		}
	}
	if n := strings.Count(s, "<image:image>"); n != 1000 {
		t.Errorf("%d images, want the first 1000", n)
	}
	if n := strings.Count(s, "<video:video>"); n != 2 {
		t.Errorf("%d videos, want 2: entries without a thumbnail, title, description or player are dropped", n)
	}
	if strings.Contains(s, "99999") {
		t.Error("a duration over 28800 must be left out")
	}

	idx := seo.Index([]seo.SitemapRef{{Loc: "https://blog.example.com/post-sitemap.xml", LastMod: up},
		{Loc: "https://blog.example.com/page-sitemap.xml"}})
	dumpSitemap(t, "writer-sitemap_index.xml", idx)
	if err := sitemapcheck.Index(idx); err != nil {
		t.Fatalf("index: %v\n%s", err, idx)
	}
}

func TestSitemapCheckRejects(t *testing.T) {
	head := `<?xml version="1.0" encoding="UTF-8"?><urlset xmlns="` + seo.NSSitemap + `" xmlns:image="` + seo.NSImage +
		`" xmlns:video="` + seo.NSVideo + `">`
	url := func(inner string) string { return head + `<url><loc>https://blog.example.com/</loc>` + inner + `</url></urlset>` }
	video := func(inner string) string { return url(`<video:video>` + inner + `</video:video>`) }
	cases := []struct{ name, doc string }{
		{"empty urlset", head + `</urlset>`},
		{"relative loc", head + `<url><loc>/relative/path/</loc></url></urlset>`},
		{"lastmod before loc", head + `<url><lastmod>2021-01-01</lastmod><loc>https://blog.example.com/</loc></url></urlset>`},
		{"bad lastmod", url(`<lastmod>yesterday</lastmod>`)},
		{"wrong namespace", `<urlset xmlns="http://example.com/"><url><loc>https://blog.example.com/</loc></url></urlset>`},
		{"image without loc", url(`<image:image></image:image>`)},
		{"video out of order", video(`<video:title>t</video:title><video:thumbnail_loc>https://e.com/t.jpg</video:thumbnail_loc>` +
			`<video:description>d</video:description><video:content_loc>https://e.com/v.mp4</video:content_loc>`)},
		{"video without player", video(`<video:thumbnail_loc>https://e.com/t.jpg</video:thumbnail_loc><video:title>t</video:title>` +
			`<video:description>d</video:description>`)},
		{"unknown element", url(`<priority>0.5</priority>`)},
	}
	for _, c := range cases {
		if err := sitemapcheck.URLSet([]byte(c.doc)); err == nil {
			t.Errorf("%s: accepted", c.name)
		}
	}
	if err := sitemapcheck.Index([]byte(head + `<url><loc>https://blog.example.com/</loc></url></urlset>`)); err == nil {
		t.Error("index check accepted a urlset")
	}
}
