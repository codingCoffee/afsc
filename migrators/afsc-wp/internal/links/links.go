// Package links rewrites hrefs in content (migration plan §6.6): site hosts become root-relative, ?p= style
// links resolve to canonical AFSC paths, and paths that change follow the same map the redirects use.
package links

import (
	"net/url"
	"strconv"
	"strings"
)

type Rewriter struct {
	Hosts   map[string]bool
	Resolve func(param string, id int64) (string, bool) // p, page_id, attachment_id, cat, tag_id, author
	PathMap map[string]string                           // decoded old path → new path
	// HomePath is the site's path under its host ("/varsity"), or "". Internal links under it lose the prefix;
	// links elsewhere on the host belong to another site and are made absolute with Origin.
	HomePath string
	Origin   string // "https://zerodha.com"
	// Changed records every href that was rewritten, so verification can apply the same mapping.
	Changed map[string]string
}

var queryParams = []string{"p", "page_id", "attachment_id", "cat", "tag_id", "author"}

// Rewrite returns the href to store; ok is false for script-capable schemes.
func (r *Rewriter) Rewrite(href string) (string, bool) {
	out, ok := r.rewrite(href)
	if ok && out != strings.TrimSpace(href) {
		if r.Changed == nil {
			r.Changed = map[string]string{}
		}
		r.Changed[strings.TrimSpace(href)] = out
	}
	return out, ok
}

func (r *Rewriter) rewrite(href string) (string, bool) {
	h := strings.TrimSpace(href)
	lower := strings.ToLower(strings.Map(func(c rune) rune {
		if c <= ' ' {
			return -1
		}
		return c
	}, h))
	for _, bad := range []string{"javascript:", "vbscript:", "data:"} {
		if strings.HasPrefix(lower, bad) {
			return "", false
		}
	}
	if h == "" || strings.HasPrefix(h, "#") || strings.HasPrefix(lower, "mailto:") || strings.HasPrefix(lower, "tel:") {
		return h, true
	}
	u, err := url.Parse(h)
	if err != nil {
		return h, true
	}
	if u.Host != "" {
		if !r.Hosts[strings.ToLower(u.Hostname())] || (u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https") {
			return h, true
		}
	} else if !strings.HasPrefix(u.Path, "/") {
		return h, true
	}
	if r.HomePath != "" {
		ep := u.EscapedPath()
		switch {
		case ep == r.HomePath || strings.HasPrefix(ep, r.HomePath+"/"):
			rest := strings.TrimPrefix(ep, r.HomePath)
			if rest == "" {
				rest = "/"
			}
			nu, err := url.Parse(rest)
			if err != nil {
				return h, true
			}
			u.Path, u.RawPath = nu.Path, nu.RawPath
		case u.Host != "":
			return h, true
		default:
			return r.Origin + h, true
		}
	}
	frag := ""
	if u.Fragment != "" {
		frag = "#" + u.EscapedFragment()
	}
	q := u.Query()
	if u.Path == "" || u.Path == "/" || u.Path == "/index.php" {
		for _, p := range queryParams {
			if v := q.Get(p); v != "" && r.Resolve != nil {
				if id, err := strconv.ParseInt(v, 10, 64); err == nil {
					if target, ok := r.Resolve(p, id); ok {
						return target + frag, true
					}
				}
			}
		}
	}
	path := u.EscapedPath()
	if path == "" {
		path = "/"
	}
	if dec, err := url.PathUnescape(path); err == nil {
		if np, ok := r.lookup(dec); ok {
			path = np
		}
	}
	if u.RawQuery != "" {
		path += "?" + u.RawQuery
	}
	return path + frag, true
}

func (r *Rewriter) lookup(p string) (string, bool) {
	p = strings.ToLower(p)
	if np, ok := r.PathMap[p]; ok {
		return np, true
	}
	if strings.HasSuffix(p, "/") {
		np, ok := r.PathMap[strings.TrimSuffix(p, "/")]
		return np, ok
	}
	np, ok := r.PathMap[p+"/"]
	return np, ok
}
