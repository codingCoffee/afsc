package theme

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"time"

	"afsc/internal/content"
)

func (t *Theme) funcMap(st *state) template.FuncMap {
	fm := template.FuncMap{}
	for _, name := range st.missing {
		fm[name] = missingFunc
	}
	maps.Copy(fm, t.opts.Funcs)
	maps.Copy(fm, t.coreFuncs(st))
	return fm
}

func (t *Theme) coreFuncs(st *state) template.FuncMap {
	return template.FuncMap{
		"head":          t.head(st),
		"footer":        t.footer,
		"body_class":    BodyClass,
		"post_class":    PostClass,
		"url":           t.url,
		"asset":         st.assetURL,
		"image":         st.image,
		"image_url":     t.imageURL(st),
		"avatar":        st.avatar,
		"menu":          t.menu(st),
		"pagination":    st.pagination,
		"breadcrumbs":   st.breadcrumbs,
		"excerpt":       st.excerpt,
		"teaser_html":   teaserHTML,
		"reading_time":  ReadingTime,
		"date":          t.date,
		"setting":       t.setting,
		"theme_setting": t.themeSetting(st),
		"t":             st.str,
	}
}

func id(n int64) string { return strconv.FormatInt(n, 10) }

// BodyClass returns WordPress-compatible body classes for the page.
func BodyClass(d *PageData) string {
	var c []string
	switch d.Kind {
	case KindHome:
		c = []string{"home", "blog"}
	case KindFrontPage:
		c = append([]string{"home"}, pageClasses(d.Post)...)
	case KindPostsPage:
		c = []string{"blog"}
	case KindSingle:
		c = []string{"post-template-default", "single", "single-post", "postid-" + id(d.Post.WPID), "single-format-standard"}
	case KindPage:
		c = pageClasses(d.Post)
	case KindCategory, KindTag:
		c = []string{"archive", d.Kind, d.Kind + "-" + d.Term.Slug, d.Kind + "-" + id(d.Term.WPID)}
	case KindAuthor:
		c = []string{"archive", "author", "author-" + d.Author.Slug, "author-" + id(d.Author.WPID)}
	case KindDate:
		c = []string{"archive", "date"}
	case KindSearch:
		c = []string{"search", "search-no-results"}
		if len(d.Posts) > 0 {
			c[1] = "search-results"
		}
	case Kind404, Kind410:
		c = []string{"error404"}
	}
	if d.Request.Paged > 1 {
		c = append(c, "paged", "paged-"+strconv.Itoa(d.Request.Paged))
	}
	return strings.Join(c, " ")
}

func pageClasses(p *Post) []string {
	c := []string{"page-template-default"}
	if p.Template != "" {
		c = []string{"page-template", "page-template-" + p.Template}
	}
	c = append(c, "page", "page-id-"+id(p.WPID))
	if n := len(p.Ancestors); n > 0 {
		c = append(c, "page-child", "parent-pageid-"+id(p.Ancestors[n-1].WPID))
	}
	return c
}

// PostClass returns WordPress-compatible classes for an article element.
func PostClass(p *Post) string {
	c := []string{"post-" + id(p.WPID), p.Type, "type-" + p.Type, "status-publish"}
	if p.Type == "post" {
		c = append(c, "format-standard")
	}
	if p.FeaturedMedia != nil {
		c = append(c, "has-post-thumbnail")
	}
	if p.Sticky {
		c = append(c, "sticky")
	}
	c = append(c, "hentry")
	for _, t := range p.Categories {
		c = append(c, "category-"+t.Slug)
	}
	for _, t := range p.Tags {
		c = append(c, "tag-"+t.Slug)
	}
	return strings.Join(c, " ")
}

// url returns the absolute URL of a post, term, author, media item, or a root-relative path.
func (t *Theme) url(v any) (string, error) {
	switch x := v.(type) {
	case *Post:
		if x != nil {
			return x.URL, nil
		}
	case *Term:
		if x != nil {
			return x.URL, nil
		}
	case *Author:
		if x != nil {
			return x.URL, nil
		}
	case *Media:
		if x != nil {
			return x.URL, nil
		}
	case string:
		if strings.HasPrefix(x, "/") && !strings.HasPrefix(x, "//") {
			return t.opts.Backend.BaseURL() + x, nil
		}
		return "", fmt.Errorf("url: %q is not a root-relative path", x)
	}
	return "", fmt.Errorf("url: cannot build a URL for %T", v)
}

