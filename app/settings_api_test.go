package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"afsc/internal/pagecache"
	"afsc/internal/settings"
	"afsc/internal/site"
)

// settingsKeep restores the settings table when the test ends.
func settingsKeep(t *testing.T, env *adminEnv) {
	t.Helper()
	ctx := context.Background()
	saved := map[string][]byte{}
	rows, err := env.pool.Query(ctx, `SELECT key, value FROM settings`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var k string
		var v []byte
		if err := rows.Scan(&k, &v); err != nil {
			t.Fatal(err)
		}
		saved[k] = v
	}
	rows.Close()
	t.Cleanup(func() {
		for _, k := range settings.Keys {
			var err error
			if v, ok := saved[k]; ok {
				_, err = env.pool.Exec(ctx, settingsSQLPut, k, json.RawMessage(v))
			} else {
				_, err = env.pool.Exec(ctx, `DELETE FROM settings WHERE key = $1`, k)
			}
			if err != nil {
				t.Error(err)
			}
		}
		_ = env.deps.site.Invalidate(ctx, site.Event{Refresh: true, All: true})
	})
}

func settingsBody(t *testing.T, group string, mod func(map[string]any)) string {
	t.Helper()
	d := settings.Defaults()
	b, _ := json.Marshal(d.Group(group))
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	if mod != nil {
		mod(m)
	}
	out, _ := json.Marshal(m)
	return string(out)
}

func TestSettingsGroups(t *testing.T) {
	env := newAdminEnv(t)
	settingsKeep(t, env)
	put := func(group, body string) apiResp {
		return env.do("admin", http.MethodPut, "/settings/"+group, body)
	}
	cases := []struct {
		group string
		bad   func(map[string]any)
		field string
	}{
		{"site", func(m map[string]any) { m["timezone"] = "Mars/Base" }, "timezone"},
		{"permalinks", func(m map[string]any) { m["trailing_slash"] = "sometimes" }, "trailing_slash"},
		{"reading", func(m map[string]any) { m["posts_per_page"] = 0 }, "posts_per_page"},
		{"reading", func(m map[string]any) { m["home"] = 999999999 }, "home"},
		{"reading", func(m map[string]any) { m["home"] = "wp:post:4" }, "home"},
		{"seo", func(m map[string]any) { m["knowledge_graph"] = map[string]any{"type": "robot"} }, "knowledge_graph.type"},
		{"discussion", func(m map[string]any) { m["thread_depth"] = 0 }, "thread_depth"},
		{"discussion", func(m map[string]any) { m["spam_retention_days"] = 0 }, "spam_retention_days"},
		{"cache", func(m map[string]any) { m["page_ttl_seconds"] = -1 }, "page_ttl_seconds"},
		{"media", func(m map[string]any) { m["max_image_mb"] = 0 }, "max_image_mb"},
		{"security", func(m map[string]any) {
			m["extra_csp_hosts"] = map[string]any{"script-src": []string{"'unsafe-inline'"}}
		}, "extra_csp_hosts.script-src"},
		{"snippets", func(m map[string]any) { m["head_html"] = strings.Repeat("x", 65<<10) }, ""},
	}
	for _, c := range cases {
		if r := put(c.group, settingsBody(t, c.group, nil)); r.code != http.StatusOK {
			t.Errorf("%s defaults: %d %s", c.group, r.code, r.body)
		}
		if r := put(c.group, settingsBody(t, c.group, func(m map[string]any) { m["nope"] = 1 })); r.code != http.StatusBadRequest ||
			!strings.Contains(string(r.body), "unknown field") {
			t.Errorf("%s unknown key: %d %s", c.group, r.code, r.body)
		}
		r := put(c.group, settingsBody(t, c.group, c.bad))
		var e struct{ Message, Field string }
		_ = json.Unmarshal(r.body, &e)
		if r.code != http.StatusBadRequest || e.Field != c.field {
			t.Errorf("%s invalid: %d %s (want field %q)", c.group, r.code, r.body, c.field)
		}
	}
	if r := put("nope", "{}"); r.code != http.StatusNotFound {
		t.Errorf("unknown group: %d", r.code)
	}

	t.Run("theme settings", func(t *testing.T) {
		for body, want := range map[string]int{
			`{"default":{"show_reading_time":false}}`:     http.StatusOK,
			`{"default":{"show_reading_time":"no"}}`:      http.StatusBadRequest,
			`{"default":{"undeclared":true}}`:             http.StatusBadRequest,
			`{"default":[]}`:                              http.StatusBadRequest,
			`{"other-theme":{"anything":1},"default":{}}`: http.StatusOK,
		} {
			if r := put("theme_settings", body); r.code != want {
				t.Errorf("%s: %d %s", body, r.code, r.body)
			}
		}
	})

	t.Run("get", func(t *testing.T) {
		r := env.do("admin", http.MethodGet, "/settings", "")
		var res struct {
			Values map[string]json.RawMessage `json:"values"`
			Theme  struct {
				Name     string `json:"name"`
				Settings []struct {
					Key, Type string
				} `json:"settings"`
			} `json:"theme"`
		}
		r.json(t, &res)
		if r.code != http.StatusOK || len(res.Values) != len(settings.Keys) || res.Theme.Name != "default" ||
			len(res.Theme.Settings) == 0 || res.Theme.Settings[0].Key != "show_reading_time" {
			t.Errorf("GET /settings: %d %s", r.code, r.body)
		}
	})
}

