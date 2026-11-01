package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"afsc/internal/comments"
	"afsc/internal/site"
	"afsc/internal/spam"
)

const webDomain = "m4web.example"

type publicEnv struct {
	t       *testing.T
	e       *echo.Echo
	pub     *site.Site
	svc     *comments.Service
	postID  int64
	postURL string // absolute
	path    string
	ips     int
}

func newPublicEnv(t *testing.T) *publicEnv {
	t.Helper()
	pool := testPool(t)
	importSample(t, pool)
	ctx, cancel := context.WithCancel(context.Background())
	signer, _ := spam.NewSigner([]byte(strings.Repeat("k", 32)))
	pub, err := site.New(ctx, site.Config{Pool: pool, BaseURL: testOrigin, ThemeDir: "themes/default", Logf: t.Logf,
		CacheBytes: 8 << 20, CommentSigner: signer})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { pub.Run(ctx); close(done) }()
	t.Cleanup(func() {
		cancel()
		<-done
		if _, err := pool.Exec(context.Background(), `DELETE FROM comments WHERE author_email LIKE '%@' || $1`, webDomain); err != nil {
			t.Error(err)
		}
		_, _ = pool.Exec(context.Background(), `UPDATE posts SET comment_status = 'open' WHERE legacy_key = 'wp:post:123'`)
	})
	svc := comments.New(pool, pub, signer, t.Logf)
	e := echo.New()
	public := echo.WrapHandler(pub)
	e.GET("/*", public)
	e.POST(site.CommentsPath, postComment(svc, pub, testOrigin), clientIPMiddleware(nil))

	env := &publicEnv{t: t, e: e, pub: pub, svc: svc}
	if err := pool.QueryRow(ctx, `SELECT id FROM posts WHERE legacy_key = 'wp:post:123'`).Scan(&env.postID); err != nil {
		t.Fatal(err)
	}
	loc, err := pub.CommentLocation(ctx, env.postID, 0, "pending")
	if err != nil {
		t.Fatal(err)
	}
	env.postURL = strings.TrimSuffix(loc, "#comment-0")
	env.path = strings.TrimPrefix(env.postURL, testOrigin)
	return env
}

func (env *publicEnv) get(path string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	w := httptest.NewRecorder()
	env.e.ServeHTTP(w, req)
	return w
}

