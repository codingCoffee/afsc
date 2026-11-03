package transform

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/redirects"
)

type RedirectReport struct {
	ByNote        map[string]int       `json:"by_note"`
	Conflicts     []redirects.Conflict `json:"conflicts"`
	Shadowed      []string             `json:"shadowed"`
	SelfLoops     []string             `json:"self_loops"`
	Unconvertible []string             `json:"unconvertible_regex"`
	OldDomains    []string             `json:"old_domains"`
}

// RegexID names an unconvertible Yoast regex in decisions.json.
func RegexID(origin string) string {
	h := sha1.Sum([]byte(origin))
	return hex.EncodeToString(h[:])[:10]
}

func asString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

// YoastRedirects reads wpseo-premium-redirects-base, a list (or origin-keyed map) of origin/url/type/format.
func YoastRedirects(v any) []map[string]string {
	var raw []any
	switch x := v.(type) {
	case []any:
		raw = x
	case map[string]any:
		for _, k := range sortedKeys(x) {
			if m, ok := x[k].(map[string]any); ok {
				if _, has := m["origin"]; !has {
					m["origin"] = k
				}
				raw = append(raw, m)
			}
		}
	}
	var out []map[string]string
	for _, r := range raw {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		out = append(out, map[string]string{"origin": asString(m["origin"]), "url": asString(m["url"]),
			"type": asString(m["type"]), "format": asString(m["format"])})
	}
	return out
}

func (s *state) target(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return "/"
	}
	if !strings.Contains(t, "://") && !strings.HasPrefix(t, "/") {
		t = "/" + t
	}
	out, ok := s.rw.Rewrite(t)
	if !ok {
		return "/"
	}
	// AFSC would 301 a non-canonical internal target, turning this redirect into a chain.
	if strings.HasPrefix(out, "/") && !strings.ContainsAny(out, "?#$") && !strings.Contains(path.Base(out), ".") {
		switch {
		case s.settings.All.Permalinks.TrailingSlash == "always" && !strings.HasSuffix(out, "/"):
			out += "/"
		case s.settings.All.Permalinks.TrailingSlash == "never" && out != "/":
			out = strings.TrimSuffix(out, "/")
		}
	}
	return out
}

