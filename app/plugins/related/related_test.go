package related

import (
	"context"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/plugins"
	"afsc/internal/settings"
	"afsc/internal/theme"
)

// backend records purges and returns bare posts for ids, the way the site would after hydration.
type backend struct {
	mu     sync.Mutex
	purged [][]int64
}

func (b *backend) Posts(_ context.Context, ids []int64) ([]*theme.Post, error) {
	out := make([]*theme.Post, len(ids))
	for i, id := range ids {
		out[i] = &theme.Post{ID: id, Type: "post"}
	}
	return out, nil
}

func (b *backend) PurgePosts(_ context.Context, ids []int64) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.purged = append(b.purged, slices.Clone(ids))
	return nil
}

func (b *backend) ThemeManifest() *theme.Manifest { return nil }

type env struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	p      *Plugin
	rt     *plugins.Runtime
	s      *settings.All
	be     *backend
	suffix string
	terms  map[string]int64
}

func newEnv(t *testing.T) *env {
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, ctx: ctx, pool: pool, p: New(), be: &backend{}, suffix: uuid.NewString()[:8], terms: map[string]int64{}}
	e.rt, err = plugins.Build(ctx, plugins.Config{DB: pool, BaseURL: "https://blog.example.com", Logf: t.Logf}, []plugins.Plugin{e.p})
	if err != nil {
		t.Fatal(err)
	}
	s := settings.Defaults()
	e.s = &s
	e.rt.Bind(func() *settings.All { return e.s })
	e.rt.BindBackend(e.be)
	t.Cleanup(func() {
		for _, sql := range []string{
			`DELETE FROM jobs WHERE kind LIKE 'related.%' AND done_at IS NULL`,
			`DELETE FROM posts WHERE slug LIKE '%-` + e.suffix + `'`,
			`DELETE FROM terms WHERE slug LIKE '%-` + e.suffix + `'`,
		} {
			if _, err := pool.Exec(ctx, sql); err != nil {
				t.Error(err)
			}
		}
		pool.Close()
	})
	return e
}