func (m *Media) info() content.MediaInfo {
	return content.MediaInfo{ID: m.ID, LegacyID: m.WPID, Filename: m.Filename, Version: m.Version, Mime: m.Mime,
		Width: m.Width, Height: m.Height}
}

func (st *state) variant(fn string, m *Media, size string) (string, int, int, error) {
	v, w, h, ok := st.manifest.Images().Variant(m.info(), size)
	if !ok {
		return "", 0, 0, fmt.Errorf("%s: image size %q is not declared in theme.json", fn, size)
	}
	return v, w, h, nil
}

// image renders m at a theme size as an <img> with srcset and sizes; an optional third argument replaces sizes.
func (st *state) image(m *Media, size string, sizes ...string) (template.HTML, error) {
	if m == nil {
		return "", nil
	}
	v, w, h, err := st.variant("image", m, size)
	if err != nil {
		return "", err
	}
	esc := template.HTMLEscapeString
	info := m.info()
	b := `<img src="` + esc(content.MediaURL(info, v)) + `" alt="` + esc(m.Alt) + `"`
	if w > 0 && h > 0 {
		b += ` width="` + strconv.Itoa(w) + `" height="` + strconv.Itoa(h) + `"`
	}
	if srcset := st.manifest.Images().Srcset(info, v); srcset != "" {
		s := content.SizesAttr(w)
		if len(sizes) > 0 {
			s = sizes[0]
		}
		b += ` srcset="` + esc(srcset) + `" sizes="` + esc(s) + `"`
	}
	size = cmp.Or(size, "full")
	return template.HTML(b + ` class="attachment-` + esc(size) + ` size-` + esc(size) + `">`), nil
}

// imageURL returns the absolute URL of m at a theme size, or of the original without one.
func (t *Theme) imageURL(st *state) func(m *Media, size ...string) (string, error) {
	return func(m *Media, size ...string) (string, error) {
		if m == nil {
			return "", nil
		}
		v, _, _, err := st.variant("image_url", m, append(size, "")[0])
		if err != nil {
			return "", err
		}
		return t.opts.Backend.BaseURL() + content.MediaURL(m.info(), v), nil
	}
}

// avatar renders an author's avatar at a theme size, or an initials SVG coloured from a hash of the name (§20.1).
// avatar prints an author's avatar image, or an initials SVG for authors without one and for every commenter
// (§20.1: no Gravatar).
func (st *state) avatar(v any, size string) (template.HTML, error) {
	var name string
	var m *Media
	switch x := v.(type) {
	case *Author:
		if x == nil {
			return "", nil
		}
		name, m = x.Name, x.Avatar
	case *Comment:
		if x == nil {
			return "", nil
		}
		name = x.AuthorName
	case nil:
		return "", nil
	default:
		return "", fmt.Errorf("avatar: want an author or a comment, got %T", v)
	}
	if _, _, _, ok := st.manifest.Images().Variant(content.MediaInfo{}, size); !ok {
		return "", fmt.Errorf("avatar: image size %q is not declared in theme.json", size)
	}
	esc := template.HTMLEscapeString
	if m != nil {
		v, w, h, err := st.variant("avatar", m, size)
		if err != nil {
			return "", err
		}
		b := `<img src="` + esc(content.MediaURL(m.info(), v)) + `" alt="` + esc(name) + `"`
		if w > 0 && h > 0 {
			b += ` width="` + strconv.Itoa(w) + `" height="` + strconv.Itoa(h) + `"`
		}
		if srcset := st.manifest.Images().Srcset(m.info(), v); srcset != "" {
			b += ` srcset="` + esc(srcset) + `" sizes="` + strconv.Itoa(w) + `px"`
		}
		return template.HTML(b + ` class="avatar" loading="lazy">`), nil
	}
	px := 96
	if sz, ok := st.manifest.Images().Sizes[size]; ok && sz.Width > 0 {
		px = sz.Width
	}
	return InitialsAvatar(name, px), nil
}

