// Package site serves the public, themed site: routing, archives, feeds and search (build plan §6).
package site

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"

	"afsc/internal/settings"
)

// Permalinks builds and parses public URL paths from the permalink settings. All paths are unescaped.
type Permalinks struct {
	s      settings.Permalinks
	loc    *time.Location
	postRe *regexp.Regexp
}

var tokenRe = map[string]string{
	"slug": `(?P<slug>[^/]+)`, "year": `(?P<year>\d{4})`, "month": `(?P<month>\d{2})`,
	"day": `(?P<day>\d{2})`, "id": `(?P<id>\d+)`, "category": `(?P<category>.+?)`,
}

func NewPermalinks(p settings.Permalinks, loc *time.Location) (*Permalinks, error) {
	pat := p.PostPattern
	if !strings.HasSuffix(pat, "/") {
		pat += "/"
	}
	var re strings.Builder
	re.WriteString("^")
	seen := map[string]bool{}
	for rest := pat; rest != ""; {
		i := strings.IndexByte(rest, '{')
		if i < 0 {
			re.WriteString(regexp.QuoteMeta(rest))
			break
		}
		j := strings.IndexByte(rest[i:], '}')
		if j < 0 {
			return nil, fmt.Errorf("post_pattern: unclosed token")
		}
		tok := rest[i+1 : i+j]
		if tokenRe[tok] == "" || seen[tok] {
			return nil, fmt.Errorf("post_pattern: unknown or repeated token {%s}", tok)
		}
		seen[tok] = true
		re.WriteString(regexp.QuoteMeta(rest[:i]) + tokenRe[tok])
		rest = rest[i+j+1:]
	}
	re.WriteString("$")
	rx, err := regexp.Compile(re.String())
	if err != nil {
		return nil, err
	}
	return &Permalinks{s: p, loc: loc, postRe: rx}, nil
}

func (p *Permalinks) slash(path string) string {
	switch {
	case path == "" || path == "/":
		return "/"
	case p.s.TrailingSlash == "never":
		return strings.TrimSuffix(path, "/")
	case !strings.HasSuffix(path, "/"):
		return path + "/"
	}
	return path
}

func join(segs []string) string {
	if len(segs) == 0 {
		return "/"
	}
	return "/" + strings.Join(segs, "/") + "/"
}

// PostRef is the minimum needed to build a post or page URL and decide whether it is public.
type PostRef struct {
	ID          int64
	LegacyKey   string
	Type        string
	Slug        string
	Status      string
	PublishedAt *time.Time
	ParentID    int64
	CategoryID  int64 // primary category, else the lowest-id category
}

// Visible reports whether the public site may show the post at now.
func (r *PostRef) Visible(now time.Time) bool {
	return r.Status == "published" && r.PublishedAt != nil && !r.PublishedAt.After(now)
}

// Post builds a post path. catPath is the slash-joined primary category path, empty when it has none.
func (p *Permalinks) Post(r *PostRef, catPath string) string {
	var t time.Time
	if r.PublishedAt != nil {
		t = r.PublishedAt.In(p.loc)
	}
	if catPath == "" {
		catPath = "uncategorized"
	}
	rep := strings.NewReplacer(
		"{slug}", r.Slug, "{id}", strconv.FormatInt(r.ID, 10), "{category}", catPath,
		"{year}", fmt.Sprintf("%04d", t.Year()), "{month}", fmt.Sprintf("%02d", int(t.Month())), "{day}", fmt.Sprintf("%02d", t.Day()),
	)
	return p.slash(rep.Replace(p.s.PostPattern))
}

func (p *Permalinks) Page(slugs []string) string { return p.slash(join(slugs)) }

func (p *Permalinks) Term(taxonomy, path string) string {
	base := p.s.CategoryBase
	if taxonomy == "tag" {
		base = p.s.TagBase
	}
	return p.slash("/" + base + "/" + path + "/")
}

func (p *Permalinks) Author(slug string) string {
	return p.slash("/" + p.s.AuthorBase + "/" + slug + "/")
}
func (p *Permalinks) Year(y int) string     { return p.slash(fmt.Sprintf("/%04d/", y)) }
func (p *Permalinks) Month(y, m int) string { return p.slash(fmt.Sprintf("/%04d/%02d/", y, m)) }

