package posts

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/content"
	"afsc/internal/importer"
	"afsc/internal/site"
	"afsc/internal/theme"
)

type discard struct{}

func (discard) Put(_ context.Context, _ string, body io.ReadSeeker, _ int64, _ string) error {
	_, err := io.Copy(io.Discard, body)
	return err
}

type testEnv struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	site   *site.Site
	svc    *Service
	actors map[string]Actor
}

// newEnv imports the sample bundle and signs in one user per role plus a second author ("author2").
func newEnv(t *testing.T) *testEnv {
	t.Helper()
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	tm, err := theme.LoadManifest("../../themes/default")
	if err != nil {
		t.Fatal(err)
	}
	if rep, err := importer.Run(ctx, importer.Options{BundleDir: "../../testdata/bundle-sample", Theme: tm, Pool: pool,
		Storage: discard{}}); err != nil {
		t.Fatalf("import: %v %+v", err, rep.Errors)
	}
	st, err := site.New(ctx, site.Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "../../themes/default", Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	env := &testEnv{t: t, ctx: ctx, pool: pool, site: st, svc: New(pool, st, t.Logf), actors: map[string]Actor{}}
	suffix := uuid.NewString()[:8]
	for _, name := range []string{"admin", "editor", "author", "author2", "contributor"} {
		role := strings.TrimSuffix(name, "2")
		var id uuid.UUID
		if err := pool.QueryRow(ctx, `INSERT INTO users (username, email, role) VALUES ($1, $2, $3) RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.com", role).Scan(&id); err != nil {
			t.Fatal(err)
		}
		env.actors[name] = Actor{User: id, Role: role}
	}
	t.Cleanup(func() {
		like := "%-" + suffix
		for _, sql := range []string{
			`DELETE FROM posts WHERE id IN (SELECT pa.post_id FROM post_authors pa JOIN authors a ON a.id = pa.author_id
			   JOIN users u ON u.id = a.user_id WHERE u.username LIKE $1)`,
			`DELETE FROM posts WHERE legacy_key IS NULL AND title LIKE 'T: %' AND $1 <> ''`,
			`DELETE FROM authors WHERE user_id IN (SELECT id FROM users WHERE username LIKE $1)`,
			`DELETE FROM audit_log WHERE actor_user_id IN (SELECT id FROM users WHERE username LIKE $1)`,
			`DELETE FROM users WHERE username LIKE $1`,
			`DELETE FROM redirects WHERE origin <> 'import' AND $1 <> ''`,
			`DELETE FROM jobs WHERE kind = 'indexnow.ping' AND $1 <> ''`,
		} {
			if _, err := pool.Exec(context.Background(), sql, like); err != nil {
				t.Error(err)
			}
		}
	})
	return env
}

func para(text string) json.RawMessage {
	b, _ := json.Marshal(map[string]any{"type": "doc", "content": []any{
		map[string]any{"type": "paragraph", "content": []any{map[string]any{"type": "text", "text": text}}}}})
	return b
}

func (e *testEnv) refresh() {
	e.t.Helper()
	if err := e.site.Invalidate(e.ctx, site.Event{Refresh: true}); err != nil {
		e.t.Fatal(err)
	}
}

func (e *testEnv) save(who string, id int64, in Input) (*Detail, error) {
	return e.svc.Save(e.ctx, e.actors[who], id, in, false)
}

func (e *testEnv) mustSave(who string, id int64, in Input) *Detail {
	e.t.Helper()
	d, err := e.save(who, id, in)
	if err != nil {
		e.t.Fatalf("save as %s: %v", who, describe(err))
	}
	e.refresh()
	return d
}

func describe(err error) string {
	var pe *Error
	if errors.As(err, &pe) {
		b, _ := json.Marshal(pe)
		return pe.Code + " " + string(b)
	}
	return err.Error()
}

func code(err error) string {
	var pe *Error
	if errors.As(err, &pe) {
		return pe.Code
	}
	if err != nil {
		return "error: " + err.Error()
	}
	return ""
}

func newPost(title, status string) Input {
	return Input{Type: "post", Title: "T: " + title, Status: status, Content: para(title)}
}

func edit(d *Detail) Input { return inputOf(d) }

func (e *testEnv) get(path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	e.site.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func path(d *Detail) string {
	return strings.TrimPrefix(*d.URL, "https://blog.example.com")
}

func TestSchemaErrorsCarryNodePaths(t *testing.T) {
	e := newEnv(t)
	in := newPost("bad", "draft")
	in.Content = json.RawMessage(`{"type":"doc","content":[{"type":"paragraph"},{"type":"heading","content":[{"type":"text","text":"x"}]}]}`)
	_, err := e.save("editor", 0, in)
	var pe *Error
	if !errors.As(err, &pe) || pe.Status != 422 || pe.Code != "invalid_content" {
		t.Fatalf("err = %v", err)
	}
	if !slices.ContainsFunc(pe.Errors, func(i content.Issue) bool { return strings.HasPrefix(i.Path, "content[1]") }) {
		t.Errorf("no issue at content[1]: %+v", pe.Errors)
	}
	in.Content = json.RawMessage(`{"type":"doc","content":[{"type":"paragraph","attrs":{"classes":"not-allowed"},"content":[{"type":"text","text":"x"}]}]}`)
	if _, err := e.save("editor", 0, in); code(err) != "invalid_content" {
		t.Errorf("unknown class: %v", err)
	}
}

func TestRawHTMLRule(t *testing.T) {
	e := newEnv(t)
	raw := `{"type":"rawHtml","attrs":{"html":"<p>raw</p>"}}`
	in := newPost("raw", "draft")
	in.Content = json.RawMessage(`{"type":"doc","content":[` + raw + `]}`)
	if _, err := e.save("editor", 0, in); code(err) != "invalid_content" {
		t.Fatalf("editor adding rawHtml: %v", err)
	}
	d := e.mustSave("admin", 0, in)

	withBlock := func(raw json.RawMessage, block string, first bool) json.RawMessage {
		n, err := content.Parse(raw)
		if err != nil {
			t.Fatal(err)
		}
		b, err := content.Parse([]byte(block))
		if err != nil {
			t.Fatal(err)
		}
		if first {
			n.Content = append([]*content.Node{b}, n.Content...)
		} else {
			n.Content = append(n.Content, b)
		}
		out, _ := json.Marshal(n)
		return out
	}
	keep := edit(d)
	keep.Content = withBlock(d.Content, `{"type":"paragraph","content":[{"type":"text","text":"new"}]}`, true)
	d2, err := e.save("editor", d.ID, keep)
	if err != nil {
		t.Fatalf("editor keeping the admin's rawHtml: %v", describe(err))
	}
	add := edit(d2)
	add.Content = withBlock(d2.Content, `{"type":"rawHtml","attrs":{"html":"<p>mine</p>"}}`, false)
	_, err = e.save("editor", d.ID, add)
	var pe *Error
	if !errors.As(err, &pe) || len(pe.Errors) != 1 || pe.Errors[0].Path != "content[2]" {
		t.Errorf("editor adding a second rawHtml: %v", describe(err))
	}
}

func TestVersionConflict(t *testing.T) {
	e := newEnv(t)
	d := e.mustSave("author", 0, newPost("conflict", "draft"))
	stale := edit(d)
	fresh := edit(d)
	fresh.Title = "T: edited by editor"
	if _, err := e.save("editor", d.ID, fresh); err != nil {
		t.Fatal(describe(err))
	}
	_, err := e.save("author", d.ID, stale)
	var pe *Error
	if !errors.As(err, &pe) || pe.Status != 409 || pe.Code != "conflict" || pe.CurrentVersion == nil || *pe.CurrentVersion != d.Version+1 {
		t.Fatalf("stale save: %v", describe(err))
	}
	if _, err := e.svc.Save(e.ctx, e.actors["author"], d.ID, stale, true); code(err) != "capability" {
		t.Errorf("author force: %v", err)
	}
	if _, err := e.svc.Save(e.ctx, e.actors["editor"], d.ID, stale, true); err != nil {
		t.Errorf("editor force: %v", describe(err))
	}
}

func TestStatusRules(t *testing.T) {
	e := newEnv(t)
	author2 := e.mustSave("author2", 0, newPost("other author", "draft"))
	contrib := e.mustSave("contributor", 0, newPost("contrib", "pending"))
	future := time.Now().Add(time.Hour)
	past := time.Now().Add(-time.Hour)

	cases := []struct {
		name string
		who  string
		run  func() (*Detail, error)
		want string // error code, "" for success
		stat string // resulting status on success
	}{
		{"contributor publishes", "contributor", func() (*Detail, error) { return e.save("contributor", 0, newPost("c1", "published")) }, "capability", ""},
		{"contributor schedules", "contributor", func() (*Detail, error) {
			in := newPost("c2", "scheduled")
			in.PublishedAt = &future
			return e.save("contributor", 0, in)
		}, "capability", ""},
		{"contributor drafts", "contributor", func() (*Detail, error) { return e.save("contributor", 0, newPost("c3", "draft")) }, "", "draft"},
		{"contributor pending → draft", "contributor", func() (*Detail, error) {
			in := edit(contrib)
			in.Status = "draft"
			return e.save("contributor", contrib.ID, in)
		}, "", "draft"},
		{"contributor edits another's post", "contributor", func() (*Detail, error) { return e.save("contributor", author2.ID, edit(author2)) }, "ownership", ""},
		{"author edits another's post", "author", func() (*Detail, error) { return e.save("author", author2.ID, edit(author2)) }, "ownership", ""},
		{"author publishes own", "author", func() (*Detail, error) { return e.save("author", 0, newPost("a1", "published")) }, "", "published"},
		{"author publish in the future", "author", func() (*Detail, error) {
			in := newPost("a2", "published")
			in.PublishedAt = &future
			return e.save("author", 0, in)
		}, "", "scheduled"},
		{"scheduled in the past", "author", func() (*Detail, error) {
			in := newPost("a3", "scheduled")
			in.PublishedAt = &past
			return e.save("author", 0, in)
		}, "invalid", ""},
		{"author creates page", "author", func() (*Detail, error) {
			in := newPost("p1", "draft")
			in.Type = "page"
			return e.save("author", 0, in)
		}, "capability", ""},
		{"editor creates page", "editor", func() (*Detail, error) {
			in := newPost("p2", "draft")
			in.Type = "page"
			return e.save("editor", 0, in)
		}, "", "draft"},
		{"editor publishes another's post", "editor", func() (*Detail, error) {
			in := edit(author2)
			in.Status = "published"
			return e.save("editor", author2.ID, in)
		}, "", "published"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := c.run()
			if got := code(err); got != c.want {
				t.Fatalf("code %q, want %q (%v)", got, c.want, err)
			}
			if c.want == "" && d.Status != c.stat {
				t.Errorf("status %s, want %s", d.Status, c.stat)
			}
		})
	}

	t.Run("contributor can't touch a published post", func(t *testing.T) {
		d, err := e.svc.Get(e.ctx, e.actors["editor"], contrib.ID)
		if err != nil {
			t.Fatal(err)
		}
		in := edit(d)
		in.Status = "published"
		if d, err = e.save("editor", contrib.ID, in); err != nil {
			t.Fatal(describe(err))
		}
		in = edit(d)
		in.Status = "draft"
		if _, err := e.save("contributor", contrib.ID, in); code(err) != "ownership" {
			t.Errorf("got %v", err)
		}
	})

	t.Run("author unpublishes own", func(t *testing.T) {
		d := e.mustSave("author", 0, newPost("a4", "published"))
		in := edit(d)
		in.Status = "draft"
		if d, err := e.save("author", d.ID, in); err != nil || d.Status != "draft" {
			t.Errorf("unpublish: %v", err)
		}
	})

	t.Run("first post creates a linked author", func(t *testing.T) {
		var n int
		if err := e.pool.QueryRow(e.ctx, `
SELECT count(*) FROM post_authors pa JOIN authors a ON a.id = pa.author_id
WHERE pa.post_id = $1 AND a.user_id = $2`, contrib.ID, e.actors["contributor"].User).Scan(&n); err != nil || n != 1 {
			t.Errorf("linked author rows %d %v", n, err)
		}
	})
}

func TestSlugChangeRedirects(t *testing.T) {
	e := newEnv(t)
	d := e.mustSave("editor", 0, newPost("slug change", "published"))
	old := path(d)
	if w := e.get(old); w.Code != 200 {
		t.Fatalf("%s: %d", old, w.Code)
	}
	in := edit(d)
	in.Slug = "t-renamed-" + uuid.NewString()[:6]
	d = e.mustSave("editor", d.ID, in)
	w := e.get(old)
	if w.Code != 301 || w.Header().Get("Location") != path(d) {
		t.Errorf("old URL: %d -> %q, want 301 -> %s", w.Code, w.Header().Get("Location"), path(d))
	}
	var origin string
	if err := e.pool.QueryRow(e.ctx, `SELECT origin FROM redirects WHERE source = $1`, old).Scan(&origin); err != nil || origin != "slug_change" {
		t.Errorf("origin %q %v", origin, err)
	}
}

func TestPageMoveRedirectsDescendants(t *testing.T) {
	e := newEnv(t)
	in := newPost("parent page", "published")
	in.Type = "page"
	parent := e.mustSave("editor", 0, in)
	in = newPost("child page", "published")
	in.Type, in.ParentID = "page", &parent.ID
	child := e.mustSave("editor", 0, in)
	oldChild := path(child)

	in = edit(parent)
	in.Slug = "t-moved-" + uuid.NewString()[:6]
	parent = e.mustSave("editor", parent.ID, in)
	child, err := e.svc.Get(e.ctx, e.actors["editor"], child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(path(child), path(parent)) {
		t.Fatalf("child %s not under %s", path(child), path(parent))
	}
	if w := e.get(oldChild); w.Code != 301 || w.Header().Get("Location") != path(child) {
		t.Errorf("old child URL: %d -> %q", w.Code, w.Header().Get("Location"))
	}
}

func TestTrash(t *testing.T) {
	e := newEnv(t)
	listen, err := pgx.ConnectConfig(e.ctx, e.pool.Config().ConnConfig.Copy())
	if err != nil {
		t.Fatal(err)
	}
	defer listen.Close(context.Background())
	if _, err := listen.Exec(e.ctx, "LISTEN "+site.Channel); err != nil {
		t.Fatal(err)
	}

	for _, c := range []struct {
		gone, target string
		status       int
		location     string
	}{{"410", "", 410, ""}, {"301", "/about/", 301, "/about/"}, {"none", "", 404, ""}} {
		t.Run(c.gone, func(t *testing.T) {
			d := e.mustSave("editor", 0, newPost("trash "+c.gone, "published"))
			p := path(d)
			if _, err := e.svc.Trash(e.ctx, e.actors["editor"], d.ID, "", ""); code(err) != "invalid" {
				t.Errorf("trash without gone: %v", err)
			}
			drain(listen)
			td, err := e.svc.Trash(e.ctx, e.actors["editor"], d.ID, c.gone, c.target)
			if err != nil || td.Status != "trash" {
				t.Fatalf("trash: %v", err)
			}
			if !notified(t, listen, p) {
				t.Errorf("no purge of %s seen on %s", p, site.Channel)
			}
			e.refresh()
			w := e.get(p)
			if w.Code != c.status || w.Header().Get("Location") != c.location {
				t.Errorf("%s: %d -> %q", p, w.Code, w.Header().Get("Location"))
			}
			if c.gone != "410" {
				return
			}
			var note string
			if err := e.pool.QueryRow(e.ctx, `SELECT note FROM redirects WHERE source = $1 AND origin = 'manual'`, p).Scan(&note); err != nil ||
				!strings.Contains(note, "trashed post #") {
				t.Errorf("rule note %q %v", note, err)
			}
			rd, err := e.svc.Restore(e.ctx, e.actors["editor"], d.ID)
			if err != nil || rd.Status != "draft" {
				t.Fatalf("restore: %v %v", rd, err)
			}
			in := edit(rd)
			in.Status = "published"
			e.mustSave("editor", d.ID, in)
			var n int
			if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM redirects WHERE source = $1`, p).Scan(&n); err != nil || n != 0 {
				t.Errorf("republishing left %d rules at %s", n, p)
			}
			if w := e.get(p); w.Code != 200 {
				t.Errorf("republished: %d", w.Code)
			}
		})
	}

	t.Run("permanent delete", func(t *testing.T) {
		d := e.mustSave("editor", 0, newPost("delete me", "draft"))
		if err := e.svc.Delete(e.ctx, e.actors["editor"], d.ID); code(err) != "not_trashed" {
			t.Errorf("delete outside trash: %v", err)
		}
		if _, err := e.svc.Trash(e.ctx, e.actors["editor"], d.ID, "", ""); err != nil {
			t.Fatal(err)
		}
		if err := e.svc.Delete(e.ctx, e.actors["author"], d.ID); code(err) != "capability" {
			t.Errorf("author delete: %v", err)
		}
		if err := e.svc.Delete(e.ctx, e.actors["editor"], d.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := e.svc.Get(e.ctx, e.actors["editor"], d.ID); code(err) != "not_found" {
			t.Errorf("after delete: %v", err)
		}
	})
}

