package site

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"afsc/internal/theme"
)

// Result is what the resolver decided: a redirect (Location set), a page or feed to render, or an error page.
type Result struct {
	Status   int
	Location string
	Kind     string // a theme kind, or "feed"
	ID       int64  // post, term or author id
	FeedOf   string // feeds: home category tag author
	Paged    int
	Year     int
	Month    int
	Query    string // search
	File     string // sitemaps: the type or "index"; text files: the path
}

const kindFeed = "feed"

func gone(status int) Result { return Result{Status: status, Kind: theme.Kind410} }

func notFound() Result { return Result{Status: http.StatusNotFound, Kind: theme.Kind404} }

func redirectTo(rd *Redirect, target string) Result {
	if rd.Status == http.StatusGone || rd.Status == http.StatusUnavailableForLegalReasons {
		return gone(rd.Status)
	}
	return Result{Status: rd.Status, Location: target}
}

// resolve runs the §6.1 order: SEO files, wpcompat, exact redirects, canonical form, content, prefix/regex
// redirects, 404.
func (st *Site) resolve(ctx context.Context, snap *snapshot, r *http.Request) (Result, error) {
	raw := r.URL.Path
	if raw == "/admin" || strings.HasPrefix(raw, "/admin/") {
		return notFound(), nil
	}
	if res, ok := snap.seoFile(raw); ok {
		return res, nil
	}
	canon := Canonical(raw, snap.settings.Permalinks.TrailingSlash)
	q := r.URL.Query()

	if canon == "/" && snap.settings.Permalinks.WPCompatEnabled {
		if res, ok, err := st.wpcompat(ctx, snap, q); ok || err != nil {
			return res, err
		}
	}

	paths := []string{raw}
	if canon != raw {
		paths = append(paths, canon)
	}
	rd, err := st.lk.ExactRedirect(ctx, paths)
	if err != nil {
		return Result{}, err
	}
	if rd != nil {
		st.rh.hit(rd.ID, st.now())
		return redirectTo(rd, rd.Target), nil
	}
	if canon != raw {
		return Result{Status: http.StatusMovedPermanently, Location: location(canon, r.URL.RawQuery)}, nil
	}

	routes, redirect := snap.perm.Match(canon)
	if redirect != "" {
		return Result{Status: http.StatusMovedPermanently, Location: location(redirect, r.URL.RawQuery)}, nil
	}
	for _, rt := range routes {
		res, ok, err := st.route(ctx, snap, rt, canon, q)
		if err != nil || ok {
			return res, err
		}
	}

	if rd, target := snap.matchRedirect(canon); rd != nil {
		st.rh.hit(rd.ID, st.now())
		return redirectTo(rd, target), nil
	}
	return notFound(), nil
}

func (st *Site) route(ctx context.Context, snap *snapshot, rt Route, path string, q url.Values) (Result, bool, error) {
	ok := func(res Result) (Result, bool, error) { return res, true, nil }
	moved := func(p string) (Result, bool, error) {
		return ok(Result{Status: http.StatusMovedPermanently, Location: location(p, "")})
	}
	page := Result{Status: http.StatusOK, Paged: rt.Paged}
	p := snap.settings.Permalinks
	now := st.now()

	switch rt.Kind {
	case "home":
		if rt.Feed {
			return ok(Result{Status: http.StatusOK, Kind: kindFeed, FeedOf: "home"})
		}
		if hp := snap.pages[snap.homeID]; hp != nil && hp.Visible(now) {
			if rt.Paged > 1 {
				return Result{}, false, nil
			}
			page.Kind, page.ID = theme.KindFrontPage, hp.ID
			return ok(page)
		}
		page.Kind = theme.KindHome
		return ok(page)

	case "search":
		page.Kind, page.Query = theme.KindSearch, q.Get("q")
		if n, err := strconv.Atoi(q.Get("page")); err == nil && n > 1 {
			page.Paged = n
		}
		return ok(page)

	case "year", "month":
		if !p.DateArchivesEnabled {
			return moved("/")
		}
		page.Kind, page.Year, page.Month = theme.KindDate, rt.Year, rt.Month
		return ok(page)

	case "category", "tag":
		key := rt.Kind + ":" + strings.Join(rt.Slugs, "/")
		if rt.Kind == "tag" {
			key = "tag:" + rt.Slug
		}
		id := snap.termPaths[key]
		if id == 0 {
			return Result{}, false, nil
		}
		if rt.Feed {
			return ok(Result{Status: http.StatusOK, Kind: kindFeed, FeedOf: rt.Kind, ID: id})
		}
		page.Kind, page.ID = rt.Kind, id
		return ok(page)

	case "author":
		id := snap.authorSlugs[rt.Slug]
		if id == 0 {
			return Result{}, false, nil
		}
		if !p.AuthorArchivesEnabled {
			return moved("/")
		}
		if rt.Feed {
			return ok(Result{Status: http.StatusOK, Kind: kindFeed, FeedOf: "author", ID: id})
		}
		page.Kind, page.ID = theme.KindAuthor, id
		return ok(page)

	case "page":
		pg := snap.pageByPath(rt.Slugs)
		if pg == nil || !pg.Visible(now) {
			return Result{}, false, nil
		}
		canonical := snap.postPath(&pg.PostRef)
		switch {
		case rt.Feed || canonical == "/":
			return moved(canonical)
		case pg.ID == snap.postsPageID && snap.homeID != 0:
			page.Kind, page.ID = theme.KindPostsPage, pg.ID
			return ok(page)
		case rt.Paged > 1:
			return Result{}, false, nil
		}
		page.Kind, page.ID = theme.KindPage, pg.ID
		return ok(page)

	case "post":
		ref, err := st.lk.Post(ctx, rt.Slug, rt.ID)
		if err != nil {
			return Result{}, true, err
		}
		if ref == nil || !ref.Visible(now) || rt.Paged > 1 || (rt.ID > 0 && rt.Slug != "" && ref.Slug != rt.Slug) {
			return Result{}, false, nil
		}
		if canonical := snap.postPath(ref); rt.Feed || canonical != path {
			return moved(canonical)
		}
		page.Kind, page.ID = theme.KindSingle, ref.ID
		return ok(page)
	}
	return Result{}, false, nil
}
