package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"afsc/internal/authz"
	"afsc/internal/db"
	"afsc/internal/importer"
	"afsc/internal/plugins"
	"afsc/internal/site"
	"afsc/internal/theme"
	registry "afsc/plugins"
)

const testOrigin = "https://blog.example.com"

// adminEnv is the shared harness for admin API tests: the full mountAdmin route table on a test DB, with one
// signed-in user per role. Later milestones' API tests build on it.
type adminEnv struct {
	t      *testing.T
	api    apiClient
	h      *authHandler
	pool   *pgxpool.Pool
	site   *site.Site
	deps   *adminDeps
	tokens map[string]string
	ids    map[string]uuid.UUID
	suffix string
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// importSample loads testdata/bundle-sample, for tests that need content.
func importSample(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	tm, err := theme.LoadManifest("themes/default")
	if err != nil {
		t.Fatal(err)
	}
	if rep, err := importer.Run(context.Background(), importer.Options{BundleDir: "testdata/bundle-sample", Theme: tm,
		Pool: pool, Storage: discardStorage{}}); err != nil {
		t.Fatalf("import: %v %+v", err, rep.Errors)
	}
}

func newAdminEnv(t *testing.T) *adminEnv {
	t.Helper()
	pool := testPool(t)
	ctx := context.Background()
	rt, err := plugins.Build(ctx, plugins.Config{DB: pool, BaseURL: testOrigin, Secret: []byte(strings.Repeat("k", 32)), Logf: t.Logf},
		append(registry.All(), adminTestPlugin{}))
	if err != nil {
		t.Fatal(err)
	}
	pub, err := site.New(ctx, site.Config{Pool: pool, BaseURL: testOrigin, ThemeDir: "themes/default", Logf: t.Logf,
		Plugins: rt})
	if err != nil {
		t.Fatal(err)
	}
	h := newAuthHandler(pool, authConfig{secret: []byte(strings.Repeat("k", 32)), accessTTL: time.Minute,
		refreshTTL: 7 * 24 * time.Hour, idleTTL: 12 * time.Hour, baseURL: testOrigin})
	e := echo.New()
	deps := &adminDeps{pool: pool, site: pub, storage: discardStorage{}, baseURL: testOrigin, themeDir: "themes/default",
		plugins: rt}
	mountAdmin(e, h, deps, clientIPMiddleware(nil))

	env := &adminEnv{t: t, api: apiClient{t: t, e: e, origin: testOrigin}, h: h, pool: pool, site: pub, deps: deps,
		tokens: map[string]string{}, ids: map[string]uuid.UUID{}, suffix: uuid.NewString()[:8]}
	for _, role := range []string{"admin", "editor", "author", "contributor"} {
		env.tokens[role], env.ids[role] = env.signIn(role, role)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, sql := range []string{
			`DELETE FROM audit_log WHERE actor_user_id IN (SELECT id FROM users WHERE username LIKE '%-` + env.suffix + `')`,
			`DELETE FROM users WHERE username LIKE '%-` + env.suffix + `'`,
			`DELETE FROM login_failures WHERE key LIKE '%` + env.suffix + `%'`,
			`DELETE FROM login_lockouts WHERE key LIKE '%` + env.suffix + `%'`,
			`DELETE FROM jobs WHERE kind = 'plugins.action' AND payload->>'plugin' IN ('testadmin', 'webhooks')`,
		} {
			if _, err := pool.Exec(ctx, sql); err != nil {
				t.Error(err)
			}
		}
	})
	return env
}

// signIn creates a user named name-<suffix> with role and returns an access token on a fresh session.
func (env *adminEnv) signIn(name, role string) (string, uuid.UUID) {
	env.t.Helper()
	ctx := context.Background()
	u, err := env.h.q.CreateUserWithRole(ctx, db.CreateUserWithRoleParams{Username: name + "-" + env.suffix,
		Email: name + "-" + env.suffix + "@example.com", Role: role})
	if err != nil {
		env.t.Fatal(err)
	}
	sid := uuid.New()
	_, hash, _ := newRefreshToken()
	if _, err := env.h.q.CreateRefreshToken(ctx, db.CreateRefreshTokenParams{UserID: u.ID, SessionID: sid, TokenHash: hash,
		ExpiresAt: time.Now().Add(time.Hour)}); err != nil {
		env.t.Fatal(err)
	}
	tok, err := env.h.signAccess(u.ID, role, sid)
	if err != nil {
		env.t.Fatal(err)
	}
	return tok, u.ID
}

