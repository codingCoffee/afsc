// Package scaffold writes a starter AFSC theme from the inventory: theme.json (classes, sizes, menus, settings,
// CSP hosts), a README mapping WordPress templates to AFSC ones, and one partial per widget instance. Porting the
// markup is per-site work done later.
package scaffold

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"afsc/internal/theme"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/report"
)

type Input struct {
	DS       *extract.Dataset
	Classes  map[string]int // content class inventory
	CodeDirs []string       // theme (then child theme) directories
	Selector string         // content selector, to find content-scoped CSS
	Out      string
}

type ImageSize struct {
	Width  int  `json:"width"`
	Height int  `json:"height"`
	Crop   bool `json:"crop"`
}

type Setting struct {
	Type    string `json:"type"`
	Label   string `json:"label"`
	Default any    `json:"default"`
}

type Manifest struct {
	Name          string               `json:"name"`
	Version       string               `json:"version"`
	ImageSizes    map[string]ImageSize `json:"image_sizes"`
	SrcsetWidths  []int                `json:"srcset_widths"`
	MenuLocations []string             `json:"menu_locations"`
	Content       struct {
		AllowedClasses []string `json:"allowed_classes"`
	} `json:"content"`
	Assets   []any               `json:"assets"`
	CSP      map[string][]string `json:"csp"`
	Settings map[string]Setting  `json:"settings,omitempty"`
}

var (
	classRe       = regexp.MustCompile(`^[A-Za-z_-][A-Za-z0-9_-]{0,63}$`)
	hardMechanic  = regexp.MustCompile(`^(align(left|right|center|none)|wp-image-\d+|size-[a-z0-9_-]+|has-text-align-(left|center|right|justify))$`)
	addImageSize  = regexp.MustCompile(`add_image_size\s*\(\s*['"]([a-z0-9_-]+)['"]\s*,\s*(\d+)\s*,\s*(\d+)\s*(?:,\s*(true|false|array))?`)
	registerMenus = regexp.MustCompile(`register_nav_menus?\s*\(\s*(?:array\s*\(|\[)?([^;]*)`)
	menuKey       = regexp.MustCompile(`['"]([a-z0-9_-]+)['"]\s*=>`)
	templateName  = regexp.MustCompile(`(?m)^[ \t/*#@]*Template Name:\s*(.+)$`)
	hostRe        = regexp.MustCompile(`https?://([a-z0-9.-]+\.[a-z]{2,})[^\s'"()<>]*`)
	cssClassRe    = regexp.MustCompile(`\.([A-Za-z_-][A-Za-z0-9_-]*)`)
	settingKeyRe  = regexp.MustCompile(`[^a-z0-9_]+`)
	hexColorRe    = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)
)

// tracking hosts are reported for admin code snippets, never put in the theme CSP.
var tracking = []string{"google-analytics.com", "googletagmanager.com", "doubleclick.net", "facebook.net", "connect.facebook.net",
	"hotjar.com", "clarity.ms", "segment.com", "mixpanel.com", "quantserve.com", "scorecardresearch.com", "chartbeat.com",
	"stats.wp.com", "pixel.wp.com", "googlesyndication.com", "adservice.google.com", "taboola.com", "outbrain.com"}

func isTracking(h string) bool {
	for _, t := range tracking {
		if h == t || strings.HasSuffix(h, "."+t) {
			return true
		}
	}
	return false
}

type Result struct {
	Manifest  *Manifest
	Templates map[string]string // WP template file → AFSC template
	Widgets   []string
	Tracking  []string
	Hooks     []string
}

func intOpt(ds *extract.Dataset, name string) int {
	n, _ := strconv.Atoi(ds.OptionString(name))
	return n
}

