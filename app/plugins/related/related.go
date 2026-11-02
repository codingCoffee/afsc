// Package related stores the ten best related posts of every published post and gives themes the top ones with
// plg_related_posts (build plan §14). Scores: 3 per shared category, 1 per shared tag, plus the rank of the
// candidate's text against the post's title words.
package related

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/jobs"
	"afsc/internal/plugins"
	"afsc/internal/theme"
)

//go:embed migrations
var migrations embed.FS

const (
	ComputeKind    = "related.compute"
	ComputeAllKind = "related.compute_all"
	// Keep is how many related posts are stored per post, and the most a theme can show.
	Keep = 10
)

// batch is how many posts one related.compute_all job recomputes; tests lower it.
var batch = 200

type Plugin struct {
	h *plugins.Host
}

func New() *Plugin { return &Plugin{} }

func (*Plugin) ID() string { return "related" }

func (*Plugin) Info() plugins.Info {
	return plugins.Info{Name: "Related posts", DefaultEnabled: true,
		Description: "Lists posts that share categories, tags or title words with the one being read. Themes show them with plg_related_posts."}
}

func (p *Plugin) Init(_ context.Context, h *plugins.Host) error {
	p.h = h
	sub, err := fs.Sub(migrations, "migrations")
	if err != nil {
		return err
	}
	h.RegisterMigrations(sub)
	one, ten, twenty := 1.0, float64(Keep), 20.0
	h.RegisterSettings(
		plugins.Setting{Key: "years", Label: "Look back (years)", Type: "number", Default: 3, Min: &one, Max: &twenty,
			Help: "Only posts published this many years back are candidates. Takes effect at the nightly recompute."},
		plugins.Setting{Key: "count", Label: "Posts to show", Type: "number", Default: 3, Min: &one, Max: &ten,
			Help: "How many related posts themes show, unless the theme asks for a number."},
	)
	h.RegisterJob(ComputeKind, p.computeJob, jobs.Options{})
	h.RegisterJob(ComputeAllKind, p.computeAllJob, jobs.Options{})
	h.RegisterSchedule(jobs.Schedule{Kind: ComputeAllKind, Every: 24 * time.Hour, Payload: allPayload{}})
	h.RegisterImportJob(ComputeAllKind)
	plugins.AddAction(h, plugins.PostPublished, 0, p.onPost)
	plugins.AddAction(h, plugins.PostSaved, 0, p.onPost)
	h.RegisterTemplateFuncs(template.FuncMap{"plg_related_posts": p.posts})
	return nil
}

type computePayload struct {
	PostID int64 `json:"post_id"`
}

type allPayload struct {
	AfterID int64 `json:"after_id"`
}

// onPost queues a recompute when a post is published or saved while published, unless one is already waiting.
func (p *Plugin) onPost(ctx context.Context, ev plugins.Envelope[plugins.PostData]) error {
	if ev.Data.Type != "post" || ev.Data.Status != "published" {
		return nil
	}
	b, err := json.Marshal(computePayload{PostID: ev.Data.ID})
	if err != nil {
		return err
	}
	_, err = p.h.DB().Exec(ctx, `
INSERT INTO jobs (kind, payload) SELECT $1::text, $2::jsonb
WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE kind = $1::text AND payload = $2::jsonb AND done_at IS NULL AND locked_at IS NULL)`,
		ComputeKind, string(b))
	return err
}

func (p *Plugin) computeJob(ctx context.Context, raw json.RawMessage) error {
	var pl computePayload
	if err := json.Unmarshal(raw, &pl); err != nil {
		return err
	}
	changed, err := p.compute(ctx, pl.PostID)
	if err != nil || !changed {
		return err
	}
	return p.h.Backend().PurgePosts(ctx, []int64{pl.PostID})
}

// computeAllJob recomputes a batch of posts after AfterID, purges the ones whose list changed and queues the next batch.
func (p *Plugin) computeAllJob(ctx context.Context, raw json.RawMessage) error {
	var pl allPayload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &pl); err != nil {
			return err
		}
	}
	db := p.h.DB()
	if pl.AfterID == 0 {
		if _, err := db.Exec(ctx, `
DELETE FROM plg_related_items r USING posts p WHERE p.id = r.post_id AND (p.type <> 'post' OR p.status <> 'published')`); err != nil {
			return err
		}
	}
	rows, err := db.Query(ctx, `SELECT id FROM posts WHERE type = 'post' AND status = 'published' AND id > $1 ORDER BY id LIMIT $2`,
		pl.AfterID, batch)
	if err != nil {
		return err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return err
	}
	var changed []int64
	var runErr error
	for _, id := range ids {
		ch, err := p.compute(ctx, id)
		if err != nil {
			runErr = err
			break
		}
		if ch {
			changed = append(changed, id)
		}
	}
	// purge even after a failure: the retry finds these lists unchanged and wouldn't purge them again
	if len(changed) > 0 {
		if err := p.h.Backend().PurgePosts(ctx, changed); err != nil {
			runErr = errors.Join(runErr, err)
		}
	}
	if runErr != nil || len(ids) < batch {
		return runErr
	}
	return jobs.Enqueue(ctx, db, ComputeAllKind, allPayload{AfterID: ids[len(ids)-1]}, time.Time{})
}

