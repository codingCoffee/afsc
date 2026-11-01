package main

import (
	"context"
	"encoding/json"
	"html"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/seo"
	"afsc/internal/site"
)

// seoEnv imports the sample bundle and swaps in a site loaded after the import, cached when cache is set.
func seoEnv(t *testing.T, cache bool) *adminEnv {
	t.Helper()
	env := newAdminEnv(t)
	importSample(t, env.pool)
	cfg := site.Config{Pool: env.pool, BaseURL: testOrigin, ThemeDir: "themes/default", Logf: t.Logf}
	if cache {
		cfg.CacheBytes = 8 << 20
	}
	pub, err := site.New(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	env.deps.site = pub
	return env
}

func seoPostID(t *testing.T, pool *pgxpool.Pool, legacyKey string) int64 {
	t.Helper()
	var id int64
	if err := pool.QueryRow(context.Background(), `SELECT id FROM posts WHERE legacy_key = $1`, legacyKey).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

// seoKeepFields restores an entity's seo row when the test ends.
func seoKeepFields(t *testing.T, pool *pgxpool.Pool, typ string, id int64) {
	t.Helper()
	ctx := context.Background()
	before, err := seo.LoadFields(ctx, pool, typ, id)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := seo.SaveFields(ctx, pool, typ, id, before); err != nil {
			t.Error(err)
		}
	})
}

func seoGetPage(st *site.Site, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	st.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

var (
	seoTitleRe     = regexp.MustCompile(`<title>([^<]*)</title>`)
	seoMetaRe      = regexp.MustCompile(`<meta (?:name|property)="([^"]*)" content="([^"]*)">`)
	seoCanonicalRe = regexp.MustCompile(`<link rel="canonical" href="([^"]*)">`)
)

// seoHeadValues reads the values {{head}} printed into a rendered page.
func seoHeadValues(body string) map[string]string {
	out := map[string]string{}
	if m := seoTitleRe.FindStringSubmatch(body); m != nil {
		out["title"] = html.UnescapeString(m[1])
	}
	if m := seoCanonicalRe.FindStringSubmatch(body); m != nil {
		out["canonical"] = html.UnescapeString(m[1])
	}
	for _, m := range seoMetaRe.FindAllStringSubmatch(body, -1) {
		if k := html.UnescapeString(m[1]); out[k] == "" {
			out[k] = html.UnescapeString(m[2])
		}
	}
	return out
}

type seoResolvedJSON struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Canonical   string `json:"canonical"`
	Robots      string `json:"robots"`
	OG          struct {
		Title       string  `json:"title"`
		Description string  `json:"description"`
		Image       *string `json:"image"`
		Type        string  `json:"type"`
	} `json:"og"`
	URL    string      `json:"url"`
	Checks []seo.Check `json:"checks"`
	Sugg   []struct {
		ID  int64  `json:"id"`
		URL string `json:"url"`
	} `json:"suggestions"`
}

func (r seoResolvedJSON) check(id string) seo.Check {
	for _, c := range r.Checks {
		if c.ID == id {
			return c
		}
	}
	return seo.Check{}
}

func seoResolve(t *testing.T, env *adminEnv, role string, body any) seoResolvedJSON {
	t.Helper()
	b, _ := json.Marshal(body)
	r := env.do(role, http.MethodPost, "/seo/resolve", string(b))
	if r.code != http.StatusOK {
		t.Fatalf("resolve: %d %s", r.code, r.body)
	}
	var out seoResolvedJSON
	r.json(t, &out)
	return out
}

func TestSEOResolveMatchesHead(t *testing.T) {
	env := seoEnv(t, false)
	for _, key := range []string{"wp:post:123", "wp:post:2"} {
		id := seoPostID(t, env.pool, key)
		r := env.do("admin", http.MethodGet, "/seo/post/"+strconv.FormatInt(id, 10), "")
		if r.code != http.StatusOK {
			t.Fatalf("%s: GET seo %d %s", key, r.code, r.body)
		}
		var fields json.RawMessage = r.body
		res := seoResolve(t, env, "admin", map[string]any{"entity_type": "post", "entity_id": id, "seo": fields})

		page := seoGetPage(env.deps.site, strings.TrimPrefix(res.URL, testOrigin))
		if page.Code != http.StatusOK {
			t.Fatalf("%s: GET %s: %d", key, res.URL, page.Code)
		}
		head := seoHeadValues(page.Body.String())
		image := ""
		if res.OG.Image != nil {
			image = *res.OG.Image
		}
		for name, got := range map[string]string{"title": res.Title, "description": res.Description, "canonical": res.Canonical,
			"robots": res.Robots, "og:title": res.OG.Title, "og:description": res.OG.Description, "og:image": image,
			"og:type": res.OG.Type} {
			if head[name] != got {
				t.Errorf("%s: %s resolved %q, page has %q", key, name, got, head[name])
			}
		}
		if head["title"] == "" || res.Canonical != res.URL {
			t.Errorf("%s: title %q canonical %q url %q", key, head["title"], res.Canonical, res.URL)
		}
	}
}