// Run writes the scaffold to in.Out and validates theme.json with AFSC's own loader.
func Run(in Input) (*Result, error) {
	ds := in.DS
	stylesheet := ds.OptionString("stylesheet")
	name := strings.Trim(settingKeyRe.ReplaceAllString(strings.ToLower(stylesheet), "-"), "-_")
	if name == "" {
		name = "ported"
	}
	m := &Manifest{Name: name, Version: "0.1.0", ImageSizes: map[string]ImageSize{}, SrcsetWidths: []int{320, 640, 960, 1280, 1920},
		Assets: []any{}, CSP: map[string][]string{}, MenuLocations: []string{}}
	m.Content.AllowedClasses = []string{}
	for _, sz := range []string{"thumbnail", "medium", "medium_large", "large"} {
		w, h := intOpt(ds, sz+"_size_w"), intOpt(ds, sz+"_size_h")
		if w == 0 && h == 0 {
			continue
		}
		crop := sz == "thumbnail" && ds.OptionString("thumbnail_crop") == "1"
		m.ImageSizes[sz] = ImageSize{Width: w, Height: h, Crop: crop && w > 0 && h > 0}
	}
	res := &Result{Manifest: m, Templates: map[string]string{}}
	locations := map[string]bool{}
	mods := ds.OptionMap("theme_mods_" + stylesheet)
	if locs, ok := mods["nav_menu_locations"].(map[string]any); ok {
		for k := range locs {
			locations[k] = true
		}
	}
	cssClasses := map[string]bool{}
	hosts := map[string]map[string]bool{}
	addHost := func(dir, h string) {
		if isTracking(h) {
			if !slices.Contains(res.Tracking, h) {
				res.Tracking = append(res.Tracking, h)
			}
			return
		}
		if hosts[dir] == nil {
			hosts[dir] = map[string]bool{}
		}
		hosts[dir]["https://"+h] = true
	}
	for _, dir := range in.CodeDirs {
		err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return err
			}
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			switch {
			case strings.HasSuffix(p, ".php"):
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				src := string(b)
				for _, mm := range addImageSize.FindAllStringSubmatch(src, -1) {
					w, _ := strconv.Atoi(mm[2])
					h, _ := strconv.Atoi(mm[3])
					m.ImageSizes[mm[1]] = ImageSize{Width: w, Height: h, Crop: mm[4] == "true" || mm[4] == "array"}
				}
				for _, mm := range registerMenus.FindAllStringSubmatch(src, -1) {
					for _, k := range menuKey.FindAllStringSubmatch(mm[1], -1) {
						locations[k[1]] = true
					}
				}
				if t := templateName.FindStringSubmatch(src); t != nil {
					res.Templates[rel] = "page-" + strings.TrimPrefix(strings.Trim(settingKeyRe.ReplaceAllString(strings.ToLower(strings.TrimSuffix(filepath.Base(rel), ".php")), "-"), "-"), "page-") +
						".html (Template Name: " + strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(t[1]), "*/")) + ")"
				} else if t, ok := templateMap(rel); ok {
					res.Templates[rel] = t
				}
				for _, h := range hostRe.FindAllStringSubmatch(src, -1) {
					if strings.Contains(src, "wp_enqueue") || strings.Contains(src, "<script") || strings.Contains(src, "<link") {
						addHost(cspDir(h[0], src), h[1])
					}
				}
			case strings.HasSuffix(p, ".css"):
				b, err := os.ReadFile(p)
				if err != nil {
					return err
				}
				for _, c := range cssClassRe.FindAllStringSubmatch(string(b), -1) {
					cssClasses[c[1]] = true
				}
				for _, h := range hostRe.FindAllStringSubmatch(string(b), -1) {
					addHost("font", h[1])
				}
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	for dir, set := range hosts {
		for h := range set {
			m.CSP[dir] = append(m.CSP[dir], h)
		}
		sort.Strings(m.CSP[dir])
	}
	for l := range locations {
		m.MenuLocations = append(m.MenuLocations, l)
	}
	sort.Strings(m.MenuLocations)
	for c := range in.Classes {
		if !classRe.MatchString(c) || hardMechanic.MatchString(c) || strings.HasPrefix(c, "wp-image-") {
			continue
		}
		if len(cssClasses) == 0 || cssClasses[c] {
			m.Content.AllowedClasses = append(m.Content.AllowedClasses, c)
		}
	}
	sort.Strings(m.Content.AllowedClasses)
	m.Settings = themeSettings(mods)
	sort.Strings(res.Tracking)

	if err := write(in.Out, m, res, ds); err != nil {
		return nil, err
	}
	if _, err := theme.LoadManifest(in.Out); err != nil {
		return res, fmt.Errorf("scaffolded theme.json does not validate: %w", err)
	}
	return res, nil
}

func cspDir(u, _ string) string {
	switch {
	case strings.Contains(u, "fonts.gstatic.com"), strings.Contains(u, ".woff"), strings.HasSuffix(u, ".ttf"):
		return "font"
	case strings.Contains(u, "fonts.googleapis.com"), strings.Contains(u, ".css"):
		return "style"
	case strings.Contains(u, ".png"), strings.Contains(u, ".jpg"), strings.Contains(u, ".svg"), strings.Contains(u, ".gif"):
		return "img"
	}
	return "script"
}