// sqlScore ranks the candidates for $1: published posts from the last $2 years that share a term with it or match
// any of its title words. The title query ORs the title's lexemes; plainto_tsquery quotes them safely.
const sqlScore = `
WITH src AS (
  SELECT p.id, replace(plainto_tsquery('english', p.title)::text, ' & ', ' | ')::tsquery AS q
  FROM posts p WHERE p.id = $1 AND p.type = 'post' AND p.status = 'published'
),
shared AS (
  SELECT ct.post_id AS id, sum(CASE WHEN t.taxonomy = 'category' THEN 3 ELSE 1 END) AS s
  FROM src
  JOIN post_terms st ON st.post_id = src.id
  JOIN terms t ON t.id = st.term_id
  JOIN post_terms ct ON ct.term_id = st.term_id AND ct.post_id <> src.id
  GROUP BY ct.post_id
),
cands AS (
  SELECT id FROM shared
  UNION
  SELECT c.id FROM posts c, src WHERE c.search @@ src.q
)
SELECT c.id, (coalesce(sh.s, 0) + ts_rank(c.search, src.q))::real AS score
FROM cands
JOIN posts c ON c.id = cands.id
CROSS JOIN src
LEFT JOIN shared sh ON sh.id = c.id
WHERE c.id <> src.id AND c.type = 'post' AND c.status = 'published'
  AND c.published_at <= now() AND c.published_at >= now() - make_interval(years => $2::int)
ORDER BY score DESC, c.id DESC
LIMIT $3`

// compute replaces postID's stored list in one transaction and reports whether the order of ids changed. A post
// that isn't a published post ends up with no rows.
func (p *Plugin) compute(ctx context.Context, postID int64) (bool, error) {
	tx, err := p.h.DB().Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('plg_related_items'), ($1::bigint % 2147483647)::int)`, postID); err != nil {
		return false, err
	}
	rows, err := tx.Query(ctx, `SELECT related_id FROM plg_related_items WHERE post_id = $1 ORDER BY score DESC, related_id DESC`, postID)
	if err != nil {
		return false, err
	}
	old, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return false, err
	}
	years := max(1, int(p.h.Number("years")))
	rows, err = tx.Query(ctx, sqlScore, postID, years, Keep)
	if err != nil {
		return false, err
	}
	var ids []int64
	var scores []float32
	var id int64
	var score float32
	if _, err := pgx.ForEachRow(rows, []any{&id, &score}, func() error {
		ids, scores = append(ids, id), append(scores, score)
		return nil
	}); err != nil {
		return false, err
	}
	if slices.Equal(old, ids) {
		return false, nil
	}
	if _, err := tx.Exec(ctx, `DELETE FROM plg_related_items WHERE post_id = $1`, postID); err != nil {
		return false, err
	}
	if len(ids) > 0 {
		if _, err := tx.Exec(ctx, `
INSERT INTO plg_related_items (post_id, related_id, score) SELECT $1::bigint, r, s FROM unnest($2::bigint[], $3::real[]) AS u (r, s)`,
			postID, ids, scores); err != nil {
			return false, err
		}
	}
	return true, tx.Commit(ctx)
}

// posts is plg_related_posts: the post's top n related posts (default the count setting, at most 10). It's empty
// for pages, before the first compute, and when the plugin is off.
func (p *Plugin) posts(post *theme.Post, n ...int) []*theme.Post {
	if post == nil || post.Type != "post" {
		return nil
	}
	limit := int(p.h.Number("count"))
	if len(n) > 0 && n[0] > 0 {
		limit = n[0]
	}
	limit = min(max(limit, 1), Keep)
	if p.h.Fixtures() {
		return theme.FixtureRelated(post, limit)
	}
	db := p.h.DB()
	if db == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := db.Query(ctx, `
SELECT r.related_id FROM plg_related_items r JOIN posts c ON c.id = r.related_id
WHERE r.post_id = $1 AND c.status = 'published' AND c.published_at <= now()
ORDER BY r.score DESC, r.related_id DESC LIMIT $2`, post.ID, limit)
	if err != nil {
		p.h.Logf("load related posts of %d: %v", post.ID, err)
		return nil
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(ids) == 0 {
		return nil
	}
	out, err := p.h.Backend().Posts(ctx, ids)
	if err != nil {
		p.h.Logf("hydrate related posts of %d: %v", post.ID, err)
		return nil
	}
	return out
}
