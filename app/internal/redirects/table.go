package redirects

import (
	"slices"
	"strings"
)

// Table is every redirect in memory, so a batch of changes can be checked against each other before any is saved.
// Unsaved rules get temporary ids above every real one, which keeps the site's id-based order.
type Table struct {
	rt      Router
	byID    map[int64]*Rule
	exact   map[string]*Rule
	set     Set
	targets map[string]map[*Rule]bool // canonical internal target path → rules pointing there
	nextTmp int64
}

const tmpIDBase = int64(1) << 62

func NewTable(rt Router, rules []*Rule) *Table {
	t := &Table{rt: rt, byID: map[int64]*Rule{}, exact: map[string]*Rule{}, targets: map[string]map[*Rule]bool{},
		nextTmp: tmpIDBase}
	for _, r := range rules {
		t.add(r)
	}
	return t
}

// targetKey is the canonical path a literal internal target lands on, or "" for external and template targets.
func (t *Table) targetKey(r *Rule) string {
	if r.Gone() || (r.Match == "regex" && strings.Contains(r.Target, "$")) {
		return ""
	}
	p, _, ok := internal(r.Target, t.rt.BaseURL())
	if !ok {
		return ""
	}
	return t.rt.Canonical(p)
}

func (t *Table) add(r *Rule) {
	if r.ID == 0 {
		t.nextTmp++
		r.ID = t.nextTmp
	}
	t.byID[r.ID] = r
	if r.Match == "exact" {
		t.exact[r.Source] = r
	} else {
		t.set.add(r)
	}
	if k := t.targetKey(r); k != "" {
		if t.targets[k] == nil {
			t.targets[k] = map[*Rule]bool{}
		}
		t.targets[k][r] = true
	}
}

func (t *Table) remove(r *Rule) {
	delete(t.byID, r.ID)
	if r.Match == "exact" {
		if t.exact[r.Source] == r {
			delete(t.exact, r.Source)
		}
	} else {
		t.set.remove(r)
	}
	if k := t.targetKey(r); k != "" {
		delete(t.targets[k], r)
	}
}

// update applies a change to a rule already in the table, keeping every index in step.
func (t *Table) update(r *Rule, change func(*Rule)) {
	t.remove(r)
	change(r)
	t.add(r)
}

func (t *Table) saved(r *Rule) bool { return r.ID < tmpIDBase }

// setID gives an inserted rule its real id. Real ids are larger than any existing one, so the order holds.
func (t *Table) setID(r *Rule, id int64) {
	delete(t.byID, r.ID)
	r.ID = id
	t.byID[id] = r
}

func (t *Table) exactFor(raw, canon string) *Rule {
	if r := t.exact[raw]; r != nil {
		return r
	}
	return t.exact[canon]
}

// find returns the rule a new row with this match and source would replace: exact sources are unique, prefix and
// regex rules are matched on the same source.
func (t *Table) find(match, source string) *Rule {
	if match == "exact" {
		return t.exact[source]
	}
	var found *Rule
	for _, list := range [][]*Rule{t.set.prefix, t.set.regex} {
		for _, r := range list {
			if r.Match == match && r.Source == source && (found == nil || r.ID < found.ID) {
				found = r
			}
		}
	}
	return found
}

// pointingAt returns the rules whose literal target could be redirected by r.
func (t *Table) pointingAt(r *Rule) []*Rule {
	var out []*Rule
	keys := []string{r.Source}
	if r.Match != "exact" {
		keys = keys[:0]
		for k := range t.targets {
			if _, ok := r.matchCanon(k); ok {
				keys = append(keys, k)
			}
		}
	}
	for _, k := range keys {
		for x := range t.targets[k] {
			if x != r {
				out = append(out, x)
			}
		}
	}
	slices.SortFunc(out, func(a, b *Rule) int { return cmpID(a.ID, b.ID) })
	return out
}
