package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"html/template"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"afsc/internal/authz"
	"afsc/internal/jobs"
	"afsc/internal/seo"
	"afsc/internal/settings"
	"afsc/internal/theme"
)

type fake struct {
	id   string
	info *Info
	init func(h *Host)
}

func (f *fake) ID() string { return f.id }
func (f *fake) Init(_ context.Context, h *Host) error {
	if f.init != nil {
		f.init(h)
	}
	return nil
}

type described struct{ *fake }

func (d described) Info() Info { return *d.info }

func build(t *testing.T, ps ...Plugin) *Runtime {
	t.Helper()
	rt, err := Build(context.Background(), Config{Logf: t.Logf}, ps)
	if err != nil {
		t.Fatal(err)
	}
	return rt
}

func withSettings(rt *Runtime, mod func(*settings.All)) {
	s := settings.Defaults()
	mod(&s)
	rt.Bind(func() *settings.All { return &s })
}

func appendHTML(s string) func(context.Context, template.HTML, *theme.PageData) (template.HTML, error) {
	return func(_ context.Context, v template.HTML, _ *theme.PageData) (template.HTML, error) {
		return v + template.HTML(s), nil
	}
}

func TestFilterOrder(t *testing.T) {
	rt := build(t,
		&fake{id: "aa", init: func(h *Host) {
			AddFilter(h, HeadTags, 10, appendHTML("a10 "))
			AddFilter(h, HeadTags, 0, appendHTML("a0 "))
		}},
		&fake{id: "bb", init: func(h *Host) {
			AddFilter(h, HeadTags, 10, appendHTML("b10 "))
			AddFilter(h, HeadTags, -5, appendHTML("b-5 "))
		}},
	)
	got := ApplyFilter(context.Background(), rt, HeadTags, "", &theme.PageData{})
	if got != "b-5 a0 a10 b10 " {
		t.Errorf("order: %q", got)
	}
}

func TestFilterFailuresKeepThePreviousValue(t *testing.T) {
	var logs []string
	cases := map[string]func(context.Context, template.HTML, *theme.PageData) (template.HTML, error){
		"error": func(context.Context, template.HTML, *theme.PageData) (template.HTML, error) {
			return "bad", errors.New("boom")
		},
		"panic": func(context.Context, template.HTML, *theme.PageData) (template.HTML, error) { panic("exploded") },
		"timeout": func(ctx context.Context, _ template.HTML, _ *theme.PageData) (template.HTML, error) {
			<-ctx.Done()
			time.Sleep(10 * time.Millisecond)
			return "late", nil
		},
	}
	for name, fn := range cases {
		t.Run(name, func(t *testing.T) {
			logs = nil
			rt, err := Build(context.Background(), Config{Logf: func(f string, a ...any) { logs = append(logs, f) }}, []Plugin{
				&fake{id: "first", init: func(h *Host) { AddFilter(h, BodyEnd, 0, appendHTML("one ")) }},
				&fake{id: "broken", init: func(h *Host) { AddFilter(h, BodyEnd, 1, fn) }},
				&fake{id: "last", init: func(h *Host) { AddFilter(h, BodyEnd, 2, appendHTML("two")) }},
			})
			if err != nil {
				t.Fatal(err)
			}
			start := time.Now()
			if got := ApplyFilter(context.Background(), rt, BodyEnd, "", &theme.PageData{}); got != "one two" {
				t.Errorf("got %q", got)
			}
			if time.Since(start) > time.Second {
				t.Error("a slow handler held up the chain")
			}
			if len(logs) != 1 {
				t.Errorf("logs %v", logs)
			}
			ApplyFilter(context.Background(), rt, BodyEnd, "", &theme.PageData{})
			if len(logs) != 1 {
				t.Error("a second failure within a minute was logged again")
			}
		})
	}
}

func TestCloneProtectsThePreviousValue(t *testing.T) {
	rt := build(t,
		&fake{id: "mutator", init: func(h *Host) {
			AddFilter(h, SEOMeta, 0, func(_ context.Context, m *seo.Meta, _ *theme.PageData) (*seo.Meta, error) {
				m.Title = "changed"
				m.Tags[0].Value = "changed"
				return nil, errors.New("then failed")
			})
		}},
	)
	in := &seo.Meta{Title: "t", Tags: []seo.Tag{{Kind: "name", Key: "description", Value: "d"}}}
	out := ApplyFilter(context.Background(), rt, SEOMeta, in, &theme.PageData{})
	if out != in || out.Title != "t" || out.Tags[0].Value != "d" {
		t.Errorf("value changed by a failed handler: %+v", out)
	}
}

