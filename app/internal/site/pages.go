package site

import (
	"bytes"
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	"afsc/internal/theme"
)

// renderPage renders a themed page. query is the query string the page may see: normalised for cacheable requests.
func (st *Site) renderPage(r *http.Request, snap *snapshot, res Result, query string) (*response, error) {
	d, status, err := st.pageData(r.Context(), snap, r, res)
	if err != nil {
		return nil, err
	}
	if d == nil {
		d, status = st.basePage(snap, r, theme.Kind404), http.StatusNotFound
	}
	if status == http.StatusNotFound {
		st.nf.add(r.URL.Path, r.Referer(), st.now())
	}
	d.Request.Query = query
	if err := st.buildHead(r.Context(), snap, d); err != nil {
		return nil, err
	}
	var buf bytes.Buffer
	if _, err := st.theme.Page(&buf, d); err != nil {
		return nil, err
	}
	h := http.Header{"Content-Type": {"text/html; charset=utf-8"}}
	if d.Kind == theme.KindSearch {
		h.Set("Cache-Control", "private, no-store")
		h.Set("X-Robots-Tag", "noindex")
	}
	return &response{status: status, header: h, body: buf.Bytes()}, nil
}

func (st *Site) basePage(snap *snapshot, r *http.Request, kind string) *theme.PageData {
	s := snap.settings.Site
	return &theme.PageData{
		Kind:    kind,
		Site:    theme.Site{Title: s.Title, Tagline: s.Tagline, URL: snap.base, Language: s.Language},
		Request: theme.Request{Path: r.URL.Path, URL: snap.abs(r.URL.Path), Query: r.URL.RawQuery, Paged: 1},
	}
}

// pageData loads everything a page needs. A nil result means the page turned out not to exist.
func (st *Site) pageData(ctx context.Context, snap *snapshot, r *http.Request, res Result) (*theme.PageData, int, error) {
	d := st.basePage(snap, r, res.Kind)
	d.Request.Paged = max(1, res.Paged)
	feed := func(archive string) { d.FeedURL = snap.abs(snap.perm.Feed(archive)) }
	var ok bool
	var err error

	switch res.Kind {
	case theme.Kind404, theme.Kind410:
		return d, res.Status, nil

	case theme.KindSingle, theme.KindPage, theme.KindFrontPage:
		if d.Post, err = st.single(ctx, snap, res.ID); d.Post == nil || err != nil {
			return nil, 0, err
		}
		return d, http.StatusOK, nil

	case theme.KindHome, theme.KindPostsPage:
		archive := "/"
		if res.Kind == theme.KindPostsPage {
			if d.Post, err = st.single(ctx, snap, res.ID); d.Post == nil || err != nil {
				return nil, 0, err
			}
			archive = snap.pagePath(res.ID)
		}
		feed("/")
		ok, err = st.list(ctx, snap, d, "home", filter{sticky: true}, archive)

	case theme.KindCategory, theme.KindTag:
		d.Term = snap.termView(res.ID)
		f := filter{termIDs: []int64{res.ID}}
		if res.Kind == theme.KindCategory {
			f.termIDs = snap.termDescendants(res.ID)
		}
		archive := snap.perm.Term(d.Term.Taxonomy, d.Term.Path)
		feed(archive)
		ok, err = st.list(ctx, snap, d, res.Kind, f, archive)
		ok = ok && len(d.Posts) > 0 // §8.1: empty archives are 404

	case theme.KindAuthor:
		d.Author = snap.authorView(res.ID)
		archive := snap.perm.Author(d.Author.Slug)
		feed(archive)
		ok, err = st.list(ctx, snap, d, "author", filter{authorID: res.ID}, archive)
		ok = ok && len(d.Posts) > 0

	case theme.KindDate:
		start := time.Date(res.Year, time.January, 1, 0, 0, 0, 0, snap.loc)
		end := start.AddDate(1, 0, 0)
		archive := snap.perm.Year(res.Year)
		if res.Month > 0 {
			start = time.Date(res.Year, time.Month(res.Month), 1, 0, 0, 0, 0, snap.loc)
			end = start.AddDate(0, 1, 0)
			archive = snap.perm.Month(res.Year, res.Month)
		}
		d.Date = &theme.DateArchive{Year: res.Year, Month: res.Month, Start: start, YearURL: snap.abs(snap.perm.Year(res.Year))}
		ok, err = st.list(ctx, snap, d, "date", filter{from: &start, to: &end}, archive)
		ok = ok && len(d.Posts) > 0

	case theme.KindSearch:
		ok, err = st.searchPage(ctx, snap, d, res.Query)
	}
	if err != nil || !ok {
		return nil, 0, err
	}
	return d, http.StatusOK, nil
}

func (st *Site) single(ctx context.Context, snap *snapshot, id int64) (*theme.Post, error) {
	rows, err := st.postsByID(ctx, []int64{id})
	if err != nil || len(rows) == 0 {
		return nil, err
	}
	if err := st.hydrate(ctx, snap, rows, hydrateOpts{render: st.cfg.Dev}); err != nil {
		return nil, err
	}
	return rows[0].view, nil
}

func (st *Site) perPage(snap *snapshot, kind string) int {
	if n := st.theme.Manifest().PostsPerPage[kind]; n > 0 {
		return n
	}
	return snap.settings.Reading.PostsPerPage
}

func views(rows []*postRow) []*theme.Post {
	out := make([]*theme.Post, len(rows))
	for i, p := range rows {
		out[i] = p.view
	}
	return out
}

// list loads one page of an archive. ok is false when the page number is past the end.
func (st *Site) list(ctx context.Context, snap *snapshot, d *theme.PageData, kind string, f filter, archive string) (bool, error) {
	per, paged := st.perPage(snap, kind), d.Request.Paged
	rows, total, err := st.listPosts(ctx, f, per, (paged-1)*per)
	if err != nil || (paged > 1 && len(rows) == 0) {
		return false, err
	}
	if err := st.hydrate(ctx, snap, rows, hydrateOpts{teaser: true}); err != nil {
		return false, err
	}
	d.Posts = views(rows)
	d.Pagination = theme.NewPagination(paged, (total+per-1)/per, func(n int) string {
		return snap.abs(snap.perm.Paged(archive, n))
	})
	return true, nil
}

func (st *Site) searchPage(ctx context.Context, snap *snapshot, d *theme.PageData, q string) (bool, error) {
	if utf8.RuneCountInString(q) > maxQueryRunes {
		q = string([]rune(q)[:maxQueryRunes])
	}
	d.Search, d.Request.NoIndex = &theme.Search{Query: q}, true
	per, paged := st.perPage(snap, "search"), d.Request.Paged
	rows, total, err := st.search(ctx, q, per, (paged-1)*per)
	if err != nil || (paged > 1 && len(rows) == 0) {
		return false, err
	}
	if err := st.hydrate(ctx, snap, rows, hydrateOpts{teaser: true}); err != nil {
		return false, err
	}
	d.Posts, d.Search.Total = views(rows), total
	d.Pagination = theme.NewPagination(paged, (total+per-1)/per, func(n int) string {
		v := url.Values{"q": {q}}
		if n > 1 {
			v.Set("page", strconv.Itoa(n))
		}
		return snap.abs(snap.perm.slash("/search/")) + "?" + v.Encode()
	})
	return true, nil
}
