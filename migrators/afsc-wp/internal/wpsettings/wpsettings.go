package wpsettings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"strings"

	"afsc/internal/settings"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/permalink"
	"afsc/migrators/afsc-wp/internal/wpseo"
)

type Input struct {
	DS       *extract.Dataset
	Timezone string // IANA name resolved by the caller ("" when WordPress used a fractional gmt_offset)
	Language string
	Yoast    bool
	Keys     struct{ Titles, Social string }
	// Decide and Option read confirmed decisions.
	Decide   func(kind, name, def string) string
	Option   func(kind, name, key string) string
	Page     func(id int64) (string, bool)
	Media    wpseo.Resolver
	Variable func(name string) wpseo.Variable
	// RobotsTxt is the site's customized robots.txt, "" for WordPress's virtual default.
	RobotsTxt   string
	IndexNowKey string
	UserName    func(id int64) string
}

type Output struct {
	All      settings.All
	Problems []string // blocking: transform fails
	Notes    []string // reported
	// TypeNoindex holds Yoast's per-type noindex defaults AFSC has no setting for (applied per entity).
	TypeNoindex       map[string]bool
	DisableAttachment bool
	WPStructure       string
}

func intOpt(ds *extract.Dataset, name string, def int) int {
	if v, err := strconv.Atoi(ds.OptionString(name)); err == nil {
		return v
	}
	return def
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }

// Build maps options to settings. Every value is checked by settings.Validate before it's returned.
func Build(in Input) Output {
	ds := in.DS
	out := Output{All: settings.Defaults(), TypeNoindex: map[string]bool{}}
	a := &out.All
	problem := func(f string, args ...any) { out.Problems = append(out.Problems, fmt.Sprintf(f, args...)) }
	note := func(f string, args ...any) { out.Notes = append(out.Notes, fmt.Sprintf(f, args...)) }

	a.Site.Title = ds.OptionString("blogname")
	a.Site.Tagline = ds.OptionString("blogdescription")
	if in.Timezone == "" {
		problem("site timezone: WordPress uses a fractional gmt_offset with no IANA zone; set site.timezone in afsc-wp.json")
	} else {
		a.Site.Timezone = in.Timezone
	}
	if f := ds.OptionString("date_format"); f != "" {
		if layout, err := DateLayout(f); err == nil {
			a.Site.DateFormat = layout
		} else {
			note("date_format %q kept as AFSC's default: %v", f, err)
		}
	}
	a.Site.Language = in.Language
	if a.Site.Language == "" {
		a.Site.Language = "en"
		if wl := ds.OptionString("WPLANG"); wl != "" {
			a.Site.Language = strings.ReplaceAll(wl, "_", "-")
		}
	}

	out.WPStructure = ds.OptionString("permalink_structure")
	pattern, trailing, unsupported := permalink.Convert(out.WPStructure)
	switch in.Decide("permalink_structure", out.WPStructure, "keep") {
	case "change":
		pattern = in.Option("permalink_structure", out.WPStructure, "pattern")
		note("permalink structure %q changes to %q; every post gets a 301", out.WPStructure, pattern)
	default:
		if len(unsupported) > 0 {
			problem("permalink_structure %q uses %s, which AFSC can't serve; decide permalink_structure: change", out.WPStructure, strings.Join(unsupported, ", "))
		}
	}
	a.Permalinks.PostPattern, a.Permalinks.TrailingSlash = pattern, trailing
	for _, tb := range []struct {
		opt, kind, def string
		dst            *string
	}{{"category_base", "category", "category", &a.Permalinks.CategoryBase}, {"tag_base", "tag", "tag", &a.Permalinks.TagBase}} {
		base := strings.Trim(ds.OptionString(tb.opt), "/")
		if base == "" {
			base = tb.def
		}
		if base == "." || strings.ContainsAny(base, "/?#{} ") {
			if in.Decide("category_base", tb.kind, "keep") == "change" {
				base = in.Option("category_base", tb.kind, "base")
			} else {
				problem("%s %q is not a single path segment; decide category_base:%s change", tb.opt, base, tb.kind)
			}
		}
		*tb.dst = base
	}

	r := &a.Reading
	if ds.OptionString("show_on_front") == "page" {
		if id := int64(intOpt(ds, "page_on_front", 0)); id > 0 {
			if k, ok := in.Page(id); ok {
				r.Home = &settings.Ref{LegacyKey: k}
			} else {
				problem("page_on_front %d is not an imported page", id)
			}
		}
		if id := int64(intOpt(ds, "page_for_posts", 0)); id > 0 {
			if k, ok := in.Page(id); ok {
				r.PostsPage = &settings.Ref{LegacyKey: k}
			} else {
				note("page_for_posts %d is not an imported page; posts page not set", id)
			}
		}
	}
	r.PostsPerPage = clamp(intOpt(ds, "posts_per_page", 10), 1, 100)
	r.FeedItems = clamp(intOpt(ds, "posts_per_rss", 10), 1, 100)
	r.FeedExcerptOnly = ds.OptionString("rss_use_excerpt") == "1"

	d := &a.Discussion
	if ds.OptionString("close_comments_for_old_posts") == "1" {
		d.CloseAfterDays = max(intOpt(ds, "close_comments_days_old", 14), 0)
	}
	if ds.OptionString("thread_comments") == "1" {
		d.ThreadDepth = clamp(intOpt(ds, "thread_comments_depth", 5), 1, 10)
	} else if _, ok := ds.Options["thread_comments"]; ok {
		d.ThreadDepth = 1
	}
	if ds.OptionString("page_comments") == "1" {
		d.CommentsPerPage = clamp(intOpt(ds, "comments_per_page", 50), 0, 500)
	} else if _, ok := ds.Options["page_comments"]; ok {
		d.CommentsPerPage = 0
	}

	s := &a.SEO
	s.RobotsTxt = in.RobotsTxt
	if in.IndexNowKey != "" {
		s.IndexNow = settings.IndexNow{Enabled: true, Key: in.IndexNowKey}
	}
	if in.Yoast {
		yoast(in, out.TypeNoindex, &out.DisableAttachment, a, note)
	}

	if err := a.Validate(); err != nil {
		problem("settings: %v", err)
	}
	return out
}

