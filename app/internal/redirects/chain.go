package redirects

import (
	"context"
	"net/url"
	"strconv"
	"strings"
)

// MaxHops is how far a target is followed before it counts as a chain that's too long to flatten.
const MaxHops = 10

// Router is the public site's request handling minus the redirects table, which the checks supply themselves.
type Router interface {
	BaseURL() string
	Canonical(path string) string
	// Before covers what answers ahead of exact redirects: /admin, theme assets, SEO files, media, legacy upload
	// paths and WordPress query URLs.
	Before(ctx context.Context, path, query string) (Outcome, error)
	// Content covers content and archive routing of a canonical path, which runs before prefix and regex redirects.
	Content(ctx context.Context, path string) (Outcome, error)
}

// Outcome is a status (0 = nothing answered) and, for 3xx, the Location.
type Outcome struct {
	Status   int
	Location string
}

func isRedirect(status int) bool { return status >= 300 && status < 400 }

// Hop is one redirect on the way from a target to where it ends up.
type Hop struct {
	From       string `json:"from"`
	To         string `json:"to,omitempty"`
	Status     int    `json:"status"`
	Via        string `json:"via"` // redirect, canonical or site
	RedirectID int64  `json:"redirect_id,omitempty"`
	rule       *Rule
}

// Where a trace ends.
const (
	EndPage     = "page"
	EndExternal = "external"
	EndNotFound = "not_found"
	EndGone     = "gone"
	EndLoop     = "loop"
	EndTooLong  = "too_long"
)

// Trace is the path a visitor's browser takes from a URL.
type Trace struct {
	Hops  []Hop
	End   string
	Final string // where it lands; empty for gone, loop and too_long
}

func (t *Trace) through(r *Rule) bool {
	for _, h := range t.Hops {
		if h.rule == r {
			return true
		}
	}
	return false
}

// internal splits a same-site URL into its unescaped path and raw query. External URLs return ok=false.
func internal(loc, base string) (path, query string, ok bool) {
	u, err := url.Parse(loc)
	if err != nil {
		return "", "", false
	}
	if u.Scheme != "" || u.Host != "" {
		b, _ := url.Parse(base)
		if b == nil || !strings.EqualFold(u.Host, b.Host) {
			return "", "", false
		}
	}
	if !strings.HasPrefix(u.Path, "/") {
		return "", "", false
	}
	return u.Path, u.RawQuery, true
}

func joinQuery(path, query string) string {
	u := url.URL{Path: path, RawQuery: query}
	return u.String()
}

// Resolve follows loc through t and the site, in the public resolver's order: Before, exact redirects (raw, then
// canonical path), the canonical 301, content, then prefix and regex redirects.
func Resolve(ctx context.Context, rt Router, t *Table, loc string) (Trace, error) {
	var tr Trace
	seen := map[string]bool{}
	end := func(kind, final string) (Trace, error) {
		tr.End, tr.Final = kind, final
		return tr, nil
	}
	hop := func(from, to string, status int, via string, r *Rule) {
		h := Hop{From: from, To: to, Status: status, Via: via, rule: r}
		if r != nil {
			h.RedirectID = r.ID
		}
		tr.Hops = append(tr.Hops, h)
	}
	for {
		path, query, ok := internal(loc, rt.BaseURL())
		if !ok {
			return end(EndExternal, loc)
		}
		key := path + "?" + query
		if seen[key] {
			return end(EndLoop, "")
		}
		seen[key] = true
		if len(tr.Hops) >= MaxHops {
			return end(EndTooLong, "")
		}

		o, err := rt.Before(ctx, path, query)
		if err != nil {
			return tr, err
		}
		if o.Status != 0 {
			if isRedirect(o.Status) {
				hop(loc, o.Location, o.Status, "site", nil)
				loc = o.Location
				continue
			}
			return end(pageOr404(o.Status), loc)
		}

		canon := rt.Canonical(path)
		if r := t.exactFor(path, canon); r != nil {
			if r.Gone() {
				hop(loc, "", r.Status, "redirect", r)
				return end(EndGone, "")
			}
			hop(loc, r.Target, r.Status, "redirect", r)
			loc = r.Target
			continue
		}
		if canon != path {
			next := joinQuery(canon, query)
			hop(loc, next, 301, "canonical", nil)
			loc = next
			continue
		}

		if o, err = rt.Content(ctx, canon); err != nil {
			return tr, err
		}
		if isRedirect(o.Status) {
			hop(loc, o.Location, o.Status, "site", nil)
			loc = o.Location
			continue
		}
		if o.Status != 0 {
			return end(pageOr404(o.Status), loc)
		}

		if r, target := t.set.Match(canon); r != nil {
			if r.Gone() {
				hop(loc, "", r.Status, "redirect", r)
				return end(EndGone, "")
			}
			hop(loc, target, r.Status, "redirect", r)
			loc = target
			continue
		}
		return end(EndNotFound, loc)
	}
}

func pageOr404(status int) string {
	if status == 200 {
		return EndPage
	}
	return EndNotFound
}

// ChainError rejects a redirect whose target is itself redirected (chain, with the target to flatten to), leads back
// to the redirect (loop), ends at a 410/451 (gone) or takes more than MaxHops (too_long).
type ChainError struct {
	Code      string
	Hops      []Hop
	Flattened string
}

func (e *ChainError) Error() string {
	switch e.Code {
	case "chain":
		return "the target redirects again (" + strconv.Itoa(len(e.Hops)) + " hops); point it at " + e.Flattened
	case "loop":
		return "the target leads back to this redirect"
	case "gone":
		return "the target is answered with a " + strconv.Itoa(e.Hops[len(e.Hops)-1].Status)
	}
	return "the target redirects more than " + strconv.Itoa(MaxHops) + " times"
}

// check follows r's target with r already in t. It returns a ChainError for chains, loops and gone targets, plus
// warnings that don't block the save.
func check(ctx context.Context, rt Router, t *Table, r *Rule) ([]string, error) {
	var warns []string
	if r.Match == "exact" {
		if o, err := rt.Before(ctx, r.Source, ""); err != nil {
			return nil, err
		} else if o.Status != 0 {
			return nil, &FieldError{Field: "source", Message: "this path is answered before redirects run (admin, media, " +
				"sitemaps or a WordPress URL), so the redirect would never apply"}
		}
		o, err := rt.Content(ctx, r.Source)
		if err != nil {
			return nil, err
		}
		if o.Status == 200 {
			warns = append(warns, "the source is a live page; the redirect hides it")
		}
	}
	if r.Gone() {
		return warns, nil
	}
	if r.Match == "regex" && strings.Contains(r.Target, "$") {
		return append(warns, "targets that use $ groups aren't checked for chains"), nil
	}
	tr, err := Resolve(ctx, rt, t, r.Target)
	if err != nil {
		return nil, err
	}
	switch {
	case tr.through(r) || tr.End == EndLoop:
		return nil, &ChainError{Code: "loop", Hops: tr.Hops}
	case tr.End == EndGone:
		return nil, &ChainError{Code: "gone", Hops: tr.Hops}
	case tr.End == EndTooLong:
		return nil, &ChainError{Code: "too_long", Hops: tr.Hops}
	case len(tr.Hops) > 0:
		return nil, &ChainError{Code: "chain", Hops: tr.Hops, Flattened: tr.Final}
	case tr.End == EndNotFound:
		warns = append(warns, "the target is a 404")
	}
	return warns, nil
}