func TestNoHandlersNoGoroutines(t *testing.T) {
	rt := build(t)
	before := runtime.NumGoroutine()
	for range 100 {
		ApplyFilter(context.Background(), rt, HeadTags, "x", &theme.PageData{})
	}
	if runtime.NumGoroutine() > before {
		t.Error("ApplyFilter started goroutines with no handlers")
	}
	if got := ApplyFilter(context.Background(), (*Runtime)(nil), HeadTags, "x", nil); got != "x" {
		t.Errorf("nil runtime: %q", got)
	}
}

func BenchmarkApplyFilterNoHandlers(b *testing.B) {
	rt, _ := Build(context.Background(), Config{}, nil)
	d := &theme.PageData{}
	for b.Loop() {
		ApplyFilter(context.Background(), rt, HeadTags, "x", d)
	}
}

func TestEnabled(t *testing.T) {
	rt := build(t,
		&fake{id: "plain"},
		described{&fake{id: "off", info: &Info{DefaultEnabled: false}, init: func(h *Host) {
			AddFilter(h, HeadTags, 0, appendHTML("off"))
		}}},
		described{&fake{id: "always", info: &Info{AlwaysOn: true}}},
	)
	if !rt.Enabled("plain") || rt.Enabled("off") || !rt.Enabled("always") || rt.Enabled("missing") {
		t.Fatal("defaults")
	}
	if got := ApplyFilter(context.Background(), rt, HeadTags, "", &theme.PageData{}); got != "" {
		t.Errorf("a disabled plugin's filter ran: %q", got)
	}
	withSettings(rt, func(s *settings.All) {
		s.Plugins.Enabled = map[string]bool{"plain": false, "off": true, "always": false}
	})
	if rt.Enabled("plain") || !rt.Enabled("off") || !rt.Enabled("always") {
		t.Fatal("settings")
	}
	if got := ApplyFilter(context.Background(), rt, HeadTags, "", &theme.PageData{}); got != "off" {
		t.Errorf("enabled filter: %q", got)
	}
}

func TestRegistrationIsValidated(t *testing.T) {
	cases := map[string]Plugin{
		"bad id":           &fake{id: "Bad_ID"},
		"unknown hook":     &fake{id: "x1", init: func(h *Host) { h.addHandler("nope", 0, nil) }},
		"func prefix":      &fake{id: "x1", init: func(h *Host) { h.RegisterTemplateFuncs(template.FuncMap{"related_posts": func() string { return "" }}) }},
		"func not a func":  &fake{id: "x1", init: func(h *Host) { h.RegisterTemplateFuncs(template.FuncMap{"plg_x1_a": 1}) }},
		"route traversal":  &fake{id: "x1", init: func(h *Host) { h.RegisterPublicRoute("GET", "../admin", http.NotFoundHandler()) }},
		"route assets":     &fake{id: "x1", init: func(h *Host) { h.RegisterPublicRoute("GET", "assets/x", http.NotFoundHandler()) }},
		"route method":     &fake{id: "x1", init: func(h *Host) { h.RegisterPublicRoute("DELETE", "x", http.NotFoundHandler()) }},
		"admin public":     &fake{id: "x1", init: func(h *Host) { h.RegisterAdminAPI("GET", "x", authz.Public, http.NotFoundHandler()) }},
		"job prefix":       &fake{id: "x1", init: func(h *Host) { h.RegisterJob("other.kind", nil, jobs.Options{}) }},
		"schedule unknown": &fake{id: "x1", init: func(h *Host) { h.RegisterSchedule(jobs.Schedule{Kind: "x1.a", Every: time.Hour}) }},
		"setting default":  &fake{id: "x1", init: func(h *Host) { h.RegisterSettings(Setting{Key: "c", Type: "color", Default: "red"}) }},
		"setting type":     &fake{id: "x1", init: func(h *Host) { h.RegisterSettings(Setting{Key: "c", Type: "json", Default: ""}) }},
		"migration prefix": &fake{id: "x1", init: func(h *Host) {
			h.RegisterMigrations(fstest.MapFS{"0001_a.up.sql": {Data: []byte("CREATE TABLE posts2 (id int);")}})
		}},
		"migration name":    &fake{id: "x1", init: func(h *Host) { h.RegisterMigrations(fstest.MapFS{"a.sql": {Data: []byte("")}}) }},
		"admin bad subpath": &fake{id: "x1", init: func(h *Host) { h.RegisterAdminAPI("GET", "../x", authz.ManageSettings, http.NotFoundHandler()) }},
	}
	for name, p := range cases {
		if _, err := Build(context.Background(), Config{}, []Plugin{p}); err == nil {
			t.Errorf("%s: no error", name)
		}
	}
	if _, err := Build(context.Background(), Config{}, []Plugin{&fake{id: "dup"}, &fake{id: "dup"}}); err == nil {
		t.Error("duplicate id accepted")
	}
}

