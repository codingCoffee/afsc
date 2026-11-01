package theme

import (
	"bytes"
	"strings"
	"testing"

	"afsc/internal/settings"
)

func TestSettingCheck(t *testing.T) {
	sel := Setting{Type: "select", Options: []string{"wide", "narrow"}}
	cases := []struct {
		s  Setting
		v  any
		ok bool
	}{
		{Setting{Type: "bool"}, true, true},
		{Setting{Type: "bool"}, "true", false},
		{Setting{Type: "number"}, 3.5, true},
		{Setting{Type: "number"}, "3", false},
		{Setting{Type: "string"}, "hello", true},
		{Setting{Type: "string"}, 1.0, false},
		{Setting{Type: "string"}, strings.Repeat("x", 5000), false},
		{Setting{Type: "color"}, "#1a2B3c", true},
		{Setting{Type: "color"}, "red", false},
		{sel, "narrow", true},
		{sel, "full", false},
		{Setting{Type: "date"}, "2024-01-01", false},
	}
	for _, c := range cases {
		if err := c.s.Check(c.v); (err == nil) != c.ok {
			t.Errorf("%s %v: err %v, want ok=%v", c.s.Type, c.v, err, c.ok)
		}
	}
}

func TestManifestSettingsAndCSP(t *testing.T) {
	cases := []struct{ json, want string }{
		{`{"name":"t","settings":{"x":{"type":"date","label":"X","default":"a"}}}`, "type must be one of"},
		{`{"name":"t","settings":{"x":{"type":"select","label":"X","default":"a"}}}`, "needs options"},
		{`{"name":"t","settings":{"x":{"type":"bool","label":"X","default":"yes"}}}`, "must be true or false"},
		{`{"name":"t","csp":{"script":["*"]}}`, "must be an https origin"},
		{`{"name":"t","csp":{"frame":["http://a.example.com"]}}`, "must be an https origin"},
		{`{"name":"t","csp":{"font":["https://fonts.example.com"]},"settings":{"x":{"type":"select","label":"X","default":"a","options":["a","b"]}}}`, ""},
	}
	for _, c := range cases {
		dir := writeTheme(t, map[string]string{"theme.json": c.json, "templates/index.html": "x"})
		_, err := LoadManifest(dir)
		if c.want == "" {
			if err != nil {
				t.Errorf("%s: %v", c.json, err)
			}
			continue
		}
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err %v, want %q", c.json, err, c.want)
		}
	}
}

func TestSnippetsAndThemeSettingFuncs(t *testing.T) {
	dir := writeTheme(t, map[string]string{
		"theme.json":           `{"name":"t","settings":{"wide":{"type":"bool","label":"Wide","default":true}}}`,
		"templates/index.html": `<head>{{head .}}</head><body>{{if theme_setting "wide"}}WIDE{{else}}NARROW{{end}}<script>a()</script>{{footer .}}</body>`,
	})
	s := settings.Defaults()
	s.Snippets = settings.Snippets{HeadHTML: `<meta name="x-head" content="1">`, BodyEndHTML: `<script>tail()</script>`}
	th, err := Load(dir, Options{Backend: &StaticBackend{S: &s, Base: "https://blog.example.com"}})
	if err != nil {
		t.Fatal(err)
	}
	render := func() string {
		var b bytes.Buffer
		if _, err := th.Page(&b, Fixture(KindHome, false)); err != nil {
			t.Fatal(err)
		}
		return b.String()
	}
	out := render()
	for _, w := range []string{`<meta name="x-head" content="1">`, `<script>tail()</script></body>`, "WIDE"} {
		if !strings.Contains(out, w) {
			t.Errorf("missing %q in %s", w, out)
		}
	}
	s.ThemeSettings = map[string]map[string]any{"t": {"wide": false}, "other": {"wide": true}}
	if out := render(); !strings.Contains(out, "NARROW") {
		t.Errorf("admin value ignored: %s", out)
	}
	if _, hashes := th.InlineScripts(); len(hashes) != 1 || hashes[0] != InlineScriptHashes([]byte("<script>a()</script>"))[0] {
		t.Errorf("template script hashes: %v", hashes)
	}
}