func TestSEODuplicatesAndKeyphrase(t *testing.T) {
	env := seoEnv(t, false)
	a, b := seoPostID(t, env.pool, "wp:post:123"), seoPostID(t, env.pool, "wp:post:124")
	seoKeepFields(t, env.pool, "post", b)
	put := `{"title":"Shared SEO title ` + env.suffix + `","description":"Shared description ` + env.suffix + `",
		"canonical":null,"robots_noindex":null,"robots_nofollow":null,"robots_advanced":null,"og_title":null,
		"og_description":null,"og_image_media_id":null,"twitter_title":null,"twitter_description":null,
		"twitter_image_media_id":null,"focus_keyphrase":"Index funds ` + env.suffix + `","breadcrumb_title":null,
		"is_cornerstone":false}`
	if r := env.do("editor", http.MethodPut, "/seo/post/"+strconv.FormatInt(b, 10), put); r.code != http.StatusOK {
		t.Fatalf("PUT: %d %s", r.code, r.body)
	}
	var bTitle string
	if err := env.pool.QueryRow(context.Background(), `SELECT title FROM posts WHERE id = $1`, b).Scan(&bTitle); err != nil {
		t.Fatal(err)
	}

	dup := map[string]any{"title": "shared seo title " + env.suffix, "description": "Shared description " + env.suffix,
		"focus_keyphrase": "INDEX FUNDS " + env.suffix, "is_cornerstone": false}
	res := seoResolve(t, env, "admin", map[string]any{"entity_type": "post", "entity_id": a, "seo": dup})
	for _, id := range []string{"duplicate_title", "duplicate_description", "keyphrase_used"} {
		if c := res.check(id); c.Status != seo.Warn || !strings.Contains(c.Message, bTitle) {
			t.Errorf("%s: %+v", id, c)
		}
	}

	unique := map[string]any{"title": "Unique " + env.suffix, "focus_keyphrase": "unique " + env.suffix, "is_cornerstone": false}
	res = seoResolve(t, env, "admin", map[string]any{"entity_type": "post", "entity_id": a, "seo": unique})
	for _, id := range []string{"duplicate_title", "keyphrase_used"} {
		if c := res.check(id); c.Status != seo.OK {
			t.Errorf("unique %s: %+v", id, c)
		}
	}
	if c := res.check("duplicate_description"); c.ID != "" {
		t.Errorf("no description override, yet %+v", c)
	}
	// b itself never counts as its own duplicate
	res = seoResolve(t, env, "admin", map[string]any{"entity_type": "post", "entity_id": b, "seo": dup})
	if c := res.check("keyphrase_used"); c.Status != seo.OK {
		t.Errorf("self: %+v", c)
	}
}

