package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"

	"afsc/internal/theme"
)

// Backend is the site data plugins read at run time. `afsc serve` and `afsc worker` bind the site to it; elsewhere
// plugins see a StaticBackend with no posts.
type Backend interface {
	// Posts returns the published posts among ids, in the order given, hydrated like listing cards (URL, terms,
	// authors, featured media). Missing and unpublished ids are skipped.
	Posts(ctx context.Context, ids []int64) ([]*theme.Post, error)
	// PurgePosts drops the cached pages of these posts on every replica.
	PurgePosts(ctx context.Context, ids []int64) error
	// ThemeManifest is the active theme's theme.json; nil when no theme is loaded.
	ThemeManifest() *theme.Manifest
}

// StaticBackend has no posts and purges nothing. `afsc theme check` binds one with the checked theme's manifest.
type StaticBackend struct {
	Manifest *theme.Manifest
}

func (StaticBackend) Posts(context.Context, []int64) ([]*theme.Post, error) { return nil, nil }
func (StaticBackend) PurgePosts(context.Context, []int64) error             { return nil }
func (b StaticBackend) ThemeManifest() *theme.Manifest                      { return b.Manifest }

// BindBackend gives plugins the site's data.
func (rt *Runtime) BindBackend(b Backend) {
	if rt != nil {
		rt.backend.Store(&b)
	}
}

// Backend is the bound site backend, or a StaticBackend with no manifest.
func (h *Host) Backend() Backend {
	if b := h.rt.backend.Load(); b != nil {
		return *b
	}
	return StaticBackend{}
}

// Fixtures reports `afsc theme check`: template funcs should return sample data so the theme's partials render.
func (h *Host) Fixtures() bool { return h.rt.cfg.Fixtures }

// RegisterThemeConfig lets themes configure this plugin in theme.json under "plugins": {"<id>": …}. check
// validates the value; it runs in `afsc theme check` and when the site loads a theme.
func (h *Host) RegisterThemeConfig(check func(raw json.RawMessage) error) {
	if h.themeCheck != nil || check == nil {
		h.fail("theme config registered twice or without a check")
		return
	}
	h.themeCheck = check
}

// ThemeConfig is the active theme's value for this plugin; nil when the theme doesn't configure it.
func (h *Host) ThemeConfig() json.RawMessage {
	if h.themeCheck == nil {
		return nil
	}
	if m := h.Backend().ThemeManifest(); m != nil {
		return m.Plugins[h.id]
	}
	return nil
}

// CheckThemeConfig validates a theme.json "plugins" object: every key must be a compiled-in plugin that accepts
// theme config, and its value must pass that plugin's check.
func (rt *Runtime) CheckThemeConfig(cfg map[string]json.RawMessage) error {
	var errs []error
	for _, id := range slices.Sorted(maps.Keys(cfg)) {
		e := rt.entry(id)
		if e == nil || e.host.themeCheck == nil {
			errs = append(errs, fmt.Errorf("plugins.%s: no compiled-in plugin %q takes theme settings", id, id))
			continue
		}
		if err := e.host.themeCheck(cfg[id]); err != nil {
			errs = append(errs, fmt.Errorf("plugins.%s: %w", id, err))
		}
	}
	return errors.Join(errs...)
}

// RegisterImportJob queues one of this plugin's job kinds after every successful `afsc import`.
func (h *Host) RegisterImportJob(kind string) {
	if j, ok := h.rt.jobs[kind]; !ok || j.Plugin != h.id {
		h.fail("import job %q: register the job kind first", kind)
		return
	}
	h.rt.importJobs = append(h.rt.importJobs, kind)
}

// ImportJobs are the job kinds to queue after an import, in registry order.
func (rt *Runtime) ImportJobs() []string {
	if rt == nil {
		return nil
	}
	return rt.importJobs
}
