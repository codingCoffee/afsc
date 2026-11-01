package site

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/importer"
	"afsc/internal/pagecache"
	"afsc/internal/settings"
	"afsc/internal/theme"
)

// importedPool connects to AFSC_TEST_DATABASE_URL and imports the sample bundle. Run DB tests with -p 1.
func importedPool(t testing.TB) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tm, err := theme.LoadManifest("../../themes/default")
	if err != nil {
		t.Fatal(err)
	}
	if rep, err := importer.Run(ctx, importer.Options{BundleDir: "../../testdata/bundle-sample", Theme: tm, Pool: pool,
		Storage: memStorage{}}); err != nil {
		t.Fatalf("import: %v %+v", err, rep.Errors)
	}
	return pool
}

func cachedSite(t testing.TB, pool *pgxpool.Pool) *Site {
	t.Helper()
	st, err := New(context.Background(), Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "../../themes/default",
		Logf: t.Logf, CacheBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	return st
}

func request(st *Site, method, target string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, target, nil)
	for _, m := range mod {
		m(r)
	}
	w := httptest.NewRecorder()
	st.ServeHTTP(w, r)
	return w
}

func TestPageCache(t *testing.T) {
	pool := importedPool(t)
	st := cachedSite(t, pool)
	get := func(target string, mod ...func(*http.Request)) *httptest.ResponseRecorder {
		return request(st, "GET", target, mod...)
	}
	public := pagecache.CacheControl(60)

	t.Run("hit, etag and 304", func(t *testing.T) {
		first := get("/example-post/")
		second := get("/example-post/")
		for i, w := range []*httptest.ResponseRecorder{first, second} {
			want := []string{"MISS", "HIT"}[i]
			if w.Code != 200 || w.Header().Get("X-Cache") != want || w.Header().Get("Cache-Control") != public ||
				w.Header().Get("ETag") == "" || !strings.Contains(w.Body.String(), "postid-123") {
				t.Fatalf("request %d: %d %v", i+1, w.Code, w.Header())
			}
		}
		etag := first.Header().Get("ETag")
		if second.Header().Get("ETag") != etag || second.Body.String() != first.Body.String() {
			t.Error("cached response differs from the rendered one")
		}
		nm := get("/example-post/", func(r *http.Request) { r.Header.Set("If-None-Match", etag) })
		if nm.Code != http.StatusNotModified || nm.Body.Len() != 0 || nm.Header().Get("ETag") != etag ||
			nm.Header().Get("Cache-Control") != public {
			t.Errorf("If-None-Match: %d %v", nm.Code, nm.Header())
		}
		head := request(st, "HEAD", "/example-post/")
		if head.Code != 200 || head.Body.Len() != 0 || head.Header().Get("X-Cache") != "HIT" || head.Header().Get("Content-Length") == "" {
			t.Errorf("HEAD: %d %v", head.Code, head.Header())
		}
	})

	t.Run("every cacheable kind", func(t *testing.T) {
		for _, p := range []string{"/", "/about/", "/category/markets/", "/tag/example/", "/author/author-a/", "/2021/", "/2021/03/", "/feed/",
			"/category/markets/feed/"} {
			get(p)
			if w := get(p); w.Code != 200 || w.Header().Get("X-Cache") != "HIT" {
				t.Errorf("%s: %d %q", p, w.Code, w.Header().Get("X-Cache"))
			}
		}
		if ct := get("/feed/").Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/rss+xml") {
			t.Errorf("cached feed Content-Type %q", ct)
		}
	})

	t.Run("tracking params share the entry and never reach the page", func(t *testing.T) {
		st.purgeAll()
		get("/about/?utm_source=first-visitor")
		w := get("/about/?utm_source=other")
		if w.Header().Get("X-Cache") != "HIT" || strings.Contains(w.Body.String(), "first-visitor") {
			t.Errorf("utm variant: %q", w.Header().Get("X-Cache"))
		}
	})

	t.Run("never cached", func(t *testing.T) {
		st.purgeAll()
		home := get("/").Body.String()
		asset := regexp.MustCompile(`href="(/_theme/[0-9a-f]+/style\.css)"`).FindStringSubmatch(home)
		if asset == nil {
			t.Fatal("no stylesheet link")
		}
		before, _ := st.cache.Stats()
		cases := []struct {
			target string
			mod    func(*http.Request)
			cc     string
		}{
			{"/search/?q=bonds", nil, "private, no-store"},
			{"/nope/", nil, public},
			{"/deleted-post/", nil, public},
			{"/old-path/", nil, public},
			{"/Example-Post", nil, public},
			{"/?p=123", nil, ""},
			{"/about/", func(r *http.Request) { r.AddCookie(&http.Cookie{Name: "afsc_preview", Value: "1"}) }, ""},
			{"/about/", func(r *http.Request) { r.Header.Set("Authorization", "Bearer x") }, ""},
			{"/m/1/00000000/orig/x.png", nil, ""},
			{asset[1], nil, "public, max-age=31536000, immutable"},
		}
		for _, c := range cases {
			var mods []func(*http.Request)
			if c.mod != nil {
				mods = append(mods, c.mod)
			}
			for range 2 {
				w := get(c.target, mods...)
				if w.Header().Get("X-Cache") != "" || w.Header().Get("Cache-Control") != c.cc {
					t.Errorf("%s: X-Cache %q, Cache-Control %q", c.target, w.Header().Get("X-Cache"), w.Header().Get("Cache-Control"))
				}
			}
		}
		if n, _ := st.cache.Stats(); n != before {
			t.Errorf("uncacheable requests changed the cache: %d entries, had %d", n, before)
		}
	})

	t.Run("ttl zero", func(t *testing.T) {
		st.purgeAll()
		swapSettings(t, st, func(a *settings.All) { a.Cache.PageTTLSeconds = 0 })
		for range 2 {
			w := get("/about/")
			if w.Header().Get("X-Cache") != "MISS" || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("ETag") == "" {
				t.Errorf("ttl 0: %v", w.Header())
			}
		}
	})

	t.Run("purge by group", func(t *testing.T) {
		st.purgeAll()
		for _, p := range []string{"/category/markets/", "/category/markets/feed/", "/tag/example/"} {
			get(p)
		}
		st.apply(context.Background(), Event{Paths: []string{"/category/markets/"}})
		want := map[string]string{"/category/markets/": "MISS", "/category/markets/feed/": "MISS", "/tag/example/": "HIT"}
		for p, x := range want {
			if got := get(p).Header().Get("X-Cache"); got != x {
				t.Errorf("%s after purge: %s, want %s", p, got, x)
			}
		}
	})

	t.Run("term and author sets", func(t *testing.T) {
		snap := st.snap.Load()
		ev, err := st.TermChange(context.Background(), snap.termPaths["tag:example"])
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range []string{"/tag/example/", "/example-post/", "/", "/2021/03/"} {
			if !ev.All && !slices.Contains(ev.Paths, p) {
				t.Errorf("TermChange(example) misses %s: %v", p, ev.Paths)
			}
		}
		ev, err = st.AuthorChange(context.Background(), snap.authorSlugs["author-a"])
		if err != nil {
			t.Fatal(err)
		}
		if !ev.All && (!slices.Contains(ev.Paths, "/author/author-a/") || !slices.Contains(ev.Paths, "/")) {
			t.Errorf("AuthorChange(author-a): %v", ev.Paths)
		}
	})
}

