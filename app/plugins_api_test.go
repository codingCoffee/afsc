package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"afsc/internal/authz"
	"afsc/internal/plugins"
)

// adminTestPlugin registers admin routes, so the capability test and the mount are exercised, and subscribes to
// every event, so emits land in jobs for TestPluginEvents.
type adminTestPlugin struct{}

func subscribe[D any](h *plugins.Host, e plugins.Event[D]) {
	plugins.AddAction(h, e, 0, func(context.Context, plugins.Envelope[D]) error { return nil })
}

func (adminTestPlugin) ID() string { return "testadmin" }

func (adminTestPlugin) Init(_ context.Context, h *plugins.Host) error {
	h.RegisterAdminAPI(http.MethodGet, "items/:id", authz.ManageSettings, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a, ok := plugins.AdminFrom(r.Context())
		plugins.JSON(w, http.StatusOK, map[string]any{"id": plugins.PathParam(r, "id"), "role": a.Role, "ok": ok,
			"user": a.User.String()})
	}))
	h.RegisterAdminAPI(http.MethodPost, "items", authz.ModerateComments, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plugins.Error(w, http.StatusBadRequest, "name", "required")
	}))
	subscribe(h, plugins.PostSaved)
	subscribe(h, plugins.PostPublished)
	subscribe(h, plugins.PostUnpublished)
	subscribe(h, plugins.PostDeleted)
	subscribe(h, plugins.TermSaved)
	subscribe(h, plugins.CommentCreated)
	subscribe(h, plugins.CommentStatusChanged)
	subscribe(h, plugins.MediaUploaded)
	subscribe(h, plugins.UserLogin)
	subscribe(h, plugins.SettingsChanged)
	return nil
}

func TestPluginAdminRoutes(t *testing.T) {
	env := newAdminEnv(t)
	for _, key := range []string{"GET " + apiPrefix + "/plugins/testadmin/items/:id", "POST " + apiPrefix + "/plugins/testadmin/items"} {
		if _, ok := env.h.caps[key]; !ok {
			t.Errorf("%s not registered through route()", key)
		}
	}
	r := env.do("admin", http.MethodGet, "/plugins/testadmin/items/42", "")
	var got struct {
		ID, Role, User string
		OK             bool
	}
	r.json(t, &got)
	if r.code != http.StatusOK || got.ID != "42" || got.Role != "admin" || !got.OK || got.User != env.ids["admin"].String() {
		t.Errorf("GET: %d %s", r.code, r.body)
	}
	if r := env.do("editor", http.MethodGet, "/plugins/testadmin/items/42", ""); r.code != http.StatusForbidden {
		t.Errorf("editor without settings.manage: %d", r.code)
	}
	if r := env.do("editor", http.MethodPost, "/plugins/testadmin/items", "{}"); r.code != http.StatusBadRequest ||
		!strings.Contains(string(r.body), `"field":"name"`) {
		t.Errorf("POST: %d %s", r.code, r.body)
	}

	withSettingsRestored(t, env)
	if r := env.do("admin", http.MethodPut, "/settings/plugins", `{"enabled":{"testadmin":false},"settings":{}}`); r.code != http.StatusOK {
		t.Fatalf("disable: %d %s", r.code, r.body)
	}
	if r := env.do("admin", http.MethodGet, "/plugins/testadmin/items/42", ""); r.code != http.StatusNotFound {
		t.Errorf("disabled plugin route: %d", r.code)
	}
}

func TestPluginSettingsAPI(t *testing.T) {
	env := newAdminEnv(t)
	withSettingsRestored(t, env)

	r := env.do("admin", http.MethodGet, "/settings", "")
	var res struct {
		Plugins []plugins.Listing `json:"plugins"`
	}
	r.json(t, &res)
	var progress *plugins.Listing
	for i := range res.Plugins {
		if res.Plugins[i].ID == "progress" {
			progress = &res.Plugins[i]
		}
	}
	if progress == nil || progress.Default || len(progress.Settings) != 4 {
		t.Fatalf("GET /settings plugins: %s", r.body)
	}

	for body, field := range map[string]string{
		`{"enabled":{},"settings":{"progress":{"height":42}}}`:   "settings.progress.height",
		`{"enabled":{},"settings":{"progress":{"color":"red"}}}`: "settings.progress.color",
		`{"enabled":{},"settings":{"progress":{"nope":1}}}`:      "settings.progress.nope",
		`{"enabled":{"Bad_ID":true},"settings":{}}`:              "enabled",
	} {
		r := env.do("admin", http.MethodPut, "/settings/plugins", body)
		var e struct{ Field string }
		_ = json.Unmarshal(r.body, &e)
		if r.code != http.StatusBadRequest || e.Field != field {
			t.Errorf("%s: %d %s (want field %q)", body, r.code, r.body, field)
		}
	}
	if r := env.do("editor", http.MethodPut, "/settings/plugins", `{}`); r.code != http.StatusForbidden {
		t.Errorf("editor: %d", r.code)
	}

	env.api.e.GET(plugins.PublicPrefix+"*", pluginPublic(env.deps.plugins, env.site.SecurityHeaders))
	get := func(target string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		env.api.e.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
		return rec
	}
	host, err := env.deps.plugins.Host("progress")
	if err != nil {
		t.Fatal(err)
	}
	css := host.AssetURL("progress.css")
	if rec := get(css); rec.Code != http.StatusNotFound {
		t.Errorf("asset of a disabled plugin: %d", rec.Code)
	}
	body := `{"enabled":{"progress":true},"settings":{"progress":{"height":5,"color":"#112233","placement":"auto","posts_only":true}}}`
	if r := env.do("admin", http.MethodPut, "/settings/plugins", body); r.code != http.StatusOK {
		t.Fatalf("enable: %d %s", r.code, r.body)
	}
	rec := get(css)
	if rec.Code != http.StatusOK || rec.Header().Get("Content-Security-Policy") == "" || rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("asset of an enabled plugin: %d %v", rec.Code, rec.Header())
	}
	var audit int
	if err := env.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'settings.update' AND entity_id = 'plugins'
		AND actor_user_id = $1`, env.ids["admin"]).Scan(&audit); err != nil || audit == 0 {
		t.Errorf("no audit entry: %d %v", audit, err)
	}
}

// withSettingsRestored puts the plugins group back as it was after the test.
func withSettingsRestored(t *testing.T, env *adminEnv) {
	ctx := context.Background()
	var saved []byte
	_ = env.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'plugins'`).Scan(&saved)
	t.Cleanup(func() {
		var err error
		if saved != nil {
			_, err = env.pool.Exec(ctx, settingsSQLPut, "plugins", json.RawMessage(saved))
		} else {
			_, err = env.pool.Exec(ctx, `DELETE FROM settings WHERE key = 'plugins'`)
		}
		if err != nil {
			t.Error(err)
		}
	})
}