// InitialsAvatar is an SVG with the first and last initials of name, coloured from an FNV-1a hash of the name.
func InitialsAvatar(name string, px int) template.HTML {
	esc := template.HTMLEscapeString
	hash := uint32(2166136261)
	for _, c := range []byte(name) {
		hash = (hash ^ uint32(c)) * 16777619
	}
	initials := ""
	if words := strings.Fields(name); len(words) > 0 {
		initials = string([]rune(words[0])[:1])
		if len(words) > 1 {
			initials += string([]rune(words[len(words)-1])[:1])
		}
	}
	n := strconv.Itoa(px)
	return template.HTML(`<svg class="avatar avatar-initials" width="` + n + `" height="` + n + `" viewBox="0 0 ` + n + ` ` + n +
		`" role="img" aria-label="` + esc(name) + `"><rect width="100%" height="100%" fill="hsl(` +
		strconv.Itoa(int(hash%360)) + ` 45% 40%)"/><text x="50%" y="50%" dy=".35em" text-anchor="middle" fill="#fff" ` +
		`font-family="sans-serif" font-size="` + strconv.Itoa(px*2/5) + `">` + esc(strings.ToUpper(initials)) + `</text></svg>`)
}

func (t *Theme) menu(st *state) func(d *PageData, loc string) ([]*MenuItem, error) {
	return func(d *PageData, loc string) ([]*MenuItem, error) {
		if !slices.Contains(st.manifest.MenuLocations, loc) {
			return nil, fmt.Errorf("menu: location %q is not declared in theme.json", loc)
		}
		items, err := t.opts.Backend.Menu(loc)
		if err != nil {
			return nil, err
		}
		out, _ := markCurrent(items, d.Request.URL)
		return out, nil
	}
}

// markCurrent copies items, flagging the one linking to here and its ancestors. Backend items are shared.
func markCurrent(items []*MenuItem, here string) ([]*MenuItem, bool) {
	out := make([]*MenuItem, len(items))
	found := false
	for i, it := range items {
		c := *it
		var sub bool
		c.Children, sub = markCurrent(it.Children, here)
		c.Current = here != "" && c.URL == here
		c.CurrentAncestor = sub
		found = found || c.Current || sub
		out[i] = &c
	}
	return out, found
}

// Class returns the WordPress-style class list of a menu item.
func (m *MenuItem) Class() string {
	c := []string{"menu-item", "menu-item-type-" + m.Kind, "menu-item-" + id(m.ID)}
	if len(m.Children) > 0 {
		c = append(c, "menu-item-has-children")
	}
	if m.Current {
		c = append(c, "current-menu-item")
	}
	if m.CurrentAncestor {
		c = append(c, "current-menu-ancestor", "current-menu-parent")
	}
	return strings.Join(append(c, m.Classes...), " ")
}

// pagination returns numbered links with WordPress paginate_links markup.
func (st *state) pagination(d *PageData) template.HTML {
	p := d.Pagination
	if p == nil || p.Total <= 1 {
		return ""
	}
	esc := template.HTMLEscapeString
	var parts []string
	if p.Prev != "" {
		parts = append(parts, `<a class="prev page-numbers" href="`+esc(p.Prev)+`">`+esc(st.strings["pagination.prev"])+`</a>`)
	}
	for _, l := range p.Links {
		switch {
		case l.Dots:
			parts = append(parts, `<span class="page-numbers dots">&hellip;</span>`)
		case l.Current:
			parts = append(parts, `<span aria-current="page" class="page-numbers current">`+strconv.Itoa(l.N)+`</span>`)
		default:
			parts = append(parts, `<a class="page-numbers" href="`+esc(l.URL)+`">`+strconv.Itoa(l.N)+`</a>`)
		}
	}
	if p.Next != "" {
		parts = append(parts, `<a class="next page-numbers" href="`+esc(p.Next)+`">`+esc(st.strings["pagination.next"])+`</a>`)
	}
	return template.HTML(strings.Join(parts, "\n"))
}

