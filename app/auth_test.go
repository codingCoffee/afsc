package main

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestRealIP(t *testing.T) {
	trusted := []netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")}
	cases := []struct {
		remote, xff string
		trusted     []netip.Prefix
		want        string
	}{
		{"203.0.113.9:1", "198.51.100.1", trusted, "203.0.113.9"},                  // untrusted peer: header ignored
		{"10.0.0.2:1", "198.51.100.1", trusted, "198.51.100.1"},                    // one trusted proxy
		{"10.0.0.2:1", "6.6.6.6, 198.51.100.1, 10.0.0.3", trusted, "198.51.100.1"}, // spoofed left part ignored
		{"10.0.0.2:1", "", trusted, "10.0.0.2"},
		{"10.0.0.2:1", "198.51.100.1", nil, "10.0.0.2"}, // nobody trusted
		{"[::ffff:10.0.0.2]:1", "198.51.100.1", trusted, "198.51.100.1"},
	}
	for _, c := range cases {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = c.remote
		if c.xff != "" {
			r.Header.Set("X-Forwarded-For", c.xff)
		}
		if got := realIP(r, c.trusted); got != c.want {
			t.Errorf("%s / %q: %s, want %s", c.remote, c.xff, got, c.want)
		}
	}
}

func TestInlineScriptHashes(t *testing.T) {
	doc := `<html><head><script src="/a.js"></script><script>window.__NUXT__={}</script>
<script type="application/json" id="d">{"x":1}</script><script type="module">import "/b.js"</script></head></html>`
	got, err := inlineScriptHashes([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.HasPrefix(got[0], "sha256-") {
		t.Fatalf("%v", got)
	}
	sum := sha256.Sum256([]byte("window.__NUXT__={}"))
	if want := "sha256-" + base64.StdEncoding.EncodeToString(sum[:]); got[0] != want {
		t.Errorf("%s, want %s", got[0], want)
	}
}

func TestSessionExpiry(t *testing.T) {
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	h := newAuthHandler(nil, authConfig{refreshTTL: 7 * 24 * time.Hour, idleTTL: 12 * time.Hour})
	h.now = func() time.Time { return now }
	if got := h.sessionExpiry(now); !got.Equal(now.Add(12 * time.Hour)) {
		t.Errorf("fresh session: %v", got)
	}
	started := now.Add(-7*24*time.Hour + time.Hour)
	if got := h.sessionExpiry(started); !got.Equal(started.Add(7 * 24 * time.Hour)) {
		t.Errorf("near the absolute limit: %v", got)
	}
	if h2 := newAuthHandler(nil, authConfig{refreshTTL: time.Hour}); h2.cfg.idleTTL != time.Hour {
		t.Errorf("idle defaults to the absolute lifetime: %v", h2.cfg.idleTTL)
	}
}

func TestSetupClosedOnceUsersExist(t *testing.T) {
	env := newAdminEnv(t)
	if r := env.do("", http.MethodGet, "/setup/status", ""); r.code != http.StatusNotFound {
		t.Errorf("status: %d", r.code)
	}
	r := env.do("", http.MethodPost, "/setup", `{"token":"x","username":"root-x","email":"root@example.com","password":"p"}`)
	if r.code != http.StatusNotFound {
		t.Errorf("setup: %d %s", r.code, r.body)
	}
	if tok, err := ensureSetupToken(context.Background(), env.pool, ""); err != nil || tok != "" {
		t.Errorf("a token was issued with users present: %q %v", tok, err)
	}
	if r := env.do("", http.MethodPost, "/signup", `{}`); r.code != http.StatusNotFound && r.code != http.StatusMethodNotAllowed {
		t.Errorf("public signup still answers: %d", r.code)
	}
}

func TestInviteFlow(t *testing.T) {
	env := newAdminEnv(t)
	email := "invitee-" + env.suffix + "@example.com"
	r := env.do("admin", http.MethodPost, "/users", `{"email":"`+email+`","username":"invitee-`+env.suffix+`","role":"author"}`)
	var created struct {
		User      adminUser `json:"user"`
		InviteURL string    `json:"invite_url"`
	}
	r.json(t, &created)
	if r.code != http.StatusCreated || created.User.HasPassword || !created.User.InvitePending ||
		!strings.HasPrefix(created.InviteURL, testOrigin+"/admin/invite/") {
		t.Fatalf("%d %s", r.code, r.body)
	}
	raw := strings.TrimPrefix(created.InviteURL, testOrigin+"/admin/invite/")
	var stored []byte
	if err := env.pool.QueryRow(context.Background(), `SELECT token_hash FROM user_invites WHERE user_id = $1`,
		created.User.ID).Scan(&stored); err != nil || string(stored) == raw {
		t.Fatalf("token must be stored hashed: %v", err)
	}
	if r := env.do("", http.MethodGet, "/invites/"+raw, ""); r.code != http.StatusOK || !strings.Contains(string(r.body), email) {
		t.Fatalf("lookup: %d %s", r.code, r.body)
	}
	// a new invite voids the old link
	r = env.do("admin", http.MethodPost, "/users/"+created.User.ID.String()+"/invite", "")
	var again struct {
		InviteURL string `json:"invite_url"`
	}
	r.json(t, &again)
	if r := env.do("", http.MethodGet, "/invites/"+raw, ""); r.code != http.StatusNotFound {
		t.Errorf("old link still works: %d", r.code)
	}
	raw = strings.TrimPrefix(again.InviteURL, testOrigin+"/admin/invite/")
	if r := env.do("", http.MethodPost, "/invites/"+raw, `{"password":"Correct-horse-9"}`); r.code != http.StatusOK ||
		!strings.Contains(string(r.body), "access_token") {
		t.Fatalf("accept: %d %s", r.code, r.body)
	}
	if r := env.do("", http.MethodPost, "/invites/"+raw, `{"password":"Correct-horse-9"}`); r.code != http.StatusNotFound {
		t.Errorf("an invite is single-use: %d", r.code)
	}
	if r := env.do("", http.MethodPost, "/signin", `{"login":"`+email+`","password":"Correct-horse-9"}`); r.code != http.StatusOK {
		t.Errorf("sign in after accepting: %d %s", r.code, r.body)
	}
	// expired links are refused
	if _, err := env.pool.Exec(context.Background(), `UPDATE user_invites SET expires_at = now() - interval '1 minute', used_at = NULL
WHERE user_id = $1`, created.User.ID); err != nil {
		t.Fatal(err)
	}
	if r := env.do("", http.MethodGet, "/invites/"+raw, ""); r.code != http.StatusNotFound {
		t.Errorf("expired invite: %d", r.code)
	}
}

func TestRoleChangeAndDisableEndSessions(t *testing.T) {
	env := newAdminEnv(t)
	tok, id := env.signIn("target", "editor")
	if r := env.api.do(tok, http.MethodGet, "/me", "", ""); r.code != http.StatusOK {
		t.Fatalf("before: %d", r.code)
	}
	if r := env.do("admin", http.MethodPatch, "/users/"+id.String(), `{"role":"author"}`); r.code != http.StatusOK {
		t.Fatalf("role change: %d %s", r.code, r.body)
	}
	if r := env.api.do(tok, http.MethodGet, "/me", "", ""); r.code != http.StatusUnauthorized {
		t.Errorf("a role change must end the session: %d", r.code)
	}
	tok, id = env.signIn("target2", "author")
	if r := env.do("admin", http.MethodPost, "/users/"+id.String()+"/disable", ""); r.code != http.StatusOK {
		t.Fatalf("disable: %d %s", r.code, r.body)
	}
	if r := env.api.do(tok, http.MethodGet, "/me", "", ""); r.code != http.StatusUnauthorized {
		t.Errorf("disabling must end the session: %d", r.code)
	}
	if r := env.do("admin", http.MethodPost, "/users/"+env.ids["admin"].String()+"/disable", ""); r.code != http.StatusConflict {
		t.Errorf("self-disable: %d", r.code)
	}
	var n int
	env.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action IN ('user.role', 'user.disable')
AND entity_id IN ($1, $2)`, id.String(), id.String()).Scan(&n)
	if n == 0 {
		t.Error("no audit entries")
	}
}

func TestRevokeOtherSessions(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	other := uuid.New()
	_, hash, _ := newRefreshToken()
	if _, err := env.pool.Exec(ctx, `INSERT INTO refresh_tokens (user_id, session_id, token_hash, expires_at)
VALUES ($1, $2, $3, now() + interval '1 hour')`, env.ids["author"], other, hash); err != nil {
		t.Fatal(err)
	}
	if r := env.do("author", http.MethodPost, "/me/sessions/revoke-others", ""); r.code != http.StatusOK ||
		!strings.Contains(string(r.body), `"revoked":1`) {
		t.Fatalf("%d %s", r.code, r.body)
	}
	if r := env.do("author", http.MethodGet, "/me", ""); r.code != http.StatusOK {
		t.Errorf("the current session must survive: %d", r.code)
	}
}

func TestRefreshHonoursSessionLimits(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	raw, hash, _ := newRefreshToken()
	if _, err := env.pool.Exec(ctx, `INSERT INTO refresh_tokens (user_id, session_id, token_hash, expires_at, session_started_at)
VALUES ($1, $2, $3, now() - interval '1 second', now() - interval '13 hours')`, env.ids["author"], uuid.New(), hash); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, apiPrefix+"/token_refresh", nil)
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(&http.Cookie{Name: refreshCookie, Value: raw})
	w := httptest.NewRecorder()
	env.api.e.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Errorf("idle session refreshed: %d %s", w.Code, w.Body)
	}

	raw, hash, _ = newRefreshToken()
	started := time.Now().Add(-2 * time.Hour)
	if _, err := env.pool.Exec(ctx, `INSERT INTO refresh_tokens (user_id, session_id, token_hash, expires_at, session_started_at)
VALUES ($1, $2, $3, now() + interval '1 hour', $4)`, env.ids["author"], uuid.New(), hash, started); err != nil {
		t.Fatal(err)
	}
	req = httptest.NewRequest(http.MethodPost, apiPrefix+"/token_refresh", nil)
	req.Header.Set("Origin", testOrigin)
	req.AddCookie(&http.Cookie{Name: refreshCookie, Value: raw})
	w = httptest.NewRecorder()
	env.api.e.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh: %d %s", w.Code, w.Body)
	}
	var carried time.Time
	if err := env.pool.QueryRow(ctx, `SELECT session_started_at FROM refresh_tokens
WHERE user_id = $1 AND revoked_at IS NULL ORDER BY created_at DESC LIMIT 1`, env.ids["author"]).Scan(&carried); err != nil {
		t.Fatal(err)
	}
	if carried.Sub(started).Abs() > time.Second {
		t.Errorf("rotation must carry the session start: %v vs %v", carried, started)
	}
}

func TestSigninLockout(t *testing.T) {
	env := newAdminEnv(t)
	ctx := context.Background()
	t.Cleanup(func() {
		env.pool.Exec(context.Background(), `DELETE FROM login_failures WHERE key = 'ip:192.0.2.1'`)
		env.pool.Exec(context.Background(), `DELETE FROM login_lockouts WHERE key = 'ip:192.0.2.1'`)
	})
	login := "author-" + env.suffix
	for i := 0; i < 5; i++ {
		if r := env.do("", http.MethodPost, "/signin", `{"login":"`+login+`","password":"wrong"}`); r.code != http.StatusUnauthorized {
			t.Fatalf("failure %d: %d", i+1, r.code)
		}
	}
	r := env.do("", http.MethodPost, "/signin", `{"login":"`+login+`","password":"wrong"}`)
	if r.code != http.StatusTooManyRequests || r.hdr.Get("Retry-After") == "" {
		t.Fatalf("6th attempt: %d %v", r.code, r.hdr)
	}
	var n int
	env.pool.QueryRow(ctx, `SELECT count(*) FROM audit_log WHERE action = 'login.failure' AND entity_id = $1`,
		env.ids["author"].String()).Scan(&n)
	if n != 5 {
		t.Errorf("%d failure audit entries", n)
	}
}
