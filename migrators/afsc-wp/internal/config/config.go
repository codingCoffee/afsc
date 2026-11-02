// Package config loads afsc-wp.json, the only place site-specific settings live (docs/config.md).
package config

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
)

var (
	PrefixRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
	sourceRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
)

type MySQL struct {
	DSN         string `json:"dsn"`
	DSNEnv      string `json:"dsn_env"`
	TablePrefix string `json:"table_prefix"`
}

type Site struct {
	BaseURL       string            `json:"base_url"`
	OldHosts      []string          `json:"old_hosts"`
	Language      string            `json:"language"`
	Timezone      string            `json:"timezone"`
	RobotsTxtFile string            `json:"robots_txt_file"`
	IndexNowKey   string            `json:"indexnow_key"`
	PageTemplates map[string]string `json:"page_templates"`
	// PostTypes gives custom post types their WordPress permalink (e.g. "/chapter/%postname%/"),
	// relative to the site's home path. Unlisted types use permalink_structure.
	PostTypes map[string]PostType `json:"post_types"`
	// TaxonomyBases gives custom taxonomies their WordPress archive base (e.g. "modules": "module").
	TaxonomyBases map[string]string `json:"taxonomy_bases"`
	// Permalinks overrides the AFSC permalinks derived from WordPress. Empty fields keep the derived value.
	Permalinks Permalinks `json:"permalinks"`
}

type PostType struct {
	WPPath        string `json:"wp_path"`
	MenuOrderMeta string `json:"menu_order_meta"` // numeric post meta copied into menu_order
}

type Permalinks struct {
	PostPattern  string `json:"post_pattern"`
	CategoryBase string `json:"category_base"`
}