func TestSettingsAuditDiff(t *testing.T) {
	env := newAdminEnv(t)
	settingsKeep(t, env)
	body := `{"head_html":"<meta name=\"audit\" content=\"1\">","body_end_html":""}`
	if r := env.do("admin", http.MethodPut, "/settings/snippets", body); r.code != http.StatusOK {
		t.Fatalf("%d %s", r.code, r.body)
	}
	var diff map[string]struct{ From, To any }
	var raw []byte
	if err := env.pool.QueryRow(context.Background(), `SELECT diff FROM audit_log WHERE action = 'settings.update'
AND entity_id = 'snippets' AND actor_user_id = $1 ORDER BY id DESC LIMIT 1`, env.ids["admin"]).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &diff); err != nil {
		t.Fatal(err)
	}
	if d, ok := diff["snippets.head_html"]; !ok || d.To != `<meta name="audit" content="1">` || len(diff) != 1 {
		t.Errorf("diff: %s", raw)
	}
}

// TestSettingsPublicEffects saves settings through the API and checks the cached public site picks them up.
func TestSettingsPublicEffects(t *testing.T) {
	env := newAdminEnv(t)
	importSample(t, env.pool)
	settingsKeep(t, env)
	ctx := context.Background()
	pub, err := site.New(ctx, site.Config{Pool: env.pool, BaseURL: testOrigin, ThemeDir: "themes/default", Logf: t.Logf,
		CacheBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	env.deps.site = pub
	get := func(target string, hdr ...string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, target, nil)
		for i := 0; i+1 < len(hdr); i += 2 {
			r.Header.Set(hdr[i], hdr[i+1])
		}
		w := httptest.NewRecorder()
		pub.ServeHTTP(w, r)
		return w
	}
	put := func(group, body string) {
		t.Helper()
		if r := env.do("admin", http.MethodPut, "/settings/"+group, body); r.code != http.StatusOK {
			t.Fatalf("PUT %s: %d %s", group, r.code, r.body)
		}
	}

	t.Run("snippet hash in CSP on miss, hit and 304", func(t *testing.T) {
		put("snippets", `{"head_html":"<script>window.afsc=1</script>","body_end_html":""}`)
		sum := sha256.Sum256([]byte("window.afsc=1"))
		hash := "'sha256-" + base64.StdEncoding.EncodeToString(sum[:]) + "'"
		miss := get("/example-post/")
		csp := miss.Header().Get("Content-Security-Policy")
		if miss.Code != 200 || miss.Header().Get("X-Cache") != "MISS" || !strings.Contains(csp, hash) ||
			!strings.Contains(miss.Body.String(), "<script>window.afsc=1</script>") {
			t.Fatalf("miss: %d %q %q", miss.Code, miss.Header().Get("X-Cache"), csp)
		}
		hit := get("/example-post/")
		notMod := get("/example-post/", "If-None-Match", miss.Header().Get("ETag"))
		if hit.Header().Get("X-Cache") != "HIT" || hit.Header().Get("Content-Security-Policy") != csp {
			t.Errorf("hit: %q %q", hit.Header().Get("X-Cache"), hit.Header().Get("Content-Security-Policy"))
		}
		if notMod.Code != http.StatusNotModified || notMod.Header().Get("Content-Security-Policy") != csp ||
			notMod.Header().Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("304: %d %v", notMod.Code, notMod.Header())
		}
	})

	t.Run("report only", func(t *testing.T) {
		put("security", `{"extra_csp_hosts":{"img-src":["https://img.example.com"]},"csp_report_only":true,"hsts":true}`)
		w := get("/example-post/")
		ro := w.Header().Get("Content-Security-Policy-Report-Only")
		if w.Header().Get("Content-Security-Policy") != "" || !strings.Contains(ro, "https://img.example.com") ||
			w.Header().Get("Strict-Transport-Security") == "" {
			t.Errorf("headers: %v", w.Header())
		}
		var d struct {
			CSPReportOnly bool `json:"csp_report_only"`
		}
		env.do("author", http.MethodGet, "/dashboard", "").json(t, &d)
		if !d.CSPReportOnly {
			t.Error("dashboard csp_report_only false")
		}
	})

	t.Run("date archives off redirects home", func(t *testing.T) {
		if w := get("/2021/"); w.Code != http.StatusOK {
			t.Fatalf("warm-up: %d", w.Code)
		}
		put("permalinks", settingsBody(t, "permalinks", func(m map[string]any) { m["date_archives_enabled"] = false }))
		w := get("/2021/")
		if loc := w.Header().Get("Location"); w.Code != http.StatusMovedPermanently || (loc != "/" && loc != testOrigin+"/") {
			t.Errorf("/2021/: %d %q", w.Code, loc)
		}
	})

	t.Run("cache ttl", func(t *testing.T) {
		put("cache", `{"page_ttl_seconds":120}`)
		if cc := get("/about/").Header().Get("Cache-Control"); cc != pagecache.CacheControl(120) {
			t.Errorf("ttl 120: %q", cc)
		}
		put("cache", `{"page_ttl_seconds":0}`)
		if w := get("/about/"); w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("X-Cache") == "HIT" {
			t.Errorf("ttl 0: %v", w.Header())
		}
	})

	t.Run("theme setting value", func(t *testing.T) {
		if !strings.Contains(get("/example-post/").Body.String(), "min read") {
			t.Fatal("reading time missing by default")
		}
		put("theme_settings", `{"default":{"show_reading_time":false}}`)
		if strings.Contains(get("/example-post/").Body.String(), "min read") {
			t.Error("theme setting not applied")
		}
	})
}

func TestDashboard(t *testing.T) {
	env := newAdminEnv(t)
	importSample(t, env.pool)
	for _, role := range []string{"admin", "contributor"} {
		r := env.do(role, http.MethodGet, "/dashboard", "")
		var d struct {
			MyDrafts      []json.RawMessage `json:"my_drafts"`
			PendingReview []json.RawMessage `json:"pending_review"`
			Scheduled     []struct {
				Status  string `json:"status"`
				CanEdit bool   `json:"can_edit"`
			} `json:"scheduled"`
			Top404s []json.RawMessage `json:"top_404s"`
			Counts  map[string]int    `json:"counts"`
		}
		r.json(t, &d)
		if r.code != http.StatusOK || d.MyDrafts == nil || d.Top404s == nil || len(d.Counts) != 6 {
			t.Errorf("%s: %d %s", role, r.code, r.body)
		}
		if role == "admin" && (d.Counts["published"] == 0 || len(d.Scheduled) == 0 || !d.Scheduled[0].CanEdit) {
			t.Errorf("admin sees every post: %s", r.body)
		}
		if role == "contributor" && (d.Counts["published"] != 0 || len(d.Scheduled) != 0) {
			t.Errorf("contributor sees others' posts: %s", r.body)
		}
	}
}
