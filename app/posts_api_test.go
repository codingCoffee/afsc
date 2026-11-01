package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/posts"
	"afsc/internal/site"
)

// postsEnv is the admin harness with the sample content, removing every post its users wrote afterwards.
func postsEnv(t *testing.T) *adminEnv {
	env := newAdminEnv(t)
	importSample(t, env.pool)
	if err := env.site.Invalidate(context.Background(), site.Event{Refresh: true}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		like := "%-" + env.suffix
		for _, sql := range []string{
			`DELETE FROM posts WHERE id IN (SELECT pa.post_id FROM post_authors pa JOIN authors a ON a.id = pa.author_id
			   JOIN users u ON u.id = a.user_id WHERE u.username LIKE $1)`,
			`DELETE FROM authors WHERE user_id IN (SELECT id FROM users WHERE username LIKE $1)`,
			`DELETE FROM redirects WHERE origin <> 'import' AND $1 <> ''`,
		} {
			if _, err := env.pool.Exec(context.Background(), sql, like); err != nil {
				t.Error(err)
			}
		}
	})
	return env
}

func postsBody(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func postsNew(title, status string) map[string]any {
	return map[string]any{"type": "post", "title": title, "slug": "", "status": status, "published_at": nil, "excerpt": "",
		"content":   json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + title + `"}]}]}`),
		"parent_id": nil, "template": nil, "featured_media_id": nil, "author_ids": []int64{}, "category_ids": []int64{},
		"primary_category_id": nil, "tag_ids": []int64{}, "comment_status": "open", "sticky": false, "menu_order": 0}
}

func postsCreate(t *testing.T, env *adminEnv, role string, in map[string]any) posts.Detail {
	t.Helper()
	r := env.do(role, http.MethodPost, "/posts", postsBody(in))
	if r.code != http.StatusCreated {
		t.Fatalf("create as %s: %d %s", role, r.code, r.body)
	}
	var d posts.Detail
	r.json(t, &d)
	return d
}

// postsInput turns a detail back into the PUT body the editor sends.
func postsInput(d posts.Detail) map[string]any {
	return map[string]any{"type": d.Type, "version": d.Version, "title": d.Title, "slug": d.Slug, "status": d.Status,
		"published_at": d.PublishedAt, "excerpt": d.Excerpt, "content": d.Content, "parent_id": d.ParentID,
		"template": d.Template, "featured_media_id": d.FeaturedMediaID, "author_ids": d.AuthorIDs,
		"category_ids": d.CategoryIDs, "primary_category_id": d.PrimaryCategoryID, "tag_ids": d.TagIDs,
		"comment_status": d.CommentStatus, "sticky": d.Sticky, "menu_order": d.MenuOrder, "seo": d.SEO}
}

