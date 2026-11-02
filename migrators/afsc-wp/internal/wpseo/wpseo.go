// Package wpseo maps Yoast SEO data (per post, term and author, templates and video metadata) to AFSC SEO
// records. Key names come from the yoast adapter so they can be overridden per site.
package wpseo

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"afsc/migrators/afsc-wp/internal/records"
)

// Separators maps Yoast's separator tokens to characters (wpseo option "separator").
var Separators = map[string]string{
	"sc-dash": "-", "sc-ndash": "–", "sc-mdash": "—", "sc-colon": ":", "sc-middot": "·", "sc-bull": "•",
	"sc-star": "*", "sc-smstar": "⋆", "sc-pipe": "|", "sc-tilde": "~", "sc-laquo": "«", "sc-raquo": "»",
	"sc-lt": "<", "sc-gt": ">",
}

// Supported are the Yoast variables AFSC expands itself (docs/import-format.md).
var Supported = map[string]bool{}

func init() {
	for _, v := range strings.Fields(`title sitename sitedesc sep page pagenumber pagetotal primary_category category tag
		term_title term_description category_description tag_description name author_first_name author_last_name excerpt
		excerpt_only date modified currentyear currentmonth searchphrase pt_single pt_plural`) {
		Supported[v] = true
	}
}

var varRe = regexp.MustCompile(`%%([a-z0-9_]+)%%`)

// Unsupported lists the variables in s AFSC can't expand.
func Unsupported(s string) []string {
	var out []string
	for _, m := range varRe.FindAllStringSubmatch(s, -1) {
		if !Supported[m[1]] && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	sort.Strings(out)
	return out
}

// Variable decides what happens to one unsupported variable: keep, remove, replace (with) or expand.
type Variable struct {
	Decision string
	With     string
}

// Expandable are unsupported variables afsc-wp can fill in per post at migration time.
var Expandable = map[string]bool{"focuskw": true, "id": true, "parent_title": true, "post_year": true,
	"post_month": true, "post_day": true}

// Apply rewrites unsupported variables in s. values holds per-post data for "expand"; templates pass nil.
func Apply(s string, decide func(name string) Variable, values map[string]string) string {
	return varRe.ReplaceAllStringFunc(s, func(m string) string {
		name := m[2 : len(m)-2]
		if Supported[name] {
			return m
		}
		d := decide(name)
		switch d.Decision {
		case "remove":
			return ""
		case "replace":
			return d.With
		case "expand":
			if v, ok := values[name]; ok {
				return v
			}
			return ""
		}
		return m
	})
}

func str(v string) *string {
	if strings.TrimSpace(v) == "" {
		return nil
	}
	return &v
}

// Tri maps Yoast's per-entity noindex value: "1" noindex, "2" index, anything else default.
func Tri(v string) *bool {
	switch strings.TrimSpace(v) {
	case "1", "noindex":
		return records.Ptr(true)
	case "2", "index":
		return records.Ptr(false)
	}
	return nil
}

func advanced(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		p = strings.TrimSpace(p)
		if p != "" && p != "none" && p != "-" {
			out = append(out, p)
		}
	}
	return out
}

// Getter reads one field; keys come from the yoast adapter.
type Getter func(field string) string

// Resolver maps an image (attachment id or URL) to a media legacy key.
type Resolver func(id, url string) *string

// Post builds a post's SEO record. canonical is already rewritten and compared against the self URL by the caller.
func Post(get Getter, media Resolver) *records.SEO {
	s := &records.SEO{
		Title: str(get("title")), Description: str(get("description")), FocusKeyphrase: str(get("focus_keyphrase")),
		Canonical: str(get("canonical")), RobotsNoindex: Tri(get("noindex")), RobotsAdvanced: advanced(get("robots_advanced")),
		BreadcrumbTitle: str(get("breadcrumb_title")), OGTitle: str(get("og_title")), OGDescription: str(get("og_description")),
		TwitterTitle: str(get("twitter_title")), TwitterDescription: str(get("twitter_description")),
		IsCornerstone: get("cornerstone") == "1",
	}
	if get("nofollow") == "1" {
		s.RobotsNofollow = records.Ptr(true)
	}
	if id, u := get("og_image_id"), get("og_image"); id != "" || u != "" {
		s.OGImageMedia = media(id, u)
	}
	if id, u := get("twitter_image_id"), get("twitter_image"); id != "" || u != "" {
		s.TwitterImageMedia = media(id, u)
	}
	return s
}

