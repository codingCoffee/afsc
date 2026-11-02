package inventory

import (
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/convert"
	"afsc/migrators/afsc-wp/internal/decisions"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/mediaidx"
	"afsc/migrators/afsc-wp/internal/permalink"
	"afsc/migrators/afsc-wp/internal/plugins"
	"afsc/migrators/afsc-wp/internal/redirects"
	"afsc/migrators/afsc-wp/internal/report"
	"afsc/migrators/afsc-wp/internal/transform"
	"afsc/migrators/afsc-wp/internal/wpseo"
)

var coreShortcodes = map[string]bool{"caption": true, "wp_caption": true, "gallery": true, "embed": true, "video": true, "audio": true, "playlist": true}

// internalTypes are WordPress-internal post types; they are counted and never proposed for import.
var internalTypes = map[string]bool{"post": true, "page": true, "attachment": true, "revision": true, "nav_menu_item": true,
	"wp_block": true, "customize_changeset": true, "oembed_cache": true, "wp_global_styles": true, "wp_template": true,
	"wp_template_part": true, "wp_navigation": true, "user_request": true, "custom_css": true, "wp_font_family": true,
	"wp_font_face": true, "scheduled-action": true}

// knownPlugins have a built-in AFSC replacement; anything else active is proposed as needs a decision.
var knownPlugins = map[string]string{
	"akismet/": "AFSC's spam pipeline replaces it", "wp-super-cache/": "AFSC's page cache replaces it",
	"w3-total-cache/": "AFSC's page cache replaces it", "classic-editor/": "nothing to migrate",
	"redirection/": "its redirects are NOT imported; export them to Yoast format or add them in AFSC",
}

var jetpackModules = map[string][2]string{
	"photon": {"drop", "CDN image URLs in content are rewritten to AFSC media"}, "photon-cdn": {"drop", "static asset CDN"},
	"sharedaddy": {"builtin", "AFSC share_links"}, "related-posts": {"builtin", "AFSC related_posts"},
	"carousel": {"builtin", "AFSC lightbox plugin"}, "tiled-gallery": {"replicate_theme", "gallery markup in the theme"},
	"stats": {"drop", "add analytics through code snippets if needed"}, "shortcodes": {"builtin", "converted per shortcode decision"},
	"lazy-images": {"builtin", "AFSC lazy-loads images"}, "sitemaps": {"drop", "AFSC sitemaps"}, "widgets": {"replicate_theme", "widget partials"},
}

type Inventory struct {
	DBVersion     string            `json:"db_version"`
	Tables        []extract.Table   `json:"tables"`
	LatinTables   []string          `json:"latin1_tables"`
	Stats         extract.Stats     `json:"stats"`
	Options       map[string]string `json:"options"`
	ActivePlugins []string          `json:"active_plugins"`
	Adapters      []plugins.Status  `json:"adapters"`
	Unhandled     []string          `json:"unhandled_plugins"`
	JetpackMods   []string          `json:"jetpack_modules"`
	Scan          *Scan             `json:"content"`
	Hooks         []Hook            `json:"theme_hooks"`
	Files         map[string]int    `json:"upload_files"`
	NotMedia      []*extract.File   `json:"upload_files_not_imported"`
	Widgets       map[string]int    `json:"widgets"`
	Menus         int               `json:"menus"`
	Permalink     map[string]any    `json:"permalinks"`
	YoastVars     map[string]int    `json:"unsupported_yoast_variables"`
	Redirects     map[string]int    `json:"yoast_redirects"`
	BadRegex      map[string]string `json:"unconvertible_regex"`
	Fixes         int               `json:"charset_fixes"`
	Password      []string          `json:"password_protected"`
	Decisions     *decisions.File   `json:"-"`
}

var scalarOptions = []string{"siteurl", "home", "blogname", "permalink_structure", "category_base", "tag_base", "posts_per_page",
	"posts_per_rss", "rss_use_excerpt", "show_on_front", "page_on_front", "page_for_posts", "timezone_string", "gmt_offset",
	"date_format", "blog_charset", "use_smilies", "template", "stylesheet", "WPLANG"}