func (e *env) term(taxonomy, name string) int64 {
	e.t.Helper()
	if id, ok := e.terms[name]; ok {
		return id
	}
	var id int64
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO terms (taxonomy, slug, name) VALUES ($1, $2, $3) RETURNING id`,
		taxonomy, strings.ToLower(name)+"-"+e.suffix, name).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	e.terms[name] = id
	return id
}

// post inserts a post; terms name categories ("c:…") and tags ("t:…").
func (e *env) post(typ, status, title string, age time.Duration, terms ...string) int64 {
	e.t.Helper()
	var id int64
	slug := strings.ToLower(strings.ReplaceAll(title, " ", "-")) + "-" + uuid.NewString()[:4] + "-" + e.suffix
	if err := e.pool.QueryRow(e.ctx, `
INSERT INTO posts (type, status, slug, title, content, content_html, content_text, published_at)
VALUES ($1, $2, $3, $4, '{"type":"doc","content":[]}', '', '', now() - make_interval(secs => $5)) RETURNING id`,
		typ, status, slug, title, age.Seconds()).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	for _, tm := range terms {
		tax, name, _ := strings.Cut(tm, ":")
		taxonomy := map[string]string{"c": "category", "t": "tag"}[tax]
		if _, err := e.pool.Exec(e.ctx, `INSERT INTO post_terms (post_id, term_id) VALUES ($1, $2)`, id, e.term(taxonomy, name)); err != nil {
			e.t.Fatal(err)
		}
	}
	return id
}

func (e *env) stored(id int64) ([]int64, []float32) {
	e.t.Helper()
	rows, err := e.pool.Query(e.ctx, `SELECT related_id, score FROM plg_related_items WHERE post_id = $1 ORDER BY score DESC, related_id DESC`, id)
	if err != nil {
		e.t.Fatal(err)
	}
	var ids []int64
	var scores []float32
	var rid int64
	var sc float32
	if _, err := pgx.ForEachRow(rows, []any{&rid, &sc}, func() error {
		ids, scores = append(ids, rid), append(scores, sc)
		return nil
	}); err != nil {
		e.t.Fatal(err)
	}
	return ids, scores
}

const day = 24 * time.Hour

func TestScoring(t *testing.T) {
	e := newEnv(t)
	src := e.post("post", "published", "Zorblax quiffle", day, "c:Alpha", "c:Beta", "t:Gamma")
	b := e.post("post", "published", "Something else entirely", 2*day, "c:Alpha", "t:Gamma")
	c := e.post("post", "published", "Zorblax daily", 3*day, "t:Gamma")
	d := e.post("post", "published", "Quiffle notes", 4*day)
	e.post("post", "published", "Old but close", 5*365*day, "c:Alpha", "c:Beta")
	e.post("post", "draft", "Draft but close", day, "c:Alpha", "c:Beta")
	e.post("page", "published", "Zorblax page", day, "c:Alpha")
	e.post("post", "published", "Unrelated words", day)

	changed, err := e.p.compute(e.ctx, src)
	if err != nil || !changed {
		t.Fatalf("compute: %v %v", changed, err)
	}
	ids, scores := e.stored(src)
	if !slices.Equal(ids, []int64{b, c, d}) {
		t.Fatalf("related %v, want %v", ids, []int64{b, c, d})
	}
	if scores[0] < 4 || scores[0] >= 5 || scores[1] <= 1 || scores[1] >= 2 || scores[2] <= 0 || scores[2] >= 1 {
		t.Errorf("scores %v: want 3×categories + tags + title rank", scores)
	}
	if changed, err := e.p.compute(e.ctx, src); err != nil || changed {
		t.Errorf("a second compute changed the list: %v %v", changed, err)
	}

	for range 10 {
		e.post("post", "published", "Filler", 6*day, "t:Gamma")
	}
	if _, err := e.p.compute(e.ctx, src); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.stored(src); len(ids) != Keep || ids[0] != b || ids[1] != c || slices.Contains(ids, d) {
		t.Errorf("top %d: %v", Keep, ids)
	}

	if _, err := e.pool.Exec(e.ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, src); err != nil {
		t.Fatal(err)
	}
	if changed, err := e.p.compute(e.ctx, src); err != nil || !changed {
		t.Errorf("unpublished post: %v %v", changed, err)
	}
	if ids, _ := e.stored(src); len(ids) != 0 {
		t.Errorf("an unpublished post kept %v", ids)
	}
}

func TestFunc(t *testing.T) {
	e := newEnv(t)
	src := e.post("post", "published", "Zorblax quiffle", day, "c:Alpha")
	b := e.post("post", "published", "Second", 2*day, "c:Alpha", "t:Gamma")
	c := e.post("post", "published", "Third", 3*day, "c:Alpha")
	if _, err := e.p.compute(e.ctx, src); err != nil {
		t.Fatal(err)
	}
	fn := e.rt.Funcs()["plg_related_posts"].(func(*theme.Post, ...int) []*theme.Post)
	ids := func(ps []*theme.Post) []int64 {
		var out []int64
		for _, p := range ps {
			out = append(out, p.ID)
		}
		return out
	}
	post := &theme.Post{ID: src, Type: "post"}
	if got := ids(fn(post)); len(got) != 2 {
		t.Errorf("default count: %v", got)
	}
	if got := ids(fn(post, 1)); len(got) != 1 {
		t.Errorf("n=1: %v", got)
	}
	if _, err := e.pool.Exec(e.ctx, `UPDATE posts SET status = 'draft' WHERE id = $1`, b); err != nil {
		t.Fatal(err)
	}
	if got := ids(fn(post)); !slices.Equal(got, []int64{c}) {
		t.Errorf("an unpublished related post shows: %v", got)
	}
	if got := fn(&theme.Post{ID: c, Type: "post"}); got != nil {
		t.Errorf("not computed yet: %v", ids(got))
	}
	if got := fn(&theme.Post{ID: src, Type: "page"}); got != nil {
		t.Error("pages have no related posts")
	}
	e.s.Plugins.Enabled = map[string]bool{"related": false}
	if got := fn(post); got != nil {
		t.Error("disabled plugin returned posts")
	}
}

func TestPublishQueuesComputeAndPurges(t *testing.T) {
	e := newEnv(t)
	src := e.post("post", "published", "Zorblax quiffle", day, "c:Alpha")
	e.post("post", "published", "Other", 2*day, "c:Alpha")
	ev := plugins.Envelope[plugins.PostData]{ID: uuid.New(), Event: "post.published",
		Data: plugins.PostData{ID: src, Type: "post", Status: "published"}}
	for range 2 {
		if err := e.p.onPost(e.ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	page := ev
	page.Data.Type = "page"
	if err := e.p.onPost(e.ctx, page); err != nil {
		t.Fatal(err)
	}
	rows, err := e.pool.Query(e.ctx, `SELECT payload::text FROM jobs WHERE kind = $1 AND done_at IS NULL AND (payload->>'post_id')::bigint = $2`,
		ComputeKind, src)
	if err != nil {
		t.Fatal(err)
	}
	payloads, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil || len(payloads) != 1 {
		t.Fatalf("queued %v %v, want one job", payloads, err)
	}
	if err := e.p.computeJob(e.ctx, []byte(payloads[0])); err != nil {
		t.Fatal(err)
	}
	if len(e.be.purged) != 1 || !slices.Equal(e.be.purged[0], []int64{src}) {
		t.Errorf("purged %v", e.be.purged)
	}
	if err := e.p.computeJob(e.ctx, []byte(payloads[0])); err != nil || len(e.be.purged) != 1 {
		t.Errorf("an unchanged list was purged again: %v %v", e.be.purged, err)
	}
}

func TestComputeAllBatches(t *testing.T) {
	e := newEnv(t)
	defer func(n int) { batch = n }(batch)
	batch = 2
	first := e.post("post", "published", "Zorblax one", day, "c:Alpha")
	second := e.post("post", "published", "Zorblax two", day, "c:Alpha")
	e.post("post", "published", "Zorblax three", day, "c:Alpha")
	if err := e.p.computeAllJob(e.ctx, []byte(`{"after_id":`+strconv.FormatInt(first-1, 10)+`}`)); err != nil {
		t.Fatal(err)
	}
	if ids, _ := e.stored(first); len(ids) != 2 {
		t.Errorf("first post: %v", ids)
	}
	if len(e.be.purged) != 1 || !slices.Equal(e.be.purged[0], []int64{first, second}) {
		t.Errorf("purged %v", e.be.purged)
	}
	var next int64
	if err := e.pool.QueryRow(e.ctx, `SELECT (payload->>'after_id')::bigint FROM jobs WHERE kind = $1 AND done_at IS NULL
ORDER BY id DESC LIMIT 1`, ComputeAllKind).Scan(&next); err != nil || next != second {
		t.Errorf("continuation after %d (%v), want %d", next, err, second)
	}
}
