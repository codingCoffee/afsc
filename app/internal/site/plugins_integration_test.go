package site

import (
	"context"
	"html/template"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/importer"
	"afsc/internal/plugins"
	"afsc/internal/seo"
	"afsc/internal/theme"
)

// hookPlugin registers on every render filter; one handler panics to prove a page still renders.
type hookPlugin struct{}

func (hookPlugin) ID() string { return "hooks" }

func (hookPlugin) Init(_ context.Context, h *plugins.Host) error {
	plugins.AddFilter(h, plugins.HeadTags, 0, func(_ context.Context, v template.HTML, _ *theme.PageData) (template.HTML, error) {
		return v + `<meta name="plg-head" content="1">`, nil
	})
	plugins.AddFilter(h, plugins.HeadTags, 1, func(context.Context, template.HTML, *theme.PageData) (template.HTML, error) {
		panic("broken plugin")
	})
	plugins.AddFilter(h, plugins.BodyEnd, 0, func(_ context.Context, v template.HTML, _ *theme.PageData) (template.HTML, error) {
		return v + `<div id="plg-body"></div>`, nil
	})
	plugins.AddFilter(h, plugins.ContentHTML, 0, func(_ context.Context, v template.HTML, _ *theme.PageData) (template.HTML, error) {
		return `<p id="plg-content"></p>` + v, nil
	})
	plugins.AddFilter(h, plugins.SEOMeta, 0, func(_ context.Context, m *seo.Meta, _ *theme.PageData) (*seo.Meta, error) {
		m.Tags = append(m.Tags, seo.Tag{Kind: "name", Key: "plg-meta", Value: "1"})
		return m, nil
	})
	plugins.AddFilter(h, plugins.SEOSchemaGraph, 0, func(_ context.Context, g []map[string]any, _ *theme.PageData) ([]map[string]any, error) {
		return append(g, map[string]any{"@type": "Thing", "name": "plg-graph"}), nil
	})
	plugins.AddFilter(h, plugins.FeedItemFilter, 0, func(_ context.Context, it *plugins.FeedItem, _ *theme.Post) (*plugins.FeedItem, error) {
		it.Title += " [plg]"
		return it, nil
	})
	plugins.AddFilter(h, plugins.SitemapEntries, 0, func(_ context.Context, u []seo.SitemapURL, a plugins.SitemapArgs) ([]seo.SitemapURL, error) {
		if a.Type == "page" && a.Page == 1 {
			u = append(u, seo.SitemapURL{Loc: "https://blog.example.com/plg-extra/"})
		}
		return u, nil
	})
	plugins.AddFilter(h, plugins.CSPDirectives, 0, func(_ context.Context, m map[string][]string, _ struct{}) (map[string][]string, error) {
		m["img-src"] = append(m["img-src"], "https://img.example.net", "'unsafe-inline'", "http://insecure.example.net")
		m["script-src"] = append(m["script-src"], "*")
		m["bogus-src"] = []string{"https://x.example.net"}
		return m, nil
	})
	return nil
}

