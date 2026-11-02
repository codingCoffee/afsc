package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"afsc/internal/comments"
	"afsc/internal/plugins"
	"afsc/internal/posts"
	"afsc/internal/spam"
	"afsc/internal/taxonomy"
)

const eventsDomain = "m5events.example"

type firedEvent struct {
	Event string         `json:"event"`
	Data  map[string]any `json:"data"`
	raw   string
}

// fired runs fn and returns the events it queued for adminTestPlugin, in order.
func (env *adminEnv) fired(fn func()) []firedEvent {
	env.t.Helper()
	ctx := context.Background()
	var since int64
	if err := env.pool.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM jobs`).Scan(&since); err != nil {
		env.t.Fatal(err)
	}
	fn()
	rows, err := env.pool.Query(ctx, `SELECT payload->'envelope' FROM jobs
WHERE kind = $1 AND payload->>'plugin' = 'testadmin' AND id > $2 ORDER BY id`, plugins.ActionKind, since)
	if err != nil {
		env.t.Fatal(err)
	}
	defer rows.Close()
	var out []firedEvent
	for rows.Next() {
		var raw []byte
		if err := rows.Scan(&raw); err != nil {
			env.t.Fatal(err)
		}
		ev := firedEvent{raw: string(raw)}
		if err := json.Unmarshal(raw, &ev); err != nil {
			env.t.Fatal(err)
		}
		out = append(out, ev)
	}
	return out
}

func eventNames(evs []firedEvent) string {
	names := make([]string, len(evs))
	for i, e := range evs {
		names[i] = e.Event
	}
	return strings.Join(names, ",")
}

func TestPluginEvents(t *testing.T) {
	env := postsEnv(t)
	ctx := context.Background()
	termIDs := &[]int64{}
	taxCleanup(t, env, "terms", termIDs)
	mediaIDs := &[]int64{}
	t.Cleanup(func() {
		for _, q := range []struct {
			sql string
			arg any
		}{
			{`DELETE FROM media WHERE id = ANY($1::bigint[])`, *mediaIDs},
			{`DELETE FROM comments WHERE author_email LIKE '%@' || $1::text OR body LIKE '%' || $1::text`, eventsDomain},
			{`DELETE FROM spam_blocklist WHERE value LIKE '%' || $1::text`, eventsDomain},
		} {
			if _, err := env.pool.Exec(ctx, q.sql, q.arg); err != nil {
				t.Error(err)
			}
		}
	})
	check := func(what string, evs []firedEvent, want string) {
		t.Helper()
		if got := eventNames(evs); got != want {
			t.Fatalf("%s fired %q, want %q", what, got, want)
		}
	}
	ok := func(r apiResp) {
		t.Helper()
		if r.code >= 300 {
			t.Fatalf("%d %s", r.code, r.body)
		}
	}

	t.Run("posts", func(t *testing.T) {
		var d posts.Detail
		evs := env.fired(func() { d = postsCreate(t, env, "editor", postsNew("Events "+env.suffix, "draft")) })
		check("create", evs, "post.saved")
		if evs[0].Data["id"] != float64(d.ID) || evs[0].Data["status"] != "draft" || evs[0].Data["title"] != d.Title {
			t.Errorf("post.saved data: %s", evs[0].raw)
		}
		id := fmt.Sprint(d.ID)

		in := postsInput(d)
		in["status"] = "published"
		evs = env.fired(func() { ok(env.do("editor", http.MethodPut, "/posts/"+id, postsBody(in))) })
		check("publish", evs, "post.saved,post.published")
		if evs[1].Data["published_at"] == nil || !strings.HasPrefix(fmt.Sprint(evs[1].Data["url"]), testOrigin+"/") {
			t.Errorf("post.published data: %s", evs[1].raw)
		}

		evs = env.fired(func() { ok(env.do("editor", http.MethodPost, "/posts/"+id+"/trash", `{"gone":"none"}`)) })
		check("trash a published post", evs, "post.unpublished")
		if evs[0].Data["status"] != "trash" || evs[0].Data["previous_status"] != "published" {
			t.Errorf("post.unpublished data: %s", evs[0].raw)
		}
		evs = env.fired(func() { ok(env.do("editor", http.MethodPost, "/posts/"+id+"/restore", `{}`)) })
		check("restore", evs, "post.saved")
		evs = env.fired(func() { ok(env.do("editor", http.MethodPost, "/posts/"+id+"/trash", `{}`)) })
		check("trash a draft", evs, "")
		evs = env.fired(func() { ok(env.do("editor", http.MethodDelete, "/posts/"+id, "")) })
		check("delete", evs, "post.deleted")
		if evs[0].Data["slug"] != d.Slug || evs[0].Data["type"] != "post" {
			t.Errorf("post.deleted data: %s", evs[0].raw)
		}
	})

	t.Run("terms", func(t *testing.T) {
		var term taxonomy.Term
		evs := env.fired(func() {
			r := env.do("admin", http.MethodPost, "/terms",
				`{"taxonomy":"category","name":"Events `+env.suffix+`","slug":"","description":"","parent_id":null}`)
			ok(r)
			r.json(t, &term)
			*termIDs = append(*termIDs, term.ID)
		})
		check("create term", evs, "term.saved")
		if evs[0].Data["url"] != *term.URL || evs[0].Data["taxonomy"] != "category" {
			t.Errorf("term.saved data: %s (url %s)", evs[0].raw, *term.URL)
		}
		evs = env.fired(func() {
			ok(env.do("admin", http.MethodPut, fmt.Sprintf("/terms/%d", term.ID),
				`{"taxonomy":"category","name":"Events","slug":"events-moved-`+env.suffix+`","description":"","parent_id":null}`))
		})
		check("update term", evs, "term.saved")
		if !strings.HasSuffix(fmt.Sprint(evs[0].Data["url"]), "/events-moved-"+env.suffix+"/") {
			t.Errorf("term.saved after a slug change: %s", evs[0].raw)
		}
	})

	t.Run("settings", func(t *testing.T) {
		withSettingsRestored(t, env)
		evs := env.fired(func() {
			ok(env.do("admin", http.MethodPut, "/settings/plugins",
				`{"enabled":{"progress":true},"settings":{"progress":{"height":5,"color":"#abcdef","placement":"auto","posts_only":true}}}`))
		})
		check("settings PUT", evs, "settings.changed")
		fields, _ := evs[0].Data["fields"].([]any)
		if evs[0].Data["group"] != "plugins" || len(fields) == 0 ||
			strings.Contains(evs[0].raw, "abcdef") || strings.Contains(evs[0].raw, "true") {
			t.Errorf("settings.changed must name fields, not values: %s", evs[0].raw)
		}
		evs = env.fired(func() {
			ok(env.do("admin", http.MethodPut, "/spam/blocklist", `{"entries":{"domain":["`+eventsDomain+`"]}}`))
		})
		check("blocklist PUT", evs, "settings.changed")
		if fields, _ := evs[0].Data["fields"].([]any); evs[0].Data["group"] != "spam_blocklist" || !slices.Contains(fields, any("domain")) ||
			strings.Contains(evs[0].raw, eventsDomain) {
			t.Errorf("blocklist settings.changed: %s", evs[0].raw)
		}
	})

	t.Run("login and media", func(t *testing.T) {
		hash, err := bcrypt.GenerateFromPassword([]byte("Events-horse-9"), bcrypt.MinCost)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := env.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, env.ids["author"], string(hash)); err != nil {
			t.Fatal(err)
		}
		evs := env.fired(func() {
			ok(env.do("", http.MethodPost, "/signin", `{"login":"author-`+env.suffix+`","password":"Events-horse-9"}`))
		})
		check("sign in", evs, "user.login")
		if evs[0].Data["user_id"] != env.ids["author"].String() || evs[0].Data["role"] != "author" ||
			strings.Contains(evs[0].raw, "@example.com") {
			t.Errorf("user.login data: %s", evs[0].raw)
		}

		env.deps.storage = &mediaFakeStorage{objects: map[string][]byte{}}
		var it struct{ ID int64 }
		evs = env.fired(func() {
			r := mediaSend(env, "author", http.MethodPost, "/media", "events.png", mediaPNG(t, 4, 3, env.suffix))
			ok(r)
			r.json(t, &it)
			*mediaIDs = append(*mediaIDs, it.ID)
		})
		check("upload", evs, "media.uploaded")
		if d := evs[0].Data; d["width"] != float64(4) || d["height"] != float64(3) || d["mime"] != "image/png" ||
			!strings.HasPrefix(fmt.Sprint(d["url"]), testOrigin+"/") {
			t.Errorf("media.uploaded data: %s", evs[0].raw)
		}
		evs = env.fired(func() {
			ok(mediaSend(env, "author", http.MethodPost, "/media", "again.png", mediaPNG(t, 4, 3, env.suffix)))
		})
		check("duplicate upload", evs, "")
	})

	t.Run("comments", func(t *testing.T) {
		var postID int64
		if err := env.pool.QueryRow(ctx, `SELECT id FROM posts WHERE legacy_key = 'wp:post:123'`).Scan(&postID); err != nil {
			t.Fatal(err)
		}
		signer, _ := spam.NewSigner([]byte(strings.Repeat("k", 32)))
		svc := comments.New(env.pool, env.site, signer, t.Logf)
		email := "pii-" + env.suffix + "@" + eventsDomain
		body := "A perfectly ordinary comment about release notes " + env.suffix
		in := comments.Input{PostID: postID, Token: signer.FormToken(postID, time.Now().Add(-time.Minute)), Name: "Events Tester",
			Email: email, Body: body, IP: "198.18.9.9", UserAgent: "EventsAgent/1.0"}
		var res *comments.Result
		evs := env.fired(func() {
			var err error
			if res, err = svc.Create(ctx, in); err != nil {
				t.Fatal(err)
			}
		})
		check("public comment", evs, "comment.created")
		for _, pii := range []string{email, env.suffix, "198.18.9.9", "EventsAgent"} {
			if strings.Contains(evs[0].raw, pii) {
				t.Errorf("comment.created leaks %q: %s", pii, evs[0].raw)
			}
		}
		if evs[0].Data["author_name"] != "Events Tester" || evs[0].Data["status"] != res.Status || evs[0].Data["parent_id"] != nil {
			t.Errorf("comment.created data: %s", evs[0].raw)
		}

		spamIn := in
		spamIn.Honeypot, spamIn.Email, spamIn.IP = "x", "bot-"+env.suffix+"@"+eventsDomain, "198.18.9.10"
		evs = env.fired(func() {
			if r, err := svc.Create(ctx, spamIn); err != nil || r.Status != "spam" {
				t.Fatalf("honeypot: %+v %v", r, err)
			}
		})
		check("spam comment", evs, "")

		parent := env.addComment(postID, "approved", "Parent for events "+eventsDomain)
		evs = env.fired(func() {
			ok(env.do("editor", http.MethodPost, fmt.Sprintf("/comments/%d/reply", parent), `{"body":"Staff reply `+eventsDomain+`"}`))
		})
		check("reply", evs, "comment.created")
		if evs[0].Data["parent_id"] != float64(parent) || evs[0].Data["status"] != "approved" {
			t.Errorf("reply data: %s", evs[0].raw)
		}

		bulk := func(action string) []firedEvent {
			return env.fired(func() {
				ok(env.do("editor", http.MethodPost, "/comments/bulk", fmt.Sprintf(`{"ids":[%d],"action":%q}`, res.ID, action)))
			})
		}
		evs = bulk("trash")
		check("trash comment", evs, "comment.status_changed")
		if evs[0].Data["from"] != res.Status || evs[0].Data["to"] != "trash" {
			t.Errorf("status_changed data: %s", evs[0].raw)
		}
		check("trash again", bulk("trash"), "")
		evs = bulk("delete")
		check("delete comment", evs, "comment.status_changed")
		if evs[0].Data["from"] != "trash" || evs[0].Data["to"] != "deleted" {
			t.Errorf("delete data: %s", evs[0].raw)
		}
	})
}
