// Package transform turns an extract into bundle records (migration plan §5–§7). It refuses to run while any
// decision is unconfirmed, and fails on invalid content, redirect loops or settings AFSC would reject.
package transform

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"afsc/internal/theme"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/decisions"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/filter"
	"afsc/migrators/afsc-wp/internal/links"
	"afsc/migrators/afsc-wp/internal/mediaidx"
	"afsc/migrators/afsc-wp/internal/permalink"
	"afsc/migrators/afsc-wp/internal/plugins"
	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/wpseo"
	"afsc/migrators/afsc-wp/internal/wpsettings"
)

type Options struct {
	Cfg       *config.Config
	DS        *extract.Dataset
	Plugins   *plugins.Set
	Decisions *decisions.File
	Filter    filter.Runner
	Theme     *theme.Manifest // optional: content.allowed_classes and image sizes
	Baseline  []string        // baseline URLs from the crawl, optional
}

type Output struct {
	GeneratedAt time.Time
	Users       []*records.User
	Authors     []*records.Author
	Media       []*records.Media
	Terms       []*records.Term
	Posts       []*records.Post
	Comments    []*records.Comment
	Menus       []*records.Menu
	Redirects   []*records.Redirect
	Legacy      []*records.Legacy
	Settings    []byte
	URLMap      []URLExpect
	Filtered    map[string]string
	Paths       []PathRec
	Reference   map[string]string // post id → WordPress-filtered HTML with converted shortcodes expanded
	LinkMap     map[string]string // hrefs the conversion rewrote
	Report      *Report
}

// PathRec ties a post to its WordPress and AFSC URLs, for verify.
type PathRec struct {
	Key      string `json:"legacy_key"`
	Type     string `json:"type"`
	Status   string `json:"status"`
	WPPath   string `json:"wp_path,omitempty"`
	AFSCPath string `json:"afsc_path,omitempty"`
	RawHTML  bool   `json:"raw_html,omitempty"`
}

// state is shared by the transform steps.
type state struct {
	o        Options
	ds       *extract.Dataset
	cfg      *config.Config
	set      *plugins.Set
	dec      *decisions.File
	loc      *time.Location
	tzName   string
	hosts    map[string]bool
	media    *mediaidx.Index
	mrep     *mediaidx.Report
	wp       *permalink.WP
	afsc     *permalink.AFSC
	settings wpsettings.Output
	rw       *links.Rewriter
	allowed  map[string]bool
	rep      *Report

	userKey    map[int64]string // users with a bundle user record
	authorKey  map[int64]string // users with an author record
	guestKey   map[int64]string // post id → guest author key
	termKey    map[int64]string // term_taxonomy_id → term key
	termByID   map[int64]*extract.Term
	termTax    map[int64]string // term_taxonomy_id → AFSC taxonomy
	postKey    map[int64]string // imported posts and pages
	postPath   map[int64]string // AFSC path of published posts and pages
	wpPath     map[int64]string // WordPress path of published posts and pages
	pathMap    map[string]string
	live       map[string]bool
	primary    map[int64]int64 // post id → primary category term_taxonomy_id
	termPath   map[int64]string
	authorPath map[int64]string
	slugOf     map[int64]string
	pathRecs   []PathRec
	reference  map[string]string
}

func (s *state) key(kind string, id int64) string {
	return s.cfg.Source + ":" + kind + ":" + strconv.FormatInt(id, 10)
}

func (s *state) decide(kind, name, def string) string { return s.dec.Decision(kind, name, def) }

func (s *state) option(kind, name, key string) string { return s.dec.Get(kind, name).OptionString(key) }