func drain(c *pgx.Conn) {
	for {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		_, err := c.WaitForNotification(ctx)
		cancel()
		if err != nil {
			return
		}
	}
}

func notified(t *testing.T, c *pgx.Conn, path string) bool {
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithDeadline(context.Background(), deadline)
		n, err := c.WaitForNotification(ctx)
		cancel()
		if err != nil {
			return false
		}
		var ev site.Event
		if err := json.Unmarshal([]byte(n.Payload), &ev); err != nil {
			t.Fatal(err)
		}
		if ev.All || slices.Contains(ev.Paths, path) {
			return true
		}
	}
	return false
}

func TestAutosaveKeepsOneRow(t *testing.T) {
	e := newEnv(t)
	d := e.mustSave("author", 0, newPost("autosave", "draft"))
	for _, text := range []string{"first", "second"} {
		if _, err := e.svc.Autosave(e.ctx, e.actors["author"], d.ID, AutosaveInput{Title: "T: " + text, Content: para(text)}); err != nil {
			t.Fatal(describe(err))
		}
	}
	if _, err := e.svc.Autosave(e.ctx, e.actors["editor"], d.ID, AutosaveInput{Title: "T: editor", Content: para("e")}); err != nil {
		t.Fatal(describe(err))
	}
	var mine, all int
	if err := e.pool.QueryRow(e.ctx, `
SELECT count(*) FILTER (WHERE user_id = $2), count(*) FROM post_revisions WHERE post_id = $1 AND kind = 'autosave'`,
		d.ID, e.actors["author"].User).Scan(&mine, &all); err != nil || mine != 1 || all != 2 {
		t.Errorf("autosave rows: mine %d all %d %v", mine, all, err)
	}
	got, err := e.svc.Get(e.ctx, e.actors["author"], d.ID)
	if err != nil || got.Autosave == nil || got.Autosave.Title != "T: second" {
		t.Fatalf("detail autosave: %+v %v", got.Autosave, err)
	}
	if got.Version != d.Version {
		t.Error("autosave changed the post row")
	}
	e.mustSave("author", d.ID, edit(got))
	if got, _ := e.svc.Get(e.ctx, e.actors["author"], d.ID); got.Autosave != nil {
		t.Error("a save left the author's autosave in place")
	}
}