func TestCheckMigrations(t *testing.T) {
	ok := fstest.MapFS{
		"0001_init.up.sql": {Data: []byte(`-- creates things
CREATE TABLE plg_x1_items (id bigint PRIMARY KEY);
CREATE UNIQUE INDEX IF NOT EXISTS plg_x1_items_idx ON plg_x1_items (id);
ALTER TABLE ONLY plg_x1_items ADD COLUMN n int;`)},
		"0001_init.down.sql": {Data: []byte(`DROP TABLE IF EXISTS plg_x1_items;`)},
	}
	if err := CheckMigrations("x1", ok); err != nil {
		t.Errorf("valid migrations: %v", err)
	}
	for _, sql := range []string{
		`CREATE TABLE posts_extra (id int);`,
		`CREATE INDEX posts_x ON plg_x1_items (id);`,
		`ALTER TABLE posts ADD COLUMN x int;`,
		`DROP TABLE IF EXISTS users;`,
		`CREATE TABLE public.comments2 (id int);`,
	} {
		if err := CheckMigrations("x1", fstest.MapFS{"0002_x.up.sql": {Data: []byte(sql)}}); err == nil {
			t.Errorf("accepted %q", sql)
		}
	}
}

func TestTemplateFuncGuard(t *testing.T) {
	rt := build(t, &fake{id: "fn", init: func(h *Host) {
		h.RegisterTemplateFuncs(template.FuncMap{
			"plg_fn_hello": func(name string) (string, error) { return "hi " + name, nil },
			"plg_fn_boom":  func() string { panic("boom") },
			"plg_fn_join":  func(xs ...string) string { return strings.Join(xs, ",") },
		})
	}})
	fm := rt.Funcs()
	hello := fm["plg_fn_hello"].(func(string) (string, error))
	if s, err := hello("you"); s != "hi you" || err != nil {
		t.Errorf("hello: %q %v", s, err)
	}
	if s := fm["plg_fn_boom"].(func() string)(); s != "" {
		t.Errorf("panicking func: %q", s)
	}
	if s := fm["plg_fn_join"].(func(...string) string)("a", "b"); s != "a,b" {
		t.Errorf("variadic: %q", s)
	}
	withSettings(rt, func(s *settings.All) { s.Plugins.Enabled = map[string]bool{"fn": false} })
	if s, err := hello("you"); s != "" || err != nil {
		t.Errorf("disabled: %q %v", s, err)
	}
	tpl := template.Must(template.New("t").Funcs(fm).Parse(`[{{ plg_fn_hello "x" }}]`))
	var b strings.Builder
	if err := tpl.Execute(&b, nil); err != nil || b.String() != "[]" {
		t.Errorf("template with a disabled func: %q %v", b.String(), err)
	}
}

func TestSettings(t *testing.T) {
	one, ten := 1.0, 10.0
	var host *Host
	rt := build(t, &fake{id: "opts", init: func(h *Host) {
		host = h
		h.RegisterSettings(
			Setting{Key: "n", Type: "number", Default: 3, Min: &one, Max: &ten},
			Setting{Key: "mode", Type: "select", Default: "a", Options: []string{"a", "b"}},
		)
	}})
	if host.Number("n") != 3 || host.String("mode") != "a" {
		t.Fatalf("defaults %v", host.Settings())
	}
	withSettings(rt, func(s *settings.All) {
		s.Plugins.Settings = map[string]map[string]any{"opts": {"n": 7.0, "mode": "zzz"}}
	})
	if host.Number("n") != 7 || host.String("mode") != "a" {
		t.Errorf("saved values (invalid ones ignored): %v", host.Settings())
	}
	for _, c := range []struct {
		p     settings.Plugins
		field string
	}{
		{settings.Plugins{Settings: map[string]map[string]any{"opts": {"n": 11.0}}}, "settings.opts.n"},
		{settings.Plugins{Settings: map[string]map[string]any{"opts": {"other": 1.0}}}, "settings.opts.other"},
		{settings.Plugins{Settings: map[string]map[string]any{"opts": {"mode": "c"}}}, "settings.opts.mode"},
		{settings.Plugins{Settings: map[string]map[string]any{"gone": {"anything": 1.0}}}, ""},
		{settings.Plugins{Settings: map[string]map[string]any{"opts": {"n": 2.0, "mode": "b"}}}, ""},
	} {
		if field, _ := rt.ValidateSettings(c.p); field != c.field {
			t.Errorf("%v: field %q, want %q", c.p, field, c.field)
		}
	}
	always := build(t, described{&fake{id: "spam", info: &Info{AlwaysOn: true}}})
	if field, _ := always.ValidateSettings(settings.Plugins{Enabled: map[string]bool{"spam": false}}); field != "enabled.spam" {
		t.Errorf("switching off an always-on plugin: %q", field)
	}
}

