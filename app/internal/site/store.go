package site

import (
	"context"
	"errors"
	"html"
	"html/template"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/content"
	"afsc/internal/theme"
)

// Lookup is the per-request data the resolver needs beyond the snapshot. Tests use a fake.
type Lookup interface {
	ExactRedirect(ctx context.Context, paths []string) (*Redirect, error)
	Post(ctx context.Context, slug string, id int64) (*PostRef, error)
	LegacyPost(ctx context.Context, key string) (*PostRef, error)
	LegacyMedia(ctx context.Context, key string) (string, error)
}

const refCols = `p.id, coalesce(p.legacy_key, ''), p.type, p.slug, p.status, p.published_at, coalesce(p.parent_id, 0),
  coalesce((SELECT pt.term_id FROM post_terms pt JOIN terms t ON t.id = pt.term_id
            WHERE pt.post_id = p.id AND t.taxonomy = 'category' ORDER BY pt.is_primary DESC, t.id LIMIT 1), 0)`

const postCols = refCols + `, p.title, p.excerpt, p.content, p.content_html, p.content_text, coalesce(p.template, ''),
  p.sticky, p.comment_status, p.updated_at, coalesce(p.feed_guid, ''), coalesce(p.featured_media_id, 0), p.menu_order`

const (
	sqlExactRedirect = `
SELECT id, source, match, coalesce(target, ''), status FROM redirects
WHERE match = 'exact' AND source = ANY($1::text[])
ORDER BY array_position($1::text[], source) LIMIT 1`
	sqlPostRef       = `SELECT ` + refCols + ` FROM posts p WHERE p.type = 'post' AND CASE WHEN $2::bigint > 0 THEN p.id = $2 ELSE p.slug = $1 END`
	sqlLegacyPostRef = `SELECT ` + refCols + ` FROM posts p WHERE p.legacy_key = $1`
	sqlPostRefs      = `SELECT ` + refCols + ` FROM posts p WHERE p.id = ANY($1::bigint[])`
	sqlLegacyMedia   = `SELECT id, filename, substr(encode(sha256, 'hex'), 1, 8) FROM media WHERE legacy_key = $1`
	sqlPostsByID     = `SELECT ` + postCols + ` FROM posts p WHERE p.id = ANY($1::bigint[])`
	sqlListPosts     = `
SELECT ` + postCols + `, count(*) OVER () FROM posts p
WHERE p.type = 'post' AND p.status = 'published' AND p.published_at <= now()
  AND (coalesce(cardinality($1::bigint[]), 0) = 0
       OR EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = ANY($1::bigint[])))
  AND ($2::bigint = 0 OR EXISTS (SELECT 1 FROM post_authors pa WHERE pa.post_id = p.id AND pa.author_id = $2))
  AND ($3::timestamptz IS NULL OR p.published_at >= $3)
  AND ($4::timestamptz IS NULL OR p.published_at < $4)
ORDER BY CASE WHEN $5::bool THEN p.sticky END DESC NULLS LAST,
  CASE WHEN coalesce(cardinality($1::bigint[]), 0) > 0 THEN p.menu_order END ASC NULLS LAST,
  p.published_at DESC, p.id DESC
LIMIT $6 OFFSET $7`
	sqlSearch = `
WITH q AS (SELECT websearch_to_tsquery('english', $1) AS q),
hits AS (
  SELECT p.id, ts_rank_cd(p.search, q.q) AS rank, count(*) OVER () AS total
  FROM posts p, q
  WHERE p.status = 'published' AND p.published_at <= now() AND p.search @@ q.q
  ORDER BY rank DESC, p.published_at DESC, p.id DESC
  LIMIT $2 OFFSET $3
)
SELECT ` + postCols + `, h.total, ts_headline('english', p.content_text, q.q, $4)
FROM hits h JOIN posts p ON p.id = h.id CROSS JOIN q
ORDER BY h.rank DESC, p.published_at DESC, p.id DESC`
	sqlPostAuthors = `SELECT post_id, author_id FROM post_authors WHERE post_id = ANY($1::bigint[]) ORDER BY post_id, position, author_id`
	sqlPostTerms   = `SELECT post_id, term_id FROM post_terms WHERE post_id = ANY($1::bigint[])`
	sqlMedia       = `
SELECT id, coalesce(legacy_key, ''), filename, substr(encode(sha256, 'hex'), 1, 8), mime, coalesce(width, 0), coalesce(height, 0),
       alt, caption
FROM media`
)

// headline markers are private-use characters, swapped for <b> after escaping the snippet.
const (
	hlStart       = "\uE000"
	hlStop        = "\uE001"
	headlineOpts  = "StartSel=" + hlStart + ", StopSel=" + hlStop + ", MaxFragments=2, MaxWords=30, MinWords=10"
	maxQueryRunes = 200
)

type pgLookup struct{ db *pgxpool.Pool }

