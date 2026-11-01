package site

import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)

// URL sets for auto redirects map an entity key ("post:12", "term:3", "author:5") to its public path. Take one
// before a slug, parent or date change and one after (once the snapshot has the change), then pair them with
// redirects.Moves. Only URLs the public site serves are included.

// PostURLs is a published post's path, or a published page's path plus those of its published descendants.
func (st *Site) PostURLs(ctx context.Context, id int64) (map[string]string, error) {
	s := st.snap.Load()
	refs, err := st.postRefs(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	ref := refs[id]
	if ref == nil || !ref.Visible(st.now()) {
		return out, nil
	}
	out[postKey(id)] = s.refPath(ref)
	if ref.Type == "page" {
		for _, k := range s.pageDescendants(id) {
			if p := s.pages[k]; p != nil && p.Visible(st.now()) {
				out[postKey(k)] = s.postPath(&p.PostRef)
			}
		}
	}
	return out, nil
}

func postKey(id int64) string { return fmt.Sprintf("post:%d", id) }

const sqlCategoryPosts = `
SELECT DISTINCT pt.post_id FROM post_terms pt JOIN posts p ON p.id = pt.post_id
WHERE pt.term_id = ANY($1::bigint[]) AND p.type = 'post' AND p.status = 'published'`

// TermURLs is the archive path of a term and its descendants. When the post pattern has {category}, it also holds
// every published post whose primary category is among them, because their URLs move with the term.
func (st *Site) TermURLs(ctx context.Context, id int64) (map[string]string, error) {
	s := st.snap.Load()
	out := map[string]string{}
	if s.terms[id] == nil {
		return out, nil
	}
	ids := s.termDescendants(id)
	for _, d := range ids {
		out[fmt.Sprintf("term:%d", d)] = s.termArchive(d)
	}
	if s.terms[id].Taxonomy != "category" || !strings.Contains(s.settings.Permalinks.PostPattern, "{category}") {
		return out, nil
	}
	rows, err := st.db.Query(ctx, sqlCategoryPosts, ids)
	if err != nil {
		return nil, err
	}
	postIDs, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return nil, err
	}
	refs, err := st.postRefs(ctx, postIDs)
	if err != nil {
		return nil, err
	}
	in := map[int64]bool{}
	for _, d := range ids {
		in[d] = true
	}
	for pid, ref := range refs {
		if in[ref.CategoryID] && ref.Visible(st.now()) {
			out[postKey(pid)] = s.postPath(ref)
		}
	}
	return out, nil
}

// AuthorURLs is an author's archive path, or nothing when author archives are off.
func (st *Site) AuthorURLs(id int64) map[string]string {
	s := st.snap.Load()
	out := map[string]string{}
	if p := s.authorArchive(id); p != "" && s.settings.Permalinks.AuthorArchivesEnabled {
		out[fmt.Sprintf("author:%d", id)] = p
	}
	return out
}