// Run scans everything and proposes decisions.
func Run(cfg *config.Config, ds *extract.Dataset, set *plugins.Set, codeDirs []string) (*Inventory, error) {
	inv := &Inventory{DBVersion: ds.OptionString("db_version"), Tables: ds.Tables, Stats: ds.Stats, Options: map[string]string{},
		Scan: newScan(), Files: map[string]int{}, Widgets: map[string]int{}, YoastVars: map[string]int{}, Redirects: map[string]int{},
		BadRegex: map[string]string{}, Fixes: len(ds.Fixes), Permalink: map[string]any{}}
	for _, t := range ds.Tables {
		if strings.HasPrefix(strings.ToLower(t.Collation), "latin1") {
			inv.LatinTables = append(inv.LatinTables, t.Name)
		}
	}
	for _, o := range scalarOptions {
		inv.Options[o] = ds.OptionString(o)
	}
	if l, ok := ds.Option("active_plugins").([]any); ok {
		for _, v := range l {
			if s, ok := v.(string); ok {
				inv.ActivePlugins = append(inv.ActivePlugins, s)
			}
		}
	}
	inv.Adapters = set.Report()
	for _, p := range inv.ActivePlugins {
		if !adapterPlugin(p) {
			inv.Unhandled = append(inv.Unhandled, p)
		}
	}
	if l, ok := ds.Option(set.Key("jetpack", "opt_modules")).([]any); ok {
		for _, v := range l {
			if s, ok := v.(string); ok {
				inv.JetpackMods = append(inv.JetpackMods, s)
			}
		}
	}
	hosts := map[string]bool{}
	for _, h := range cfg.SiteHosts(host(ds.OptionString("siteurl")), host(ds.OptionString("home"))) {
		hosts[h] = true
	}
	mo := mediaidx.Options{Source: cfg.Source, UploadsPath: cfg.UploadsURLPath}
	for h := range hosts {
		mo.SiteHosts = append(mo.SiteHosts, h)
	}
	mo.CDNHosts = set.List("jetpack", "cdn_hosts")
	mo.Keys.AttachedFile, mo.Keys.Metadata, mo.Keys.Alt = set.Key("core", "attached_file"), set.Key("core", "attachment_metadata"), set.Key("core", "image_alt")
	media, _, _ := mediaidx.Build(ds, mo)

	for _, p := range ds.Posts {
		key := cfg.Source + ":post:" + strconv.FormatInt(p.ID, 10)
		switch p.Status {
		case "publish", "draft", "future", "pending", "private":
		default:
			continue
		}
		if internalTypes[p.Type] && p.Type != "post" && p.Type != "page" {
			continue
		}
		inv.Scan.content(key, p.Content, media, hosts)
		if p.Password != "" {
			inv.Password = append(inv.Password, key)
		}
		for k, vals := range p.Meta {
			if strings.HasPrefix(k, "_oembed_") && !strings.HasPrefix(k, "_oembed_time_") {
				for _, v := range vals {
					if v == "{{unknown}}" {
						inv.Scan.OEmbedCache.add("unknown")
					} else {
						inv.Scan.OEmbedCache.add("embedded")
					}
				}
			}
			if set.On("yoast") && strings.HasPrefix(k, "_yoast_wpseo_") {
				for _, v := range vals {
					for _, name := range wpseo.Unsupported(v) {
						inv.YoastVars[name]++
					}
				}
			}
		}
	}
	if set.On("yoast") {
		for _, v := range ds.OptionMap(set.Key("yoast", "opt_titles")) {
			if s, ok := v.(string); ok {
				for _, name := range wpseo.Unsupported(s) {
					inv.YoastVars[name]++
				}
			}
		}
	}
	var err error
	if inv.Hooks, err = Hooks(codeDirs); err != nil {
		return nil, fmt.Errorf("scan site code: %w", err)
	}
	for _, f := range ds.Files {
		ext := strings.ToLower(extOf(f.Path))
		inv.Files[ext]++
		if !f.Media {
			inv.NotMedia = append(inv.NotMedia, f)
		}
	}
	for name, v := range ds.Options {
		if strings.HasPrefix(name, "widget_") {
			if m, ok := v.(map[string]any); ok {
				for k := range m {
					if k != "_multiwidget" {
						inv.Widgets[strings.TrimPrefix(name, "widget_")]++
					}
				}
			}
		}
	}
	for _, t := range ds.Terms {
		if t.Taxonomy == "nav_menu" {
			inv.Menus++
		}
	}
	structure := ds.OptionString("permalink_structure")
	pattern, trailing, unsupported := permalink.Convert(structure)
	inv.Permalink = map[string]any{"structure": structure, "afsc_pattern": pattern, "trailing_slash": trailing, "unsupported": unsupported,
		"category_base": ds.OptionString("category_base"), "tag_base": ds.OptionString("tag_base")}
	if set.On("yoast_premium") {
		for _, y := range transform.YoastRedirects(ds.Option(set.Key("yoast_premium", "opt_redirects"))) {
			inv.Redirects[y["format"]+" "+y["type"]]++
			if _, _, err := redirects.YoastFormat(y["origin"], y["format"]); err != nil {
				inv.BadRegex[transform.RegexID(y["origin"])] = y["origin"] + " — " + err.Error()
			}
		}
	}
	inv.Decisions = propose(inv, ds, set)
	return inv, nil
}