func TestPostsAPI(t *testing.T) {
	env := postsEnv(t)
	d := postsCreate(t, env, "author", postsNew("API post", "draft"))
	if d.Slug != "api-post" && !strings.HasPrefix(d.Slug, "api-post-") {
		t.Errorf("derived slug %q", d.Slug)
	}
	if len(d.Authors) != 1 || !d.CanPublish || d.CanOverwrite {
		t.Errorf("detail %+v", d)
	}
	id := fmt.Sprint(d.ID)

	t.Run("update and conflict", func(t *testing.T) {
		in := postsInput(d)
		in["title"] = "API post v2"
		r := env.do("author", http.MethodPut, "/posts/"+id, postsBody(in))
		if r.code != 200 {
			t.Fatalf("update: %d %s", r.code, r.body)
		}
		r = env.do("author", http.MethodPut, "/posts/"+id, postsBody(in))
		var conflict struct {
			Code           string `json:"code"`
			CurrentVersion int    `json:"current_version"`
			ModifiedBy     string `json:"modified_by"`
		}
		r.json(t, &conflict)
		if r.code != 409 || conflict.Code != "conflict" || conflict.CurrentVersion != d.Version+1 || conflict.ModifiedBy == "" {
			t.Errorf("stale update: %d %s", r.code, r.body)
		}
		if r := env.do("author", http.MethodPut, "/posts/"+id+"?force=1", postsBody(in)); r.code != 403 {
			t.Errorf("author force: %d %s", r.code, r.body)
		}
	})

	t.Run("invalid content", func(t *testing.T) {
		in := postsNew("bad", "draft")
		in["content"] = json.RawMessage(`{"type":"doc","content":[{"type":"heading","content":[{"type":"text","text":"x"}]}]}`)
		r := env.do("editor", http.MethodPost, "/posts", postsBody(in))
		var e struct {
			Code   string `json:"code"`
			Errors []struct{ Path string }
		}
		r.json(t, &e)
		if r.code != 422 || e.Code != "invalid_content" || len(e.Errors) == 0 || !strings.HasPrefix(e.Errors[0].Path, "content[0]") {
			t.Errorf("%d %s", r.code, r.body)
		}
	})

	t.Run("ownership", func(t *testing.T) {
		r := env.do("contributor", http.MethodGet, "/posts/"+id, "")
		var cur posts.Detail
		r.json(t, &cur)
		r = env.do("contributor", http.MethodPut, "/posts/"+id, postsBody(postsInput(cur)))
		if r.code != 403 || !strings.Contains(string(r.body), `"code":"ownership"`) {
			t.Errorf("contributor editing another's post: %d %s", r.code, r.body)
		}
	})

	t.Run("quick edit", func(t *testing.T) {
		var cur posts.Detail
		env.do("author", http.MethodGet, "/posts/"+id, "").json(t, &cur)
		r := env.do("author", http.MethodPatch, "/posts/"+id, postsBody(map[string]any{"version": cur.Version, "status": "published"}))
		var got posts.Detail
		r.json(t, &got)
		if r.code != 200 || got.Status != "published" || got.URL == nil || got.Title != cur.Title {
			t.Errorf("patch: %d %s", r.code, r.body)
		}
	})

	t.Run("list", func(t *testing.T) {
		r := env.do("author", http.MethodGet, "/posts?mine=1&sort=-modified", "")
		var page pageResponse[posts.ListItem]
		r.json(t, &page)
		if r.code != 200 || page.Total != 1 || page.Items[0].ID != d.ID || !page.Items[0].CanEdit || page.Items[0].URL == nil {
			t.Errorf("mine: %d %s", r.code, r.body)
		}
		r = env.do("contributor", http.MethodGet, "/posts?type=post&q=API", "")
		r.json(t, &page)
		if r.code != 200 || page.Total < 1 || page.Items[0].CanEdit {
			t.Errorf("contributor list: %d %s", r.code, r.body)
		}
	})

	t.Run("slug check and templates", func(t *testing.T) {
		var got struct {
			Available  bool   `json:"available"`
			Suggestion string `json:"suggestion"`
		}
		env.do("author", http.MethodGet, "/posts/slug-check?type=post&slug=example-post", "").json(t, &got)
		if got.Available || got.Suggestion != "example-post-2" {
			t.Errorf("taken slug: %+v", got)
		}
		env.do("author", http.MethodGet, "/posts/slug-check?type=post&slug=fresh-slug-xyz", "").json(t, &got)
		if !got.Available || got.Suggestion != "fresh-slug-xyz" {
			t.Errorf("free slug: %+v", got)
		}
		r := env.do("author", http.MethodGet, "/templates", "")
		if r.code != 200 || !strings.Contains(string(r.body), `"page":[`) {
			t.Errorf("templates: %d %s", r.code, r.body)
		}
	})

	t.Run("autosave, preview, revisions", func(t *testing.T) {
		body := postsBody(map[string]any{"title": "autosaved", "excerpt": "",
			"content": json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"draft body"}]}]}`)})
		r := env.do("author", http.MethodPost, "/posts/"+id+"/autosave", body)
		var as struct {
			RevisionID int64 `json:"revision_id"`
		}
		r.json(t, &as)
		if r.code != 200 || as.RevisionID == 0 {
			t.Fatalf("autosave: %d %s", r.code, r.body)
		}
		r = env.do("author", http.MethodPost, "/posts/"+id+"/preview", postsBody(map[string]any{"revision_id": as.RevisionID}))
		var pv struct {
			URL string `json:"url"`
		}
		r.json(t, &pv)
		if r.code != 200 || !strings.HasPrefix(pv.URL, testOrigin+"/?preview=") {
			t.Fatalf("preview: %d %s", r.code, r.body)
		}
		w := httptest.NewRecorder()
		env.site.ServeHTTP(w, httptest.NewRequest(http.MethodGet, strings.TrimPrefix(pv.URL, testOrigin), nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), "draft body") || w.Header().Get("Cache-Control") != "no-store" {
			t.Errorf("preview page: %d %v", w.Code, w.Header())
		}

		var revs []posts.RevisionItem
		env.do("author", http.MethodGet, "/posts/"+id+"/revisions", "").json(t, &revs)
		if len(revs) < 3 {
			t.Fatalf("revisions %+v", revs)
		}
		var oldest posts.RevisionItem
		for _, rv := range revs {
			if rv.Kind != "autosave" {
				oldest = rv
			}
		}
		r = env.do("author", http.MethodGet, fmt.Sprintf("/revisions/%d", oldest.ID), "")
		if r.code != 200 || !strings.Contains(string(r.body), `"content"`) {
			t.Errorf("revision: %d %s", r.code, r.body)
		}
		r = env.do("author", http.MethodGet, fmt.Sprintf("/revisions/%d/diff?against=current", oldest.ID), "")
		var diff posts.RevisionDiff
		r.json(t, &diff)
		if r.code != 200 || len(diff.Title) == 0 {
			t.Errorf("diff: %d %s", r.code, r.body)
		}
	})

	t.Run("trash, restore, delete, bulk", func(t *testing.T) {
		r := env.do("author", http.MethodPost, "/posts/"+id+"/trash", `{}`)
		if r.code != 422 {
			t.Errorf("trashing a published post without gone: %d %s", r.code, r.body)
		}
		r = env.do("author", http.MethodPost, "/posts/"+id+"/trash", `{"gone":"410"}`)
		var got posts.Detail
		r.json(t, &got)
		if r.code != 200 || got.Status != "trash" {
			t.Fatalf("trash: %d %s", r.code, r.body)
		}
		if r := env.do("author", http.MethodDelete, "/posts/"+id, ""); r.code != 403 {
			t.Errorf("author delete: %d", r.code)
		}
		r = env.do("author", http.MethodPost, "/posts/"+id+"/restore", `{}`)
		if r.json(t, &got); r.code != 200 || got.Status != "draft" {
			t.Errorf("restore: %d %s", r.code, r.body)
		}
		other := postsCreate(t, env, "author", postsNew("Bulk post", "draft"))
		r = env.do("author", http.MethodPost, "/posts/bulk", postsBody(map[string]any{"ids": []int64{d.ID, other.ID}, "action": "trash"}))
		var bulk struct {
			Results []posts.BulkResult `json:"results"`
		}
		r.json(t, &bulk)
		if r.code != 200 || len(bulk.Results) != 2 || !bulk.Results[0].OK || !bulk.Results[1].OK {
			t.Errorf("bulk trash: %d %s", r.code, r.body)
		}
		if r := env.do("editor", http.MethodDelete, "/posts/"+id, ""); r.code != 204 {
			t.Errorf("editor delete: %d %s", r.code, r.body)
		}
		if r := env.do("editor", http.MethodGet, "/posts/"+id, ""); r.code != 404 {
			t.Errorf("deleted post: %d", r.code)
		}
	})
}

// TestPostsScheduledPublish is the §19 criterion: a post scheduled 2 s ahead is live, its cached archive purged and
// an IndexNow ping queued within 60 s, using the real scheduler interval.
func TestPostsScheduledPublish(t *testing.T) {
	env := postsEnv(t)
	ctx := context.Background()
	pub, err := site.New(ctx, site.Config{Pool: env.pool, BaseURL: testOrigin, ThemeDir: "themes/default", Logf: t.Logf,
		CacheBytes: 8 << 20})
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	var wg sync.WaitGroup
	t.Cleanup(func() {
		stop()
		wg.Wait()
	})
	wg.Go(func() { pub.Run(runCtx) })

	var old []byte
	if err := env.pool.QueryRow(ctx, `SELECT value FROM settings WHERE key = 'seo'`).Scan(&old); err != nil && !errors.Is(err, pgx.ErrNoRows) {
		t.Fatal(err)
	}
	seo := pub.Settings().SEO
	seo.IndexNow.Enabled, seo.IndexNow.Key = true, "abcdef123456"
	if _, err := env.pool.Exec(ctx, `INSERT INTO settings (key, value) VALUES ('seo', $1)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, postsBody(seo)); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if old == nil {
			_, _ = env.pool.Exec(context.Background(), `DELETE FROM settings WHERE key = 'seo'`)
		} else {
			_, _ = env.pool.Exec(context.Background(), `UPDATE settings SET value = $1 WHERE key = 'seo'`, string(old))
		}
	})
	for _, st := range []*site.Site{pub, env.site} {
		if err := st.Invalidate(ctx, site.Event{Refresh: true, All: true}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := env.pool.Exec(ctx, `DELETE FROM jobs WHERE done_at IS NULL`); err != nil {
		t.Fatal(err)
	}

	get := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		pub.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		return w
	}
	title := "Scheduled " + env.suffix
	in := postsNew(title, "published")
	in["published_at"] = time.Now().Add(2 * time.Second).Format(time.RFC3339Nano)
	d := postsCreate(t, env, "author", in)
	if d.Status != "scheduled" {
		t.Fatalf("status %s", d.Status)
	}
	get("/")
	if w := get("/"); w.Header().Get("X-Cache") != "HIT" || strings.Contains(w.Body.String(), title) {
		t.Fatalf("archive not cached before publishing: %s", w.Header().Get("X-Cache"))
	}

	pings := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer pings.Close()
	runner := newJobsRunner(env.pool, pub, testOrigin, 2)
	runner.pinger.Client, runner.pinger.Endpoint = pings.Client(), pings.URL
	wg.Go(func() { runner.run(runCtx) })

	start := time.Now()
	var url string
	for time.Since(start) < 60*time.Second {
		var cur posts.Detail
		env.do("author", http.MethodGet, fmt.Sprintf("/posts/%d", d.ID), "").json(t, &cur)
		if cur.Status == "published" && cur.URL != nil {
			url = *cur.URL
			break
		}
		time.Sleep(250 * time.Millisecond)
	}
	if url == "" {
		t.Fatal("not published within 60 s")
	}
	ok := func() bool {
		var n int
		if err := env.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = 'indexnow.ping' AND payload->'urls' ? $1`, url).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return get(strings.TrimPrefix(url, testOrigin)).Code == 200 && strings.Contains(get("/").Body.String(), title) && n > 0
	}
	for !ok() {
		if time.Since(start) > 60*time.Second {
			t.Fatal("post page, purged archive and IndexNow job not all seen within 60 s")
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Logf("published and purged after %s", time.Since(start).Round(time.Millisecond))
}
