package theme

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"golang.org/x/net/html"
)

// Querier is implemented by backends that can load content for templates beyond the page's own data (the live
// site). Backends without it (theme check fixtures, the importer) make the query funcs return nothing.
type Querier interface {
	// TermPosts lists published posts directly in a term (not its descendants), by menu_order then date.
	TermPosts(termID int64, limit int) ([]*Post, error)
	// RecentPosts lists the newest published posts, only those in termID when it isn't 0.
	RecentPosts(termID int64, limit int) ([]*Post, error)
	// TopTerms lists a taxonomy's terms with the most published posts, most first.
	TopTerms(taxonomy string, limit int) ([]*Term, error)
	// Terms lists every term of a taxonomy ("category" or "tag").
	Terms(taxonomy string) ([]*Term, error)
	// TermChildren lists a term's direct children.
	TermChildren(termID int64) ([]*Term, error)
	// RecentComments lists the newest approved comments.
	RecentComments(n int) ([]*RecentComment, error)
}

// RecentComment is a comment for site-wide listings.
type RecentComment struct {
	ID               int64
	AuthorName       string
	Text             string // plain text body
	CreatedAt        time.Time
	PostTitle        string
	PostURL          string
	URL              string // the comment's anchor on its post
	PostCommentCount int
}

const maxQueryLimit = 1000

func (t *Theme) querier() Querier {
	q, _ := t.opts.Backend.(Querier)
	return q
}

func termID(v any) (int64, error) {
	switch x := v.(type) {
	case *Term:
		if x == nil {
			return 0, nil
		}
		return x.ID, nil
	case int64:
		return x, nil
	case int:
		return int64(x), nil
	case nil:
		return 0, nil
	}
	return 0, fmt.Errorf("want a term or term id, got %T", v)
}

func limitArg(n []int, def int) int {
	if len(n) == 0 || n[0] <= 0 {
		return def
	}
	return min(n[0], maxQueryLimit)
}

// termPosts: {{range term_posts .Term}} or {{term_posts .Post.Primary 50}}.
func (t *Theme) termPosts(term any, n ...int) ([]*Post, error) {
	id, err := termID(term)
	q := t.querier()
	if err != nil || id == 0 || q == nil {
		return nil, err
	}
	return q.TermPosts(id, limitArg(n, maxQueryLimit))
}

// recentPosts: {{range recent_posts nil 10}} (all posts) or {{recent_posts (term "category" "news") 4}}.
func (t *Theme) recentPosts(term any, n ...int) ([]*Post, error) {
	id, err := termID(term)
	q := t.querier()
	if err != nil || q == nil {
		return nil, err
	}
	return q.RecentPosts(id, limitArg(n, 10))
}

func checkTaxonomy(fn, taxonomy string) error {
	if taxonomy != "category" && taxonomy != "tag" {
		return fmt.Errorf("%s: taxonomy must be category or tag, got %q", fn, taxonomy)
	}
	return nil
}

func (t *Theme) terms(taxonomy string) ([]*Term, error) {
	if err := checkTaxonomy("terms", taxonomy); err != nil {
		return nil, err
	}
	q := t.querier()
	if q == nil {
		return nil, nil
	}
	return q.Terms(taxonomy)
}

// topTerms: {{range top_terms "category" 5}}.
func (t *Theme) topTerms(taxonomy string, n ...int) ([]*Term, error) {
	if err := checkTaxonomy("top_terms", taxonomy); err != nil {
		return nil, err
	}
	q := t.querier()
	if q == nil {
		return nil, nil
	}
	return q.TopTerms(taxonomy, limitArg(n, 10))
}

// term finds a term by slug: {{with term "category" "featured"}}. Missing terms give nil.
func (t *Theme) term(taxonomy, slug string) (*Term, error) {
	all, err := t.terms(taxonomy)
	for _, x := range all {
		if x.Slug == slug {
			return x, nil
		}
	}
	return nil, err
}

func (t *Theme) termChildren(term any) ([]*Term, error) {
	id, err := termID(term)
	q := t.querier()
	if err != nil || id == 0 || q == nil {
		return nil, err
	}
	return q.TermChildren(id)
}

func (t *Theme) recentComments(n ...int) ([]*RecentComment, error) {
	q := t.querier()
	if q == nil {
		return nil, nil
	}
	return q.RecentComments(min(limitArg(n, 5), 50))
}

// loadData reads data/*.json from the theme: static lookup tables templates read with theme_data.
func loadData(dir string) (map[string]any, error) {
	out := map[string]any{}
	files, err := filepath.Glob(filepath.Join(dir, "data", "*.json"))
	if err != nil {
		return nil, err
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		} else if err != nil {
			return nil, err
		}
		var v any
		if err := json.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("data/%s: %w", filepath.Base(f), err)
		}
		out[strings.TrimSuffix(filepath.Base(f), ".json")] = v
	}
	return out, nil
}

// themeData returns data/<name>.json, optionally indexed by keys: {{theme_data "modules" .Term.Slug "color"}}.
// Missing keys give nil, so templates can guard with `with`.
func (st *state) themeData(name string, keys ...string) any {
	var v any = st.data[name]
	for _, k := range keys {
		m, ok := v.(map[string]any)
		if !ok {
			return nil
		}
		v = m[k]
	}
	return v
}

// truncate cuts s to at most n characters (runes), like PHP's substr on the WordPress themes it replaces.
func truncate(s string, n int) string {
	r := []rune(strings.TrimSpace(s))
	if n <= 0 || len(r) <= n {
		return string(r)
	}
	return string(r[:n])
}

// stripTags returns the text of an HTML fragment (entities decoded, tags dropped), for HTML that imported
// plain-text fields such as term descriptions carry.
func stripTags(s string) string {
	z := html.NewTokenizer(strings.NewReader(s))
	var b strings.Builder
	for {
		switch z.Next() {
		case html.ErrorToken:
			return strings.Join(strings.Fields(b.String()), " ")
		case html.TextToken:
			b.Write(z.Text())
		case html.StartTagToken, html.EndTagToken, html.SelfClosingTagToken:
			b.WriteByte(' ')
		}
	}
}

// reverse returns a reversed copy of a slice: {{range reverse .Comments.Threads}}.
func reverse(v any) (any, error) {
	rv := reflect.ValueOf(v)
	if !rv.IsValid() {
		return nil, nil
	}
	if rv.Kind() != reflect.Slice {
		return nil, fmt.Errorf("reverse: want a slice, got %T", v)
	}
	n := rv.Len()
	out := reflect.MakeSlice(rv.Type(), n, n)
	for i := range n {
		out.Index(i).Set(rv.Index(n - 1 - i))
	}
	return out.Interface(), nil
}
