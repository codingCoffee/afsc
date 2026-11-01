// Package redirects is the redirect manager: validation, chain and loop checks, CSV, auto redirects and the
// not-found log. The public site shares its Rule and Set matching, so a check sees what visitors get.
package redirects

import (
	"regexp"
	"slices"
	"strings"
)

// Rule is one row of the redirects table.
type Rule struct {
	ID     int64
	Source string
	Match  string // exact, prefix or regex
	Target string
	Status int
	Note   string
	re     *regexp.Regexp
}

// Gone reports whether the rule answers with 410 or 451 instead of redirecting.
func (r *Rule) Gone() bool { return r.Status == 410 || r.Status == 451 }

// Compile prepares a regex rule for matching. Other rules need nothing.
func (r *Rule) Compile() error {
	if r.Match != "regex" || r.re != nil {
		return nil
	}
	re, err := regexp.Compile(r.Source)
	if err != nil {
		return err
	}
	r.re = re
	return nil
}

// matchCanon matches a prefix or regex rule against a canonical path and returns the target it sends it to.
// Prefix targets are used as-is; regex targets expand $1 and friends.
func (r *Rule) matchCanon(path string) (string, bool) {
	switch r.Match {
	case "prefix":
		if strings.HasPrefix(path, strings.ToLower(r.Source)) {
			return r.Target, true
		}
	case "regex":
		if r.re == nil {
			return "", false
		}
		if m := r.re.FindStringSubmatchIndex(path); m != nil {
			return string(r.re.ExpandString(nil, r.Target, path, m)), true
		}
	}
	return "", false
}

// matches reports whether the rule applies to a request with this raw and canonical path.
func (r *Rule) matches(raw, canon string) (string, bool) {
	if r.Match == "exact" {
		return r.Target, r.Source == raw || r.Source == canon
	}
	return r.matchCanon(canon)
}

// Set holds prefix rules (longest source first, then id) and regex rules (id order), the order the site tries them.
type Set struct {
	prefix []*Rule
	regex  []*Rule
}

// NewSet orders compiled prefix and regex rules for matching. Exact rules are ignored.
func NewSet(rules []*Rule) *Set {
	s := &Set{}
	for _, r := range rules {
		s.add(r)
	}
	return s
}

func prefixCmp(a, b *Rule) int {
	if la, lb := len(a.Source), len(b.Source); la != lb {
		return lb - la
	}
	return cmpID(a.ID, b.ID)
}

func regexCmp(a, b *Rule) int { return cmpID(a.ID, b.ID) }

func cmpID(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func (s *Set) add(r *Rule) {
	insert := func(list []*Rule, cmp func(a, b *Rule) int) []*Rule {
		i, _ := slices.BinarySearchFunc(list, r, cmp)
		return slices.Insert(list, i, r)
	}
	switch r.Match {
	case "prefix":
		s.prefix = insert(s.prefix, prefixCmp)
	case "regex":
		s.regex = insert(s.regex, regexCmp)
	}
}

func (s *Set) remove(r *Rule) {
	same := func(x *Rule) bool { return x == r }
	s.prefix = slices.DeleteFunc(s.prefix, same)
	s.regex = slices.DeleteFunc(s.regex, same)
}

// Match tries prefix rules, then regex rules, against a canonical path.
func (s *Set) Match(path string) (*Rule, string) {
	if s == nil {
		return nil, ""
	}
	for _, list := range [][]*Rule{s.prefix, s.regex} {
		for _, r := range list {
			if target, ok := r.matchCanon(path); ok {
				return r, target
			}
		}
	}
	return nil, ""
}