func (s *state) redirects(pages map[int64]int) ([]*records.Redirect, error) {
	trailing := s.settings.All.Permalinks.TrailingSlash
	b := redirects.NewBuilder(s.cfg.Source, trailing)
	rr := RedirectReport{ByNote: map[string]int{}}
	add := func(src, match, target string, status int, note string) {
		rr.ByNote[note]++
		b.Add(src, match, target, status, note)
	}
	if s.set.On("yoast_premium") {
		for _, y := range YoastRedirects(s.ds.Option(s.set.Key("yoast_premium", "opt_redirects"))) {
			status, _ := strconv.Atoi(y["type"])
			if status == 0 {
				status = 301
			}
			match, src, err := redirects.YoastFormat(y["origin"], y["format"])
			if err != nil {
				id := RegexID(y["origin"])
				switch s.decide("redirect_regex", id, "") {
				case "rewrite":
					src = s.option("redirect_regex", id, "source")
					if _, err := regexp.Compile(src); err != nil {
						return nil, fmt.Errorf("redirect_regex:%s rewrite is not valid RE2: %v", id, err)
					}
				default:
					rr.Unconvertible = append(rr.Unconvertible, y["origin"]+" ("+err.Error()+")")
					continue
				}
			}
			add(src, match, s.target(y["url"]), status, "yoast-premium")
		}
	}
	structure := s.ds.OptionString("permalink_structure")
	for _, p := range s.ds.Posts {
		ap, ok := s.postPath[p.ID]
		if !ok {
			continue
		}
		if s.set.On("yoast") {
			if t := strings.TrimSpace(p.Meta.First(s.set.Key("yoast", "redirect"))); t != "" {
				add(ap, "exact", s.target(t), 301, "yoast-post")
			}
		}
		if structure == "" {
			continue
		}
		d := s.date(p).In(s.loc)
		for _, old := range p.Meta[s.set.Key("core", "old_slug")] {
			if old = strings.TrimSpace(old); old != "" && old != p.Name {
				add(s.wpPostPath(p, old, d), "exact", ap, 301, "old-slug")
			}
		}
		for _, od := range p.Meta[s.set.Key("core", "old_date")] {
			t, err := time.ParseInLocation("2006-01-02", strings.TrimSpace(od), s.loc)
			if err != nil {
				continue
			}
			old := time.Date(t.Year(), t.Month(), t.Day(), d.Hour(), d.Minute(), d.Second(), 0, s.loc)
			add(s.wpPostPath(p, p.Name, old), "exact", ap, 301, "old-date")
		}
		for n := 2; n <= pages[p.ID]; n++ {
			add(strings.TrimSuffix(ap, "/")+"/"+strconv.Itoa(n)+"/", "exact", ap, 301, "nextpage")
		}
	}
	if structure != "" {
		for _, p := range s.ds.Posts {
			if p.Type != "attachment" || p.Name == "" {
				continue
			}
			target, ok := s.attachmentTarget(p.ID)
			if !ok {
				continue
			}
			name := p.Name
			if _, err := strconv.Atoi(name); err == nil || strings.Contains(structure, "%category%") {
				name = "attachment/" + name
			}
			src := "/" + name + "/"
			if pp, ok := s.wpPath[p.Parent]; ok && !strings.Contains(pp, "?") {
				src = strings.TrimSuffix(pp, "/") + "/" + name + "/"
			}
			add(src, "exact", target, 301, "attachment")
		}
	}
	// A post type's WordPress archive (/updates/ for /updates/%postname%/) moves to its category's archive.
	for _, typ := range sortedKeys(s.typeCat) {
		seg, _, _ := strings.Cut(strings.TrimPrefix(s.cfg.Site.PostTypes[typ].WPPath, "/"), "/")
		if target, ok := s.termPath[s.typeCat[typ]]; ok && seg != "" && !strings.Contains(seg, "%") && target != "/"+seg+"/" {
			add("/"+seg+"/", "exact", target, 301, "type-archive")
		}
	}
	for _, old := range sortedKeys(s.pathMap) {
		add(old, "exact", s.pathMap[old], 301, "path-change")
	}
	for _, u := range s.o.Baseline {
		if p, ok := s.dayArchive(u); ok {
			add(p[0], "exact", p[1], 301, "day-archive")
		}
	}
	out, err := b.Finish(func(p string) bool { return s.live[p] })
	if err != nil {
		return nil, err
	}
	rr.Conflicts, rr.Shadowed, rr.SelfLoops = b.Conflicts, b.Shadowed, b.SelfLoops
	rr.OldDomains = s.cfg.Site.OldHosts
	s.rep.Redirects = rr
	return out, nil
}

var dayRe = regexp.MustCompile(`^/(\d{4})/(\d{2})/(\d{2})/?$`)

// dayArchive maps a WordPress day archive URL to its month archive; AFSC has no day archives.
func (s *state) dayArchive(raw string) ([2]string, bool) {
	u, err := url.Parse(raw)
	if err != nil || !s.settings.All.Permalinks.DateArchivesEnabled {
		return [2]string{}, false
	}
	m := dayRe.FindStringSubmatch(u.Path)
	if m == nil {
		return [2]string{}, false
	}
	y, _ := strconv.Atoi(m[1])
	mo, _ := strconv.Atoi(m[2])
	return [2]string{u.Path, s.afsc.Month(y, mo)}, true
}

// URLExpect is the expected AFSC outcome for one URL (work/url-map.ndjson, §7).
type URLExpect struct {
	URL    string `json:"url"`
	Path   string `json:"path"`
	Expect string `json:"expect"` // 200, 301, 410, 404-approved or unknown
	Target string `json:"target,omitempty"`
	Source string `json:"source"` // baseline or wordpress
}

func (s *state) urlMap(list []*records.Redirect) []URLExpect {
	base := strings.TrimSuffix(s.cfg.Site.BaseURL, "/")
	cands := map[string]string{}
	for _, u := range s.o.Baseline {
		cands[u] = "baseline"
	}
	addPath := func(p string) {
		if p == "" {
			return
		}
		u := base + p
		if _, ok := cands[u]; !ok {
			cands[u] = "wordpress"
		}
	}
	for id := range s.wpPath {
		addPath(s.wpPath[id])
	}
	for tt := range s.termKey {
		if s.live[redirects.Canonical(s.termPath[tt], s.settings.All.Permalinks.TrailingSlash)] {
			addPath(s.wp.Term(s.termByID[tt].Taxonomy, s.termRawPath(tt)))
		}
	}
	for _, r := range list {
		if r.Match == "exact" {
			addPath(r.Source)
		}
	}
	var out []URLExpect
	for u, src := range cands {
		e := s.expect(u, list)
		e.Source = src
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].URL < out[j].URL })
	return out
}