func adapterPlugin(p string) bool {
	for _, a := range plugins.Adapters {
		for _, prefix := range a.PluginFiles {
			if strings.HasPrefix(p, prefix) {
				return true
			}
		}
	}
	return false
}

func extOf(p string) string {
	base := p[strings.LastIndexByte(p, '/')+1:]
	if i := strings.LastIndexByte(base, '.'); i > 0 {
		return base[i:]
	}
	if strings.HasPrefix(base, ".") {
		return base
	}
	return "(none)"
}

// propose builds one decision item per shortcode, block, module, hook, plugin and unusual pattern.
func propose(inv *Inventory, ds *extract.Dataset, set *plugins.Set) *decisions.File {
	f := &decisions.File{Version: 1}
	add := func(it *decisions.Item) { f.Items = append(f.Items, it) }
	registered := map[string]string{}
	for _, h := range inv.Hooks {
		if h.Kind == "shortcode" {
			registered[h.Name] = h.File
		}
	}
	jetpack := map[string]bool{}
	for _, t := range set.List("jetpack", "shortcodes") {
		jetpack[t] = set.On("jetpack")
	}
	for _, tag := range sortedKeys(inv.Scan.Shortcodes) {
		st := inv.Scan.Shortcodes[tag]
		var it *decisions.Item
		switch {
		case convert.Handlers[tag] && (coreShortcodes[tag] || jetpack[tag]):
			st.Origin = "core"
			if jetpack[tag] {
				st.Origin = "jetpack"
			}
			it = decisions.New("shortcode", tag, st.Count, st.Posts, "convert", "has a converter")
		case registered[tag] != "":
			st.Origin = "site-code"
			it = decisions.New("shortcode", tag, st.Count, st.Posts, "render_php",
				"registered in "+registered[tag]+"; set options.include to the PHP files that register it", "render_php", "drop", "keep_text")
		case jetpack[tag] || coreShortcodes[tag]:
			st.Origin = "core"
			if jetpack[tag] {
				st.Origin = "jetpack"
			}
			it = decisions.New("shortcode", tag, st.Count, st.Posts, "render_php", "no converter; render with PHP", "render_php", "drop", "keep_text")
		default:
			st.Origin = "unknown"
			it = decisions.New("shortcode", tag, st.Count, st.Posts, "keep_text",
				"not registered in scanned code; probably literal text like [sic]", "render_php", "drop", "keep_text")
		}
		add(it)
	}
	for _, name := range sortedKeys(inv.Scan.Blocks) {
		prop := "raw_html"
		if strings.HasPrefix(name, "core/") {
			prop = "convert"
		}
		add(decisions.New("block", name, inv.Scan.Blocks[name], inv.Scan.BlockPosts[name], prop, ""))
	}
	for _, m := range inv.JetpackMods {
		p, note := "drop", "no AFSC equivalent known; decide"
		if v, ok := jetpackModules[m]; ok {
			p, note = v[0], v[1]
		}
		add(decisions.New("jetpack_module", m, 1, nil, p, note))
	}
	seenHook := map[string]bool{}
	for _, h := range inv.Hooks {
		if h.Kind == "shortcode" || seenHook[h.Name] {
			continue
		}
		seenHook[h.Name] = true
		add(decisions.New("theme_hook", h.Name, 1, []string{fmt.Sprintf("%s:%d", h.File, h.Line)}, "replicate_theme",
			"markup this hook injects today; port it to the theme or drop it"))
	}
	for _, p := range inv.Unhandled {
		note := "not converted by afsc-wp"
		for prefix, n := range knownPlugins {
			if strings.HasPrefix(p, prefix) {
				note = n
			}
		}
		add(decisions.New("plugin", strings.SplitN(p, "/", 2)[0], 1, nil, "drop", note))
	}
	iframeHosts := map[string]int{}
	for k, n := range inv.Scan.EmbedHosts {
		if el, h, ok := strings.Cut(k, " "); ok && el == "iframe" {
			iframeHosts[h] += n
		}
	}
	for _, h := range sortedKeys(iframeHosts) {
		prop := "raw_html"
		if _, ok := convert.EmbedFromURL("https://" + h + "/embed/abcdefghijk"); ok || knownEmbedHost(h) {
			prop = "embed"
		}
		add(decisions.New("iframe_host", h, iframeHosts[h], inv.Scan.EmbedPosts[h], prop, ""))
	}
	for _, el := range sortedKeys(inv.Scan.ElementPosts) {
		if el == "audio" || el == "iframe" {
			continue
		}
		add(decisions.New("element", el, inv.Scan.Elements[el], inv.Scan.ElementPosts[el], "raw_html",
			"kept as sanitized rawHtml; the trusted policy removes script, style and form contents"))
	}
	for _, c := range ds.Stats.Taxonomies {
		if c.Key != "category" && c.Key != "post_tag" && c.Key != "nav_menu" {
			add(decisions.New("taxonomy", c.Key, int(c.Count), nil, "drop", ""))
		}
	}
	types := map[string]int{}
	for _, c := range ds.Stats.PostTypeStatus {
		if !internalTypes[c.Key] && !strings.HasPrefix(c.Key, "jp_") && c.Sub == "publish" {
			types[c.Key] += int(c.Count)
		}
	}
	for _, t := range sortedKeys(types) {
		add(decisions.New("post_type", t, types[t], nil, "skip", "published items of a custom post type"))
	}
	if n := inv.Scan.Markers["nextpage"]; n > 0 {
		add(decisions.New("marker", "nextpage", n, inv.Scan.MarkerPosts["nextpage"], "merge",
			"pages merge into one post; /post/2/ etc. 301 to the post", "merge"))
	}
	if n := inv.Scan.Markers["empty_paragraphs"]; n > 0 {
		add(decisions.New("empty_paragraphs", "empty_paragraphs", n, inv.Scan.MarkerPosts["empty_paragraphs"], "drop",
			"keep only if the spacing matters visually"))
	}
	for _, v := range sortedKeys(inv.YoastVars) {
		prop := "remove"
		if wpseo.Expandable[v] {
			prop = "expand"
		}
		add(decisions.New("yoast_variable", v, inv.YoastVars[v], nil, prop, "AFSC renders unknown variables as nothing"))
	}
	if un, _ := inv.Permalink["unsupported"].([]string); len(un) > 0 {
		it := decisions.New("permalink_structure", ds.OptionString("permalink_structure"), 1, nil, "change",
			"AFSC can't express "+strings.Join(un, ", ")+"; every post URL changes and gets a 301", "change")
		it.Options = map[string]any{"pattern": "/{slug}/"}
		add(it)
	}
	for _, b := range []struct{ opt, name string }{{"category_base", "category"}, {"tag_base", "tag"}} {
		v := strings.Trim(ds.OptionString(b.opt), "/")
		if v == "." || strings.ContainsAny(v, "/?#{} ") {
			it := decisions.New("category_base", b.name, 1, nil, "change", fmt.Sprintf("%s %q is not one path segment", b.opt, v), "change")
			it.Options = map[string]any{"base": b.name}
			add(it)
		}
	}
	for _, id := range sortedKeys(inv.BadRegex) {
		add(decisions.New("redirect_regex", id, 1, nil, "drop", inv.BadRegex[id]+"; rewrite as RE2 in options.source or drop"))
	}
	for _, rel := range sortedKeys(inv.Scan.MissingImg) {
		add(decisions.New("missing_media", rel, len(inv.Scan.MissingImg[rel]), inv.Scan.MissingImg[rel], "keep_src",
			"file is not in the uploads copy; keep_src points at the old URL"))
	}
	if len(inv.Password) > 0 {
		add(decisions.New("password_protected", "all", len(inv.Password), inv.Password, "private", "AFSC has no password-protected posts"))
	}
	for _, h := range sortedKeys(inv.Scan.OEmbedLines) {
		if _, ok := convert.EmbedFromURL("https://" + h + "/x"); ok || knownEmbedHost(h) {
			continue
		}
		add(decisions.New("oembed", h, inv.Scan.OEmbedLines[h], inv.Scan.OEmbedPosts[h], "link",
			"bare URL on its own line from a provider AFSC can't embed"))
	}
	return f
}

