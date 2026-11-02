// Package decisions is the machine-readable per-site decision table (migration plan §3.4). inventory proposes,
// a human confirms, and transform refuses to run while anything is unconfirmed.
package decisions

import (
	"errors"
	"fmt"
	"os"
	"slices"
	"sort"
	"strings"

	"afsc/migrators/afsc-wp/internal/extract"
)

const (
	StatusProposed  = "proposed"
	StatusConfirmed = "confirmed"
)

// Kinds maps each decision kind to its allowed values. docs/decisions.md documents them.
var Kinds = map[string][]string{
	"shortcode":           {"convert", "render_php", "drop", "keep_text"},
	"block":               {"convert", "raw_html"},
	"jetpack_module":      {"replicate_theme", "builtin", "drop"},
	"theme_hook":          {"replicate_theme", "drop", "needs_afsc"},
	"plugin":              {"replicate_theme", "drop", "needs_afsc"},
	"iframe_host":         {"embed", "raw_html", "drop"},
	"element":             {"raw_html", "drop"},
	"taxonomy":            {"drop", "category", "tag"},
	"post_type":           {"skip", "post", "page"},
	"marker":              {"merge", "read_more"},
	"empty_paragraphs":    {"drop", "keep"},
	"yoast_variable":      {"keep", "remove", "expand", "replace"},
	"permalink_structure": {"keep", "change"},
	"category_base":       {"keep", "change"},
	"redirect_regex":      {"rewrite", "drop"},
	"missing_media":       {"keep_src", "remove"},
	"password_protected":  {"private"},
	"oembed":              {"link", "raw_html"},
}

type Item struct {
	ID       string         `json:"id"`
	Kind     string         `json:"kind"`
	Name     string         `json:"name"`
	Count    int            `json:"count"`
	Samples  []string       `json:"samples,omitempty"`
	Allowed  []string       `json:"allowed"`
	Proposed string         `json:"proposed"`
	Decision string         `json:"decision"`
	Options  map[string]any `json:"options,omitempty"`
	Status   string         `json:"status"`
	Stale    bool           `json:"stale,omitempty"`
	Note     string         `json:"note,omitempty"`
}

type File struct {
	Version int     `json:"version"`
	Items   []*Item `json:"items"`

	byID map[string]*Item
}

func ID(kind, name string) string { return kind + ":" + name }

// New builds a proposed item. allowed narrows the kind's values (for example, convert only when a handler exists).
func New(kind, name string, count int, samples []string, proposed, note string, allowed ...string) *Item {
	if len(allowed) == 0 {
		allowed = Kinds[kind]
	}
	if len(samples) > 5 {
		samples = samples[:5]
	}
	return &Item{ID: ID(kind, name), Kind: kind, Name: name, Count: count, Samples: samples, Allowed: allowed,
		Proposed: proposed, Decision: proposed, Status: StatusProposed, Note: note}
}

func Load(path string) (*File, error) {
	f := &File{}
	if err := extract.ReadJSON(path, f); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("%s not found: run afsc-wp inventory first", path)
		}
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	f.index()
	return f, nil
}

func (f *File) Save(path string) error {
	sort.Slice(f.Items, func(i, j int) bool { return f.Items[i].ID < f.Items[j].ID })
	f.Version = 1
	return extract.WriteJSON(path, f)
}

func (f *File) index() {
	f.byID = make(map[string]*Item, len(f.Items))
	for _, it := range f.Items {
		f.byID[it.ID] = it
	}
}

// Get returns the item for kind and name, nil when there is none.
func (f *File) Get(kind, name string) *Item {
	if f.byID == nil {
		f.index()
	}
	return f.byID[ID(kind, name)]
}

// Decision returns the confirmed decision, or def when no item exists.
func (f *File) Decision(kind, name, def string) string {
	if it := f.Get(kind, name); it != nil && !it.Stale {
		return it.Decision
	}
	return def
}

// OptionString reads a string option of an item.
func (it *Item) OptionString(key string) string {
	if it == nil {
		return ""
	}
	s, _ := it.Options[key].(string)
	return s
}

// OptionList reads a []string option of an item.
func (it *Item) OptionList(key string) []string {
	if it == nil {
		return nil
	}
	var out []string
	switch v := it.Options[key].(type) {
	case []any:
		for _, x := range v {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
	case []string:
		out = v
	}
	return out
}

// requiredOptions lists the option each decision value needs.
var requiredOptions = map[string]string{
	"yoast_variable/replace":     "with",
	"permalink_structure/change": "pattern",
	"category_base/change":       "base",
	"redirect_regex/rewrite":     "source",
}

// Merge carries confirmed human decisions from old into the freshly proposed items. Items that vanished are
// kept as stale, so a later re-appearance keeps its decision.
func Merge(old, fresh *File) *File {
	out := &File{Version: 1}
	seen := map[string]bool{}
	for _, it := range fresh.Items {
		seen[it.ID] = true
		var o *Item
		if old != nil {
			o = old.Get(it.Kind, it.Name)
		}
		if o != nil && o.Status == StatusConfirmed && slices.Contains(it.Allowed, o.Decision) {
			it.Decision, it.Options, it.Note, it.Status = o.Decision, o.Options, o.Note, StatusConfirmed
		}
		out.Items = append(out.Items, it)
	}
	if old != nil {
		for _, o := range old.Items {
			if !seen[o.ID] {
				o.Stale = true
				out.Items = append(out.Items, o)
			}
		}
	}
	out.index()
	return out
}

// Check fails closed: every non-stale item must be valid and confirmed.
func (f *File) Check() error {
	var bad, pending []string
	for _, it := range f.Items {
		if it.Stale {
			continue
		}
		allowed, ok := Kinds[it.Kind]
		switch {
		case !ok:
			bad = append(bad, fmt.Sprintf("%s: unknown kind %q", it.ID, it.Kind))
		case !slices.Contains(allowed, it.Decision) || (len(it.Allowed) > 0 && !slices.Contains(it.Allowed, it.Decision)):
			bad = append(bad, fmt.Sprintf("%s: decision %q not in %v", it.ID, it.Decision, it.Allowed))
		case it.Status != StatusConfirmed:
			pending = append(pending, it.ID)
		}
		if opt := requiredOptions[it.Kind+"/"+it.Decision]; opt != "" && it.OptionString(opt) == "" {
			bad = append(bad, fmt.Sprintf("%s: decision %s needs options.%s", it.ID, it.Decision, opt))
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("invalid decisions:\n  %s", strings.Join(bad, "\n  "))
	}
	if len(pending) > 0 {
		return fmt.Errorf("%d decisions are not confirmed (set status to \"confirmed\" after review):\n  %s",
			len(pending), strings.Join(pending, "\n  "))
	}
	return nil
}