func TestServePublic(t *testing.T) {
	var host *Host
	rt := build(t, &fake{id: "web", init: func(h *Host) {
		host = h
		h.RegisterAssets(fstest.MapFS{"app.js": {Data: []byte("console.log(1)")}, "css/app.css": {Data: []byte("a{}")}})
		h.RegisterPublicRoute("GET", "hello", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("hello"))
		}))
		h.RegisterPublicRoute("POST", "form", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusAccepted)
		}))
	}})
	get := func(method, target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		rt.ServePublic(rec, httptest.NewRequest(method, target, nil))
		return rec
	}
	js := host.AssetURL("app.js")
	if !strings.HasPrefix(js, "/_plugins/web/assets/") || !strings.HasSuffix(js, "/app.js") {
		t.Fatalf("asset url %q", js)
	}
	rec := get("GET", js)
	if rec.Code != 200 || rec.Body.String() != "console.log(1)" || rec.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" ||
		!strings.HasPrefix(rec.Header().Get("Content-Type"), "text/javascript") || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("asset: %d %q %v", rec.Code, rec.Body.String(), rec.Header())
	}
	if rec := get("GET", "/_plugins/web/assets/000000000000/app.js"); rec.Code != 200 || rec.Header().Get("Cache-Control") != "public, max-age=60" {
		t.Errorf("stale hash: %d %v", rec.Code, rec.Header())
	}
	if rec := get("GET", host.AssetURL("css/app.css")); rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "text/css") {
		t.Errorf("nested asset: %d %v", rec.Code, rec.Header())
	}
	if rec := get("GET", "/_plugins/web/hello"); rec.Code != 200 || rec.Body.String() != "hello" {
		t.Errorf("route: %d", rec.Code)
	}
	if rec := get("HEAD", "/_plugins/web/hello"); rec.Code != 200 {
		t.Errorf("HEAD route: %d", rec.Code)
	}
	if rec := get("POST", "/_plugins/web/form"); rec.Code != http.StatusAccepted {
		t.Errorf("POST route: %d", rec.Code)
	}
	for _, target := range []string{"/_plugins/web/nope", "/_plugins/other/hello", "/_plugins/web/assets/x/missing.js"} {
		if rec := get("GET", target); rec.Code != 404 {
			t.Errorf("%s: %d", target, rec.Code)
		}
	}
	if rec := get("POST", "/_plugins/web/hello"); rec.Code != 404 {
		t.Errorf("POST to a GET route: %d", rec.Code)
	}
	withSettings(rt, func(s *settings.All) { s.Plugins.Enabled = map[string]bool{"web": false} })
	if rec := get("GET", js); rec.Code != 404 {
		t.Errorf("disabled asset: %d", rec.Code)
	}
	if rec := get("GET", "/_plugins/web/hello"); rec.Code != 404 {
		t.Errorf("disabled route: %d", rec.Code)
	}
}

func TestJobsNoOpWhenDisabled(t *testing.T) {
	ran := 0
	rt := build(t, &fake{id: "worker", init: func(h *Host) {
		h.RegisterJob("worker.run", func(context.Context, json.RawMessage) error { ran++; return nil }, jobs.Options{})
		h.RegisterSchedule(jobs.Schedule{Kind: "worker.run", Every: time.Hour})
	}})
	h := rt.Jobs()["worker.run"].Handler
	_ = h(context.Background(), nil)
	withSettings(rt, func(s *settings.All) { s.Plugins.Enabled = map[string]bool{"worker": false} })
	_ = h(context.Background(), nil)
	if ran != 1 || len(rt.Schedules()) != 1 {
		t.Errorf("ran %d, schedules %d", ran, len(rt.Schedules()))
	}
}
