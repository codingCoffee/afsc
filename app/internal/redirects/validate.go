package redirects

import (
	"fmt"
	"net/url"
	"regexp"
	"regexp/syntax"
	"slices"
	"strings"
	"unicode/utf8"
)

const (
	MaxSource   = 2048
	MaxTarget   = 2048
	MaxNote     = 1000
	MaxRegex    = 500
	MaxRegexOps = 1000 // compiled RE2 instructions
	MaxTestPath = 100
)

var Statuses = []int{301, 302, 307, 308, 410, 451}

// Input is a redirect as an editor or a CSV row gives it.
type Input struct {
	Source string  `json:"source"`
	Match  string  `json:"match"`
	Target *string `json:"target"`
	Status int     `json:"status"`
	Note   string  `json:"note"`
}

// FieldError is a validation failure on one field. Conflict marks a clash with an existing redirect.
type FieldError struct {
	Field    string
	Message  string
	Conflict bool
}

func (e *FieldError) Error() string { return e.Field + ": " + e.Message }

func fieldErr(field, format string, args ...any) *FieldError {
	return &FieldError{Field: field, Message: fmt.Sprintf(format, args...)}
}

var multiSlash = regexp.MustCompile(`/{2,}`)

// Normalize validates in and returns the rule to store. Exact sources and internal targets take the site's canonical
// form, prefix sources are lowercased (the site matches them that way), and same-site absolute targets become paths.
func Normalize(in Input, rt Router) (*Rule, error) {
	r := &Rule{Match: in.Match, Status: in.Status, Note: strings.TrimSpace(in.Note)}
	if r.Match == "" {
		r.Match = "exact"
	}
	if r.Status == 0 {
		r.Status = 301
	}
	if !slices.Contains(Statuses, r.Status) {
		return nil, fieldErr("status", "must be one of 301, 302, 307, 308, 410, 451")
	}
	if utf8.RuneCountInString(r.Note) > MaxNote {
		return nil, fieldErr("note", "must be at most %d characters", MaxNote)
	}
	src, err := normalizeSource(r.Match, in.Source, rt)
	if err != nil {
		return nil, err
	}
	r.Source = src
	if err := r.Compile(); err != nil {
		return nil, fieldErr("source", "not a valid regular expression: %v", err)
	}

	target := ""
	if in.Target != nil {
		target = strings.TrimSpace(*in.Target)
	}
	if r.Gone() {
		if target != "" {
			return nil, fieldErr("target", "a %d takes no target", r.Status)
		}
		return r, nil
	}
	if target == "" {
		return nil, fieldErr("target", "required for a %d", r.Status)
	}
	if r.Target, err = normalizeTarget(target, r.Match == "regex" && strings.Contains(target, "$"), rt); err != nil {
		return nil, err
	}
	if r.Match == "exact" {
		if p, q, ok := internal(r.Target, rt.BaseURL()); ok && q == "" && rt.Canonical(p) == r.Source {
			return nil, fieldErr("target", "is the same URL as the source")
		}
	}
	return r, nil
}

func normalizeSource(match, src string, rt Router) (string, error) {
	src = strings.TrimSpace(src)
	switch {
	case src == "":
		return "", fieldErr("source", "required")
	case len(src) > MaxSource:
		return "", fieldErr("source", "must be at most %d bytes", MaxSource)
	case strings.ContainsFunc(src, func(c rune) bool { return c < 0x20 || c == 0x7f }):
		return "", fieldErr("source", "must not contain control characters")
	}
	switch match {
	case "exact", "prefix":
		if strings.Contains(src, "%") {
			u, err := url.PathUnescape(src)
			if err != nil {
				return "", fieldErr("source", "invalid percent-encoding")
			}
			src = u
		}
		if !strings.HasPrefix(src, "/") {
			return "", fieldErr("source", "must start with /")
		}
		if strings.ContainsAny(src, "?#") {
			return "", fieldErr("source", "query strings and fragments aren't matched; use the path only")
		}
		src = multiSlash.ReplaceAllString(src, "/")
		if reserved(src) {
			return "", fieldErr("source", "/admin is reserved")
		}
		if match == "exact" {
			return rt.Canonical(src), nil
		}
		return strings.ToLower(src), nil
	case "regex":
		if len(src) > MaxRegex {
			return "", fieldErr("source", "a regex must be at most %d bytes", MaxRegex)
		}
		if strings.HasPrefix(src, "/admin") || strings.HasPrefix(src, "^/admin") {
			return "", fieldErr("source", "/admin is reserved")
		}
		if err := regexCost(src); err != nil {
			return "", err
		}
		return src, nil
	}
	return "", fieldErr("match", "must be exact, prefix or regex")
}