func TestPluginRenderFilters(t *testing.T) {
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tm, err := theme.LoadManifest("../../themes/default")
	if err != nil {
		t.Fatal(err)
	}
	if rep, err := importer.Run(ctx, importer.Options{BundleDir: "../../testdata/bundle-sample", Theme: tm, Pool: pool,
		Storage: memStorage{}}); err != nil {
		t.Fatalf("import: %v %+v", err, rep.Errors)
	}
	rt, err := plugins.Build(ctx, plugins.Config{DB: pool, BaseURL: "https://blog.example.com", Logf: t.Logf},
		[]plugins.Plugin{hookPlugin{}})
	if err != nil {
		t.Fatal(err)
	}
	st, err := New(ctx, Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "../../themes/default", Logf: t.Logf,
		Plugins: rt})
	if err != nil {
		t.Fatal(err)
	}
	get := func(target string) (int, string, string) {
		rec := httptest.NewRecorder()
		st.ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		return rec.Code, rec.Body.String(), rec.Header().Get("Content-Security-Policy")
	}

	code, body, csp := get("/example-post/")
	if code != 200 {
		t.Fatalf("single: %d", code)
	}
	for _, want := range []string{`<meta name="plg-head" content="1">`, `<div id="plg-body"></div>`, `<p id="plg-content"></p>`,
		`<meta name="plg-meta" content="1">`, `"name":"plg-graph"`} {
		if !strings.Contains(body, want) {
			t.Errorf("single lacks %s", want)
		}
	}
	if !strings.Contains(csp, "img-src 'self' data: https://img.example.net") {
		t.Errorf("csp lacks the plugin host: %s", csp)
	}
	for _, bad := range []string{"'unsafe-inline' http", "insecure.example.net", "script-src 'self' *", "bogus-src"} {
		if strings.Contains(csp, bad) {
			t.Errorf("csp kept %q: %s", bad, csp)
		}
	}

	_, home, _ := get("/")
	if !strings.Contains(home, "plg-head") || strings.Contains(home, "plg-content") {
		t.Error("home: head filter missing, or content.html ran on a listing")
	}
	if _, feed, _ := get("/feed/"); !strings.Contains(feed, "[plg]</title>") || strings.Contains(feed, "plg-content") {
		t.Error("feed: feed.item missing, or content.html ran on the feed")
	}
	if _, sm, _ := get("/page-sitemap.xml"); !strings.Contains(sm, "<loc>https://blog.example.com/plg-extra/</loc>") {
		t.Errorf("page sitemap lacks the plugin entry: %s", sm)
	}
}

func TestPluginBackend(t *testing.T) {
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	tm, err := theme.LoadManifest("../../themes/default")
	if err != nil {
		t.Fatal(err)
	}
	if rep, err := importer.Run(ctx, importer.Options{BundleDir: "../../testdata/bundle-sample", Theme: tm, Pool: pool,
		Storage: memStorage{}}); err != nil {
		t.Fatalf("import: %v %+v", err, rep.Errors)
	}
	rt, err := plugins.Build(ctx, plugins.Config{DB: pool, BaseURL: "https://blog.example.com", Logf: t.Logf}, nil)
	if err != nil {
		t.Fatal(err)
	}
	st, err := New(ctx, Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "../../themes/default", Logf: t.Logf,
		Plugins: rt})
	if err != nil {
		t.Fatal(err)
	}
	var published, unpublished []int64
	rows, err := pool.Query(ctx, `SELECT id, status = 'published' AND published_at <= now() FROM posts ORDER BY id DESC LIMIT 50`)
	if err != nil {
		t.Fatal(err)
	}
	var id int64
	var live bool
	for rows.Next() {
		if err := rows.Scan(&id, &live); err != nil {
			t.Fatal(err)
		}
		if live {
			published = append(published, id)
		} else {
			unpublished = append(unpublished, id)
		}
	}
	rows.Close()
	if len(published) < 2 {
		t.Fatalf("the sample bundle has %d published posts", len(published))
	}
	ids := []int64{published[1], -1, published[0]}
	ids = append(ids, unpublished...)
	got, err := st.Posts(ctx, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != published[1] || got[1].ID != published[0] {
		t.Fatalf("Posts kept the wrong posts or order: %d", len(got))
	}
	for _, p := range got {
		if !strings.HasPrefix(p.URL, "https://blog.example.com/") || p.Title == "" {
			t.Errorf("post %d is not hydrated: %+v", p.ID, p)
		}
	}
	if err := st.PurgePosts(ctx, published[:2]); err != nil {
		t.Error(err)
	}
	if st.ThemeManifest().Plugins["lightbox"] == nil {
		t.Error("the default theme opts into the lightbox")
	}
}