func (env *publicEnv) post(form url.Values) *httptest.ResponseRecorder {
	env.ips++
	req := httptest.NewRequest(http.MethodPost, site.CommentsPath, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.RemoteAddr = fmt.Sprintf("198.18.7.%d:4000", env.ips)
	w := httptest.NewRecorder()
	env.e.ServeHTTP(w, req)
	return w
}

// form is a valid submission with a token issued a minute ago, so it passes the 3 s check.
func (env *publicEnv) form(body string) url.Values {
	return url.Values{
		"post_id": {fmt.Sprint(env.postID)}, "parent_id": {"0"},
		"token":        {env.pub.CommentSigner().FormToken(env.postID, time.Now().Add(-time.Minute))},
		"author_name":  {"Web Tester"},
		"author_email": {fmt.Sprintf("w%d@%s", time.Now().UnixNano(), webDomain)},
		"body":         {body},
	}
}

func (env *publicEnv) waitFor(what string, ok func() bool) {
	env.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			env.t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

var tokenRe = regexp.MustCompile(`name="token" value="([^"]+)"`)

func TestPublicCommentFlow(t *testing.T) {
	env := newPublicEnv(t)

	first := env.get(env.path)
	if first.Code != http.StatusOK {
		t.Fatalf("GET post: %d", first.Code)
	}
	html := first.Body.String()
	for _, want := range []string{`id="comments"`, "Nice post", `action="` + testOrigin + `/comments"`, `name="website_url"`,
		`id="comment-999"`, `rel="nofollow ugc noopener"`} {
		if !strings.Contains(html, want) {
			t.Errorf("post page lacks %q", want)
		}
	}
	for _, leak := range []string{"commenter@example.com", "Buy now", "Welcome."} {
		if strings.Contains(html, leak) {
			t.Errorf("post page shows %q (email, spam or pending)", leak)
		}
	}
	m := tokenRe.FindStringSubmatch(html)
	if m == nil {
		t.Fatal("no form token")
	}
	if id, _, ok := env.pub.CommentSigner().ParseFormToken(m[1]); !ok || id != env.postID {
		t.Fatalf("rendered token: id %d ok %v", id, ok)
	}
	env.waitFor("cache hit", func() bool { return env.get(env.path).Header().Get("X-Cache") == "HIT" })

	body := fmt.Sprintf("Pending web comment %d", time.Now().UnixNano())
	w := env.post(env.form(body))
	if w.Code != http.StatusSeeOther {
		t.Fatalf("POST: %d %s", w.Code, w.Body)
	}
	loc := w.Header().Get("Location")
	if !strings.HasPrefix(loc, env.postURL+"#comment-") {
		t.Fatalf("Location %q", loc)
	}
	var notice *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == site.NoticeCookie {
			notice = c
		}
	}
	if notice == nil || !notice.HttpOnly || notice.Path != "/" {
		t.Fatalf("notice cookie: %+v", notice)
	}

	mine := env.get(env.path, notice)
	if mine.Header().Get("X-Cache") != "" || !strings.Contains(mine.Header().Get("Cache-Control"), "no-store") {
		t.Errorf("cookie request: X-Cache %q Cache-Control %q", mine.Header().Get("X-Cache"), mine.Header().Get("Cache-Control"))
	}
	if s := mine.Body.String(); !strings.Contains(s, body) || !strings.Contains(s, "awaiting moderation") {
		t.Errorf("commenter doesn't see their pending comment")
	}
	if anon := env.get(env.path); anon.Header().Get("X-Cache") != "HIT" || strings.Contains(anon.Body.String(), body) {
		t.Errorf("anonymous view: X-Cache %q, shows pending %v", anon.Header().Get("X-Cache"), strings.Contains(anon.Body.String(), body))
	}

	id := mustID(t, loc)
	if _, err := env.svc.SetStatus(context.Background(), []int64{id}, "approved"); err != nil {
		t.Fatal(err)
	}
	env.waitFor("approval purge", func() bool { return strings.Contains(env.get(env.path).Body.String(), body) })

	t.Run("honeypot answers like pending", func(t *testing.T) {
		f := env.form("Bot body")
		f.Set(site.HoneypotField, "http://bot.example")
		if w := env.post(f); w.Code != http.StatusSeeOther || w.Header().Get("Set-Cookie") == "" {
			t.Fatalf("honeypot: %d", w.Code)
		}
	})
	t.Run("errors", func(t *testing.T) {
		f := env.form("Bad token body")
		f.Set("token", "nope")
		if w := env.post(f); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "expired") {
			t.Errorf("bad token: %d", w.Code)
		}
		f = env.form("x")
		if w := env.post(f); w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "2 to 5000") {
			t.Errorf("short body: %d %s", w.Code, w.Body)
		}
		f = env.form(strings.Repeat("a", 40<<10))
		if w := env.post(f); w.Code != http.StatusRequestEntityTooLarge {
			t.Errorf("oversized: %d", w.Code)
		}
		if _, err := testPool(t).Exec(context.Background(), `UPDATE posts SET comment_status = 'closed' WHERE id = $1`, env.postID); err != nil {
			t.Fatal(err)
		}
		if w := env.post(env.form("Closed body")); w.Code != http.StatusForbidden {
			t.Errorf("closed: %d", w.Code)
		}
		_, _ = testPool(t).Exec(context.Background(), `UPDATE posts SET comment_status = 'open' WHERE id = $1`, env.postID)
	})
	t.Run("escaping", func(t *testing.T) {
		evil := `<script>alert(1)</script> see https://x.example/a"onmouseover="b`
		w := env.post(env.form(evil))
		if w.Code != http.StatusSeeOther {
			t.Fatalf("POST: %d", w.Code)
		}
		if _, err := env.svc.SetStatus(context.Background(), []int64{mustID(t, w.Header().Get("Location"))}, "approved"); err != nil {
			t.Fatal(err)
		}
		var page string
		env.waitFor("approved xss comment", func() bool {
			page = env.get(env.path).Body.String()
			return strings.Contains(page, "&lt;script&gt;alert(1)&lt;/script&gt;")
		})
		if strings.Contains(page, "<script>alert(1)") || strings.Contains(page, `"onmouseover="`) {
			t.Error("comment markup was not escaped")
		}
		if !strings.Contains(page, `<a href="https://x.example/a" rel="nofollow ugc noopener">`) {
			t.Error("URL not autolinked with nofollow ugc noopener")
		}
	})
	t.Run("replytocom", func(t *testing.T) {
		r := env.get(env.path + "?replytocom=" + fmt.Sprint(sampleCommentID(t)))
		if r.Header().Get("X-Cache") != "" || !strings.Contains(r.Body.String(), "Reply to Commenter A") {
			t.Errorf("replytocom: X-Cache %q", r.Header().Get("X-Cache"))
		}
		if !strings.Contains(r.Body.String(), fmt.Sprintf(`name="parent_id" value="%d"`, sampleCommentID(t))) {
			t.Error("reply form lacks parent_id")
		}
	})
	t.Run("comment-page redirect", func(t *testing.T) {
		r := env.get(env.path + "comment-page-2/")
		if r.Code != http.StatusMovedPermanently || r.Header().Get("Location") != env.path+"?cpage=2" {
			t.Errorf("comment-page: %d %q", r.Code, r.Header().Get("Location"))
		}
	})
}

func mustID(t *testing.T, loc string) int64 {
	t.Helper()
	var id int64
	if _, err := fmt.Sscanf(loc[strings.LastIndex(loc, "#comment-"):], "#comment-%d", &id); err != nil {
		t.Fatalf("no comment id in %q", loc)
	}
	return id
}

func sampleCommentID(t *testing.T) int64 {
	t.Helper()
	var id int64
	if err := testPool(t).QueryRow(context.Background(), `SELECT id FROM comments WHERE legacy_key = 'wp:comment:999'`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCommentThreadDepth(t *testing.T) {
	env := newPublicEnv(t)
	pool := testPool(t)
	parent := sampleCommentID(t)
	// wp:comment:999 → 1000 are depth 1–2; add three more levels under 1000
	if err := pool.QueryRow(context.Background(), `SELECT id FROM comments WHERE legacy_key = 'wp:comment:1000'`).Scan(&parent); err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if err := pool.QueryRow(context.Background(), `
INSERT INTO comments (post_id, parent_id, author_name, author_email, body, status)
VALUES ($1, $2, 'Deep', $3, $4, 'approved') RETURNING id`, env.postID, parent, fmt.Sprintf("deep%d@%s", i, webDomain),
			fmt.Sprintf("Deep reply %d", i)).Scan(&parent); err != nil {
			t.Fatal(err)
		}
	}
	page := env.get(env.path + "?replytocom=1").Body.String() // bypasses the cache
	if strings.Contains(page, "depth-4") || strings.Count(page, "depth-3") != 3 {
		t.Errorf("want three depth-3 comments and no depth-4: %d", strings.Count(page, "depth-3"))
	}
	if strings.Contains(page, fmt.Sprintf("replytocom=%d#", parent)) {
		t.Error("deepest comment has a reply link")
	}
}