func reserved(path string) bool {
	return path == "/admin" || strings.HasPrefix(path, "/admin/")
}

// regexCost rejects patterns RE2 can't compile or whose compiled program is too large.
func regexCost(src string) error {
	re, err := syntax.Parse(src, syntax.Perl)
	if err != nil {
		return fieldErr("source", "not a valid RE2 regular expression: %v", err)
	}
	prog, err := syntax.Compile(re.Simplify())
	if err != nil {
		return fieldErr("source", "not a valid RE2 regular expression: %v", err)
	}
	if len(prog.Inst) > MaxRegexOps {
		return fieldErr("source", "regex is too complex (%d steps, at most %d)", len(prog.Inst), MaxRegexOps)
	}
	return nil
}

func normalizeTarget(target string, template bool, rt Router) (string, error) {
	if len(target) > MaxTarget {
		return "", fieldErr("target", "must be at most %d bytes", MaxTarget)
	}
	u, err := url.Parse(target)
	if err != nil {
		return "", fieldErr("target", "not a valid URL")
	}
	switch {
	case u.Scheme != "" || u.Host != "":
		if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
			return "", fieldErr("target", "must be a path starting with / or an http(s) URL")
		}
		base, _ := url.Parse(rt.BaseURL())
		if !strings.EqualFold(u.Host, base.Host) {
			return target, nil
		}
		u.Scheme, u.Host = "", ""
		if u.Path == "" {
			u.Path = "/"
		}
	case !strings.HasPrefix(u.Path, "/"):
		return "", fieldErr("target", "must be a path starting with / or an http(s) URL")
	}
	if template {
		return u.String(), nil
	}
	if reserved(u.Path) {
		return "", fieldErr("target", "/admin can't be a target")
	}
	u.Path, u.RawPath = rt.Canonical(multiSlash.ReplaceAllString(u.Path, "/")), ""
	return u.String(), nil
}

// TestResult is what a rule does with one sample path.
type TestResult struct {
	Path      string `json:"path"`
	Canonical string `json:"canonical"`
	Matched   bool   `json:"matched"`
	Target    string `json:"target,omitempty"`
}

// Test runs a source pattern against sample paths the way the site does: exact rules see the raw and canonical
// path, prefix and regex rules the canonical one. target may be empty.
func Test(match, source, target string, paths []string, rt Router) ([]TestResult, error) {
	if len(paths) > MaxTestPath {
		return nil, fieldErr("paths", "at most %d paths", MaxTestPath)
	}
	if match == "" {
		match = "exact"
	}
	src, err := normalizeSource(match, source, rt)
	if err != nil {
		return nil, err
	}
	r := &Rule{Source: src, Match: match, Target: target}
	if err := r.Compile(); err != nil {
		return nil, fieldErr("source", "not a valid regular expression: %v", err)
	}
	out := make([]TestResult, 0, len(paths))
	for _, p := range paths {
		if len(p) > MaxSource {
			return nil, fieldErr("paths", "each path must be at most %d bytes", MaxSource)
		}
		if u, err := url.Parse(p); err == nil && u.Path != "" {
			p = u.Path
		}
		res := TestResult{Path: p, Canonical: rt.Canonical(p)}
		res.Target, res.Matched = r.matches(p, res.Canonical)
		if !res.Matched {
			res.Target = ""
		}
		out = append(out, res)
	}
	return out, nil
}
