package site

import (
	"cmp"
	"context"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/theme"
)

// Template queries (theme.Querier). They run during a render, so each gets its own short deadline; their results
// live in the page cache with the page that asked.

const queryTimeout = 5 * time.Second

var _ theme.Querier = (*Site)(nil)

const sqlTermPostList = `
SELECT ` + postCols + `
FROM posts p
WHERE p.type = 'post' AND p.status = 'published' AND p.published_at <= now()
  AND EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = $1)
ORDER BY p.menu_order, p.published_at, p.id
LIMIT $2`

const sqlRecentPostList = `
SELECT ` + postCols + `
FROM posts p
WHERE p.type = 'post' AND p.status = 'published' AND p.published_at <= now()
  AND ($1 = 0 OR EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = $1))
ORDER BY p.published_at DESC, p.id DESC
LIMIT $2`

func (st *Site) TermPosts(termID int64, limit int) ([]*theme.Post, error) {
	return st.queryPosts(sqlTermPostList, termID, limit)
}

func (st *Site) RecentPosts(termID int64, limit int) ([]*theme.Post, error) {
	return st.queryPosts(sqlRecentPostList, termID, limit)
}

const sqlTopTerms = `
SELECT pt.term_id
FROM post_terms pt
JOIN terms t ON t.id = pt.term_id
JOIN posts p ON p.id = pt.post_id
WHERE t.taxonomy = $1 AND p.type = 'post' AND p.status = 'published' AND p.published_at <= now()
GROUP BY pt.term_id
ORDER BY count(*) DESC, pt.term_id
LIMIT $2`

func (st *Site) TopTerms(taxonomy string, limit int) ([]*theme.Term, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rows, err := st.db.Query(ctx, sqlTopTerms, taxonomy, limit)
	if err != nil {
		return nil, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	snap := st.snap.Load()
	out := make([]*theme.Term, 0, len(ids))
	for _, id := range ids {
		if v := snap.termView(id); v != nil {
			out = append(out, v)
		}
	}
	return out, nil
}

func (st *Site) queryPosts(query string, termID int64, limit int) ([]*theme.Post, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rows, err := st.db.Query(ctx, query, termID, limit)
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
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := st.hydrate(ctx, st.snap.Load(), out, hydrateOpts{teaser: true}); err != nil {
		return nil, err
	}
	return views(out), nil
}

func (st *Site) Terms(taxonomy string) ([]*theme.Term, error) {
	snap := st.snap.Load()
	var ids []int64
	for id, t := range snap.terms {
		if t.Taxonomy == taxonomy {
			ids = append(ids, id)
		}
	}
	return termViews(snap, ids), nil
}

func (st *Site) TermChildren(termID int64) ([]*theme.Term, error) {
	snap := st.snap.Load()
	return termViews(snap, slices.Clone(snap.termChildren[termID])), nil
}

// termViews returns views by WordPress id (AFSC id for terms made in AFSC), so migrated terms keep their
// WordPress order.
func termViews(snap *snapshot, ids []int64) []*theme.Term {
	out := make([]*theme.Term, 0, len(ids))
	for _, id := range ids {
		if v := snap.termView(id); v != nil {
			out = append(out, v)
		}
	}
	slices.SortFunc(out, func(a, b *theme.Term) int { return cmp.Compare(a.WPID, b.WPID) })
	return out
}

const sqlRecentComments = `
SELECT c.id, coalesce(c.legacy_key, ''), c.author_name, c.body, c.created_at, c.post_id, p.title,
  (SELECT count(*) FROM comments x WHERE x.post_id = c.post_id AND x.status = 'approved')
FROM comments c JOIN posts p ON p.id = c.post_id
WHERE c.status = 'approved' AND p.status = 'published' AND p.published_at <= now()
ORDER BY c.created_at DESC, c.id DESC
LIMIT $1`

func (st *Site) RecentComments(n int) ([]*theme.RecentComment, error) {
	ctx, cancel := context.WithTimeout(context.Background(), queryTimeout)
	defer cancel()
	rows, err := st.db.Query(ctx, sqlRecentComments, n)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*theme.RecentComment
	var postIDs []int64
	keys := map[*theme.RecentComment]string{}
	for rows.Next() {
		c := &theme.RecentComment{}
		var key string
		var postID int64
		if err := rows.Scan(&c.ID, &key, &c.AuthorName, &c.Text, &c.CreatedAt, &postID, &c.PostTitle, &c.PostCommentCount); err != nil {
			return nil, err
		}
		keys[c] = key
		out = append(out, c)
		postIDs = append(postIDs, postID)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	refs, err := st.postRefs(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	snap := st.snap.Load()
	for i, c := range out {
		if r := refs[postIDs[i]]; r != nil {
			c.PostURL = snap.abs(snap.refPath(r))
			c.URL = c.PostURL + "#" + commentAnchor(c.ID, keys[c])
		}
	}
	return out, nil
}