// TestCacheNotify checks that an event from one process purges and refreshes another through LISTEN/NOTIFY.
func TestCacheNotify(t *testing.T) {
	pool := importedPool(t)
	a, b := cachedSite(t, pool), cachedSite(t, pool)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		b.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	waitFor := func(what string, cond func() bool) {
		t.Helper()
		for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
			if cond() {
				return
			}
		}
		t.Fatalf("timed out waiting for %s", what)
	}
	waitFor("the listener", b.listening.Load)

	request(b, "GET", "/about/")
	request(b, "GET", "/example-post/")
	if request(b, "GET", "/about/").Header().Get("X-Cache") != "HIT" {
		t.Fatal("warm-up failed")
	}
	if err := a.Invalidate(ctx, Event{Paths: []string{"/about/"}}); err != nil {
		t.Fatal(err)
	}
	waitFor("the purge on the second process", func() bool { return request(b, "GET", "/about/").Header().Get("X-Cache") == "MISS" })
	if request(b, "GET", "/example-post/").Header().Get("X-Cache") != "HIT" {
		t.Error("a path purge dropped an unrelated page")
	}

	// a CLI command (import, rerender) notifies without a Site; the snapshot must refresh before the purge
	const src = "/notify-test/"
	if _, err := pool.Exec(ctx, `INSERT INTO redirects (source, match, target, status, origin) VALUES ($1, 'prefix', '/about/', 301, 'manual')`, src); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM redirects WHERE source = $1 AND origin = 'manual'`, src)
	})
	if err := Notify(ctx, pool, Event{Refresh: true, All: true}); err != nil {
		t.Fatal(err)
	}
	waitFor("purge all", func() bool { return request(b, "GET", "/example-post/").Header().Get("X-Cache") == "MISS" })
	if w := request(b, "GET", src+"x/"); w.Code != http.StatusMovedPermanently {
		t.Errorf("redirect table not refreshed before the purge: %d", w.Code)
	}
}
