package site

import (
	"context"
	"slices"

	"github.com/jackc/pgx/v5"

	"afsc/internal/theme"
)

// Cache groups are base paths: an archive root, a post or page path, or "/" for home. Every page of an archive,
// its query variants and its feed share the archive's group, so purging the group drops them all.

// group is the purge group of a cacheable response. path is the canonical request path.
func (s *snapshot) group(res Result, path string) string {
	switch res.Kind {
	case theme.KindHome, theme.KindFrontPage:
		return "/"
	case theme.KindPostsPage, theme.KindPage:
		return s.pagePath(res.ID)
	case theme.KindCategory, theme.KindTag:
		return s.termArchive(res.ID)
	case theme.KindAuthor:
		return s.authorArchive(res.ID)
	case theme.KindDate:
		if res.Month > 0 {
			return s.perm.Month(res.Year, res.Month)
		}
		return s.perm.Year(res.Year)
	case kindFeed:
		switch res.FeedOf {
		case "category", "tag":
			return s.termArchive(res.ID)
		case "author":
			return s.authorArchive(res.ID)
		}
		return "/"
	}
	return path
}

func (s *snapshot) termArchive(id int64) string {
	if t := s.terms[id]; t != nil {
		return s.perm.Term(t.Taxonomy, s.termSlugPath(id))
	}
	return ""
}

func (s *snapshot) authorArchive(id int64) string {
	if a := s.authors[id]; a != nil {
		return s.perm.Author(a.Slug)
	}
	return ""
}

// PostState is what decides which cached pages show a post: its ref, its terms and its authors.
type PostState struct {
	Ref       PostRef
	TermIDs   []int64
	AuthorIDs []int64
}

type pathSet map[string]struct{}

func (p pathSet) add(paths ...string) {
	for _, path := range paths {
		if path != "" {
			p[path] = struct{}{}
		}
	}
}

func (p pathSet) event(refresh bool) Event {
	if len(p) > maxPaths {
		return Event{Refresh: refresh, All: true}
	}
	paths := make([]string, 0, len(p))
	for path := range p {
		paths = append(paths, path)
	}
	slices.Sort(paths)
	return Event{Refresh: refresh, Paths: paths}
}

// PostChange is the purge event for a post or page that changed from before to after. Pass nil before for a new
// post and nil after for a deleted one. Unpublished states add nothing.
func (st *Site) PostChange(before, after *PostState) Event {
	return st.snap.Load().postChange(before, after)
}

func (s *snapshot) postChange(before, after *PostState) Event {
	set := pathSet{}
	var paths [2]string
	page := false
	for i, p := range []*PostState{before, after} {
		if p == nil || p.Ref.Status != "published" {
			continue
		}
		paths[i] = s.refPath(&p.Ref)
		if p.Ref.Type == "page" {
			page = true
			set.add(paths[i])
			continue
		}
		set.add(paths[i], "/")
		if s.postsPageID != 0 {
			set.add(s.pagePath(s.postsPageID))
		}
		if p.Ref.PublishedAt != nil {
			t := p.Ref.PublishedAt.In(s.loc)
			set.add(s.perm.Year(t.Year()), s.perm.Month(t.Year(), int(t.Month())))
		}
		for _, id := range p.TermIDs {
			for _, t := range s.termChain(id) {
				set.add(s.termArchive(t.ID))
			}
		}
		for _, id := range p.AuthorIDs {
			set.add(s.authorArchive(id))
		}
	}
	moved := paths[0] != paths[1]
	if moved && (s.inMenu(paths[0]) || s.inMenu(paths[1])) {
		return Event{Refresh: page, All: true}
	}
	if page {
		id := after
		if id == nil {
			id = before
		}
		kids := s.pageDescendants(id.Ref.ID)
		if moved && len(kids) > 0 {
			return Event{Refresh: true, All: true}
		}
		for _, k := range kids {
			set.add(s.pagePath(k))
		}
	}
	return set.event(page)
}

// refPath is the path a post or page has with its own slug and parent, which may differ from the snapshot's copy.
func (s *snapshot) refPath(r *PostRef) string {
	if r.Type != "page" {
		return s.postPath(r)
	}
	if r.ID == s.homeID {
		return "/"
	}
	var slugs []string
	for _, p := range s.pageChain(r.ParentID) {
		slugs = append(slugs, p.Slug)
	}
	return s.perm.Page(append(slugs, r.Slug))
}

