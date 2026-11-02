package progress

import (
	"context"
	"html/template"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"afsc/internal/plugins"
	"afsc/internal/plugins/plugintest"
	"afsc/internal/settings"
	"afsc/internal/theme"
)

func setup(t *testing.T) (*plugins.Runtime, *settings.All, *Plugin) {
	s := settings.Defaults()
	p := New()
	return plugintest.Runtime(t, &s, p), &s, p
}

func render(rt *plugins.Runtime, kind string) (head, body template.HTML) {
	d := &theme.PageData{Kind: kind}
	ctx := context.Background()
	return plugins.ApplyFilter(ctx, rt, plugins.HeadTags, "", d), plugins.ApplyFilter(ctx, rt, plugins.BodyEnd, "", d)
}

var srcRe = regexp.MustCompile(`(?:href|src)="([^"]+)"`)

func TestOffByDefault(t *testing.T) {
	rt, _, p := setup(t)
	if rt.Enabled("progress") {
		t.Fatal("the example plugin must be off by default")
	}
	if head, body := render(rt, theme.KindSingle); head != "" || body != "" {
		t.Errorf("disabled plugin printed %q %q", head, body)
	}
	if got := rt.Funcs()["plg_progress_bar"].(func(*theme.PageData) template.HTML)(&theme.PageData{Kind: theme.KindSingle}); got != "" || p.h == nil {
		t.Errorf("disabled func: %q", got)
	}
}

func TestAutoPlacement(t *testing.T) {
	rt, s, _ := setup(t)
	s.Plugins.Enabled = map[string]bool{"progress": true}
	head, body := render(rt, theme.KindSingle)
	if !strings.Contains(string(head), `<link rel="stylesheet" href="/_plugins/progress/assets/`) {
		t.Errorf("head: %q", head)
	}
	for _, want := range []string{`class="plg-progress"`, `--plg-progress-color:#2271b1;--plg-progress-height:3px`,
		`aria-hidden="true"`, `<script src="/_plugins/progress/assets/`, `defer></script>`} {
		if !strings.Contains(string(body), want) {
			t.Errorf("body lacks %q: %q", want, body)
		}
	}
	if strings.Contains(string(body), "<script>") {
		t.Error("inline script; the public CSP is hash-based, so plugins load JS from assets")
	}
	for _, kind := range []string{theme.KindPage, theme.KindHome, theme.KindCategory} {
		if head, body := render(rt, kind); head != "" || body != "" {
			t.Errorf("%s with posts_only: %q %q", kind, head, body)
		}
	}
	s.Plugins.Settings = map[string]map[string]any{"progress": {"posts_only": false, "color": "#ff0000", "height": 5.0}}
	_, body = render(rt, theme.KindPage)
	if !strings.Contains(string(body), "--plg-progress-color:#ff0000;--plg-progress-height:5px") {
		t.Errorf("page with posts_only off: %q", body)
	}

	for _, m := range srcRe.FindAllStringSubmatch(string(head+body), -1) {
		rec := httptest.NewRecorder()
		rt.ServePublic(rec, httptest.NewRequest("GET", m[1], nil))
		if rec.Code != 200 || rec.Body.Len() == 0 || !strings.Contains(rec.Header().Get("Cache-Control"), "immutable") {
			t.Errorf("%s: %d %v", m[1], rec.Code, rec.Header())
		}
	}
}

func TestManualPlacement(t *testing.T) {
	rt, s, _ := setup(t)
	s.Plugins.Enabled = map[string]bool{"progress": true}
	s.Plugins.Settings = map[string]map[string]any{"progress": {"placement": "manual"}}
	head, body := render(rt, theme.KindSingle)
	if head == "" || body != "" {
		t.Errorf("manual: head %q body %q", head, body)
	}
	bar := rt.Funcs()["plg_progress_bar"].(func(*theme.PageData) template.HTML)
	if got := bar(&theme.PageData{Kind: theme.KindSingle}); !strings.Contains(string(got), `class="plg-progress"`) {
		t.Errorf("func: %q", got)
	}
	if got := bar(&theme.PageData{Kind: theme.KindHome}); got != "" {
		t.Errorf("func on home: %q", got)
	}
}

func TestSettingsValidation(t *testing.T) {
	rt, _, _ := setup(t)
	for vals, field := range map[string]string{"height": "settings.progress.height", "color": "settings.progress.color"} {
		bad := map[string]any{"height": 11.0, "color": "red"}
		if f, _ := rt.ValidateSettings(settings.Plugins{Settings: map[string]map[string]any{"progress": {vals: bad[vals]}}}); f != field {
			t.Errorf("%s: field %q", vals, f)
		}
	}
}

func TestTemplateUse(t *testing.T) {
	rt, s, _ := setup(t)
	s.Plugins.Enabled = map[string]bool{"progress": true}
	s.Plugins.Settings = map[string]map[string]any{"progress": {"placement": "manual"}}
	tpl := template.Must(template.New("t").Funcs(rt.Funcs()).Parse(`<body>{{ plg_progress_bar . }}</body>`))
	var b strings.Builder
	if err := tpl.Execute(&b, &theme.PageData{Kind: theme.KindSingle}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), `<div class="plg-progress" style="--plg-progress-color:#2271b1;--plg-progress-height:3px"`) {
		t.Errorf("rendered: %s", b.String())
	}
}