func TestSEOLinkSuggestions(t *testing.T) {
	env := seoEnv(t, false)
	ctx := context.Background()
	a := seoPostID(t, env.pool, "wp:post:123")
	var titles string
	if err := env.pool.QueryRow(ctx, `SELECT string_agg(title, ' ') FROM posts`).Scan(&titles); err != nil {
		t.Fatal(err)
	}
	draft := map[string]any{"type": "post", "title": titles, "slug": "", "excerpt": "", "content": nil,
		"featured_media_id": nil, "category_ids": []int64{}, "primary_category_id": nil, "published_at": nil, "parent_id": nil}
	res := seoResolve(t, env, "admin", map[string]any{"entity_type": "post", "entity_id": a, "post": draft,
		"seo": map[string]any{"is_cornerstone": false}})
	if len(res.Sugg) == 0 || len(res.Sugg) > 10 {
		t.Fatalf("suggestions: %+v", res.Sugg)
	}
	for _, s := range res.Sugg {
		var visible bool
		if err := env.pool.QueryRow(ctx, `SELECT status = 'published' AND published_at <= now() FROM posts WHERE id = $1`,
			s.ID).Scan(&visible); err != nil {
			t.Fatal(err)
		}
		if s.ID == a || !visible || !strings.HasPrefix(s.URL, testOrigin+"/") {
			t.Errorf("suggestion %+v (self %d, visible %v)", s, a, visible)
		}
	}

	// a new post resolves from its draft alone
	res = seoResolve(t, env, "author", map[string]any{"entity_type": "post", "entity_id": 0, "post": draft,
		"seo": map[string]any{"is_cornerstone": false}})
	if !strings.HasPrefix(res.URL, testOrigin+"/") || res.Title == "" {
		t.Errorf("new post: %+v", res)
	}
}

func TestSEOAccess(t *testing.T) {
	env := seoEnv(t, false)
	a := strconv.FormatInt(seoPostID(t, env.pool, "wp:post:123"), 10)
	var term int64
	if err := env.pool.QueryRow(context.Background(), `SELECT id FROM terms ORDER BY id LIMIT 1`).Scan(&term); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		role, method, path, body string
		code                     int
		errCode                  string
	}{
		{"contributor", http.MethodGet, "/seo/post/" + a, "", http.StatusForbidden, "ownership"},
		{"author", http.MethodGet, "/seo/videos/" + a, "", http.StatusForbidden, "ownership"},
		{"editor", http.MethodGet, "/seo/post/" + a, "", http.StatusOK, ""},
		{"author", http.MethodGet, "/seo/term/" + strconv.FormatInt(term, 10), "", http.StatusForbidden, "capability"},
		{"author", http.MethodGet, "/seo/home/0", "", http.StatusForbidden, "capability"},
		{"editor", http.MethodGet, "/seo/home/0", "", http.StatusOK, ""},
		{"editor", http.MethodGet, "/seo/home/1", "", http.StatusBadRequest, ""},
		{"author", http.MethodGet, "/seo/bulk", "", http.StatusForbidden, "capability"},
		{"author", http.MethodPatch, "/seo/bulk", `{"rows":[{"post_id":` + a + `,"title":"x"}]}`, http.StatusForbidden, "capability"},
		{"editor", http.MethodGet, "/seo/bulk?missing=nope", "", http.StatusBadRequest, ""},
	}
	for _, c := range cases {
		r := env.do(c.role, c.method, c.path, c.body)
		if r.code != c.code || (c.errCode != "" && !strings.Contains(string(r.body), `"code":"`+c.errCode+`"`)) {
			t.Errorf("%s %s %s: %d %s", c.role, c.method, c.path, r.code, r.body)
		}
	}
}