func TestRevisionsAndDiff(t *testing.T) {
	e := newEnv(t)
	d := e.mustSave("editor", 0, newPost("one two three", "draft"))
	in := edit(d)
	in.Content = para("one 2 three four")
	e.mustSave("editor", d.ID, in)
	revs, err := e.svc.Revisions(e.ctx, d.ID)
	if err != nil || len(revs) != 2 {
		t.Fatalf("revisions %+v %v", revs, err)
	}
	first := revs[1].ID
	a, err := e.svc.Diff(e.ctx, first, "current")
	if err != nil {
		t.Fatal(describe(err))
	}
	b, _ := e.svc.Diff(e.ctx, first, "")
	if ja, jb := mustJSON(t, a), mustJSON(t, b); ja != jb {
		t.Errorf("diff not deterministic:\n%s\n%s", ja, jb)
	}
	if rebuild(a.Text, "delete") != "one 2 three four" || rebuild(a.Text, "insert") != "one two three" {
		t.Errorf("text ops %+v", a.Text)
	}
	if len(a.Blocks) != 1 || a.Blocks[0].Change != "changed" {
		t.Errorf("blocks %+v", a.Blocks)
	}
	if _, err := e.svc.Diff(e.ctx, first, "abc"); code(err) != "invalid" {
		t.Errorf("bad against: %v", err)
	}
}