// Run executes every transform step in dependency order.
func Run(ctx context.Context, o Options) (*Output, error) {
	if err := o.Decisions.Check(); err != nil {
		return nil, err
	}
	s := &state{o: o, ds: o.DS, cfg: o.Cfg, set: o.Plugins, dec: o.Decisions, rep: newReport(),
		userKey: map[int64]string{}, authorKey: map[int64]string{}, guestKey: map[int64]string{}, termKey: map[int64]string{},
		termByID: map[int64]*extract.Term{}, termTax: map[int64]string{}, postKey: map[int64]string{},
		postPath: map[int64]string{}, wpPath: map[int64]string{}, pathMap: map[string]string{}, live: map[string]bool{},
		primary: map[int64]int64{}, termPath: map[int64]string{}, authorPath: map[int64]string{}, slugOf: map[int64]string{},
		reference: map[string]string{}}
	out := &Output{}
	if err := s.setup(); err != nil {
		return nil, err
	}
	out.GeneratedAt = s.generatedAt()

	var media []*records.Media
	s.media, media, s.mrep = mediaidx.Build(s.ds, s.mediaOptions())
	out.Media = media
	s.rep.Media = s.mrep

	out.Users, out.Authors = s.people()
	out.Terms = s.terms()
	if err := s.planPosts(); err != nil {
		return nil, err
	}
	s.settings = wpsettings.Build(s.settingsInput())
	if len(s.settings.Problems) > 0 {
		return nil, fmt.Errorf("settings need a decision or config change:\n  %s", strings.Join(s.settings.Problems, "\n  "))
	}
	if pp := s.cfg.Site.Permalinks.PostPattern; pp != "" {
		s.settings.All.Permalinks.PostPattern = pp
		s.settings.Notes = append(s.settings.Notes, fmt.Sprintf("post_pattern set to %q by config; posts whose URL changes get a 301", pp))
	}
	if cb := s.cfg.Site.Permalinks.CategoryBase; cb != "" {
		s.settings.All.Permalinks.CategoryBase = cb
		s.settings.Notes = append(s.settings.Notes, fmt.Sprintf("category_base set to %q by config; category archives whose URL changes get a 301", cb))
	}
	s.rep.SettingsNotes = s.settings.Notes
	if err := s.paths(); err != nil {
		return nil, err
	}
	s.applyTypeDefaults(out.Terms, out.Authors)
	s.rw = &links.Rewriter{Hosts: s.hosts, Resolve: s.resolveQuery, PathMap: s.pathMap, HomePath: s.cfg.HomePath(), Origin: s.cfg.Origin()}

	filtered, err := s.filter(ctx)
	if err != nil {
		return nil, err
	}
	out.Filtered = filtered
	rendered, err := s.renderPHP(ctx, filtered)
	if err != nil {
		return nil, err
	}
	posts, legacy, pages, err := s.posts(filtered, rendered, media)
	if err != nil {
		return nil, err
	}
	out.Posts, out.Legacy = posts, legacy
	out.Paths, out.Reference = s.pathRecs, s.reference
	out.Comments = s.comments()
	out.Menus = s.menus()
	out.Redirects, err = s.redirects(pages)
	if err != nil {
		return nil, err
	}
	b, err := wpsettings.Encode(s.settings.All)
	if err != nil {
		return nil, fmt.Errorf("settings.json: %w", err)
	}
	out.Settings = b
	out.URLMap = s.urlMap(out.Redirects)
	out.LinkMap = s.rw.Changed
	s.rep.Counts = s.counts(out)
	out.Report = s.rep
	return out, nil
}

func (s *state) setup() error {
	tz := s.cfg.Site.Timezone
	loc, name, fixed, err := extract.SiteLocation(s.ds.OptionString("timezone_string"), s.ds.OptionString("gmt_offset"))
	if tz != "" {
		loc, _ = time.LoadLocation(tz)
		name, fixed, err = tz, false, nil
	}
	if err != nil {
		return fmt.Errorf("site timezone: %w", err)
	}
	s.loc, s.tzName = loc, name
	if fixed {
		s.rep.note("WordPress used gmt_offset; mapped to %q, which has no DST rules", name)
	}
	var extra []string
	for _, opt := range []string{"siteurl", "home"} {
		if u, err := url.Parse(s.ds.OptionString(opt)); err == nil && u.Host != "" {
			extra = append(extra, u.Hostname())
		}
	}
	s.hosts = map[string]bool{}
	for _, h := range s.cfg.SiteHosts(extra...) {
		s.hosts[h] = true
	}
	s.allowed = map[string]bool{}
	if s.o.Theme != nil {
		s.allowed = s.o.Theme.AllowedClasses()
	} else {
		s.rep.note("no theme_dir configured: every content class is dropped (run theme-scaffold and set theme_dir)")
	}
	s.wp = &permalink.WP{Structure: s.ds.OptionString("permalink_structure"), CategoryBase: s.ds.OptionString("category_base"), TagBase: s.ds.OptionString("tag_base"),
		TypeStructures: map[string]string{}, TaxonomyBases: s.cfg.Site.TaxonomyBases}
	for t, pt := range s.cfg.Site.PostTypes {
		s.wp.TypeStructures[t] = pt.WPPath
	}
	s.rep.Secrets = s.ds.Stats.SecretOptions
	s.rep.Fixes = len(s.ds.Fixes)
	return nil
}