func yoast(in Input, typeNoindex map[string]bool, disableAttachment *bool, a *settings.All, note func(string, ...any)) {
	titles := in.DS.OptionMap(in.Keys.Titles)
	social := in.DS.OptionMap(in.Keys.Social)
	s := &a.SEO
	if sep, ok := titles["separator"].(string); ok {
		if c, ok := wpseo.Separators[sep]; ok {
			s.Separator = c
		} else {
			note("unknown Yoast separator %q; AFSC default kept", sep)
		}
	}
	tt, dt := wpseo.Templates(titles)
	for typ, v := range tt {
		s.TitleTemplates[typ] = wpseo.Apply(v, in.Variable, nil)
	}
	for typ, v := range dt {
		s.DescriptionTemplates[typ] = wpseo.Apply(v, in.Variable, nil)
	}
	for _, k := range []string{"post", "page", "tax-category", "author-wpseo", "archive-wpseo"} {
		if wpseo.Flag(titles, "noindex-"+k) {
			typeNoindex[k] = true
		}
	}
	s.TagNoindex = wpseo.Flag(titles, "noindex-tax-post_tag")
	a.Permalinks.DateArchivesEnabled = !wpseo.Flag(titles, "disable-date")
	a.Permalinks.AuthorArchivesEnabled = !wpseo.Flag(titles, "disable-author")
	*disableAttachment = wpseo.Flag(titles, "disable-attachment")

	kg := &s.KnowledgeGraph
	if titles["company_or_person"] == "person" {
		kg.Type = "person"
		if id, err := strconv.ParseInt(fmt.Sprint(titles["company_or_person_user_id"]), 10, 64); err == nil && in.UserName != nil {
			kg.Name = in.UserName(id)
		}
		if k := in.Media(fmt.Sprint(titles["person_logo_id"]), str(titles["person_logo"])); k != nil {
			kg.Logo = &settings.Ref{LegacyKey: *k}
		}
	} else {
		kg.Type = "organization"
		kg.Name = str(titles["company_name"])
		if k := in.Media(fmt.Sprint(titles["company_logo_id"]), str(titles["company_logo"])); k != nil {
			kg.Logo = &settings.Ref{LegacyKey: *k}
		}
	}
	for _, k := range []string{"facebook_site", "instagram_url", "linkedin_url", "myspace_url", "pinterest_url", "youtube_url", "wikipedia_url"} {
		if u := str(social[k]); strings.HasPrefix(u, "https://") {
			s.SocialProfiles = append(s.SocialProfiles, u)
		} else if u != "" {
			note("social profile %s is not https; skipped", k)
		}
	}
	if others, ok := social["other_social_urls"].([]any); ok {
		for _, o := range others {
			if u := str(o); strings.HasPrefix(u, "https://") {
				s.SocialProfiles = append(s.SocialProfiles, u)
			}
		}
	}
	if h := strings.TrimPrefix(str(social["twitter_site"]), "@"); h != "" {
		s.TwitterSite = "@" + h
		s.SocialProfiles = append(s.SocialProfiles, "https://x.com/"+h)
	}
	if k := in.Media(fmt.Sprint(social["og_default_image_id"]), str(social["og_default_image"])); k != nil {
		s.DefaultImage = &settings.Ref{LegacyKey: *k}
	}
}

func str(v any) string {
	switch x := v.(type) {
	case string:
		return strings.TrimSpace(x)
	case int64:
		return strconv.FormatInt(x, 10)
	}
	return ""
}

// Groups are the settings keys a bundle carries (docs/import-format.md).
var Groups = []string{"site", "permalinks", "reading", "seo", "discussion"}

// Encode writes only the bundle groups, then decodes them with AFSC's own decoder as a check.
func Encode(a settings.All) ([]byte, error) {
	m := map[string]any{}
	for _, g := range Groups {
		m[g] = a.Group(g)
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	if _, _, err := settings.Decode(b.Bytes()); err != nil {
		return nil, err
	}
	return b.Bytes(), nil
}

// ReadRobots reads a customized robots.txt file, "" when no path is set.
func ReadRobots(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	b, err := os.ReadFile(path)
	return string(b), err
}
