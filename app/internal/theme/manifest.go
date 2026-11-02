// Package theme loads a theme directory: theme.json, templates, node partials and assets (build plan §6.2).
package theme

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"afsc/internal/content"
	"afsc/internal/settings"
)

type Manifest struct {
	Name          string               `json:"name"`
	Version       string               `json:"version"`
	ImageSizes    map[string]ImageSize `json:"image_sizes"`
	SrcsetWidths  []int                `json:"srcset_widths"`
	MenuLocations []string             `json:"menu_locations"`
	Content       struct {
		AllowedClasses []string `json:"allowed_classes"`
	} `json:"content"`
	Assets       []Asset            `json:"assets"`
	CSP          CSP                `json:"csp"`
	Settings     map[string]Setting `json:"settings"`
	Strings      map[string]string  `json:"strings"`
	PostsPerPage map[string]int     `json:"posts_per_page"`
	// Plugins configures compiled-in plugins by ID; each plugin validates its own value (see `afsc theme check`).
	Plugins map[string]json.RawMessage `json:"plugins"`
}

type ImageSize = content.ImageSize

// Asset is a file under assets/ that the page head includes (used by `head` in M2).
type Asset struct {
	Path  string `json:"path"`
	Defer bool   `json:"defer"`
	Async bool   `json:"async"`
}

// CSP lists extra https origins per directive for the public Content-Security-Policy.
type CSP struct {
	Script  []string `json:"script"`
	Style   []string `json:"style"`
	Frame   []string `json:"frame"`
	Img     []string `json:"img"`
	Font    []string `json:"font"`
	Connect []string `json:"connect"`
}

// Directives maps each CSP directive to the theme's origins for it.
func (c CSP) Directives() map[string][]string {
	return map[string][]string{"script-src": c.Script, "style-src": c.Style, "frame-src": c.Frame, "img-src": c.Img,
		"font-src": c.Font, "connect-src": c.Connect}
}

// Setting is a theme option. Admins edit it under Settings → Theme; until then the default applies.
type Setting struct {
	Type    string   `json:"type"`
	Label   string   `json:"label"`
	Default any      `json:"default"`
	Options []string `json:"options,omitempty"`
}

// SettingTypes are the allowed theme setting types.
var SettingTypes = []string{"bool", "string", "number", "select", "color"}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// Check reports whether v (as decoded from JSON) is a valid value for s.
func (s Setting) Check(v any) error {
	switch s.Type {
	case "bool":
		if _, ok := v.(bool); !ok {
			return fmt.Errorf("must be true or false")
		}
	case "number":
		if _, ok := v.(float64); !ok {
			return fmt.Errorf("must be a number")
		}
	case "string":
		if x, ok := v.(string); !ok || len(x) > 4096 {
			return fmt.Errorf("must be text of at most 4096 bytes")
		}
	case "color":
		if x, ok := v.(string); !ok || !colorRe.MatchString(x) {
			return fmt.Errorf("must be a #rrggbb color")
		}
	case "select":
		if x, ok := v.(string); !ok || !slices.Contains(s.Options, x) {
			return fmt.Errorf("must be one of %s", strings.Join(s.Options, ", "))
		}
	default:
		return fmt.Errorf("unknown setting type %q", s.Type)
	}
	return nil
}

// PostsPerPageKinds are the archive types theme.json may override posts_per_page for.
var PostsPerPageKinds = []string{"home", "category", "tag", "author", "date", "search"}

var (
	sizeNameRe = regexp.MustCompile(`^[a-z0-9_-]{1,40}$`)
	keyRe      = regexp.MustCompile(`^[a-z0-9_.-]{1,64}$`)
	pluginIDRe = regexp.MustCompile(`^[a-z][a-z0-9]{1,31}$`)
)