func mustJSON(t *testing.T, v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestPreview(t *testing.T) {
	e := newEnv(t)
	d := e.mustSave("author", 0, newPost("preview", "draft"))
	as, err := e.svc.Autosave(e.ctx, e.actors["author"], d.ID, AutosaveInput{Title: "T: unsaved title", Content: para("unsaved body")})
	if err != nil {
		t.Fatal(describe(err))
	}
	u, exp, err := e.svc.Preview(e.ctx, d.ID, as.RevisionID)
	if err != nil || time.Until(exp) < 59*time.Minute {
		t.Fatalf("preview %v %v", exp, err)
	}
	target := strings.TrimPrefix(u, "https://blog.example.com")
	w := e.get(target)
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, "unsaved body") || !strings.Contains(body, "T: unsaved title") {
		t.Fatalf("preview: %d %s", w.Code, body)
	}
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("X-Robots-Tag") != "noindex" ||
		w.Header().Get("X-Cache") != "" || !strings.Contains(body, `name="robots" content="noindex`) {
		t.Errorf("headers %v", w.Header())
	}

	tok := strings.TrimPrefix(target, "/?preview=")
	flip := []byte(tok)
	flip[3] ^= 1
	if flip[3] == '=' || flip[3] > 'z' {
		flip[3] = 'A'
	}
	expired := e.site.PreviewToken(d.ID, as.RevisionID, time.Now().Add(-time.Second))
	other := e.site.PreviewToken(d.ID+1000000, as.RevisionID, time.Now().Add(time.Hour))
	for name, bad := range map[string]string{"tampered": string(flip), "expired": expired, "wrong post": other, "garbage": "x"} {
		w := e.get("/?preview=" + bad)
		if w.Code != 404 || w.Header().Get("Cache-Control") != "no-store" || strings.Contains(w.Body.String(), "unsaved body") {
			t.Errorf("%s token: %d %v", name, w.Code, w.Header())
		}
	}
	if _, _, err := e.svc.Preview(e.ctx, d.ID+1000000, 0); code(err) != "not_found" {
		t.Errorf("preview of a missing post: %v", err)
	}
}