func scanRef(row pgx.Row) (*PostRef, error) {
	r := &PostRef{}
	err := row.Scan(&r.ID, &r.LegacyKey, &r.Type, &r.Slug, &r.Status, &r.PublishedAt, &r.ParentID, &r.CategoryID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (l *pgLookup) ExactRedirect(ctx context.Context, paths []string) (*Redirect, error) {
	r := &Redirect{}
	err := l.db.QueryRow(ctx, sqlExactRedirect, paths).Scan(&r.ID, &r.Source, &r.Match, &r.Target, &r.Status)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

func (l *pgLookup) Post(ctx context.Context, slug string, id int64) (*PostRef, error) {
	return scanRef(l.db.QueryRow(ctx, sqlPostRef, slug, id))
}

func (l *pgLookup) LegacyPost(ctx context.Context, key string) (*PostRef, error) {
	return scanRef(l.db.QueryRow(ctx, sqlLegacyPostRef, key))
}

func (l *pgLookup) LegacyMedia(ctx context.Context, key string) (string, error) {
	var m content.MediaInfo
	err := l.db.QueryRow(ctx, sqlLegacyMedia, key).Scan(&m.ID, &m.Filename, &m.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return content.MediaURL(m, "orig"), nil
}

func (st *Site) postRefs(ctx context.Context, ids []int64) (map[int64]*PostRef, error) {
	out := map[int64]*PostRef{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := st.db.Query(ctx, sqlPostRefs, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		r, err := scanRef(rows)
		if err != nil {
			return nil, err
		}
		out[r.ID] = r
	}
	return out, rows.Err()
}

// postRow is a loaded post: its view for templates plus what hydration and rendering need.
type postRow struct {
	ref     PostRef
	view    *theme.Post
	doc     []byte
	mediaID int64
}

func scanPost(rows pgx.Rows, extra ...any) (*postRow, error) {
	p := &postRow{view: &theme.Post{}}
	r, v := &p.ref, p.view
	var contentHTML string
	dest := []any{&r.ID, &r.LegacyKey, &r.Type, &r.Slug, &r.Status, &r.PublishedAt, &r.ParentID, &r.CategoryID,
		&v.Title, &v.Excerpt, &p.doc, &contentHTML, &v.ContentText, &v.Template,
		&v.Sticky, &v.CommentStatus, &v.UpdatedAt, &v.FeedGUID, &p.mediaID, &v.MenuOrder}
	if err := rows.Scan(append(dest, extra...)...); err != nil {
		return nil, err
	}
	v.ID, v.Type, v.Slug, v.ContentHTML = r.ID, r.Type, r.Slug, template.HTML(contentHTML)
	v.WPID = wpID(r.ID, r.LegacyKey, "post")
	if r.PublishedAt != nil {
		v.PublishedAt = *r.PublishedAt
	}
	return p, nil
}

func (st *Site) postsByID(ctx context.Context, ids []int64) ([]*postRow, error) {
	rows, err := st.db.Query(ctx, sqlPostsByID, ids)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*postRow
	for rows.Next() {
		p, err := scanPost(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

type filter struct {
	termIDs  []int64
	authorID int64
	from, to *time.Time
	sticky   bool
}

func (st *Site) listPosts(ctx context.Context, f filter, limit, offset int) ([]*postRow, int, error) {
	terms := f.termIDs
	if terms == nil {
		terms = []int64{}
	}
	rows, err := st.db.Query(ctx, sqlListPosts, terms, f.authorID, f.from, f.to, f.sticky, limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*postRow
	total := 0
	for rows.Next() {
		p, err := scanPost(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, p)
	}
	return out, total, rows.Err()
}

func (st *Site) search(ctx context.Context, q string, limit, offset int) ([]*postRow, int, error) {
	if strings.TrimSpace(q) == "" {
		return nil, 0, nil
	}
	rows, err := st.db.Query(ctx, sqlSearch, q, limit, offset, headlineOpts)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var out []*postRow
	total := 0
	for rows.Next() {
		var headline string
		p, err := scanPost(rows, &total, &headline)
		if err != nil {
			return nil, 0, err
		}
		h := html.EscapeString(headline)
		h = strings.NewReplacer(hlStart, "<b>", hlStop, "</b>").Replace(h)
		p.view.Snippet = template.HTML(h)
		out = append(out, p)
	}
	return out, total, rows.Err()
}

type mediaRow struct {
	content.MediaInfo
	Alt     string
	Caption string
}

func (st *Site) loadMedia(ctx context.Context, ids []int64) (map[int64]mediaRow, error) {
	if len(ids) == 0 {
		return map[int64]mediaRow{}, nil
	}
	return queryMedia(ctx, st.db, sqlMedia+` WHERE id = ANY($1::bigint[])`, ids)
}

func queryMedia(ctx context.Context, db *pgxpool.Pool, sql string, args ...any) (map[int64]mediaRow, error) {
	out := map[int64]mediaRow{}
	rows, err := db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var m mediaRow
		var key string
		if err := rows.Scan(&m.ID, &key, &m.Filename, &m.Version, &m.Mime, &m.Width, &m.Height, &m.Alt, &m.Caption); err != nil {
			return nil, err
		}
		if n := wpID(m.ID, key, "post"); n != m.ID {
			m.LegacyID = n
		}
		out[m.ID] = m
	}
	return out, rows.Err()
}

func (s *snapshot) mediaView(m mediaRow) *theme.Media {
	return &theme.Media{ID: m.ID, WPID: m.WPID(), Filename: m.Filename, Version: m.Version, Mime: m.Mime, Width: m.Width, Height: m.Height,
		Alt: m.Alt, Caption: m.Caption, URL: s.base + content.MediaURL(m.MediaInfo, "orig")}
}