var templateFiles = map[string]string{
	"index.php": "index.html", "front-page.php": "front-page.html", "home.php": "home.html", "single.php": "single.html",
	"page.php": "page.html", "archive.php": "archive.html", "category.php": "category.html", "tag.php": "tag.html",
	"author.php": "author.html", "date.php": "date.html", "search.php": "search.html", "404.php": "404.html",
	"header.php": "parts/header.html", "footer.php": "parts/footer.html", "sidebar.php": "parts/sidebar.html",
	"comments.php": "parts/comments.html", "searchform.php": "parts/search-form.html",
}

func templateMap(rel string) (string, bool) {
	if t, ok := templateFiles[rel]; ok {
		return t, true
	}
	base := filepath.Base(rel)
	for _, prefix := range []string{"page-", "single-", "category-", "tag-", "author-"} {
		if strings.HasPrefix(base, prefix) && !strings.Contains(rel, "/") {
			return strings.TrimSuffix(base, ".php") + ".html", true
		}
	}
	if strings.HasPrefix(rel, "template-parts/") {
		return "parts/" + strings.TrimSuffix(strings.TrimPrefix(rel, "template-parts/"), ".php") + ".html", true
	}
	return "", false
}

func themeSettings(mods map[string]any) map[string]Setting {
	out := map[string]Setting{}
	for k, v := range mods {
		if k == "nav_menu_locations" || k == "custom_css_post_id" || k == "sidebars_widgets" || k == "0" {
			continue
		}
		key := strings.Trim(settingKeyRe.ReplaceAllString(strings.ToLower(k), "_"), "_")
		if key == "" {
			continue
		}
		label := strings.ReplaceAll(key, "_", " ")
		switch x := v.(type) {
		case bool:
			out[key] = Setting{"bool", label, x}
		case int64:
			out[key] = Setting{"number", label, x}
		case float64:
			out[key] = Setting{"number", label, x}
		case string:
			if hexColorRe.MatchString(x) {
				out[key] = Setting{"color", label, strings.ToLower(x)}
			} else if !strings.HasPrefix(x, "#") {
				out[key] = Setting{"string", label, x}
			}
		}
	}
	return out
}

const baseHTML = `<!doctype html>
<html lang="{{.Site.Language}}">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
{{head .}}
</head>
<body class="{{body_class .}}">
{{block "content" .}}{{end}}
{{footer .}}
</body>
</html>
`

const indexHTML = `{{define "content"}}
{{- with .Post}}
<article class="{{post_class .}}">
  <h1 class="entry-title">{{.Title}}</h1>
  <div class="entry-content">{{.ContentHTML}}</div>
</article>
{{- end}}
{{- range .Posts}}
<article class="{{post_class .}}"><h2 class="entry-title"><a href="{{.URL}}">{{.Title}}</a></h2></article>
{{- end}}
{{end}}
`