func TestAutoVideosAndPostMedia(t *testing.T) {
	e := newEnv(t)
	const yt = "https://www.youtube.com/watch?v=dQw4w9WgXcQ"
	var mediaID int64
	if err := e.pool.QueryRow(e.ctx, `SELECT id FROM media WHERE mime LIKE 'image/%' ORDER BY id LIMIT 1`).Scan(&mediaID); err != nil {
		t.Fatal(err)
	}
	in := newPost("videos", "draft")
	in.FeaturedMediaID = &mediaID
	in.Content = json.RawMessage(`{"type":"doc","content":[{"type":"embed","attrs":{"provider":"youtube","url":"` + yt +
		`","externalId":"dQw4w9WgXcQ"}}]}`)
	d := e.mustSave("editor", 0, in)
	sources := func() []string {
		rows, err := e.pool.Query(e.ctx, `SELECT source FROM post_videos WHERE post_id = $1 AND url = $2 ORDER BY source`, d.ID, yt)
		if err != nil {
			t.Fatal(err)
		}
		out, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	if got := sources(); !slices.Equal(got, []string{"auto"}) {
		t.Fatalf("after save: %v", got)
	}
	var media []int64
	rows, _ := e.pool.Query(e.ctx, `SELECT media_id FROM post_media WHERE post_id = $1`, d.ID)
	if media, _ = pgx.CollectRows(rows, pgx.RowTo[int64]); !slices.Equal(media, []int64{mediaID}) {
		t.Errorf("post_media %v", media)
	}

	if _, err := e.pool.Exec(e.ctx, `INSERT INTO post_videos (post_id, url, title, source) VALUES ($1, $2, 'Manual', 'manual')`, d.ID, yt); err != nil {
		t.Fatal(err)
	}
	d = e.mustSave("editor", d.ID, edit(d))
	if got := sources(); !slices.Equal(got, []string{"manual"}) {
		t.Errorf("manual row must win: %v", got)
	}
	in = edit(d)
	in.Content, in.FeaturedMediaID = para("no video"), nil
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM post_videos WHERE post_id = $1`, d.ID); err != nil {
		t.Fatal(err)
	}
	d = e.mustSave("editor", d.ID, in)
	var n int
	if err := e.pool.QueryRow(e.ctx, `SELECT (SELECT count(*) FROM post_videos WHERE post_id = $1) + (SELECT count(*) FROM post_media WHERE post_id = $1)`,
		d.ID).Scan(&n); err != nil || n != 0 {
		t.Errorf("derived rows left: %d %v", n, err)
	}
}

// TestPostMediaBackfill checks the importer's post_media (the same jsonpath as migration 0008) against the Go walk.
func TestPostMediaBackfill(t *testing.T) {
	e := newEnv(t)
	keys := []string{"wp:post:123", "wp:post:124", "wp:post:125", "wp:post:126", "wp:post:127", "wp:post:128",
		"wp:post:129", "wp:post:2", "wp:post:3", "wp:post:4", "wp:post:5", "wp:post:6"}
	rows, err := e.pool.Query(e.ctx, `SELECT id, content, featured_media_id FROM posts WHERE legacy_key = ANY($1)`, keys)
	if err != nil {
		t.Fatal(err)
	}
	type post struct {
		id       int64
		doc      []byte
		featured *int64
	}
	list, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (post, error) {
		var p post
		return p, r.Scan(&p.id, &p.doc, &p.featured)
	})
	if err != nil {
		t.Fatal(err)
	}
	checked := 0
	for _, p := range list {
		doc, err := content.Parse(p.doc)
		if err != nil {
			t.Fatal(err)
		}
		media, err := loadMediaInfo(e.ctx, e.pool, mediaIDs(doc, p.featured))
		if err != nil {
			t.Fatal(err)
		}
		want := []int64{}
		for _, id := range mediaIDs(doc, p.featured) {
			if _, ok := media[id]; ok {
				want = append(want, id)
			}
		}
		rows, _ := e.pool.Query(e.ctx, `SELECT media_id FROM post_media WHERE post_id = $1 ORDER BY media_id`, p.id)
		got, err := pgx.CollectRows(rows, pgx.RowTo[int64])
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(got, want) {
			t.Errorf("post %d: post_media %v, want %v", p.id, got, want)
		}
		checked += len(want)
	}
	if checked == 0 {
		t.Error("the sample bundle references no media")
	}
}

// enableIndexNow switches IndexNow on in the stored seo settings for the test.
func (e *testEnv) enableIndexNow() {
	e.t.Helper()
	var old []byte
	err := e.pool.QueryRow(e.ctx, `SELECT value FROM settings WHERE key = 'seo'`).Scan(&old)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		e.t.Fatal(err)
	}
	s := e.site.Settings().SEO
	s.IndexNow.Enabled, s.IndexNow.Key = true, "abcdef123456"
	b, _ := json.Marshal(s)
	if _, err := e.pool.Exec(e.ctx, `INSERT INTO settings (key, value) VALUES ('seo', $1)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value`, string(b)); err != nil {
		e.t.Fatal(err)
	}
	e.t.Cleanup(func() {
		if old == nil {
			_, _ = e.pool.Exec(context.Background(), `DELETE FROM settings WHERE key = 'seo'`)
		} else {
			_, _ = e.pool.Exec(context.Background(), `UPDATE settings SET value = $1 WHERE key = 'seo'`, string(old))
		}
	})
	e.refresh()
}

func (e *testEnv) pings() [][]string {
	rows, err := e.pool.Query(e.ctx, `SELECT payload FROM jobs WHERE kind = 'indexnow.ping' ORDER BY id`)
	if err != nil {
		e.t.Fatal(err)
	}
	var out [][]string
	var raw []byte
	_, err = pgx.ForEachRow(rows, []any{&raw}, func() error {
		var p struct{ URLs []string }
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		out = append(out, p.URLs)
		return nil
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return out
}

func TestIndexNowPings(t *testing.T) {
	e := newEnv(t)
	if _, err := e.pool.Exec(e.ctx, `DELETE FROM jobs WHERE kind = 'indexnow.ping'`); err != nil {
		t.Fatal(err)
	}
	e.mustSave("editor", 0, newPost("not yet", "draft"))
	if n := len(e.pings()); n != 0 {
		t.Fatalf("IndexNow off but %d pings queued", n)
	}
	e.enableIndexNow()
	e.mustSave("editor", 0, newPost("draft", "draft"))
	if n := len(e.pings()); n != 0 {
		t.Errorf("a draft queued %d pings", n)
	}
	d := e.mustSave("editor", 0, newPost("live", "published"))
	old := *d.URL
	in := edit(d)
	in.Slug = "t-live-moved-" + uuid.NewString()[:6]
	d = e.mustSave("editor", d.ID, in)
	noindex := true
	quiet := newPost("quiet", "published")
	quiet.SEO = &SeoFields{RobotsNoindex: &noindex}
	e.mustSave("editor", 0, quiet)
	pings := e.pings()
	if len(pings) != 2 || !slices.Equal(pings[0], []string{old}) || !slices.Equal(pings[1], []string{old, *d.URL}) {
		t.Errorf("pings %v", pings)
	}
}

func TestPublishDue(t *testing.T) {
	e := newEnv(t)
	e.enableIndexNow()
	at := time.Now().Add(time.Second)
	in := newPost("due", "published")
	in.PublishedAt = &at
	d := e.mustSave("author", 0, in)
	if d.Status != "scheduled" || d.URL != nil {
		t.Fatalf("status %s url %v", d.Status, d.URL)
	}
	time.Sleep(1500 * time.Millisecond)
	if _, err := e.svc.PublishDue(e.ctx); err != nil {
		t.Fatal(err)
	}
	got, err := e.svc.Get(e.ctx, e.actors["author"], d.ID)
	if err != nil || got.Status != "published" || got.URL == nil {
		t.Fatalf("after PublishDue: %+v %v", got, err)
	}
	var kind string
	var user *uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT kind, user_id FROM post_revisions WHERE post_id = $1 ORDER BY id DESC LIMIT 1`, d.ID).
		Scan(&kind, &user); err != nil || kind != "publish" || user != nil {
		t.Errorf("revision %s %v %v", kind, user, err)
	}
	if !slices.ContainsFunc(e.pings(), func(u []string) bool { return slices.Contains(u, *got.URL) }) {
		t.Error("no IndexNow ping for the published post")
	}
}
