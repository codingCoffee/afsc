package redirects

import (
	"context"
	"maps"
	"net/url"
	"slices"
	"strings"
)

// Move is a public URL that changed: canonical paths before and after a slug, parent or date change.
type Move struct {
	Old string `json:"old"`
	New string `json:"new"`
}

// Moves pairs two URL sets (entity key → path, taken before and after a change) into the paths that moved.
func Moves(before, after map[string]string) []Move {
	var out []Move
	for key, old := range before {
		if n, ok := after[key]; ok && n != old && old != "" && n != "" {
			out = append(out, Move{Old: old, New: n})
		}
	}
	slices.SortFunc(out, func(a, b Move) int { return strings.Compare(a.Old, b.Old) })
	return out
}

// AutoResult lists what SlugChange did, by redirect id.
type AutoResult struct {
	Created   []int64
	Updated   []int64
	Deleted   []int64
	Flattened []int64
	Purge     Purge
}

// withPath swaps a target's path, keeping its query and fragment.
func withPath(target, path string) string {
	u, err := url.Parse(target)
	if err != nil {
		return (&url.URL{Path: path}).String()
	}
	u.Path, u.RawPath = path, ""
	return u.String()
}

// SlugChange records moved URLs: a 301 from each old path to its new one (origin slug_change), existing redirects
// that pointed at an old path now point at the new one, and exact redirects from a new path are deleted because
// they would hide the content now living there. The importer never calls this (§17.1).
func (s *Store) SlugChange(ctx context.Context, moves []Move, user *string) (*AutoResult, error) {
	var created, updated, flattened []*Rule
	b, err := s.withBatch(ctx, "slug_change", user, func(b *batch) error {
		created, updated, flattened = b.slugChange(moves)
		return nil
	})
	if err != nil {
		return nil, err
	}
	live := func(rules []*Rule) []*Rule {
		return slices.DeleteFunc(slices.Clone(rules), func(r *Rule) bool { return b.t.byID[r.ID] != r })
	}
	return &AutoResult{Created: ids(live(created)), Updated: ids(live(updated)), Flattened: ids(live(flattened)),
		Deleted: ids(b.deleted), Purge: b.purge()}, nil
}

func (b *batch) slugChange(moves []Move) (created, updated, flattened []*Rule) {
	for _, m := range moves {
		if m.Old == m.New {
			continue
		}
		if r := b.t.exact[m.New]; r != nil {
			b.del(r)
		}
		target := (&url.URL{Path: m.New}).String()
		if r := b.t.exact[m.Old]; r != nil {
			b.set(r, func(x *Rule) {
				x.Target = target
				if x.Gone() {
					x.Status = 301
				}
			})
			updated = append(updated, r)
		} else {
			r := &Rule{Source: m.Old, Match: "exact", Target: target, Status: 301}
			b.create(r)
			created = append(created, r)
		}
		incoming := slices.SortedFunc(maps.Keys(b.t.targets[m.Old]), func(a, c *Rule) int { return cmpID(a.ID, c.ID) })
		for _, x := range incoming {
			if x.Source == m.Old && x.Match == "exact" {
				continue
			}
			b.set(x, func(x *Rule) { x.Target = withPath(x.Target, m.New) })
			flattened = append(flattened, x)
		}
	}
	for _, r := range slices.Concat(created, updated, flattened) {
		if r.Match == "exact" && b.t.byID[r.ID] == r && b.t.targetKey(r) == r.Source {
			b.del(r)
		}
	}
	return created, updated, flattened
}