// Breadcrumbs is the trail the breadcrumbs func returns, for building the JSON-LD BreadcrumbList.
func (t *Theme) Breadcrumbs(d *PageData) []Crumb {
	st, _ := t.current()
	return st.breadcrumbs(d)
}

func (st *state) breadcrumbs(d *PageData) []Crumb {
	out := []Crumb{{Name: st.strings["breadcrumbs.home"], URL: d.Site.URL + "/"}}
	terms := func(ts []*Term) {
		for _, t := range ts {
			out = append(out, Crumb{Name: cmp.Or(t.Breadcrumb, t.Name), URL: t.URL})
		}
	}
	post := func(p *Post) Crumb { return Crumb{Name: cmp.Or(p.Breadcrumb, p.Title)} }
	switch d.Kind {
	case KindHome, KindFrontPage:
		out[0].URL = ""
	case KindPostsPage:
		out = append(out, post(d.Post))
	case KindSingle:
		primary := d.Post.Primary
		if primary == nil && len(d.Post.Categories) > 0 {
			primary = d.Post.Categories[0]
		}
		if primary != nil {
			terms(primary.Ancestors)
			terms([]*Term{primary})
		}
		out = append(out, post(d.Post))
	case KindPage:
		for _, a := range d.Post.Ancestors {
			out = append(out, Crumb{Name: cmp.Or(a.Breadcrumb, a.Title), URL: a.URL})
		}
		out = append(out, post(d.Post))
	case KindCategory, KindTag:
		terms(d.Term.Ancestors)
		out = append(out, Crumb{Name: cmp.Or(d.Term.Breadcrumb, d.Term.Name)})
	case KindAuthor:
		out = append(out, Crumb{Name: cmp.Or(d.Author.Breadcrumb, d.Author.Name)})
	case KindDate:
		if d.Date.Month > 0 {
			out = append(out, Crumb{Name: strconv.Itoa(d.Date.Year), URL: d.Date.YearURL},
				Crumb{Name: d.Date.Start.Format("January 2006")})
		} else {
			out = append(out, Crumb{Name: strconv.Itoa(d.Date.Year)})
		}
	case KindSearch:
		out = append(out, Crumb{Name: st.strings["breadcrumbs.search"] + " “" + d.Search.Query + "”"})
	case Kind404, Kind410:
		out = append(out, Crumb{Name: st.strings["breadcrumbs.404"]})
	}
	return out
}

// head prints headTags followed by the admin's head snippet (§7.6).
func (t *Theme) head(st *state) func(d *PageData) (template.HTML, error) {
	return func(d *PageData) (template.HTML, error) {
		h, err := st.headTags(d)
		if err != nil {
			return "", err
		}
		if t.opts.HeadFilter != nil {
			h = t.opts.HeadFilter(d, h)
		}
		return h + template.HTML(t.opts.Backend.Settings().Snippets.HeadHTML), nil
	}
}

// footer prints plugin body.end output and the admin's body-end snippet; themes call it right before </body>.
func (t *Theme) footer(d *PageData) template.HTML {
	var h template.HTML
	if t.opts.BodyFilter != nil {
		h = t.opts.BodyFilter(d, h)
	}
	return h + template.HTML(t.opts.Backend.Settings().Snippets.BodyEndHTML)
}

// headTags prints the title, SEO tags, theme assets and feed links. Without resolved SEO it prints a bare title.
func (st *state) headTags(d *PageData) (template.HTML, error) {
	esc := template.HTMLEscapeString
	var b strings.Builder
	if h := d.Head; h != nil {
		b.WriteString("<title>" + esc(h.Title) + "</title>\n")
		b.WriteString(string(h.Tags))
	} else {
		b.WriteString("<title>" + esc(d.Site.Title) + "</title>\n")
		if d.Request.NoIndex {
			b.WriteString(`<meta name="robots" content="noindex, follow">` + "\n")
		}
	}
	for _, a := range st.manifest.Assets {
		u, err := st.assetURL(a.Path)
		if err != nil {
			return "", err
		}
		switch path.Ext(a.Path) {
		case ".css":
			b.WriteString(`<link rel="stylesheet" href="` + esc(u) + "\">\n")
		case ".js":
			attr := ""
			if a.Defer {
				attr += " defer"
			}
			if a.Async {
				attr += " async"
			}
			b.WriteString(`<script src="` + esc(u) + `"` + attr + "></script>\n")
		}
	}
	feed := func(title, href string) {
		b.WriteString(`<link rel="alternate" type="application/rss+xml" title="` + esc(title) + `" href="` + esc(href) + "\">\n")
	}
	site := d.Site.URL + "/feed/"
	feed(d.Site.Title+" » "+st.strings["feed.label"], site)
	if d.FeedURL != "" && d.FeedURL != site {
		feed(d.Site.Title+" » "+st.archiveName(d)+" "+st.strings["feed.label"], d.FeedURL)
	}
	return template.HTML(b.String()), nil
}

