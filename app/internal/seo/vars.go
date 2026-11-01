// Package seo resolves titles, meta tags and JSON-LD for public pages (build plan §8).
package seo

import (
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Vars holds the values Yoast-compatible %%variables%% expand to. Empty values render as nothing.
type Vars struct {
	Title           string
	SiteName        string
	SiteDesc        string
	Sep             string
	Page            int // current page, 1 when not paginated
	PageTotal       int
	PrimaryCategory string
	Category        string // comma-joined categories
	Tag             string // comma-joined tags
	TermTitle       string
	TermDescription string
	Name            string // author display name
	AuthorFirst     string
	AuthorLast      string
	Excerpt         string // explicit excerpt, or the content fallback when allowed
	ExcerptOnly     string // explicit excerpt only
	Date            string // formatted publish date, or the archive date
	Modified        string
	SearchPhrase    string
	PTSingle        string
	PTPlural        string
	Now             time.Time
}

var varRe = regexp.MustCompile(`%%([a-zA-Z0-9_]+)%%`)

// Expand replaces every %%var%% in tpl and collapses whitespace and repeated separators. Unknown names
// expand to "" and are returned so the admin can warn about them.
func Expand(tpl string, v Vars) (string, []string) {
	var unknown []string
	out := varRe.ReplaceAllStringFunc(tpl, func(m string) string {
		name := m[2 : len(m)-2]
		val, ok := v.lookup(name)
		if !ok {
			unknown = append(unknown, name)
		}
		return val
	})
	return tidy(out, v.Sep), unknown
}

func (v Vars) lookup(name string) (string, bool) {
	switch name {
	case "title":
		return v.Title, true
	case "sitename":
		return v.SiteName, true
	case "sitedesc":
		return v.SiteDesc, true
	case "sep":
		return v.Sep, true
	case "page":
		if v.Page > 1 {
			return v.Sep + " Page " + strconv.Itoa(v.Page) + " of " + strconv.Itoa(max(v.Page, v.PageTotal)), true
		}
		return "", true
	case "pagenumber":
		return strconv.Itoa(max(1, v.Page)), true
	case "pagetotal":
		return strconv.Itoa(max(1, v.PageTotal)), true
	case "primary_category":
		return v.PrimaryCategory, true
	case "category":
		return v.Category, true
	case "tag":
		return v.Tag, true
	case "term_title":
		return v.TermTitle, true
	case "term_description", "category_description", "tag_description":
		return v.TermDescription, true
	case "name":
		return v.Name, true
	case "author_first_name":
		return v.AuthorFirst, true
	case "author_last_name":
		return v.AuthorLast, true
	case "excerpt":
		return v.Excerpt, true
	case "excerpt_only":
		return v.ExcerptOnly, true
	case "date":
		return v.Date, true
	case "modified":
		return v.Modified, true
	case "currentyear":
		return strconv.Itoa(v.Now.Year()), true
	case "currentmonth":
		return v.Now.Format("January"), true
	case "searchphrase":
		return v.SearchPhrase, true
	case "pt_single":
		return v.PTSingle, true
	case "pt_plural":
		return v.PTPlural, true
	}
	return "", false
}

// tidy collapses whitespace, drops separators that end up adjacent, leading or trailing.
func tidy(s, sep string) string {
	words := strings.Fields(s)
	if sep == "" {
		return strings.Join(words, " ")
	}
	out := words[:0]
	for _, w := range words {
		if w == sep && (len(out) == 0 || out[len(out)-1] == sep) {
			continue
		}
		out = append(out, w)
	}
	for len(out) > 0 && out[len(out)-1] == sep {
		out = out[:len(out)-1]
	}
	return strings.Join(out, " ")
}