// Term builds term SEO from one wpseo_taxonomy_meta entry.
func Term(m map[string]any, media Resolver) *records.SEO {
	g := func(k string) string {
		switch v := m[k].(type) {
		case string:
			return v
		case int64:
			return strconv.FormatInt(v, 10)
		}
		return ""
	}
	s := &records.SEO{
		Title: str(g("wpseo_title")), Description: str(g("wpseo_desc")), Canonical: str(g("wpseo_canonical")),
		BreadcrumbTitle: str(g("wpseo_bctitle")), FocusKeyphrase: str(g("wpseo_focuskw")),
		OGTitle: str(g("wpseo_opengraph-title")), OGDescription: str(g("wpseo_opengraph-description")),
		TwitterTitle: str(g("wpseo_twitter-title")), TwitterDescription: str(g("wpseo_twitter-description")),
		IsCornerstone: g("wpseo_is_cornerstone") == "1",
	}
	switch g("wpseo_noindex") {
	case "noindex":
		s.RobotsNoindex = records.Ptr(true)
	case "index":
		s.RobotsNoindex = records.Ptr(false)
	}
	if id, u := g("wpseo_opengraph-image-id"), g("wpseo_opengraph-image"); id != "" || u != "" {
		s.OGImageMedia = media(id, u)
	}
	if id, u := g("wpseo_twitter-image-id"), g("wpseo_twitter-image"); id != "" || u != "" {
		s.TwitterImageMedia = media(id, u)
	}
	return s
}

// TemplateKeys maps AFSC template types to wpseo_titles keys (title, description), first match wins.
var TemplateKeys = map[string][2][]string{
	"post":     {{"title-post"}, {"metadesc-post"}},
	"page":     {{"title-page"}, {"metadesc-page"}},
	"category": {{"title-tax-category", "title-category"}, {"metadesc-tax-category", "metadesc-category"}},
	"tag":      {{"title-tax-post_tag"}, {"metadesc-tax-post_tag"}},
	"author":   {{"title-author-wpseo"}, {"metadesc-author-wpseo"}},
	"home":     {{"title-home-wpseo"}, {"metadesc-home-wpseo"}},
	"search":   {{"title-search-wpseo"}, nil},
	"404":      {{"title-404-wpseo"}, nil},
	"date":     {{"title-archive-wpseo"}, {"metadesc-archive-wpseo"}},
}

// Templates reads title and description templates from wpseo_titles.
func Templates(titles map[string]any) (title, desc map[string]string) {
	title, desc = map[string]string{}, map[string]string{}
	first := func(keys []string) string {
		for _, k := range keys {
			if v, ok := titles[k].(string); ok && strings.TrimSpace(v) != "" {
				return v
			}
		}
		return ""
	}
	for typ, keys := range TemplateKeys {
		if v := first(keys[0]); v != "" {
			title[typ] = v
		}
		if v := first(keys[1]); v != "" {
			desc[typ] = v
		}
	}
	return title, desc
}

// Flag reads a Yoast boolean option (true, "1", "on").
func Flag(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case int64:
		return v == 1
	case string:
		return v == "1" || v == "on" || v == "true"
	}
	return false
}

var videoProviders = map[string]bool{"youtube": true, "vimeo": true}

// Video maps one Yoast Video meta array to a video record. embedURL canonicalizes provider URLs.
func Video(m map[string]any, embedURL func(string) (provider, url, id string, ok bool), media Resolver) (records.Video, bool) {
	g := func(k string) string {
		switch v := m[k].(type) {
		case string:
			return strings.TrimSpace(v)
		case int64:
			return strconv.FormatInt(v, 10)
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		}
		return ""
	}
	v := records.Video{Title: g("title"), Description: g("description")}
	for _, k := range []string{"url", "player_loc", "content_loc"} {
		u := g(k)
		if !strings.HasPrefix(u, "http://") && !strings.HasPrefix(u, "https://") {
			continue
		}
		if p, cu, id, ok := embedURL(u); ok {
			v.Provider, v.URL, v.ExternalID = p, cu, id
			break
		}
		if v.URL == "" {
			v.URL = u
		}
	}
	if v.URL == "" {
		return v, false
	}
	if t := g("type"); v.Provider == "" && videoProviders[t] {
		v.Provider = t
	}
	if v.ExternalID == "" && v.Provider != "" {
		v.ExternalID = g("id")
	}
	if th := g("thumbnail_loc"); strings.HasPrefix(th, "http") {
		v.ThumbnailURL = th
		v.ThumbnailMedia = media("", th)
	}
	if d, err := strconv.ParseFloat(g("duration"), 64); err == nil && d > 0 {
		v.DurationSeconds = records.Ptr(int(d))
	}
	for _, layout := range []string{time.RFC3339, "2006-01-02T15:04:05-0700", "2006-01-02 15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, g("publication_date")); err == nil {
			t = t.UTC()
			v.UploadDate = &t
			break
		}
	}
	return v, true
}
