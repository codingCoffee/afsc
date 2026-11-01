package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v5"

	"afsc/internal/site"
	"afsc/internal/taxonomy"
)

// taxCachedAPI mounts the admin API on a site with a page cache, so tests can see what a change purges.
func taxCachedAPI(t *testing.T, env *adminEnv) (apiClient, *site.Site) {
	t.Helper()
	pub, err := site.New(context.Background(), site.Config{Pool: env.pool, BaseURL: testOrigin, ThemeDir: "themes/default",
		Logf: t.Logf, CacheBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	h := newAuthHandler(env.pool, authConfig{secret: []byte(strings.Repeat("k", 32)), accessTTL: time.Minute,
		refreshTTL: 7 * 24 * time.Hour, idleTTL: 12 * time.Hour, baseURL: testOrigin})
	e := echo.New()
	mountAdmin(e, h, &adminDeps{pool: env.pool, site: pub, storage: discardStorage{}, baseURL: testOrigin, themeDir: "themes/default"},
		clientIPMiddleware(nil))
	return apiClient{t: t, e: e, origin: testOrigin}, pub
}

func taxCache(pub *site.Site, path string) string {
	w := httptest.NewRecorder()
	pub.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w.Header().Get("X-Cache")
}

// taxWarm caches path and checks the next request hits.
func taxWarm(t *testing.T, pub *site.Site, path string) {
	t.Helper()
	taxCache(pub, path)
	if s := taxCache(pub, path); s != "HIT" {
		t.Fatalf("%s not cached: %q", path, s)
	}
}

func taxCleanup(t *testing.T, env *adminEnv, table string, ids *[]int64) {
	t.Cleanup(func() {
		if _, err := env.pool.Exec(context.Background(), `DELETE FROM `+table+` WHERE id = ANY($1::bigint[])`, *ids); err != nil {
			t.Error(err)
		}
	})
}

func taxPath(u *string) string { return strings.TrimPrefix(*u, testOrigin) }

func taxFirstPost(t *testing.T, env *adminEnv) int64 {
	t.Helper()
	var id int64
	if err := env.pool.QueryRow(context.Background(),
		`SELECT id FROM posts WHERE type = 'post' AND status = 'published' AND published_at <= now() ORDER BY id LIMIT 1`).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestTaxTermRules(t *testing.T) {
	env := newAdminEnv(t)
	ids := &[]int64{}
	taxCleanup(t, env, "terms", ids)
	mk := func(role, body string, want int) taxonomy.Term {
		t.Helper()
		r := env.do(role, http.MethodPost, "/terms", body)
		if r.code != want {
			t.Fatalf("create %s as %s: %d %s", body, role, r.code, r.body)
		}
		var term taxonomy.Term
		if want == http.StatusCreated {
			r.json(t, &term)
			*ids = append(*ids, term.ID)
		}
		return term
	}
	a := mk("editor", `{"taxonomy":"category","name":"Parent `+env.suffix+`","slug":"","description":"","parent_id":null}`, http.StatusCreated)
	b := mk("editor", fmt.Sprintf(`{"taxonomy":"category","name":"Child","slug":"","description":"","parent_id":%d}`, a.ID), http.StatusCreated)
	if a.Slug != "parent-"+env.suffix || !strings.HasSuffix(*b.URL, "/parent-"+env.suffix+"/child/") {
		t.Errorf("slugs: %q %q", a.Slug, *b.URL)
	}
	// the same name under the same parent gets a numbered slug
	b2 := mk("editor", fmt.Sprintf(`{"taxonomy":"category","name":"Child","slug":"","description":"","parent_id":%d}`, a.ID), http.StatusCreated)
	if b2.Slug != "child-2" {
		t.Errorf("second child slug %q", b2.Slug)
	}
	mk("editor", fmt.Sprintf(`{"taxonomy":"category","name":"Child","slug":"child","description":"","parent_id":%d}`, a.ID), http.StatusConflict)

	for _, parent := range []int64{a.ID, b.ID} {
		body := fmt.Sprintf(`{"taxonomy":"category","name":"Parent","slug":"%s","description":"","parent_id":%d}`, a.Slug, parent)
		if r := env.do("editor", http.MethodPut, fmt.Sprintf("/terms/%d", a.ID), body); r.code != http.StatusBadRequest ||
			!strings.Contains(string(r.body), "parent_id") {
			t.Errorf("parent %d: %d %s", parent, r.code, r.body)
		}
	}
	mk("author", `{"taxonomy":"category","name":"Nope","slug":"","description":"","parent_id":null}`, http.StatusForbidden)
	mk("contributor", `{"taxonomy":"tag","name":"Tag `+env.suffix+`","slug":"","description":"","parent_id":null}`, http.StatusCreated)
	mk("editor", fmt.Sprintf(`{"taxonomy":"tag","name":"T","slug":"","description":"","parent_id":%d}`, a.ID), http.StatusBadRequest)
}

func TestTaxMergeTerm(t *testing.T) {
	env := newAdminEnv(t)
	importSample(t, env.pool)
	api, pub := taxCachedAPI(t, env)
	ctx := context.Background()
	ids := &[]int64{}
	taxCleanup(t, env, "terms", ids)
	tag := func(name string) taxonomy.Term {
		r := api.do(env.tokens["editor"], http.MethodPost, "/terms", "application/json",
			`{"taxonomy":"tag","name":"`+name+`","slug":"","description":"","parent_id":null}`)
		if r.code != http.StatusCreated {
			t.Fatalf("create tag: %d %s", r.code, r.body)
		}
		var term taxonomy.Term
		r.json(t, &term)
		*ids = append(*ids, term.ID)
		return term
	}
	src, dst := tag("Merge from "+env.suffix), tag("Merge into "+env.suffix)
	t.Cleanup(func() {
		_, _ = env.pool.Exec(ctx, `DELETE FROM redirects WHERE source = $1`, taxPath(src.URL))
	})
	postID := taxFirstPost(t, env)
	if _, err := env.pool.Exec(ctx, `INSERT INTO post_terms (post_id, term_id) VALUES ($1, $2)`, postID, src.ID); err != nil {
		t.Fatal(err)
	}
	urls, err := pub.PostURLs(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	postPath := urls[fmt.Sprintf("post:%d", postID)]
	taxWarm(t, pub, postPath)

	r := api.do(env.tokens["editor"], http.MethodPost, fmt.Sprintf("/terms/%d/merge", src.ID), "application/json",
		fmt.Sprintf(`{"into":%d}`, dst.ID))
	if r.code != http.StatusOK {
		t.Fatalf("merge: %d %s", r.code, r.body)
	}
	var terms []int64
	rows, _ := env.pool.Query(ctx, `SELECT term_id FROM post_terms WHERE post_id = $1 AND term_id = ANY($2::bigint[])`, postID,
		[]int64{src.ID, dst.ID})
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		terms = append(terms, id)
	}
	if len(terms) != 1 || terms[0] != dst.ID {
		t.Errorf("post terms after merge: %v", terms)
	}
	if r := env.do("editor", http.MethodGet, fmt.Sprintf("/terms/%d", src.ID), ""); r.code != http.StatusNotFound {
		t.Errorf("source still exists: %d", r.code)
	}
	var target string
	if err := env.pool.QueryRow(ctx, `SELECT target FROM redirects WHERE source = $1 AND origin = 'slug_change'`,
		taxPath(src.URL)).Scan(&target); err != nil || target != taxPath(dst.URL) {
		t.Errorf("redirect %s → %q (%v), want %s", taxPath(src.URL), target, err, taxPath(dst.URL))
	}
	if s := taxCache(pub, postPath); s != "MISS" {
		t.Errorf("post page not purged: %q", s)
	}
	if r := api.do(env.tokens["editor"], http.MethodPost, fmt.Sprintf("/terms/%d/merge", dst.ID), "application/json",
		fmt.Sprintf(`{"into":%d}`, dst.ID)); r.code != http.StatusBadRequest {
		t.Errorf("merge into itself: %d", r.code)
	}
}

func TestTaxAuthors(t *testing.T) {
	env := newAdminEnv(t)
	importSample(t, env.pool)
	ctx := context.Background()
	ids := &[]int64{}
	taxCleanup(t, env, "authors", ids)
	var redirectSources []string
	t.Cleanup(func() {
		_, _ = env.pool.Exec(ctx, `DELETE FROM redirects WHERE source = ANY($1::text[])`, redirectSources)
	})
	mk := func(role, body string, want int) taxonomy.Author {
		t.Helper()
		r := env.do(role, http.MethodPost, "/authors", body)
		if r.code != want {
			t.Fatalf("create author as %s: %d %s", role, r.code, r.body)
		}
		var a taxonomy.Author
		if want == http.StatusCreated {
			r.json(t, &a)
			*ids = append(*ids, a.ID)
		}
		return a
	}
	body := func(name, user string) string {
		return `{"slug":"","display_name":"` + name + `","bio":"","website":"https://example.com","social":{"x":"https://x.com/example"},` +
			`"avatar_media_id":null,"user_id":` + user + `}`
	}
	user := `"` + env.ids["author"].String() + `"`
	a := mk("admin", body("Author A "+env.suffix, user), http.StatusCreated)
	if a.UserID == nil || *a.UserID != env.ids["author"].String() || a.URL == nil {
		t.Fatalf("linked author: %+v", a)
	}
	mk("admin", body("Author B "+env.suffix, user), http.StatusConflict)
	// without users.manage the link is ignored rather than refused
	b := mk("editor", body("Author B "+env.suffix, `"`+env.ids["editor"].String()+`"`), http.StatusCreated)
	if b.UserID != nil {
		t.Errorf("editor linked a user: %v", *b.UserID)
	}
	mk("editor", `{"slug":"","display_name":"","bio":"","website":"","social":{},"avatar_media_id":null,"user_id":null}`, http.StatusBadRequest)
	mk("editor", `{"slug":"","display_name":"C","bio":"","website":"javascript:alert(1)","social":{},"avatar_media_id":null,"user_id":null}`, http.StatusBadRequest)

	oldPath := taxPath(a.URL)
	redirectSources = append(redirectSources, oldPath)
	r := env.do("admin", http.MethodPut, fmt.Sprintf("/authors/%d", a.ID),
		`{"slug":"renamed-`+env.suffix+`","display_name":"Author A","bio":"","website":"","social":{},"avatar_media_id":null,"user_id":`+user+`}`)
	if r.code != http.StatusOK {
		t.Fatalf("rename: %d %s", r.code, r.body)
	}
	r.json(t, &a)
	var target string
	if err := env.pool.QueryRow(ctx, `SELECT target FROM redirects WHERE source = $1`, oldPath).Scan(&target); err != nil ||
		target != taxPath(a.URL) {
		t.Errorf("author redirect %s → %q (%v), want %s", oldPath, target, err, taxPath(a.URL))
	}

	postID := taxFirstPost(t, env)
	if _, err := env.pool.Exec(ctx, `INSERT INTO post_authors (post_id, author_id, position) VALUES ($1, $2, 9)`, postID, a.ID); err != nil {
		t.Fatal(err)
	}
	if r := env.do("editor", http.MethodDelete, fmt.Sprintf("/authors/%d", a.ID), ""); r.code != http.StatusConflict ||
		!strings.Contains(string(r.body), "has_posts") {
		t.Errorf("delete with posts: %d %s", r.code, r.body)
	}
	redirectSources = append(redirectSources, taxPath(a.URL))
	if r := env.do("editor", http.MethodDelete, fmt.Sprintf("/authors/%d?reassign=%d", a.ID, b.ID), ""); r.code != http.StatusNoContent {
		t.Fatalf("delete with reassign: %d %s", r.code, r.body)
	}
	var n int
	if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM post_authors WHERE post_id = $1 AND author_id = $2`, postID, b.ID).Scan(&n); err != nil || n != 1 {
		t.Errorf("post not reassigned: %d %v", n, err)
	}
}

func TestTaxMenus(t *testing.T) {
	env := newAdminEnv(t)
	importSample(t, env.pool)
	api, pub := taxCachedAPI(t, env)
	ctx := context.Background()
	ids := &[]int64{}
	taxCleanup(t, env, "menus", ids)
	var prev *int64
	_ = env.pool.QueryRow(ctx, `SELECT menu_id FROM menu_locations WHERE location = 'footer'`).Scan(&prev)
	t.Cleanup(func() {
		if prev != nil {
			_, _ = env.pool.Exec(ctx, `INSERT INTO menu_locations (location, menu_id) VALUES ('footer', $1)
ON CONFLICT (location) DO UPDATE SET menu_id = excluded.menu_id`, *prev)
		}
	})

	do := func(method, path, body string) apiResp {
		return api.do(env.tokens["editor"], method, path, "application/json", body)
	}
	r := do(http.MethodPost, "/menus", `{"name":"Test menu `+env.suffix+`","slug":""}`)
	if r.code != http.StatusCreated {
		t.Fatalf("create menu: %d %s", r.code, r.body)
	}
	var m taxonomy.Menu
	r.json(t, &m)
	*ids = append(*ids, m.ID)
	path := fmt.Sprintf("/menus/%d", m.ID)

	item := func(kind, target, url, children string) string {
		return `{"label":"L","kind":"` + kind + `","target_id":` + target + `,"url":` + url +
			`,"new_tab":false,"css_classes":["menu-x"],"children":[` + children + `]}`
	}
	deep := item("url", "null", `"/a"`, "")
	for range 5 {
		deep = item("url", "null", `"/a"`, deep)
	}
	for name, items := range map[string]string{
		"missing post":    item("post", "999999999", "null", ""),
		"missing target":  item("term", "null", "null", ""),
		"script url":      item("url", "null", `"javascript:alert(1)"`, ""),
		"protocol-rel":    item("url", "null", `"//evil.example/"`, ""),
		"too deep":        deep,
		"bad kind":        item("page", "1", "null", ""),
		"bad class":       `{"label":"L","kind":"url","target_id":null,"url":"/","new_tab":false,"css_classes":["a b"],"children":[]}`,
		"blank label":     `{"label":" ","kind":"url","target_id":null,"url":"/","new_tab":false,"css_classes":[],"children":[]}`,
		"unknown field":   `{"label":"L","kind":"url","url":"/","target_id":null,"new_tab":false,"css_classes":[],"children":[],"x":1}`,
		"child bad url":   item("url", "null", `"/ok"`, item("url", "null", `"ftp://x.example/"`, "")),
		"author missing":  item("author", "999999999", "null", ""),
		"term id missing": item("term", "999999999", "null", ""),
	} {
		if r := do(http.MethodPut, path, `{"name":"Test menu","slug":"","items":[`+items+`]}`); r.code != http.StatusBadRequest {
			t.Errorf("%s: %d %s", name, r.code, r.body)
		}
	}

	postID := taxFirstPost(t, env)
	urls, err := pub.PostURLs(ctx, postID)
	if err != nil {
		t.Fatal(err)
	}
	postPath := urls[fmt.Sprintf("post:%d", postID)]
	taxWarm(t, pub, postPath)
	r = do(http.MethodPut, path, `{"name":"Test menu","slug":"","items":[`+
		item("post", fmt.Sprint(postID), "null", item("url", "null", `"https://example.com/x"`, ""))+`]}`)
	if r.code != http.StatusOK {
		t.Fatalf("save menu: %d %s", r.code, r.body)
	}
	r.json(t, &m)
	if len(m.Items) != 1 || m.Items[0].ResolvedURL == nil || *m.Items[0].ResolvedURL != testOrigin+postPath ||
		len(m.Items[0].Children) != 1 || *m.Items[0].Children[0].ResolvedURL != "https://example.com/x" {
		t.Errorf("saved tree: %s", r.body)
	}
	if s := taxCache(pub, postPath); s != "MISS" {
		t.Errorf("menu save didn't purge: %q", s)
	}

	if r := do(http.MethodPut, "/menu-locations", `{"locations":{"sidebar":null}}`); r.code != http.StatusBadRequest {
		t.Errorf("unknown location: %d", r.code)
	}
	r = do(http.MethodPut, "/menu-locations", fmt.Sprintf(`{"locations":{"footer":%d}}`, m.ID))
	if r.code != http.StatusOK || !strings.Contains(string(r.body), fmt.Sprintf(`"location":"footer","label":"Footer","menu_id":%d`, m.ID)) {
		t.Errorf("assign location: %d %s", r.code, r.body)
	}
}