func (s *snapshot) pageDescendants(id int64) []int64 {
	children := map[int64][]int64{}
	for _, p := range s.pages {
		children[p.ParentID] = append(children[p.ParentID], p.ID)
	}
	var out []int64
	queue := []int64{id}
	for len(queue) > 0 && len(out) < 10000 {
		next := children[queue[0]]
		queue = append(queue[1:], next...)
		out = append(out, next...)
	}
	return out
}

// inMenu reports whether a menu item links to path, so moving it changes every page that shows the menu.
func (s *snapshot) inMenu(path string) bool {
	if path == "" {
		return false
	}
	url := s.abs(path)
	var walk func([]*theme.MenuItem) bool
	walk = func(items []*theme.MenuItem) bool {
		for _, it := range items {
			if it.URL == url || walk(it.Children) {
				return true
			}
		}
		return false
	}
	for _, items := range s.menus {
		if walk(items) {
			return true
		}
	}
	return false
}

const (
	sqlTermPosts = `
SELECT DISTINCT pt.post_id FROM post_terms pt JOIN posts p ON p.id = pt.post_id
WHERE pt.term_id = ANY($1::bigint[]) AND p.status = 'published' LIMIT $2`
	sqlAuthorPosts = `
SELECT pa.post_id FROM post_authors pa JOIN posts p ON p.id = pa.post_id
WHERE pa.author_id = $1 AND p.status = 'published' LIMIT $2`
)

// TermChange is the purge event for a term: its archive, its ancestors' and descendants' archives, and every page
// showing a post filed under it. A slug or parent change needs the event computed both before and after the
// change (see Event.Merge). A term linked from a menu purges everything.
func (st *Site) TermChange(ctx context.Context, id int64) (Event, error) {
	s := st.snap.Load()
	ids := s.termDescendants(id)
	set := pathSet{}
	for _, t := range s.termChain(id) {
		set.add(s.termArchive(t.ID))
	}
	for _, d := range ids {
		path := s.termArchive(d)
		if s.inMenu(path) {
			return Event{Refresh: true, All: true}, nil
		}
		set.add(path)
	}
	return st.withPosts(ctx, s, set, sqlTermPosts, ids)
}

// AuthorChange is the purge event for an author: the archive and every page showing one of their posts.
func (st *Site) AuthorChange(ctx context.Context, id int64) (Event, error) {
	s := st.snap.Load()
	path := s.authorArchive(id)
	if s.inMenu(path) {
		return Event{Refresh: true, All: true}, nil
	}
	set := pathSet{}
	set.add(path)
	return st.withPosts(ctx, s, set, sqlAuthorPosts, id)
}

// withPosts adds the pages of every published post the query returns, or gives up and purges everything.
func (st *Site) withPosts(ctx context.Context, s *snapshot, set pathSet, sql string, arg any) (Event, error) {
	rows, err := st.db.Query(ctx, sql, arg, maxPaths+1)
	if err != nil {
		return Event{}, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil {
		return Event{}, err
	}
	if len(ids) > maxPaths {
		return Event{Refresh: true, All: true}, nil
	}
	states, err := st.postStates(ctx, ids)
	if err != nil {
		return Event{}, err
	}
	for _, p := range states {
		ev := s.postChange(nil, p)
		if ev.All {
			return Event{Refresh: true, All: true}, nil
		}
		set.add(ev.Paths...)
	}
	return set.event(true), nil
}

// postStates loads the refs, terms and authors of the given posts.
func (st *Site) postStates(ctx context.Context, ids []int64) ([]*PostState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	refs, err := st.postRefs(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := map[int64]*PostState{}
	out := make([]*PostState, 0, len(refs))
	for _, id := range ids {
		if r := refs[id]; r != nil {
			byID[id] = &PostState{Ref: *r}
			out = append(out, byID[id])
		}
	}
	for _, q := range []struct {
		sql string
		add func(p *PostState, id int64)
	}{
		{sqlPostTerms, func(p *PostState, id int64) { p.TermIDs = append(p.TermIDs, id) }},
		{sqlPostAuthors, func(p *PostState, id int64) { p.AuthorIDs = append(p.AuthorIDs, id) }},
	} {
		rows, err := st.db.Query(ctx, q.sql, ids)
		if err != nil {
			return nil, err
		}
		var postID, otherID int64
		_, err = pgx.ForEachRow(rows, []any{&postID, &otherID}, func() error {
			if p := byID[postID]; p != nil {
				q.add(p, otherID)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}
