// Package progress is the example plugin from docs/plugins.md: a reading progress bar at the top of posts. It
// shows settings, the head.tags and body.end filters, a template func and assets.
package progress

import (
	"context"
	"embed"
	"html/template"
	"io/fs"
	"strconv"

	"afsc/internal/plugins"
	"afsc/internal/theme"
)

//go:embed assets
var assets embed.FS

type Plugin struct {
	h *plugins.Host
}

func New() *Plugin { return &Plugin{} }

func (*Plugin) ID() string { return "progress" }

func (*Plugin) Info() plugins.Info {
	return plugins.Info{Name: "Reading progress bar",
		Description: "A thin bar at the top of the page that fills as the reader scrolls. The example plugin from docs/plugins.md."}
}

func (p *Plugin) Init(_ context.Context, h *plugins.Host) error {
	p.h = h
	sub, err := fs.Sub(assets, "assets")
	if err != nil {
		return err
	}
	h.RegisterAssets(sub)
	one, ten := 1.0, 10.0
	h.RegisterSettings(
		plugins.Setting{Key: "color", Label: "Bar color", Type: "color", Default: "#2271b1"},
		plugins.Setting{Key: "height", Label: "Bar height (px)", Type: "number", Default: 3, Min: &one, Max: &ten},
		plugins.Setting{Key: "placement", Label: "Placement", Type: "select", Default: "auto",
			Options: []string{"auto", "manual"},
			Help:    "auto adds the bar to every matching page; manual leaves it to the theme's {{ plg_progress_bar . }}."},
		plugins.Setting{Key: "posts_only", Label: "Posts only", Type: "bool", Default: true,
			Help: "When off, pages show the bar too."},
	)
	plugins.AddFilter(h, plugins.HeadTags, 10, p.head)
	plugins.AddFilter(h, plugins.BodyEnd, 10, p.body)
	h.RegisterTemplateFuncs(template.FuncMap{"plg_progress_bar": p.bar})
	return nil
}

func (p *Plugin) applies(d *theme.PageData) bool {
	switch d.Kind {
	case theme.KindSingle:
		return true
	case theme.KindPage, theme.KindFrontPage:
		return !p.h.Bool("posts_only")
	}
	return false
}

func (p *Plugin) head(_ context.Context, v template.HTML, d *theme.PageData) (template.HTML, error) {
	if !p.applies(d) {
		return v, nil
	}
	return v + template.HTML(`<link rel="stylesheet" href="`+template.HTMLEscapeString(p.h.AssetURL("progress.css"))+"\">\n"), nil
}

func (p *Plugin) body(_ context.Context, v template.HTML, d *theme.PageData) (template.HTML, error) {
	if p.h.String("placement") != "auto" || !p.applies(d) {
		return v, nil
	}
	return v + p.markup(), nil
}

// bar is plg_progress_bar: the markup for themes that place the bar themselves (placement "manual").
func (p *Plugin) bar(d *theme.PageData) template.HTML {
	if d == nil || p.h.String("placement") != "manual" || !p.applies(d) {
		return ""
	}
	return p.markup()
}

func (p *Plugin) markup() template.HTML {
	esc := template.HTMLEscapeString
	style := "--plg-progress-color:" + p.h.String("color") + ";--plg-progress-height:" +
		strconv.FormatFloat(p.h.Number("height"), 'f', -1, 64) + "px"
	return template.HTML(`<div class="plg-progress" style="` + esc(style) + `" aria-hidden="true">` +
		`<div class="plg-progress__bar"></div></div>` + "\n" +
		`<script src="` + esc(p.h.AssetURL("progress.js")) + `" defer></script>` + "\n")
}
