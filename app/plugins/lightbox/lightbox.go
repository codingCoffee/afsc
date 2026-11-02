// Package lightbox opens linked images in an accessible full-screen viewer (build plan §14). Themes opt in with
// theme.json "plugins": {"lightbox": {"selector": ".entry-content"}}; stored and rendered HTML don't change.
package lightbox

import (
	"bytes"
	"context"
	"embed"
	"encoding/json"
	"errors"
	"html/template"
	"io/fs"
	"strings"
	"unicode"

	"afsc/internal/plugins"
	"afsc/internal/theme"
)

//go:embed assets
var assets embed.FS

// Config is the theme's "plugins": {"lightbox": …} value.
type Config struct {
	// Selector scopes the viewer: only image links inside matching elements open in it.
	Selector string `json:"selector"`
}

// labels are the viewer's texts, overridable with theme.json strings "lightbox.<key>".
var labels = []struct{ key, def string }{
	{"viewer", "Image viewer"},
	{"close", "Close"},
	{"prev", "Previous image"},
	{"next", "Next image"},
	{"open", "Open image"},
	{"counter", "{n} of {total}"},
}

type Plugin struct {
	h *plugins.Host
}

func New() *Plugin { return &Plugin{} }

func (*Plugin) ID() string { return "lightbox" }

func (*Plugin) Info() plugins.Info {
	return plugins.Info{Name: "Image lightbox", DefaultEnabled: true,
		Description: "Opens linked images in a keyboard- and screen-reader-friendly viewer, with galleries as sets. Only for themes that opt in."}
}

func (p *Plugin) Init(_ context.Context, h *plugins.Host) error {
	p.h = h
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return err
	}
	h.RegisterAssets(sub)
	h.RegisterThemeConfig(func(raw json.RawMessage) error {
		_, err := parse(raw)
		return err
	})
	plugins.AddFilter(h, plugins.HeadTags, 20, p.head)
	return nil
}

func parse(raw json.RawMessage) (Config, error) {
	var c Config
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&c); err != nil {
		return c, err
	}
	c.Selector = strings.TrimSpace(c.Selector)
	if c.Selector == "" || len(c.Selector) > 200 || strings.ContainsFunc(c.Selector, func(r rune) bool {
		return unicode.IsControl(r) || r == '<' || r == '>'
	}) {
		return c, errors.New("selector must be a CSS selector of 1-200 characters")
	}
	return c, nil
}

// head adds the stylesheet and the deferred script to single views when the theme opted in.
func (p *Plugin) head(_ context.Context, v template.HTML, d *theme.PageData) (template.HTML, error) {
	switch d.Kind {
	case theme.KindSingle, theme.KindPage, theme.KindFrontPage:
	default:
		return v, nil
	}
	raw := p.h.ThemeConfig()
	if raw == nil {
		return v, nil
	}
	c, err := parse(raw)
	if err != nil {
		return v, err
	}
	var strs map[string]string
	if m := p.h.Backend().ThemeManifest(); m != nil {
		strs = m.Strings
	}
	esc := template.HTMLEscapeString
	var b strings.Builder
	b.WriteString(`<link rel="stylesheet" href="` + esc(p.h.AssetURL("lightbox.css")) + "\">\n")
	b.WriteString(`<script src="` + esc(p.h.AssetURL("lightbox.js")) + `" defer data-selector="` + esc(c.Selector) + `"`)
	for _, l := range labels {
		text := l.def
		if s := strs["lightbox."+l.key]; s != "" {
			text = s
		}
		b.WriteString(` data-` + l.key + `="` + esc(text) + `"`)
	}
	b.WriteString("></script>\n")
	return v + template.HTML(b.String()), nil
}