func (s *state) mediaOptions() mediaidx.Options {
	o := mediaidx.Options{Source: s.cfg.Source, UploadsPath: s.cfg.UploadsURLPath, HomePath: s.cfg.HomePath(), Loc: s.loc}
	for h := range s.hosts {
		o.SiteHosts = append(o.SiteHosts, h)
	}
	sort.Strings(o.SiteHosts)
	if s.set.On("jetpack") {
		o.CDNHosts = s.set.List("jetpack", "cdn_hosts")
	}
	o.Keys.AttachedFile = s.set.Key("core", "attached_file")
	o.Keys.Metadata = s.set.Key("core", "attachment_metadata")
	o.Keys.Alt = s.set.Key("core", "image_alt")
	return o
}

// generatedAt is deterministic: the configured value, else the latest modification in the source data.
func (s *state) generatedAt() time.Time {
	if s.cfg.GeneratedAt != "" {
		t, _ := time.Parse(time.RFC3339, s.cfg.GeneratedAt)
		return t.UTC()
	}
	var latest time.Time
	for _, p := range s.ds.Posts {
		if t, err := extract.UTC(p.ModifiedGMT, p.Modified, s.loc); err == nil && t.After(latest) {
			latest = t
		}
	}
	for _, c := range s.ds.Comments {
		if t, err := extract.UTC(c.DateGMT, c.Date, s.loc); err == nil && t.After(latest) {
			latest = t
		}
	}
	if latest.IsZero() {
		latest = time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	}
	return latest.Truncate(time.Second)
}

func (s *state) settingsInput() wpsettings.Input {
	in := wpsettings.Input{
		DS: s.ds, Timezone: s.tzName, Language: s.cfg.Site.Language, Yoast: s.set.On("yoast"),
		Decide: s.decide, Option: s.option, Media: s.resolveImage, Variable: s.variable,
		IndexNowKey: s.cfg.Site.IndexNowKey,
		Page: func(id int64) (string, bool) {
			p := s.ds.Post(id)
			k, ok := s.postKey[id]
			return k, ok && p != nil && p.Type == "page"
		},
		UserName: func(id int64) string {
			for _, u := range s.ds.Users {
				if u.ID == id {
					return u.DisplayName
				}
			}
			return ""
		},
	}
	in.Keys.Titles, in.Keys.Social = s.set.Key("yoast", "opt_titles"), s.set.Key("yoast", "opt_social")
	if robots, err := wpsettings.ReadRobots(s.cfg.Path(s.cfg.Site.RobotsTxtFile)); err == nil {
		in.RobotsTxt = robots
	} else {
		s.rep.note("robots_txt_file: %v", err)
	}
	return in
}

func (s *state) variable(name string) wpseo.Variable {
	return wpseo.Variable{Decision: s.decide("yoast_variable", name, "keep"), With: s.option("yoast_variable", name, "with")}
}

// resolveImage maps an attachment id or an image URL to a media key.
func (s *state) resolveImage(id, u string) *string {
	if n, err := strconv.ParseInt(strings.TrimSpace(id), 10, 64); err == nil && n > 0 {
		if k, ok := s.media.Attachment(n); ok {
			return records.Ptr(k)
		}
	}
	if u != "" {
		if ref, _, ok := s.media.Resolve(u); ok {
			return records.Ptr(ref.Key)
		}
	}
	return nil
}

var slugRe = regexp.MustCompile(`^[\p{Ll}\p{Lo}\p{Nd}_-]{1,200}$`)

// Slugify makes a valid AFSC slug: lowercase letters of any script, digits, "-" and "_".
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		if unicode.IsLower(r) || unicode.Is(unicode.Lo, r) || unicode.IsDigit(r) || r == '_' {
			b.WriteRune(r)
			dash = false
		} else if !dash && b.Len() > 0 {
			b.WriteByte('-')
			dash = true
		}
	}
	out := strings.Trim(b.String(), "-")
	if len(out) > 200 {
		out = strings.TrimRight(out[:200], "-")
	}
	return out
}

func validSlug(s string) bool { return slugRe.MatchString(s) }

// uniq returns s, or s-2, s-3… when taken.
func uniq(s string, taken map[string]bool) string {
	out := s
	for i := 2; taken[out]; i++ {
		out = s + "-" + strconv.Itoa(i)
	}
	taken[out] = true
	return out
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