// Feed is the feed path of an archive path ("/" for the main feed).
func (p *Permalinks) Feed(archive string) string {
	return p.slash(strings.TrimSuffix(archive, "/") + "/feed/")
}

// Paged is page n of an archive path; page 1 is the archive itself.
func (p *Permalinks) Paged(archive string, n int) string {
	if n <= 1 {
		return archive
	}
	return p.slash(strings.TrimSuffix(archive, "/") + "/page/" + strconv.Itoa(n) + "/")
}

// Route is one way to read a request path. Match returns candidates in precedence order.
type Route struct {
	Kind     string // home search year month category tag author page post
	Slugs    []string
	Slug     string
	ID       int64
	Year     int
	Month    int
	Day      int
	Category string
	Paged    int
	Feed     bool
}

var feedFormats = map[string]bool{"rss2": true, "rss": true, "atom": true, "rdf": true}

func digits(s string, n int) (int, bool) {
	if len(s) != n {
		return 0, false
	}
	v, err := strconv.Atoi(s)
	return v, err == nil && strings.Trim(s, "0123456789") == ""
}

// Match parses a canonical path. A non-empty redirect means the path has a canonical form elsewhere.
func (p *Permalinks) Match(path string) (routes []Route, redirect string) {
	var segs []string
	for _, s := range strings.Split(path, "/") {
		if s != "" {
			segs = append(segs, s)
		}
	}
	paged, feed := 1, false
	if n := len(segs); n >= 2 && segs[n-2] == "page" {
		if v, err := strconv.Atoi(segs[n-1]); err == nil && strings.Trim(segs[n-1], "0123456789") == "" && v >= 1 {
			if v == 1 {
				return nil, p.slash(join(segs[:n-2]))
			}
			paged, segs = v, segs[:n-2]
		}
	}
	if n := len(segs); n >= 2 && segs[n-2] == "feed" && feedFormats[segs[n-1]] {
		if paged > 1 {
			return nil, ""
		}
		return nil, p.slash(join(segs[:n-1]))
	}
	if n := len(segs); n >= 1 && segs[n-1] == "feed" {
		if paged > 1 {
			return nil, ""
		}
		feed, segs = true, segs[:n-1]
	}

	add := func(r Route) {
		r.Paged, r.Feed = paged, feed
		routes = append(routes, r)
	}
	n := len(segs)
	if n == 0 {
		add(Route{Kind: "home"})
		return routes, ""
	}
	if n == 1 && segs[0] == "search" {
		if !feed && paged == 1 {
			add(Route{Kind: "search"})
		}
		return routes, ""
	}
	if y, ok := digits(segs[0], 4); ok && !feed {
		if n == 1 {
			add(Route{Kind: "year", Year: y})
		} else if m, ok := digits(segs[1], 2); ok && n == 2 && m >= 1 && m <= 12 {
			add(Route{Kind: "month", Year: y, Month: m})
		}
	}
	switch {
	case n >= 2 && segs[0] == p.s.CategoryBase:
		add(Route{Kind: "category", Slugs: segs[1:]})
	case n == 2 && segs[0] == p.s.TagBase:
		add(Route{Kind: "tag", Slug: segs[1]})
	case n == 2 && segs[0] == p.s.AuthorBase:
		add(Route{Kind: "author", Slug: segs[1]})
	}
	add(Route{Kind: "page", Slugs: segs})
	if m := p.postRe.FindStringSubmatch(join(segs)); m != nil {
		r := Route{Kind: "post"}
		for i, name := range p.postRe.SubexpNames() {
			v := m[i]
			switch name {
			case "slug":
				r.Slug = v
			case "id":
				r.ID, _ = strconv.ParseInt(v, 10, 64)
			case "year":
				r.Year, _ = strconv.Atoi(v)
			case "month":
				r.Month, _ = strconv.Atoi(v)
			case "day":
				r.Day, _ = strconv.Atoi(v)
			case "category":
				r.Category = v
			}
		}
		add(r)
	}
	return routes, ""
}