func (st *state) archiveName(d *PageData) string {
	switch {
	case d.Term != nil:
		return d.Term.Name
	case d.Author != nil:
		return d.Author.Name
	}
	return ""
}

// excerpt is the explicit excerpt, else the text before readMore, else the first n words (default 55).
func (st *state) excerpt(p *Post, n ...int) (string, error) {
	if p == nil {
		return "", errors.New("excerpt: nil post")
	}
	if p.Excerpt != "" {
		return p.Excerpt, nil
	}
	if p.HasMore {
		return p.TeaserText, nil
	}
	limit := 55
	if len(n) > 0 {
		limit = n[0]
	}
	return TrimWords(p.ContentText, limit, st.strings["excerpt.more"]), nil
}

// TrimWords keeps the first n words of s, appending more when it cut anything.
func TrimWords(s string, n int, more string) string {
	words := strings.Fields(s)
	if len(words) <= n {
		return strings.Join(words, " ")
	}
	return strings.Join(words[:n], " ") + more
}

func teaserHTML(p *Post) template.HTML {
	if p.HasMore {
		return p.Teaser
	}
	return p.ContentHTML
}

// ReadingTime is the estimated reading time in minutes at 225 words per minute, at least 1.
func ReadingTime(p *Post) int {
	return max(1, (len(strings.Fields(p.ContentText))+224)/225)
}

func (t *Theme) location() *time.Location {
	name := t.opts.Backend.Settings().Site.Timezone
	if loc, ok := t.zones.Load(name); ok {
		return loc.(*time.Location)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		loc = time.UTC
	}
	t.zones.Store(name, loc)
	return loc
}

// date formats a time in the site timezone, with the site date format unless a Go layout is given.
func (t *Theme) date(v any, layout ...string) (string, error) {
	var tm time.Time
	switch x := v.(type) {
	case time.Time:
		tm = x
	case *time.Time:
		if x == nil {
			return "", nil
		}
		tm = *x
	default:
		return "", fmt.Errorf("date: cannot format %T", v)
	}
	l := t.opts.Backend.Settings().Site.DateFormat
	if len(layout) > 0 {
		l = layout[0]
	}
	return tm.In(t.location()).Format(l), nil
}

// setting returns a site setting by dotted path, for example "site.title" or "reading.posts_per_page".
func (t *Theme) setting(path string) (any, error) {
	b, err := json.Marshal(t.opts.Backend.Settings())
	if err != nil {
		return nil, err
	}
	var v any
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	for _, k := range strings.Split(path, ".") {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("setting: %q not found", path)
		}
		if v, ok = m[k]; !ok {
			return nil, fmt.Errorf("setting: %q not found", path)
		}
	}
	return v, nil
}

// themeSetting returns the admin's value for this theme, else the theme.json default.
func (t *Theme) themeSetting(st *state) func(key string) (any, error) {
	return func(key string) (any, error) {
		s, ok := st.manifest.Settings[key]
		if !ok {
			return nil, fmt.Errorf("theme_setting: %q is not declared in theme.json", key)
		}
		if v, ok := t.opts.Backend.Settings().ThemeSettings[st.manifest.Name][key]; ok {
			return v, nil
		}
		return s.Default, nil
	}
}

func (st *state) str(key string) (string, error) {
	s, ok := st.strings[key]
	if !ok {
		return "", fmt.Errorf("t: no string %q in theme.json", key)
	}
	return s, nil
}
