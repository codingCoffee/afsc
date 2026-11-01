package site

import (
	"context"
	"errors"
	"maps"
	"slices"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/redirects"
	"afsc/internal/settings"
)

// resetRedirects drops redirects the test made and re-imports the sample bundle, which restores slugs and the
// imported redirects.
func resetRedirects(t *testing.T, pool *pgxpool.Pool) {
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM redirects WHERE origin <> 'import'`); err != nil {
			t.Error(err)
		}
		importedPool(t)
	})
}

func legacyID(t *testing.T, pool *pgxpool.Pool, table, key string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM `+table+` WHERE legacy_key = $1`, key).Scan(&id); err != nil {
		t.Fatalf("%s %s: %v", table, key, err)
	}
	return id
}

func TestAutoRedirects(t *testing.T) {
	pool := importedPool(t)
	resetRedirects(t, pool)
	st := cachedSite(t, pool)
	ctx := context.Background()
	store := &redirects.Store{DB: pool, Router: st.Router()}
	exec := func(t *testing.T, sql string, args ...any) {
		t.Helper()
		if _, err := pool.Exec(ctx, sql, args...); err != nil {
			t.Fatal(err)
		}
	}
	loc := func(t *testing.T, path string) string {
		t.Helper()
		w := request(st, "GET", path)
		if w.Code != 301 {
			t.Errorf("%s: %d, want 301", path, w.Code)
		}
		return w.Header().Get("Location")
	}

	t.Run("post slug", func(t *testing.T) {
		id := legacyID(t, pool, "posts", "wp:post:123")
		before, err := st.PostURLs(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		exec(t, `UPDATE posts SET slug = 'renamed-post' WHERE id = $1`, id)
		after, err := st.PostURLs(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		moves := redirects.Moves(before, after)
		if !slices.Equal(moves, []redirects.Move{{Old: "/example-post/", New: "/renamed-post/"}}) {
			t.Fatalf("moves = %+v", moves)
		}
		res, err := store.SlugChange(ctx, moves, nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(res.Created) != 1 || len(res.Flattened) != 1 || res.Purge.Refresh {
			t.Errorf("result = %+v", res)
		}
		for _, p := range []string{"/example-post/", "/old-path/"} {
			if got := loc(t, p); got != "/renamed-post/" {
				t.Errorf("%s -> %q", p, got)
			}
		}
		var origin string
		if err := pool.QueryRow(ctx, `SELECT origin FROM redirects WHERE source = '/example-post/'`).Scan(&origin); err != nil || origin != "slug_change" {
			t.Errorf("origin %q %v", origin, err)
		}
		if w := request(st, "GET", "/renamed-post/"); w.Code != 200 {
			t.Errorf("new URL: %d", w.Code)
		}
	})

	t.Run("page subtree", func(t *testing.T) {
		id := legacyID(t, pool, "posts", "wp:post:2")
		before, err := st.PostURLs(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		exec(t, `UPDATE posts SET slug = 'about-us' WHERE id = $1`, id)
		if err := st.Invalidate(ctx, Event{Refresh: true, All: true}); err != nil {
			t.Fatal(err)
		}
		after, err := st.PostURLs(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := []redirects.Move{{Old: "/about/", New: "/about-us/"}, {Old: "/about/intro/", New: "/about-us/intro/"},
			{Old: "/about/team/", New: "/about-us/team/"}, {Old: "/about/team/intro/", New: "/about-us/team/intro/"}}
		moves := redirects.Moves(before, after)
		if !slices.Equal(moves, want) {
			t.Fatalf("moves = %+v", moves)
		}
		if _, err := store.SlugChange(ctx, moves, nil); err != nil {
			t.Fatal(err)
		}
		if got := loc(t, "/about/team/intro/"); got != "/about-us/team/intro/" {
			t.Errorf("-> %q", got)
		}
	})

	t.Run("term subtree with {category} posts", func(t *testing.T) {
		pattern := func(a *settings.All) { a.Permalinks.PostPattern = "/{category}/{slug}/" }
		id := legacyID(t, pool, "terms", "wp:term:5")
		swapSettings(t, st, pattern)
		before, err := st.TermURLs(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		exec(t, `UPDATE terms SET slug = 'markets-x' WHERE id = $1`, id)
		if err := st.refresh(ctx); err != nil {
			t.Fatal(err)
		}
		swapSettings(t, st, pattern)
		after, err := st.TermURLs(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		want := []redirects.Move{
			{Old: "/category/markets/", New: "/category/markets-x/"},
			{Old: "/category/markets/stocks/", New: "/category/markets-x/stocks/"},
			{Old: "/markets/market-notes/", New: "/markets-x/market-notes/"},
			{Old: "/markets/stocks/renamed-post/", New: "/markets-x/stocks/renamed-post/"},
		}
		if moves := redirects.Moves(before, after); !slices.Equal(moves, want) {
			t.Errorf("moves = %+v", moves)
		}
	})

	t.Run("author", func(t *testing.T) {
		id := legacyID(t, pool, "authors", "wp:user:2")
		if got := slices.Collect(maps.Values(st.AuthorURLs(id))); !slices.Equal(got, []string{"/author/author-a/"}) {
			t.Errorf("AuthorURLs = %v", got)
		}
		swapSettings(t, st, func(a *settings.All) { a.Permalinks.AuthorArchivesEnabled = false })
		if got := st.AuthorURLs(id); len(got) != 0 {
			t.Errorf("archives off: %v", got)
		}
	})
}

func TestRedirectChecksAgainstSite(t *testing.T) {
	pool := importedPool(t)
	resetRedirects(t, pool)
	st := cachedSite(t, pool)
	ctx := context.Background()
	store := &redirects.Store{DB: pool, Router: st.Router()}
	str := func(s string) *string { return &s }

	_, err := store.Save(ctx, 0, redirects.Input{Source: "/m2c-a/", Target: str("/old-path/")}, false, nil)
	var ce *redirects.ChainError
	if !errors.As(err, &ce) || ce.Code != "chain" || ce.Flattened != "/example-post/" {
		t.Fatalf("chain through the imported redirect: %v", err)
	}
	saved, err := store.Save(ctx, 0, redirects.Input{Source: "/m2c-a/", Target: str("/old-path/")}, true, nil)
	if err != nil {
		t.Fatal(err)
	}
	if row, err := store.Get(ctx, saved.ID); err != nil || *row.Target != "/example-post/" || row.Origin != "manual" {
		t.Errorf("flattened row %+v %v", row, err)
	}
	if !slices.Equal(saved.Purge.Paths, []string{"/m2c-a/"}) {
		t.Errorf("purge %+v", saved.Purge)
	}

	for _, c := range []struct{ source, target, code string }{
		{"/m2c-b/", "/deleted-post/", "gone"},
		{"/m2c-b/", "/topics/markets/", "chain"},
		{"/m2c-b/", "/Example-Post", ""},
		{"/m2c-b/", "/2021/03/", ""},
		{"/m2c-b/", "/archive/2015/x/", "chain"},
		{"/m2c-b/", "/sitemap.xml", "chain"},
	} {
		_, err := store.Save(ctx, 0, redirects.Input{Source: c.source, Target: str(c.target)}, false, nil)
		var ce *redirects.ChainError
		switch {
		case c.code == "" && err != nil:
			t.Errorf("%s: %v", c.target, err)
		case c.code != "" && (!errors.As(err, &ce) || ce.Code != c.code):
			t.Errorf("%s: %v, want %s", c.target, err, c.code)
		}
		if err == nil {
			if _, err := pool.Exec(ctx, `DELETE FROM redirects WHERE source = '/m2c-b/'`); err != nil {
				t.Fatal(err)
			}
		}
	}

	var fe *redirects.FieldError
	if _, err := store.Save(ctx, 0, redirects.Input{Source: "/robots.txt", Target: str("/")}, false, nil); !errors.As(err, &fe) {
		t.Errorf("a redirect from robots.txt never applies: %v", err)
	}
	if _, err := store.Save(ctx, 0, redirects.Input{Source: "/old-path", Target: str("/")}, false, nil); !errors.As(err, &fe) || !fe.Conflict {
		t.Errorf("duplicate exact source: %v", err)
	}
	saved, err = store.Save(ctx, 0, redirects.Input{Source: "/about/", Target: str("/2021/")}, false, nil)
	if err != nil || len(saved.Warnings) != 1 {
		t.Errorf("shadowing a live page warns: %+v %v", saved, err)
	}
}

func TestRedirectHitsAndNotFoundLog(t *testing.T) {
	pool := importedPool(t)
	st := cachedSite(t, pool)
	ctx := context.Background()
	hits := func() (n int64) {
		t.Helper()
		if err := pool.QueryRow(ctx, `SELECT hits FROM redirects WHERE source = '/old-path/'`).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	start := hits()
	request(st, "GET", "/old-path/")
	request(st, "GET", "/Old-Path")
	st.flushRedirectHits(ctx)
	if got := hits(); got != start+2 {
		t.Errorf("hits %d, want %d", got, start+2)
	}
	var last bool
	if err := pool.QueryRow(ctx, `SELECT last_hit_at IS NOT NULL FROM redirects WHERE source = '/old-path/'`).Scan(&last); err != nil || !last {
		t.Errorf("last_hit_at not set: %v", err)
	}

	probe := []string{"/wp-login.php", "/.env", "/m2c-missing/"}
	if _, err := pool.Exec(ctx, `DELETE FROM not_found_log WHERE path = ANY($1)`, probe); err != nil {
		t.Fatal(err)
	}
	for _, p := range probe {
		if w := request(st, "GET", p); w.Code != 404 {
			t.Errorf("%s: %d", p, w.Code)
		}
	}
	st.flushNotFound(ctx)
	logged := map[string]bool{}
	rows, err := pool.Query(ctx, `SELECT path FROM not_found_log WHERE path = ANY($1)`, probe)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var p string
		_ = rows.Scan(&p)
		logged[p] = true
	}
	if !logged["/m2c-missing/"] || logged["/wp-login.php"] || logged["/.env"] {
		t.Errorf("logged = %v", logged)
	}

	if _, err := pool.Exec(ctx, `INSERT INTO not_found_log (path, hits, first_seen, last_seen)
VALUES ('/m2c-stale/', 1, now() - interval '200 days', now() - interval '91 days')
ON CONFLICT (path) DO UPDATE SET last_seen = EXCLUDED.last_seen`); err != nil {
		t.Fatal(err)
	}
	st.pruneNotFound(ctx)
	var stale, fresh bool
	if err := pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM not_found_log WHERE path = '/m2c-stale/'),
  EXISTS (SELECT 1 FROM not_found_log WHERE path = '/m2c-missing/')`).Scan(&stale, &fresh); err != nil {
		t.Fatal(err)
	}
	if stale || !fresh {
		t.Errorf("after prune: stale %v, fresh %v", stale, fresh)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM not_found_log WHERE path = '/m2c-missing/'`); err != nil {
		t.Fatal(err)
	}
}
