// Package permalink computes WordPress permalinks (to know the URLs that exist today) and AFSC paths (through
// AFSC's own site.Permalinks, so both sides agree by construction).
package permalink

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"afsc/internal/settings"
	"afsc/internal/site"
)

var tokenRe = regexp.MustCompile(`%[a-z_]+%`)

// supported maps WordPress structure tags to AFSC post_pattern tokens. %post_id% is deliberately absent:
// AFSC's {id} is its own id, not the WordPress one, so those URLs would change.
var supported = map[string]string{
	"%postname%": "{slug}", "%year%": "{year}", "%monthnum%": "{month}", "%day%": "{day}", "%category%": "{category}",
}

// Convert turns permalink_structure into an AFSC post pattern. unsupported lists tags AFSC can't express.
func Convert(structure string) (pattern string, trailing string, unsupported []string) {
	s := strings.TrimSpace(structure)
	if s == "" {
		return "", "always", []string{"(plain ?p= permalinks)"}
	}
	if strings.Contains(s, "index.php") {
		unsupported = append(unsupported, "index.php (PATHINFO permalinks)")
	}
	trailing = "never"
	if strings.HasSuffix(s, "/") {
		trailing = "always"
	}
	pattern = tokenRe.ReplaceAllStringFunc(s, func(tok string) string {
		if v, ok := supported[tok]; ok {
			return v
		}
		unsupported = append(unsupported, tok)
		return tok
	})
	if !strings.HasPrefix(pattern, "/") {
		pattern = "/" + pattern
	}
	if !strings.HasSuffix(pattern, "/") {
		pattern += "/"
	}
	slices.Sort(unsupported)
	return pattern, trailing, slices.Compact(unsupported)
}

// WPPost is what WordPress needs to build a post permalink.
type WPPost struct {
	ID       int64
	Slug     string    // raw post_name (may be percent-encoded)
	Local    time.Time // post_date in the site timezone
	Category string    // raw slug path of the permalink category, "" when none
	Author   string    // user_nicename
	Type     string    // post type; a TypeStructures entry overrides Structure
}

// WP builds today's WordPress URLs.
type WP struct {
	Structure    string
	CategoryBase string
	TagBase      string
	// TypeStructures are custom post types' permalinks ("chapter": "/chapter/%postname%/").
	TypeStructures map[string]string
	// TaxonomyBases are custom taxonomies' archive bases ("modules": "module").
	TaxonomyBases map[string]string
}

func (w *WP) slash(p string) string {
	if strings.HasSuffix(w.Structure, "/") && !strings.HasSuffix(p, "/") {
		return p + "/"
	}
	if !strings.HasSuffix(w.Structure, "/") && w.Structure != "" {
		return strings.TrimSuffix(p, "/")
	}
	return p
}

// Post returns the WordPress path of a published post, or /?p=N for plain permalinks.
func (w *WP) Post(p WPPost) string {
	structure := w.Structure
	if ts := w.TypeStructures[p.Type]; ts != "" {
		structure = ts
	}
	if strings.TrimSpace(structure) == "" {
		return "/?p=" + strconv.FormatInt(p.ID, 10)
	}
	cat := p.Category
	if cat == "" {
		cat = "uncategorized"
	}
	r := strings.NewReplacer(
		"%year%", fmt.Sprintf("%04d", p.Local.Year()), "%monthnum%", fmt.Sprintf("%02d", int(p.Local.Month())),
		"%day%", fmt.Sprintf("%02d", p.Local.Day()), "%hour%", fmt.Sprintf("%02d", p.Local.Hour()),
		"%minute%", fmt.Sprintf("%02d", p.Local.Minute()), "%second%", fmt.Sprintf("%02d", p.Local.Second()),
		"%postname%", p.Slug, "%post_id%", strconv.FormatInt(p.ID, 10), "%category%", cat, "%author%", p.Author,
	)
	out := r.Replace(structure)
	if !strings.HasPrefix(out, "/") {
		out = "/" + out
	}
	return w.slash(out)
}

// Page returns the WordPress path of a page from its raw slug chain.
func (w *WP) Page(id int64, slugs []string) string {
	if strings.TrimSpace(w.Structure) == "" {
		return "/?page_id=" + strconv.FormatInt(id, 10)
	}
	return w.slash("/" + strings.Join(slugs, "/") + "/")
}

func base(b, def string) string {
	b = strings.Trim(b, "/")
	if b == "" {
		return def
	}
	return b
}

// Term returns the WordPress archive path of a category or tag.
func (w *WP) Term(taxonomy, rawPath string) string {
	if b := w.TaxonomyBases[taxonomy]; b != "" {
		return w.slash("/" + strings.Trim(b, "/") + "/" + rawPath + "/")
	}
	if taxonomy == "category" {
		return w.slash("/" + base(w.CategoryBase, "category") + "/" + rawPath + "/")
	}
	return w.slash("/" + base(w.TagBase, "tag") + "/" + rawPath + "/")
}

func (w *WP) Author(nicename string) string { return w.slash("/author/" + nicename + "/") }

// AFSC wraps AFSC's own permalink builder.
type AFSC struct {
	P   *site.Permalinks
	Loc *time.Location
}

func NewAFSC(s settings.Permalinks, loc *time.Location) (*AFSC, error) {
	p, err := site.NewPermalinks(s, loc)
	if err != nil {
		return nil, err
	}
	return &AFSC{P: p, Loc: loc}, nil
}

// Post returns the AFSC path. id is only used by {id}, which afsc-wp never emits.
func (a *AFSC) Post(id int64, slug string, published time.Time, catPath string) string {
	t := published
	return a.P.Post(&site.PostRef{ID: id, Slug: slug, Type: "post", PublishedAt: &t}, catPath)
}

func (a *AFSC) Page(slugs []string) string         { return a.P.Page(slugs) }
func (a *AFSC) Term(taxonomy, path string) string  { return a.P.Term(taxonomy, path) }
func (a *AFSC) Author(slug string) string          { return a.P.Author(slug) }
func (a *AFSC) Feed(archive string) string         { return a.P.Feed(archive) }
func (a *AFSC) Paged(archive string, n int) string { return a.P.Paged(archive, n) }
func (a *AFSC) Year(y int) string                  { return a.P.Year(y) }
func (a *AFSC) Month(y, m int) string              { return a.P.Month(y, m) }
