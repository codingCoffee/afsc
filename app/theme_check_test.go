package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"afsc/internal/settings"
	"afsc/internal/theme"
)

// TestDefaultThemeCheck is `afsc theme check themes/default` with every compiled-in plugin.
func TestDefaultThemeCheck(t *testing.T) {
	defaults := settings.Defaults()
	_, results, err := checkTheme(context.Background(), "themes/default", &defaults)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range results {
		if r.Err != nil {
			t.Errorf("%s %s: %v", r.Template, r.Fixture, r.Err)
		}
	}
}

func TestThemeCheckRejectsUnknownPluginConfig(t *testing.T) {
	rt, err := buildPlugins(context.Background(), nil, "https://blog.example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	for name, cfg := range map[string]string{
		"unknown plugin":        `{"nope":{}}`,
		"plugin without config": `{"share":{}}`,
		"unknown lightbox key":  `{"lightbox":{"selector":".entry-content","speed":2}}`,
		"empty selector":        `{"lightbox":{"selector":" "}}`,
		"markup in selector":    `{"lightbox":{"selector":"<b>"}}`,
	} {
		m := mustManifest(t, cfg)
		if err := rt.CheckThemeConfig(m.Plugins); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := rt.CheckThemeConfig(mustManifest(t, `{"lightbox":{"selector":".entry-content, .gallery"}}`).Plugins); err != nil {
		t.Errorf("valid config: %v", err)
	}
}

func mustManifest(t *testing.T, cfg string) *theme.Manifest {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"t","plugins":`+cfg+`}`), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := theme.LoadManifest(dir)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

// TestDefaultThemeSingle renders the default theme's single template with the built-in plugins on fixture data.
func TestDefaultThemeSingle(t *testing.T) {
	defaults := settings.Defaults()
	rt, _, err := checkTheme(context.Background(), "themes/default", &defaults)
	if err != nil {
		t.Fatal(err)
	}
	m, err := theme.LoadManifest("themes/default")
	if err != nil {
		t.Fatal(err)
	}
	opts := themeOptions(rt)
	opts.Backend = theme.FixtureBackend(m)
	th, err := theme.Load("themes/default", opts)
	if err != nil {
		t.Fatal(err)
	}
	render := func(kind string) string {
		var b strings.Builder
		if _, err := th.Page(&b, theme.Fixture(kind, true)); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := render(theme.KindSingle)
	for _, want := range []string{
		`<section class="share" aria-labelledby="share-title">`,
		`href="https://x.com/intent/post?url=https%3A%2F%2Fblog.example.com%2Fexample-post-1%2F&amp;text=Example%20post%201"`,
		`href="mailto:?subject=Example%20post%201&amp;body=https%3A%2F%2Fblog.example.com%2Fexample-post-1%2F" rel="noopener noreferrer nofollow">`,
		`rel="noopener noreferrer nofollow" target="_blank">`,
		`<span class="screen-reader-text">Share on </span>LinkedIn</a>`,
		`<section class="related-posts" aria-labelledby="related-posts-title">`,
		`<span class="related-name">Example post 2</span>`,
		`<span class="related-name">Example post 3</span>`,
		`<link rel="stylesheet" href="/_plugins/lightbox/assets/`,
		`defer data-selector=".entry-content" data-viewer="Image viewer" data-close="Close"`,
		`data-counter="{n} of {total}"></script>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("single lacks %s", want)
		}
	}
	if strings.Contains(out, `<span class="related-name">Example post 1</span>`) {
		t.Error("a post is related to itself")
	}
	if h := theme.InlineScriptHashes([]byte(out)); len(h) != 0 {
		t.Errorf("inline scripts on the page: %v", h)
	}
	page := render(theme.KindPage)
	if strings.Contains(page, "related-posts") || strings.Contains(page, `class="share"`) {
		t.Error("pages show related posts or share links")
	}
	if !strings.Contains(page, "/_plugins/lightbox/assets/") {
		t.Error("the lightbox is missing on pages")
	}
	if home := render(theme.KindHome); strings.Contains(home, "lightbox") {
		t.Error("the lightbox loads on listings")
	}

	// switched off, the funcs return nothing and the page still renders
	s := settings.Defaults()
	s.Plugins.Enabled = map[string]bool{"related": false, "share": false, "lightbox": false}
	rt.Bind(func() *settings.All { return &s })
	out = render(theme.KindSingle)
	if strings.Contains(out, "related-posts") || strings.Contains(out, `class="share"`) || strings.Contains(out, "lightbox") {
		t.Error("disabled plugins still print")
	}
}