func TestSEOBulkPatchPurges(t *testing.T) {
	env := seoEnv(t, true)
	id := seoPostID(t, env.pool, "wp:post:123")
	seoKeepFields(t, env.pool, "post", id)
	pub := env.deps.site

	if w := seoGetPage(pub, "/example-post/"); w.Header().Get("X-Cache") != "MISS" {
		t.Fatalf("first GET: %d %q", w.Code, w.Header().Get("X-Cache"))
	}
	if w := seoGetPage(pub, "/example-post/"); w.Header().Get("X-Cache") != "HIT" {
		t.Fatalf("second GET: %q", w.Header().Get("X-Cache"))
	}
	title := "Bulk title " + env.suffix
	body := `{"rows":[{"post_id":` + strconv.FormatInt(id, 10) + `,"title":"  ` + title + `  "}]}`
	if r := env.do("editor", http.MethodPatch, "/seo/bulk", body); r.code != http.StatusOK {
		t.Fatalf("PATCH: %d %s", r.code, r.body)
	}
	w := seoGetPage(pub, "/example-post/")
	if w.Header().Get("X-Cache") != "MISS" || !strings.Contains(w.Body.String(), "<title>"+title+"</title>") {
		t.Errorf("after PATCH: %q, title %q", w.Header().Get("X-Cache"), seoHeadValues(w.Body.String())["title"])
	}

	var page struct {
		Items []struct {
			PostID        int64   `json:"post_id"`
			Title         *string `json:"title"`
			Description   *string `json:"description"`
			ResolvedTitle string  `json:"resolved_title"`
			URL           *string `json:"url"`
		} `json:"items"`
	}
	r := env.do("editor", http.MethodGet, "/seo/bulk?type=post&q=Example+post", "")
	r.json(t, &page)
	found := false
	for _, it := range page.Items {
		if it.PostID == id {
			found = true
			if it.Title == nil || *it.Title != title || it.ResolvedTitle != title || it.URL == nil {
				t.Errorf("bulk row: %+v", it)
			}
		}
	}
	if !found {
		t.Errorf("post %d not in %s", id, r.body)
	}
	r = env.do("editor", http.MethodGet, "/seo/bulk?type=post&missing=title&q=Example+post", "")
	if strings.Contains(string(r.body), `"post_id":`+strconv.FormatInt(id, 10)+`,`) {
		t.Errorf("missing=title still lists the post: %s", r.body)
	}
	// an absent field is left alone, null clears it
	if r := env.do("editor", http.MethodPatch, "/seo/bulk", `{"rows":[{"post_id":`+strconv.FormatInt(id, 10)+
		`,"description":"Bulk description"}]}`); r.code != http.StatusOK {
		t.Fatalf("PATCH description: %d %s", r.code, r.body)
	}
	f, err := seo.LoadFields(context.Background(), env.pool, "post", id)
	if err != nil {
		t.Fatal(err)
	}
	if f.Title == nil || *f.Title != title || f.Description == nil || *f.Description != "Bulk description" {
		t.Errorf("fields after partial PATCH: %+v", f)
	}
}

func TestSEOVideosManualWins(t *testing.T) {
	env := seoEnv(t, false)
	ctx := context.Background()
	id := seoPostID(t, env.pool, "wp:post:123")
	clip := "https://videos.example.com/clip-" + env.suffix + ".mp4"
	t.Cleanup(func() {
		if _, err := env.pool.Exec(ctx, `DELETE FROM post_videos WHERE url = $1`, clip); err != nil {
			t.Error(err)
		}
	})
	if _, err := env.pool.Exec(ctx, `
INSERT INTO post_videos (post_id, url, title, thumbnail_url, upload_date, source)
VALUES ($1, $2, 'Auto clip `+env.suffix+`', 'https://img.example.com/t.jpg', now(), 'auto')`, id, clip); err != nil {
		t.Fatal(err)
	}
	path := "/seo/videos/" + strconv.FormatInt(id, 10)
	body := `{"items":[{"id":null,"url":"` + clip + `","provider":"","title":"Manual clip ` + env.suffix + `","description":"",
		"thumbnail_url":"https://img.example.com/t.jpg","duration_seconds":60,"upload_date":"2024-01-02T00:00:00Z",
		"family_friendly":null,"source":"manual"}]}`
	r := env.do("admin", http.MethodPut, path, body)
	if r.code != http.StatusOK {
		t.Fatalf("PUT videos: %d %s", r.code, r.body)
	}
	var got struct {
		Items []struct {
			URL    string `json:"url"`
			Title  string `json:"title"`
			Source string `json:"source"`
		} `json:"items"`
	}
	r.json(t, &got)
	sources := map[string]bool{}
	for _, v := range got.Items {
		if v.URL == clip {
			sources[v.Source] = true
		}
	}
	if !sources["auto"] || !sources["manual"] {
		t.Errorf("rows for the clip: %s", r.body)
	}
	if r := env.do("contributor", http.MethodPut, path, body); r.code != http.StatusForbidden {
		t.Errorf("contributor PUT: %d", r.code)
	}

	page := seoGetPage(env.deps.site, "/example-post/").Body.String()
	if !strings.Contains(page, "Manual clip "+env.suffix) || strings.Contains(page, "Auto clip "+env.suffix) {
		t.Errorf("JSON-LD should carry the manual row only")
	}
	if r := env.do("admin", http.MethodPut, path, `{"items":[]}`); r.code != http.StatusOK {
		t.Fatalf("clear: %d %s", r.code, r.body)
	}
	page = seoGetPage(env.deps.site, "/example-post/").Body.String()
	if !strings.Contains(page, "Auto clip "+env.suffix) {
		t.Errorf("without the manual row the auto row shows again")
	}
}