func write(out string, m *Manifest, res *Result, ds *extract.Dataset) error {
	for _, d := range []string{"templates/parts", "templates/nodes", "assets"} {
		if err := os.MkdirAll(filepath.Join(out, d), 0o755); err != nil {
			return err
		}
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	files := map[string]string{"theme.json": string(b) + "\n", "templates/base.html": baseHTML, "templates/index.html": indexHTML}
	for path, body := range files {
		if err := writeNew(filepath.Join(out, path), body); err != nil {
			return err
		}
	}
	widgets, err := widgetPartials(out, ds)
	if err != nil {
		return err
	}
	res.Widgets = widgets
	return writeNew(filepath.Join(out, "README.md"), readme(m, res))
}

// writeNew never overwrites a file someone may already have ported, except theme.json.
func writeNew(p, body string) error {
	if _, err := os.Stat(p); err == nil && filepath.Base(p) != "theme.json" {
		return nil
	}
	return os.WriteFile(p, []byte(body), 0o644)
}

// widgetPartials writes parts/widget-<sidebar>-<n>.html for every active widget instance with its content.
func widgetPartials(out string, ds *extract.Dataset) ([]string, error) {
	sidebars := ds.OptionMap("sidebars_widgets")
	var list []string
	for _, sb := range sortedKeys(sidebars) {
		ids, ok := sidebars[sb].([]any)
		if !ok || sb == "wp_inactive_widgets" || sb == "array_version" {
			continue
		}
		for i, v := range ids {
			id, _ := v.(string)
			cut := strings.LastIndexByte(id, '-')
			if cut < 0 {
				continue
			}
			typ, num := id[:cut], id[cut+1:]
			inst, _ := ds.OptionMap("widget_" + typ)[num].(map[string]any)
			name := fmt.Sprintf("widget-%s-%d.html", strings.Trim(settingKeyRe.ReplaceAllString(strings.ToLower(sb), "-"), "-"), i+1)
			var body strings.Builder
			fmt.Fprintf(&body, "{{/* WordPress widget %s in sidebar %s. Port its markup; this is its saved content. */}}\n", id, sb)
			if t, _ := inst["title"].(string); t != "" {
				fmt.Fprintf(&body, "<h2 class=\"widget-title\">%s</h2>\n", t)
			}
			for _, k := range []string{"text", "content"} {
				if t, _ := inst[k].(string); t != "" {
					body.WriteString(t + "\n")
				}
			}
			if len(inst) > 0 && body.Len() < 120 {
				keys := sortedKeys(inst)
				fmt.Fprintf(&body, "{{/* settings: %s */}}\n", strings.Join(keys, ", "))
			}
			if err := writeNew(filepath.Join(out, "templates", "parts", name), body.String()); err != nil {
				return nil, err
			}
			list = append(list, name+" ← "+id+" ("+sb+")")
		}
	}
	return list, nil
}

func sortedKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var tagMap = [][2]string{
	{"wp_head() / wp_footer()", "{{head .}} / {{footer .}}"},
	{"body_class() / post_class()", "{{body_class .}} / {{post_class .Post}}"},
	{"The Loop (have_posts / the_post)", "{{range .Posts}}…{{end}}"},
	{"the_title, the_permalink, the_content", "{{.Title}}, {{.URL}}, {{.ContentHTML}}"},
	{"the_excerpt, the_post_thumbnail('size')", "{{excerpt .}}, {{image .FeaturedMedia \"size\"}}"},
	{"the_author_posts_link / author loops", "{{range .Authors}}…{{end}}"},
	{"get_the_category_list, the_tags", "{{range .Categories}}…{{end}}, {{range .Tags}}…{{end}}"},
	{"wp_nav_menu(['theme_location' => 'x'])", "{{menu . \"x\"}} with a parts template reproducing the walker markup"},
	{"paginate_links / the_posts_pagination", "{{pagination .}}"},
	{"comments_template()", "parts/comments.html"},
	{"get_template_part('x')", "{{template \"parts/x.html\" .}}"},
	{"dynamic_sidebar('id')", "{{template \"parts/widget-<id>-<n>.html\" .}} (one partial per widget below)"},
	{"get_option / get_theme_mod", "{{setting …}} / {{theme_setting …}} (settings in theme.json come from theme_mods)"},
	{"yoast_breadcrumb()", "{{breadcrumbs .}} with the same markup"},
}

func readme(m *Manifest, res *Result) string {
	d := report.New("Theme " + m.Name + " (scaffold)")
	d.P("Generated by `afsc-wp theme-scaffold`. Port the WordPress theme's markup into these templates, then run `afsc theme check <dir>` and iterate with `afsc-wp verify --checks visual`. See AFSC's docs/themes.md.")
	d.H2("WordPress template → AFSC template")
	var rows [][]string
	for _, k := range sortedTemplateKeys(res.Templates) {
		rows = append(rows, []string{k, res.Templates[k]})
	}
	d.Table([]string{"WordPress", "AFSC"}, rows)
	d.H2("Template tags")
	rows = nil
	for _, t := range tagMap {
		rows = append(rows, []string{t[0], "`" + t[1] + "`"})
	}
	d.Table([]string{"WordPress", "AFSC"}, rows)
	d.H2("Widgets (one partial each)").List(res.Widgets)
	d.H2("Tracking hosts found in the theme").P("Not ported. Add them through Admin → Settings → Code snippets and extra CSP hosts if still wanted.").List(res.Tracking)
	d.H2("Still to do")
	d.List([]string{
		"Copy CSS, JS, fonts and images into assets/ and list them in theme.json `assets`; vendor any library WordPress enqueued (pinned, with a checksum).",
		"Review `content.allowed_classes` (from the content class inventory, filtered by the theme CSS when found).",
		"Add `nodes/*.html` overrides only where the theme CSS expects different content markup (captions without html5 support, galleries).",
		"Reproduce plugin markup the CSS targets: share links, related posts, lightbox triggers, guest author and avatar blocks.",
	})
	return d.String()
}

func sortedTemplateKeys(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