type Filter struct {
	Command        []string `json:"command"`
	ReplayFile     string   `json:"replay_file"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	BatchSize      int      `json:"batch_size"`
}

type AFSC struct {
	Command       []string `json:"command"`
	TargetBaseURL string   `json:"target_base_url"`
}

type Crawl struct {
	BaseURL        string   `json:"base_url"`
	DelayMS        int      `json:"delay_ms"`
	UserAgent      string   `json:"user_agent"`
	TimeoutSeconds int      `json:"timeout_seconds"`
	MaxDepth       int      `json:"max_depth"`
	MaxURLs        int      `json:"max_urls"`
	ExtraURLFiles  []string `json:"extra_url_files"`
}

type Verify struct {
	AFSCContentSelector string   `json:"afsc_content_selector"`
	Approved404         []string `json:"approved_404"`
	ImportReport        string   `json:"import_report"`
	ScreenshotsDir      string   `json:"screenshots_dir"`
	VisualThreshold     float64  `json:"visual_threshold"`
	DynamicSelectors    []string `json:"dynamic_selectors"`
	TopURLsFile         string   `json:"top_urls_file"`
}

// Plugin overrides one adapter. Enabled nil means auto-detect. Keys replace default meta keys and option names.
type Plugin struct {
	Enabled *bool             `json:"enabled"`
	Keys    map[string]string `json:"keys"`
	Mode    string            `json:"mode"`
}

type Config struct {
	Source          string            `json:"source"`
	WorkDir         string            `json:"work_dir"`
	ReportsDir      string            `json:"reports_dir"`
	BaselineDir     string            `json:"baseline_dir"`
	BundleDir       string            `json:"bundle_dir"`
	DecisionsFile   string            `json:"decisions_file"`
	MySQL           MySQL             `json:"mysql"`
	UploadsDir      string            `json:"uploads_dir"`
	UploadsURLPath  string            `json:"uploads_url_path"`
	Site            Site              `json:"site"`
	ContentSelector string            `json:"content_selector"`
	WPCodeDirs      []string          `json:"wp_code_dirs"`
	ThemeDir        string            `json:"theme_dir"`
	Filter          Filter            `json:"filter"`
	AFSC            AFSC              `json:"afsc"`
	Crawl           Crawl             `json:"crawl"`
	Verify          Verify            `json:"verify"`
	Plugins         map[string]Plugin `json:"plugins"`
	GeneratedAt     string            `json:"generated_at"`

	dir string
}

// Load reads and validates the config. Relative paths are resolved against the config file's directory.
func Load(path string) (*Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	c := &Config{}
	if err := dec.Decode(c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	abs, err := filepath.Abs(filepath.Dir(path))
	if err != nil {
		return nil, err
	}
	c.dir = abs
	c.defaults()
	if err := c.Validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return c, nil
}

func (c *Config) defaults() {
	def := func(p *string, v string) {
		if *p == "" {
			*p = v
		}
	}
	def(&c.Source, "wp")
	def(&c.WorkDir, "work")
	def(&c.ReportsDir, "reports")
	def(&c.BaselineDir, "baseline")
	def(&c.BundleDir, "bundle")
	def(&c.DecisionsFile, "decisions.json")
	def(&c.MySQL.DSNEnv, "AFSC_WP_MYSQL_DSN")
	def(&c.MySQL.TablePrefix, "wp_")
	def(&c.UploadsURLPath, "/wp-content/uploads")
	def(&c.ContentSelector, ".entry-content")
	def(&c.Verify.AFSCContentSelector, c.ContentSelector)
	def(&c.Crawl.BaseURL, c.Site.BaseURL)
	def(&c.Crawl.UserAgent, "afsc-wp-crawler/1.0 (migration baseline; contact the site owner)")
	if c.Crawl.DelayMS == 0 {
		c.Crawl.DelayMS = 500
	}
	if c.Crawl.TimeoutSeconds == 0 {
		c.Crawl.TimeoutSeconds = 20
	}
	if c.Crawl.MaxDepth == 0 {
		c.Crawl.MaxDepth = 2
	}
	if c.Crawl.MaxURLs == 0 {
		c.Crawl.MaxURLs = 50000
	}
	if c.Filter.TimeoutSeconds == 0 {
		c.Filter.TimeoutSeconds = 600
	}
	if c.Filter.BatchSize == 0 {
		c.Filter.BatchSize = 500
	}
	if c.Verify.VisualThreshold == 0 {
		c.Verify.VisualThreshold = 0.005
	}
	if len(c.AFSC.Command) == 0 {
		c.AFSC.Command = []string{"afsc"}
	}
	if c.Plugins == nil {
		c.Plugins = map[string]Plugin{}
	}
	c.UploadsURLPath = "/" + strings.Trim(c.UploadsURLPath, "/")
}

func (c *Config) Validate() error {
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if !sourceRe.MatchString(c.Source) {
		bad("source: must match %s", sourceRe)
	}
	if !PrefixRe.MatchString(c.MySQL.TablePrefix) {
		bad("mysql.table_prefix: must match %s", PrefixRe)
	}
	if u, err := url.Parse(c.Site.BaseURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		bad("site.base_url: must be an absolute http(s) URL")
	}
	if c.Site.Timezone != "" {
		if _, err := time.LoadLocation(c.Site.Timezone); err != nil {
			bad("site.timezone: %v", err)
		}
	}
	if c.Crawl.DelayMS < 0 || c.Crawl.TimeoutSeconds < 0 || c.Crawl.MaxDepth < 0 {
		bad("crawl: delay, timeout and depth must not be negative")
	}
	if c.Verify.VisualThreshold < 0 || c.Verify.VisualThreshold > 1 {
		bad("verify.visual_threshold: must be between 0 and 1")
	}
	if c.GeneratedAt != "" {
		if _, err := time.Parse(time.RFC3339, c.GeneratedAt); err != nil {
			bad("generated_at: must be RFC 3339")
		}
	}
	for id, p := range c.Plugins {
		if !slices.Contains(PluginIDs, id) {
			bad("plugins.%s: unknown adapter (known: %s)", id, strings.Join(PluginIDs, ", "))
		}
		if id == "guest_author" && p.Mode != "" && p.Mode != "replace" && p.Mode != "join" {
			bad("plugins.guest_author.mode: must be replace or join")
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

// PluginIDs are the adapters afsc-wp ships.
var PluginIDs = []string{"core", "yoast", "yoast_premium", "yoast_video", "jetpack", "user_profile_picture", "guest_author"}

// Path resolves p against the config directory.
func (c *Config) Path(p string) string {
	if p == "" || filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

func (c *Config) Work(parts ...string) string {
	return filepath.Join(append([]string{c.Path(c.WorkDir)}, parts...)...)
}

func (c *Config) Reports(parts ...string) string {
	return filepath.Join(append([]string{c.Path(c.ReportsDir)}, parts...)...)
}

func (c *Config) Baseline(parts ...string) string {
	return filepath.Join(append([]string{c.Path(c.BaselineDir)}, parts...)...)
}

// DSN returns the MySQL DSN from the config or, preferably, from the environment variable it names.
func (c *Config) DSN() (string, error) {
	if c.MySQL.DSN != "" {
		return c.MySQL.DSN, nil
	}
	if v := os.Getenv(c.MySQL.DSNEnv); v != "" {
		return v, nil
	}
	return "", fmt.Errorf("no MySQL DSN: set %s or mysql.dsn", c.MySQL.DSNEnv)
}

// HomePath is the path of site.base_url without a trailing slash ("/varsity"), or "" at the root.
// WordPress paths are handled relative to it, the way AFSC sees requests behind a prefix-stripping proxy.
func (c *Config) HomePath() string {
	u, _ := url.Parse(c.Site.BaseURL)
	return strings.TrimRight(u.Path, "/")
}

// Origin is scheme://host of site.base_url.
func (c *Config) Origin() string {
	u, _ := url.Parse(c.Site.BaseURL)
	return u.Scheme + "://" + u.Host
}

// Host is the public host of site.base_url.
func (c *Config) Host() string {
	u, _ := url.Parse(c.Site.BaseURL)
	return strings.ToLower(u.Host)
}

// SiteHosts lists every host that should be treated as this site: base host, www/non-www twins and old hosts.
func (c *Config) SiteHosts(extra ...string) []string {
	set := map[string]bool{}
	add := func(h string) {
		h = strings.ToLower(strings.TrimSpace(h))
		if h == "" {
			return
		}
		set[h] = true
		if bare, ok := strings.CutPrefix(h, "www."); ok {
			set[bare] = true
		} else {
			set["www."+h] = true
		}
	}
	add(c.Host())
	for _, h := range append(append([]string{}, c.Site.OldHosts...), extra...) {
		add(h)
	}
	out := make([]string, 0, len(set))
	for h := range set {
		out = append(out, h)
	}
	slices.Sort(out)
	return out
}

// PluginOverride returns the override for an adapter, zero when absent.
func (c *Config) PluginOverride(id string) Plugin { return c.Plugins[id] }