var embedHosts = []string{"youtube.com", "youtu.be", "youtube-nocookie.com", "vimeo.com", "twitter.com", "x.com",
	"instagram.com", "spotify.com", "soundcloud.com", "google.com"}

func knownEmbedHost(h string) bool {
	h = strings.TrimPrefix(h, "www.")
	for _, e := range embedHosts {
		if h == e || strings.HasSuffix(h, "."+e) {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

// Markdown renders reports/inventory.md, ending with the decision table.
func (inv *Inventory) Markdown(dec *decisions.File) *report.Doc {
	d := report.New("Inventory")
	d.P("WordPress db_version %s. Every later phase is driven by this report and `decisions.json`.", inv.DBVersion)
	d.H2("Tables")
	var rows [][]string
	for _, t := range inv.Tables {
		rows = append(rows, []string{t.Name, t.Collation})
	}
	d.Table([]string{"Table", "Collation"}, rows)
	if len(inv.LatinTables) > 0 {
		d.P("**latin1 tables** (double-encoded UTF-8 is repaired per field and logged): %s. %d fields repaired.", strings.Join(inv.LatinTables, ", "), inv.Fixes)
	}
	d.H2("Content shape")
	rows = nil
	for _, c := range inv.Stats.PostTypeStatus {
		rows = append(rows, []string{c.Key, c.Sub, strconv.FormatInt(c.Count, 10)})
	}
	d.Table([]string{"Post type", "Status", "Count"}, rows)
	d.Table([]string{"Content kind", "Posts"}, counterRows(inv.Scan.Kinds, 0))
	rows = nil
	for _, c := range inv.Stats.Taxonomies {
		rows = append(rows, []string{c.Key, strconv.FormatInt(c.Count, 10)})
	}
	d.Table([]string{"Taxonomy", "Terms"}, rows)
	rows = nil
	for _, c := range inv.Stats.CommentTypes {
		rows = append(rows, []string{c.Key, c.Sub, strconv.FormatInt(c.Count, 10)})
	}
	d.Table([]string{"Comment type", "Approved", "Count"}, rows)
	d.Table([]string{"Marker", "Count"}, counterRows(inv.Scan.Markers, 0))

	d.H2("Options")
	rows = nil
	for _, k := range sortedKeys(inv.Options) {
		rows = append(rows, []string{k, inv.Options[k]})
	}
	d.Table([]string{"Option", "Value"}, rows)
	d.P("%d options in total; only the ones above (and plugin options adapters read) were loaded by value.", len(inv.Stats.OptionNames))
	d.H3("Credentials to rotate").P("Option names that look like credentials. Values were not read into any report.").List(inv.Stats.SecretOptions)
	d.H3("Usermeta keys not extracted").P("Skipped because they may hold secrets or aren't needed: %s.", report.Samples(inv.Stats.SkippedUserMeta, 40))

	d.H2("Plugins")
	d.P("Active: %s.", strings.Join(inv.ActivePlugins, ", "))
	rows = nil
	for _, a := range inv.Adapters {
		var keys []string
		for _, k := range sortedKeys(a.Keys) {
			if a.Keys[k] != "" {
				keys = append(keys, k+"="+a.Keys[k])
			}
		}
		rows = append(rows, []string{a.ID, fmt.Sprint(a.Enabled), a.Reason, report.Samples(keys, 40)})
	}
	d.Table([]string{"Adapter", "Enabled", "Why", "Keys (verify each against the meta-key counts below)"}, rows)
	d.H3("Active plugins without an adapter").List(inv.Unhandled)
	d.H3("Jetpack modules").List(inv.JetpackMods)
	d.H3("Post meta keys").Table([]string{"Key", "Count"}, countRows(inv.Stats.PostMetaKeys))
	d.H3("User meta keys").Table([]string{"Key", "Count"}, countRows(inv.Stats.UserMetaKeys))
	d.H3("Term meta keys").Table([]string{"Key", "Count"}, countRows(inv.Stats.TermMetaKeys))

	d.H2("Shortcodes")
	rows = nil
	for _, t := range sortedKeys(inv.Scan.Shortcodes) {
		s := inv.Scan.Shortcodes[t]
		rows = append(rows, []string{t, s.Origin, strconv.Itoa(s.Count), strconv.Itoa(s.Enclosing), strings.Join(s.Attrs, " "), report.Samples(s.Posts, 3)})
	}
	d.Table([]string{"Tag", "Origin", "Count", "Enclosing", "Attributes", "Posts"}, rows)
	d.P("%d shortcode-looking strings sit inside HTML attributes; they are never converted.", inv.Scan.InTagCodes)
	d.H2("Blocks").Table([]string{"Block", "Count"}, counterRows(inv.Scan.Blocks, 0))
	d.H2("HTML")
	d.H3("Elements").Table([]string{"Element", "Count"}, counterRows(inv.Scan.Elements, 0))
	d.H3("Classes (theme-scaffold builds content.allowed_classes from these)").Table([]string{"Class", "Count"}, counterRows(inv.Scan.Classes, 200))
	d.H3("Inline style properties (only text-align, color and background-color survive)").Table([]string{"Property", "Count"}, counterRows(inv.Scan.Styles, 0))
	d.H3("data-* attributes").Table([]string{"Attribute", "Count"}, counterRows(inv.Scan.DataAttrs, 50))
	d.H3("iframe, script and object hosts").Table([]string{"Element host", "Count"}, counterRows(inv.Scan.EmbedHosts, 0))
	d.H2("Images").Table([]string{"Form", "Count"}, counterRows(inv.Scan.ImageForms, 0))
	d.H3("External image hosts").Table([]string{"Host", "Count"}, counterRows(inv.Scan.ExternalImg, 50))
	var missing []string
	for _, k := range sortedKeys(inv.Scan.MissingImg) {
		missing = append(missing, k+" ("+report.Samples(inv.Scan.MissingImg[k], 3)+")")
	}
	d.H3("Referenced uploads files that are missing").List(missing)
	d.H2("Links").Table([]string{"Kind", "Count"}, counterRows(inv.Scan.Links, 0))
	d.H3("External link hosts").Table([]string{"Host", "Count"}, counterRows(inv.Scan.ExternalLink, 50))
	d.H2("oEmbed").Table([]string{"Bare-URL line host", "Count"}, counterRows(inv.Scan.OEmbedLines, 0))
	d.Table([]string{"_oembed_* cache", "Count"}, counterRows(inv.Scan.OEmbedCache, 0))
	d.H2("Theme and mu-plugin hooks")
	rows = nil
	for _, h := range inv.Hooks {
		rows = append(rows, []string{h.Kind, h.Name, fmt.Sprintf("%s:%d", h.File, h.Line)})
	}
	d.Table([]string{"Kind", "Hook", "Where"}, rows)
	d.H2("Uploads").Table([]string{"Extension", "Files"}, counterRows(inv.Files, 0))
	var nm []string
	for _, f := range inv.NotMedia {
		nm = append(nm, fmt.Sprintf("`%s` (%d bytes) — %s", f.Path, f.Size, f.Reason))
	}
	d.H3("Files that will NOT be imported").P("They may be publicly reachable on production and can contain secrets. Review every one.").List(nm)
	d.H2("Menus and widgets").P("%d menus.", inv.Menus).Table([]string{"Widget type", "Instances"}, counterRows(inv.Widgets, 0))
	d.H2("Permalinks")
	rows = nil
	for _, k := range sortedKeys(inv.Permalink) {
		rows = append(rows, []string{k, fmt.Sprint(inv.Permalink[k])})
	}
	d.Table([]string{"Setting", "Value"}, rows)
	d.H2("Yoast Premium redirects").Table([]string{"Format and type", "Count"}, counterRows(inv.Redirects, 0))
	d.H2("Password-protected posts").List(inv.Password)

	d.H2("Decision table")
	d.P("Edit `decisions.json`: set `decision` (one of `allowed`) and `status: \"confirmed\"` for every item. `transform` refuses to run while any item is unconfirmed. See docs/decisions.md.")
	rows = nil
	for _, it := range dec.Items {
		if it.Stale {
			continue
		}
		rows = append(rows, []string{it.ID, strconv.Itoa(it.Count), it.Proposed, it.Decision, it.Status, strings.Join(it.Allowed, ", "), it.Note})
	}
	d.Table([]string{"Item", "Count", "Proposed", "Decision", "Status", "Allowed", "Note"}, rows)
	return d
}

func counterRows(c map[string]int, limit int) [][]string {
	var rows [][]string
	for i, kv := range report.SortedCounts(c) {
		if limit > 0 && i >= limit {
			rows = append(rows, []string{fmt.Sprintf("… %d more", len(c)-limit), ""})
			break
		}
		rows = append(rows, []string{kv[0], kv[1]})
	}
	return rows
}

func countRows(c []extract.Count) [][]string {
	cp := append([]extract.Count{}, c...)
	sort.SliceStable(cp, func(i, j int) bool { return cp[i].Count > cp[j].Count })
	var rows [][]string
	for _, x := range cp {
		rows = append(rows, []string{x.Key, strconv.FormatInt(x.Count, 10)})
	}
	return rows
}
