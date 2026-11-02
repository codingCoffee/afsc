// Package redirects collects redirects from every source (migration plan §7), converts PCRE to RE2, and
// flattens chains so every redirect is a single hop. Loops fail the build.
package redirects

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"

	"afsc/migrators/afsc-wp/internal/records"
)

// Weak redirects only exist because WordPress falls back to them on a 404; a live page at the source wins.
var Weak = map[string]bool{"old-slug": true, "old-date": true, "attachment": true, "nextpage": true, "permalink-change": false}

type Conflict struct {
	Source string `json:"source"`
	Kept   string `json:"kept"`
	Lost   string `json:"lost"`
}

type Builder struct {
	Source        string
	TrailingSlash string // always | never
	list          []*records.Redirect
	exact         map[string]*records.Redirect
	Conflicts     []Conflict
	Shadowed      []string // weak redirects dropped because the source is live
	SelfLoops     []string
}

func NewBuilder(source, trailing string) *Builder {
	return &Builder{Source: source, TrailingSlash: trailing, exact: map[string]*records.Redirect{}}
}

func Key(source, match, src string) string {
	h := sha1.Sum([]byte(match + "\x00" + src))
	return source + ":redirect:" + hex.EncodeToString(h[:])[:12]
}

// Canonical decodes a path, lowercases it and applies the trailing-slash policy (file-like paths keep theirs).
func Canonical(p, trailing string) string {
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	p = strings.ToLower(p)
	if p == "/" || strings.Contains(path.Base(p), ".") {
		return p
	}
	if trailing == "never" {
		return strings.TrimSuffix(p, "/")
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// Add records a redirect. Exact sources are canonicalized; the first redirect for a source wins.
func (b *Builder) Add(src, match, target string, status int, note string) {
	if match == "exact" {
		src = Canonical(src, b.TrailingSlash)
	}
	r := &records.Redirect{LegacyKey: Key(b.Source, match, src), Source: src, Match: match, Status: status, Note: note}
	if status != 410 && status != 451 {
		r.Target = records.Ptr(target)
	}
	if match == "exact" {
		if old, ok := b.exact[src]; ok {
			b.Conflicts = append(b.Conflicts, Conflict{Source: src, Kept: old.Note + " → " + deref(old.Target), Lost: note + " → " + target})
			return
		}
		b.exact[src] = r
	}
	b.list = append(b.list, r)
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// targetPath returns the canonical internal path of a target, "" for external targets.
func (b *Builder) targetPath(t string) string {
	if !strings.HasPrefix(t, "/") || strings.HasPrefix(t, "//") {
		return ""
	}
	return Canonical(t, b.TrailingSlash)
}

// Finish drops weak redirects shadowed by live paths, flattens chains and returns the redirects sorted.
func (b *Builder) Finish(live func(path string) bool) ([]*records.Redirect, error) {
	var kept []*records.Redirect
	for _, r := range b.list {
		if r.Match == "exact" && Weak[r.Note] && live(r.Source) {
			b.Shadowed = append(b.Shadowed, r.Source)
			delete(b.exact, r.Source)
			continue
		}
		kept = append(kept, r)
	}
	var loops []string
	var out []*records.Redirect
	for _, r := range kept {
		if r.Target == nil {
			out = append(out, r)
			continue
		}
		if r.Match == "exact" && b.targetPath(*r.Target) == r.Source {
			b.SelfLoops = append(b.SelfLoops, r.Source)
			continue
		}
		seen := map[string]bool{r.Source: true}
		t := *r.Target
		status := r.Status
		for {
			tp := b.targetPath(t)
			next, ok := b.exact[tp]
			if tp == "" || !ok {
				break
			}
			if seen[tp] {
				loops = append(loops, r.Source)
				break
			}
			seen[tp] = true
			if next.Target == nil {
				status = next.Status
				t = ""
				break
			}
			t = *next.Target
		}
		if t != "" && r.Match == "exact" && b.targetPath(t) == r.Source {
			b.SelfLoops = append(b.SelfLoops, r.Source)
			continue
		}
		if t == "" {
			r.Target, r.Status = nil, status
		} else {
			r.Target = records.Ptr(t)
		}
		out = append(out, r)
	}
	if len(loops) > 0 {
		sort.Strings(loops)
		return nil, fmt.Errorf("redirect loops through: %s", strings.Join(loops, ", "))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LegacyKey < out[j].LegacyKey })
	return out, nil
}

// Exact returns the flattened exact redirect for a canonical path, nil when there is none.
func Exact(list []*records.Redirect, p string) *records.Redirect {
	for _, r := range list {
		if r.Match == "exact" && r.Source == p {
			return r
		}
	}
	return nil
}

var pcreOnly = regexp.MustCompile(`\(\?[=!<>]|\(\?<[=!]|\\[1-9]|\\[ZzAGKXRhHvV]|[*+?}]\+|\(\?\||\(\?P?>`)

// ConvertRegex turns a Yoast regex origin (PCRE, no leading slash) into an AFSC RE2 source.
func ConvertRegex(origin string) (string, error) {
	src := strings.TrimSpace(origin)
	if m := pcreOnly.FindString(src); m != "" {
		return "", fmt.Errorf("uses PCRE-only syntax %q", m)
	}
	switch {
	case strings.HasPrefix(src, "^/"):
	case strings.HasPrefix(src, "^"):
		src = "^/" + strings.TrimPrefix(src[1:], "/")
	case strings.HasPrefix(src, "/"):
	default:
		src = "/" + src
	}
	if _, err := regexp.Compile(src); err != nil {
		return "", err
	}
	return src, nil
}

// YoastFormat maps a Yoast redirect to AFSC's match and source.
func YoastFormat(origin, format string) (match, src string, err error) {
	if format == "regex" {
		src, err = ConvertRegex(origin)
		return "regex", src, err
	}
	o := strings.TrimSpace(origin)
	if !strings.HasPrefix(o, "/") && !strings.Contains(o, "://") {
		o = "/" + o
	}
	if strings.HasSuffix(o, "*") {
		return "prefix", strings.TrimSuffix(o, "*"), nil
	}
	return "exact", o, nil
}