func (env *adminEnv) do(role, method, path, body string) apiResp {
	env.t.Helper()
	ct := ""
	if body != "" {
		ct = "application/json"
	}
	return env.api.do(env.tokens[role], method, path, ct, body)
}

// samplePath fills route params with values that pass routing but name nothing.
func samplePath(p string) string {
	parts := strings.Split(p, "/")
	for i, s := range parts {
		if strings.HasPrefix(s, ":") {
			parts[i] = "999999999"
		}
		if s == "*" {
			parts[i] = "x"
		}
	}
	return strings.Join(parts, "/")
}

// TestEveryRouteHasACapability is the §19 check: every admin API route declares a capability, and each role gets
// 403 exactly where the matrix says no. Allowed roles are only exercised on GETs, so the test never mutates data.
func TestEveryRouteHasACapability(t *testing.T) {
	env := newAdminEnv(t)
	if len(env.h.caps) < 20 {
		t.Fatalf("only %d routes registered", len(env.h.caps))
	}
	for key, cp := range env.h.caps {
		method, full, _ := strings.Cut(key, " ")
		path := samplePath(strings.TrimPrefix(full, apiPrefix))
		body := ""
		if method != http.MethodGet && method != http.MethodDelete {
			body = "{}"
		}
		if method != http.MethodGet {
			r := apiClient{t: t, e: env.api.e, origin: "https://evil.example"}.do(env.tokens["admin"], method, path, "application/json", body)
			if r.code != http.StatusForbidden {
				t.Errorf("%s from a foreign origin: %d", key, r.code)
			}
		}
		if cp == "" {
			continue
		}
		if r := env.api.do("", method, path, "application/json", body); r.code != http.StatusUnauthorized {
			t.Errorf("%s without a token: %d", key, r.code)
		}
		for _, role := range []string{"admin", "editor", "author", "contributor"} {
			allowed := authz.Can(role, cp)
			if allowed && method != http.MethodGet {
				continue
			}
			r := env.api.do(env.tokens[role], method, path, "application/json", body)
			denied := r.code == http.StatusForbidden && strings.Contains(string(r.body), `"code":"capability"`)
			if denied == allowed {
				t.Errorf("%s as %s (cap %q): %d %s", key, role, cp, r.code, r.body)
			}
		}
	}
}

func TestCSRFGuard(t *testing.T) {
	env := newAdminEnv(t)
	tok := env.tokens["admin"]
	cases := []struct {
		origin, ct string
		want       int
	}{
		{"", "application/json", http.StatusForbidden},
		{"https://evil.example", "application/json", http.StatusForbidden},
		{testOrigin, "text/plain", http.StatusUnsupportedMediaType},
		{testOrigin, "application/x-www-form-urlencoded", http.StatusUnsupportedMediaType},
		{testOrigin, "application/json; charset=utf-8", http.StatusOK},
	}
	for _, c := range cases {
		r := apiClient{t: t, e: env.api.e, origin: c.origin}.do(tok, http.MethodPatch, "/me", c.ct, `{"display_name":"A"}`)
		if r.code != c.want {
			t.Errorf("origin %q, type %q: %d %s", c.origin, c.ct, r.code, r.body)
		}
	}
	// the access token is accepted only as a Bearer header
	req, _ := http.NewRequest(http.MethodGet, apiPrefix+"/me", nil)
	req.AddCookie(&http.Cookie{Name: accessCookie, Value: tok})
	w := httptest.NewRecorder()
	env.api.e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("cookie-only access token: %d", w.Code)
	}
}
