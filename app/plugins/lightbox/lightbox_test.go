package lightbox

import (
	"context"
	"encoding/json"
	"html/template"
	"net/http"
	"net/http/httptest"
	"os"
	"regexp"
	"strings"
	"testing"

	"afsc/internal/plugins"
	"afsc/internal/plugins/plugintest"
	"afsc/internal/settings"
	"afsc/internal/theme"
)

func setup(t *testing.T, cfg string, strs map[string]string) (*plugins.Runtime, *settings.All) {
	s := settings.Defaults()
	rt := plugintest.Runtime(t, &s, New())
	m := &theme.Manifest{Strings: strs}
	if cfg != "" {
		m.Plugins = map[string]json.RawMessage{"lightbox": json.RawMessage(cfg)}
	}
	rt.BindBackend(plugins.StaticBackend{Manifest: m})
	return rt, &s
}

func head(rt *plugins.Runtime, kind string) string {
	return string(plugins.ApplyFilter(context.Background(), rt, plugins.HeadTags, template.HTML(""), &theme.PageData{Kind: kind}))
}

func TestOptIn(t *testing.T) {
	rt, s := setup(t, `{"selector":".entry-content"}`, nil)
	for _, kind := range []string{theme.KindSingle, theme.KindPage, theme.KindFrontPage} {
		h := head(rt, kind)
		if !strings.Contains(h, `<link rel="stylesheet" href="/_plugins/lightbox/assets/`) ||
			!strings.Contains(h, `defer data-selector=".entry-content" data-viewer="Image viewer"`) {
			t.Errorf("%s: %s", kind, h)
		}
		if strings.Contains(h, "<script>") {
			t.Errorf("%s: inline script", kind)
		}
	}
	for _, kind := range []string{theme.KindHome, theme.KindCategory, theme.KindSearch, theme.Kind404} {
		if h := head(rt, kind); h != "" {
			t.Errorf("%s: %s", kind, h)
		}
	}
	s.Plugins.Enabled = map[string]bool{"lightbox": false}
	if h := head(rt, theme.KindSingle); h != "" {
		t.Errorf("disabled: %s", h)
	}

	rt, _ = setup(t, "", nil)
	if h := head(rt, theme.KindSingle); h != "" {
		t.Errorf("a theme that didn't opt in got %s", h)
	}
}

func TestLabelsComeFromTheTheme(t *testing.T) {
	rt, _ := setup(t, `{"selector":"main .content"}`, map[string]string{"lightbox.close": `Fermer "x"`, "lightbox.counter": "{n} / {total}"})
	h := head(rt, theme.KindSingle)
	for _, want := range []string{`data-selector="main .content"`, `data-close="Fermer &#34;x&#34;"`, `data-counter="{n} / {total}"`,
		`data-prev="Previous image"`} {
		if !strings.Contains(h, want) {
			t.Errorf("lacks %s: %s", want, h)
		}
	}
}

func TestConfig(t *testing.T) {
	for raw, ok := range map[string]bool{
		`{"selector":".entry-content"}`:           true,
		`{"selector":".entry-content, .gallery"}`: true,
		`{}`:                               false,
		`{"selector":""}`:                  false,
		`{"selector":"a\nb"}`:              false,
		`{"selector":"</script>"}`:         false,
		`{"selector":"main","extra":true}`: false,
		`{"selector":"` + strings.Repeat("a", 201) + `"}`: false,
	} {
		if _, err := parse(json.RawMessage(raw)); (err == nil) != ok {
			t.Errorf("%s: %v", raw, err)
		}
	}
}

var assetRe = regexp.MustCompile(`(?:href|src)="(/_plugins/lightbox/assets/[^"]+)"`)

func TestAssetsAreImmutable(t *testing.T) {
	rt, _ := setup(t, `{"selector":"main"}`, nil)
	urls := assetRe.FindAllStringSubmatch(head(rt, theme.KindSingle), -1)
	if len(urls) != 2 {
		t.Fatalf("assets %v", urls)
	}
	for _, u := range urls {
		w := httptest.NewRecorder()
		rt.ServePublic(w, httptest.NewRequest(http.MethodGet, u[1], nil))
		if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
			w.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: %d %v", u[1], w.Code, w.Header())
		}
	}
}

// TestScriptBasics guards the a11y-relevant parts of lightbox.js that a static read can check; the rest is the
// browser checklist in docs/themes.md, run against testdata/harness.html.
func TestScriptBasics(t *testing.T) {
	b, err := os.ReadFile("assets/lightbox.js")
	if err != nil {
		t.Fatal(err)
	}
	js := string(b)
	for _, want := range []string{"showModal()", "closeBtn.focus()", "back.focus()", "'aria-live': 'polite'",
		"'aria-label': text('viewer')", "ArrowLeft", "ArrowRight", "e.metaKey || e.ctrlKey", "u.origin === location.origin",
		"'.gallery'", "pointerup", "figcaption"} {
		if !strings.Contains(js, want) {
			t.Errorf("lightbox.js lacks %s", want)
		}
	}
	for _, banned := range []string{"innerHTML", "eval(", "document.write"} {
		if strings.Contains(js, banned) {
			t.Errorf("lightbox.js uses %s", banned)
		}
	}
	css, err := os.ReadFile("assets/lightbox.css")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"prefers-reduced-motion", ":focus-visible", "min-width: 44px", "min-height: 44px"} {
		if !strings.Contains(string(css), want) {
			t.Errorf("lightbox.css lacks %s", want)
		}
	}
}