var feedRe = regexp.MustCompile(`^(.*/)?feed/(rss2/|rss/|atom/|rdf/)?$`)
var pagedRe = regexp.MustCompile(`^(.*/)page/(\d+)/?$`)

func (s *state) expect(raw string, list []*records.Redirect) URLExpect {
	trailing := s.settings.All.Permalinks.TrailingSlash
	e := URLExpect{URL: raw}
	u, err := url.Parse(raw)
	if err != nil {
		e.Expect = "unknown"
		return e
	}
	e.Path = u.Path
	if u.RawQuery != "" {
		e.Path += "?" + u.RawQuery
	}
	q := u.Query()
	if u.Path == "/" || u.Path == "" || u.Path == "/index.php" {
		for _, param := range []string{"p", "page_id", "attachment_id", "cat", "author"} {
			if v := q.Get(param); v != "" {
				id, _ := strconv.ParseInt(v, 10, 64)
				if t, ok := s.resolveQuery(param, id); ok {
					e.Expect, e.Target = "301", t
					return e
				}
			}
		}
		if q.Get("s") != "" {
			e.Expect, e.Target = "301", "/search/?q="+url.QueryEscape(q.Get("s"))
			return e
		}
		if q.Get("feed") != "" {
			e.Expect, e.Target = "301", s.afsc.Feed("/")
			return e
		}
	}
	c := redirects.Canonical(u.Path, trailing)
	if r := redirects.Exact(list, c); r != nil {
		if r.Target == nil {
			e.Expect = strconv.Itoa(r.Status)
		} else {
			e.Expect, e.Target = "301", *r.Target
		}
		return e
	}
	if s.live[c] || s.livePrefix(c) {
		if dec, _ := url.PathUnescape(u.Path); dec != c && strings.ToLower(dec) != c {
			e.Expect, e.Target = "301", c
		} else {
			e.Expect = "200"
		}
		return e
	}
	for _, r := range list {
		switch r.Match {
		case "prefix":
			if strings.HasPrefix(u.Path, r.Source) {
				e.Expect, e.Target = strconv.Itoa(r.Status), deref(r.Target)
				if r.Target != nil {
					e.Expect = "301"
				}
				return e
			}
		case "regex":
			if re, err := regexp.Compile(r.Source); err == nil && re.MatchString(u.Path) {
				e.Expect = "301"
				if r.Target != nil {
					e.Target = re.ReplaceAllString(u.Path, *r.Target)
				} else {
					e.Expect = strconv.Itoa(r.Status)
				}
				return e
			}
		}
	}
	for _, g := range s.cfg.Verify.Approved404 {
		if globMatch(g, u.Path) {
			e.Expect = "404-approved"
			return e
		}
	}
	e.Expect = "unknown"
	return e
}

// livePrefix accepts feeds and paginated pages of live archives.
func (s *state) livePrefix(c string) bool {
	trailing := s.settings.All.Permalinks.TrailingSlash
	if m := feedRe.FindStringSubmatch(c); m != nil {
		archive := m[1]
		if archive == "" {
			archive = "/"
		}
		return s.live[redirects.Canonical(archive, trailing)]
	}
	if m := pagedRe.FindStringSubmatch(c); m != nil {
		return s.live[redirects.Canonical(m[1], trailing)]
	}
	return false
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// globMatch matches * across slashes and ? as one character, ignoring case (AFSC's 404 noise-pattern rules).
func globMatch(pattern, p string) bool {
	var re strings.Builder
	re.WriteString("(?i)^")
	for _, r := range pattern {
		switch r {
		case '*':
			re.WriteString(".*")
		case '?':
			re.WriteString(".")
		default:
			re.WriteString(regexp.QuoteMeta(string(r)))
		}
	}
	re.WriteString("$")
	ok, _ := regexp.MatchString(re.String(), p)
	return ok
}
