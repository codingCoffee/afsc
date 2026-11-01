package site

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"afsc/internal/media"
	"afsc/internal/redirects"
	"afsc/internal/theme"
)

// Router is the public resolver without the redirects table, for the redirect manager's chain checks.
func (st *Site) Router() redirects.Router { return router{st} }

type router struct{ st *Site }

func (r router) BaseURL() string { return r.st.cfg.BaseURL }

func (r router) Canonical(path string) string {
	return Canonical(path, r.st.snap.Load().settings.Permalinks.TrailingSlash)
}

func outcome(res Result) redirects.Outcome {
	return redirects.Outcome{Status: res.Status, Location: res.Location}
}

const sqlLegacyPathExists = `SELECT EXISTS (SELECT 1 FROM media_legacy_paths WHERE path = $1)`

func (r router) Before(ctx context.Context, path, query string) (redirects.Outcome, error) {
	st, snap := r.st, r.st.snap.Load()
	switch {
	case path == "/admin" || strings.HasPrefix(path, "/admin/"):
		return redirects.Outcome{Status: http.StatusNotFound}, nil
	case strings.HasPrefix(path, theme.AssetPrefix), strings.HasPrefix(path, media.Prefix):
		return redirects.Outcome{Status: http.StatusOK}, nil
	}
	if res, ok := snap.seoFile(path); ok {
		return outcome(res), nil
	}
	last := path[strings.LastIndexByte(path, '/')+1:]
	if strings.Contains(last, ".") {
		var found bool
		if err := st.db.QueryRow(ctx, sqlLegacyPathExists, path).Scan(&found); err != nil {
			return redirects.Outcome{}, err
		}
		if found {
			return redirects.Outcome{Status: http.StatusOK}, nil
		}
	}
	if query != "" && snap.settings.Permalinks.WPCompatEnabled && Canonical(path, snap.settings.Permalinks.TrailingSlash) == "/" {
		q, _ := url.ParseQuery(query)
		res, ok, err := st.wpcompat(ctx, snap, q)
		if err != nil || ok {
			return outcome(res), err
		}
	}
	return redirects.Outcome{}, nil
}

func (r router) Content(ctx context.Context, path string) (redirects.Outcome, error) {
	st, snap := r.st, r.st.snap.Load()
	routes, redirect := snap.perm.Match(path)
	if redirect != "" {
		return redirects.Outcome{Status: http.StatusMovedPermanently, Location: location(redirect, "")}, nil
	}
	for _, rt := range routes {
		res, ok, err := st.route(ctx, snap, rt, path, url.Values{})
		if err != nil || ok {
			return outcome(res), err
		}
	}
	return redirects.Outcome{}, nil
}
