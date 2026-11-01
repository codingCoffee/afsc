package site

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// §18 targets: a cached page under 5 ms p50 in-process, an uncached post page under 50 ms p50 with 20k posts.
// Run with: go test -run x -bench . ./internal/site/ (needs AFSC_TEST_DATABASE_URL)

const benchPosts = 20000

func BenchmarkCachedPage(b *testing.B) {
	st := cachedSite(b, importedPool(b))
	if w := request(st, "GET", "/example-post/"); w.Code != 200 {
		b.Fatalf("warm-up: %d", w.Code)
	}
	reportP50(b, 5*time.Millisecond, func() {
		if w := request(st, "GET", "/example-post/"); w.Header().Get("X-Cache") != "HIT" {
			b.Fatal("not served from cache")
		}
	})
}

func BenchmarkUncachedPost(b *testing.B) {
	pool := importedPool(b)
	seedPosts(b, pool)
	st, err := New(context.Background(), Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "../../themes/default",
		Logf: b.Logf})
	if err != nil {
		b.Fatal(err)
	}
	reportP50(b, 50*time.Millisecond, func() {
		if w := request(st, "GET", "/bench-post-10000/"); w.Code != 200 {
			b.Fatalf("status %d", w.Code)
		}
	})
}

// reportP50 times each call of fn and reports the median next to the target.
func reportP50(b *testing.B, target time.Duration, fn func()) {
	times := make([]time.Duration, 0, b.N)
	b.ResetTimer()
	for range b.N {
		start := time.Now()
		fn()
		times = append(times, time.Since(start))
	}
	b.StopTimer()
	slices.Sort(times)
	p50 := times[len(times)/2]
	b.ReportMetric(float64(p50.Microseconds())/1000, "p50-ms")
	verdict := "ok"
	if p50 >= target {
		verdict = "OVER TARGET"
	}
	b.Logf("p50 %s, target < %s: %s (n=%d)", p50, target, verdict, len(times))
}

const (
	sqlBenchClean = `DELETE FROM posts WHERE legacy_key LIKE 'bench:post:%'`
	sqlBenchPosts = `
INSERT INTO posts (type, status, slug, title, content, content_html, content_text, published_at, legacy_key)
SELECT 'post', 'published', 'bench-post-' || g, 'Bench post ' || g, src.content, src.content_html, src.content_text,
       now() - g * interval '1 hour', 'bench:post:' || g
FROM generate_series(1, $1::int) g,
     (SELECT content, content_html, content_text FROM posts WHERE legacy_key = 'wp:post:123') src`
	sqlBenchTerms = `
INSERT INTO post_terms (post_id, term_id, is_primary)
SELECT p.id, pt.term_id, pt.is_primary FROM posts p
JOIN post_terms pt ON pt.post_id = (SELECT id FROM posts WHERE legacy_key = 'wp:post:123')
WHERE p.legacy_key LIKE 'bench:post:%'`
	sqlBenchAuthors = `
INSERT INTO post_authors (post_id, author_id, position)
SELECT p.id, pa.author_id, pa.position FROM posts p
JOIN post_authors pa ON pa.post_id = (SELECT id FROM posts WHERE legacy_key = 'wp:post:123')
WHERE p.legacy_key LIKE 'bench:post:%'`
)

// seedPosts adds 20k published posts copied from the sample's example post, and removes them afterwards.
// Only rows with a bench: legacy key are touched; a run that was killed is cleaned up by the next one.
func seedPosts(b *testing.B, pool *pgxpool.Pool) {
	b.Helper()
	ctx := context.Background()
	if _, err := pool.Exec(ctx, sqlBenchClean); err != nil {
		b.Fatal(err)
	}
	b.Cleanup(func() {
		if _, err := pool.Exec(ctx, sqlBenchClean); err != nil {
			b.Errorf("remove bench posts: %v", err)
		}
	})
	for _, q := range []struct {
		sql  string
		args []any
	}{{sqlBenchPosts, []any{benchPosts}}, {sqlBenchTerms, nil}, {sqlBenchAuthors, nil}, {`ANALYZE posts`, nil}} {
		if _, err := pool.Exec(ctx, q.sql, q.args...); err != nil {
			b.Fatalf("seed: %v", err)
		}
	}
	var n int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE status = 'published'`).Scan(&n); err != nil || n < benchPosts {
		b.Fatalf("seeded %d published posts: %v", n, err)
	}
}