func LoadManifest(dir string) (*Manifest, error) {
	b, err := os.ReadFile(filepath.Join(dir, "theme.json"))
	if err != nil {
		return nil, fmt.Errorf("read theme.json: %w", err)
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var m Manifest
	if err := dec.Decode(&m); err != nil {
		return nil, fmt.Errorf("parse theme.json: %w", err)
	}
	if err := m.validate(); err != nil {
		return nil, fmt.Errorf("theme.json: %w", err)
	}
	return &m, nil
}

func (m *Manifest) validate() error {
	var errs []string
	bad := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if m.Name == "" {
		bad("name is required")
	}
	for name, s := range m.ImageSizes {
		if !sizeNameRe.MatchString(name) || s.Width < 0 || s.Height < 0 || (s.Width == 0 && s.Height == 0) {
			bad("image_sizes.%s: needs a [a-z0-9_-] name and a positive width or height", name)
		}
		if s.Crop && (s.Width <= 0 || s.Height <= 0) {
			bad("image_sizes.%s: a cropped size needs both width and height", name)
		}
		if name == "full" || s.Width > 10000 || s.Height > 10000 {
			bad("image_sizes.%s: \"full\" is reserved and dimensions are at most 10000", name)
		}
	}
	for _, w := range m.SrcsetWidths {
		if w <= 0 || w > 10000 {
			bad("srcset_widths: %d is not between 1 and 10000", w)
		}
	}
	for _, l := range m.MenuLocations {
		if !keyRe.MatchString(l) {
			bad("menu_locations: %q is not a valid name", l)
		}
	}
	for _, a := range m.Assets {
		if !validAssetPath(a.Path) {
			bad("assets: %q must be a relative path under assets/", a.Path)
		}
	}
	for k, s := range m.Settings {
		if !keyRe.MatchString(k) {
			bad("settings: %q is not a valid key", k)
		}
		switch {
		case !slices.Contains(SettingTypes, s.Type):
			bad("settings.%s: type must be one of %s", k, strings.Join(SettingTypes, " "))
		case s.Type == "select" && len(s.Options) == 0:
			bad("settings.%s: a select needs options", k)
		default:
			if err := s.Check(s.Default); err != nil {
				bad("settings.%s: default %v", k, err)
			}
		}
	}
	for dir, hosts := range m.CSP.Directives() {
		for _, h := range hosts {
			if !settings.ValidCSPHost(h) {
				bad("csp: %q (%s) must be an https origin, optionally with a leading *.", h, dir)
			}
		}
	}
	for k := range m.Strings {
		if !keyRe.MatchString(k) {
			bad("strings: %q is not a valid key", k)
		}
	}
	for k, n := range m.PostsPerPage {
		if !slices.Contains(PostsPerPageKinds, k) || n < 1 || n > 100 {
			bad("posts_per_page.%s: kind must be one of %s and the value 1-100", k, strings.Join(PostsPerPageKinds, " "))
		}
	}
	for id, v := range m.Plugins {
		if !pluginIDRe.MatchString(id) {
			bad("plugins: %q is not a plugin id", id)
		}
		if t := bytes.TrimSpace(v); len(t) == 0 || t[0] != '{' {
			bad("plugins.%s: must be an object", id)
		}
	}
	if len(errs) > 0 {
		slices.Sort(errs)
		return fmt.Errorf("%s", strings.Join(errs, "; "))
	}
	return nil
}

func validAssetPath(p string) bool {
	return p != "" && !strings.HasPrefix(p, "/") && path.Clean(p) == p && !strings.HasPrefix(p, "..")
}

// Images is what content.Render and the /m/ handler need from image_sizes and srcset_widths.
func (m *Manifest) Images() *content.ImageSizes {
	return &content.ImageSizes{Sizes: m.ImageSizes, Widths: m.SrcsetWidths}
}

func (m *Manifest) AllowedClasses() map[string]bool {
	out := make(map[string]bool, len(m.Content.AllowedClasses))
	for _, c := range m.Content.AllowedClasses {
		out[c] = true
	}
	return out
}
