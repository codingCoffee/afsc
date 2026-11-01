package main

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
)

const apiDomain = "m4api.example"

func newCommentsEnv(t *testing.T) (*adminEnv, int64) {
	t.Helper()
	env := newAdminEnv(t)
	importSample(t, env.pool)
	var postID int64
	if err := env.pool.QueryRow(context.Background(), `SELECT id FROM posts WHERE legacy_key = 'wp:post:123'`).Scan(&postID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx := context.Background()
		for _, q := range []struct {
			sql  string
			args []any
		}{
			{`DELETE FROM spam_archive WHERE tokens @> ARRAY['from:' || $1::text]`, []any{apiDomain}},
			{`DELETE FROM comments WHERE author_email LIKE '%@' || $1::text OR author_email LIKE '%-' || $2::text || '@example.com'`,
				[]any{apiDomain, env.suffix}},
			{`DELETE FROM spam_blocklist WHERE value LIKE '%' || $1::text`, []any{apiDomain}},
			{`DELETE FROM audit_log WHERE action IN ('comment.delete', 'spam.blocklist')`, nil},
			{`DELETE FROM jobs WHERE kind = 'spam.retrain' AND done_at IS NULL`, nil},
		} {
			if _, err := env.pool.Exec(ctx, q.sql, q.args...); err != nil {
				t.Error(err)
			}
		}
	})
	return env, postID
}

func (env *adminEnv) addComment(postID int64, status, body string) int64 {
	env.t.Helper()
	var id int64
	if err := env.pool.QueryRow(context.Background(), `
INSERT INTO comments (post_id, author_name, author_email, body, status, ip, spam_score, spam_reasons)
VALUES ($1, 'API Tester', $2, $3, $4, '192.0.2.44', 5, '[{"check":"duplicate","delta":5}]') RETURNING id`,
		postID, fmt.Sprintf("a%d@%s", time.Now().UnixNano(), apiDomain), body, status).Scan(&id); err != nil {
		env.t.Fatal(err)
	}
	return id
}

func (env *adminEnv) commentStatus(id int64) string {
	env.t.Helper()
	var s string
	if err := env.pool.QueryRow(context.Background(), `SELECT status FROM comments WHERE id = $1`, id).Scan(&s); err != nil {
		return "gone"
	}
	return s
}

func TestCommentsAPI(t *testing.T) {
	env, postID := newCommentsEnv(t)
	a := env.addComment(postID, "pending", "First pending API comment")
	b := env.addComment(postID, "pending", "Second pending API comment")

	t.Run("list", func(t *testing.T) {
		var out struct {
			Items []struct {
				ID          int64                    `json:"id"`
				PostTitle   string                   `json:"post_title"`
				PostURL     string                   `json:"post_url"`
				IP          *string                  `json:"ip"`
				SpamReasons []struct{ Check string } `json:"spam_reasons"`
			} `json:"items"`
			Total  int64            `json:"total"`
			Counts map[string]int64 `json:"counts"`
		}
		r := env.do("editor", http.MethodGet, fmt.Sprintf("/comments?status=pending&post_id=%d&q=pending+API", postID), "")
		r.json(t, &out)
		if r.code != http.StatusOK || out.Total != 2 || len(out.Items) != 2 || out.Items[0].ID != b {
			t.Fatalf("list: %d %s", r.code, r.body)
		}
		it := out.Items[0]
		if it.PostTitle == "" || !strings.HasPrefix(it.PostURL, testOrigin) || it.IP == nil || len(it.SpamReasons) != 1 {
			t.Errorf("row: %+v", it)
		}
		if out.Counts["pending"] < 2 || out.Counts["approved"] < 1 {
			t.Errorf("counts: %+v", out.Counts)
		}
		if r := env.do("editor", http.MethodGet, "/comments?status=nope", ""); r.code != http.StatusBadRequest {
			t.Errorf("bad status: %d", r.code)
		}
		if r := env.do("author", http.MethodGet, "/comments", ""); r.code != http.StatusForbidden {
			t.Errorf("author: %d", r.code)
		}
	})

	t.Run("bulk", func(t *testing.T) {
		do := func(action string, ids ...int64) apiResp {
			parts := make([]string, len(ids))
			for i, id := range ids {
				parts[i] = fmt.Sprint(id)
			}
			return env.do("editor", http.MethodPost, "/comments/bulk",
				fmt.Sprintf(`{"ids":[%s],"action":%q}`, strings.Join(parts, ","), action))
		}
		steps := []struct {
			action string
			want   string
		}{
			{"approve", "approved"}, {"unapprove", "pending"}, {"spam", "spam"}, {"not_spam", "approved"},
			{"trash", "trash"}, {"restore", "pending"},
		}
		for _, s := range steps {
			if r := do(s.action, a); r.code != http.StatusOK || env.commentStatus(a) != s.want {
				t.Fatalf("%s: %d %s, status %s", s.action, r.code, r.body, env.commentStatus(a))
			}
		}
		if r := do("delete", a); r.code != http.StatusConflict {
			t.Errorf("delete pending: %d", r.code)
		}
		do("spam", a, b)
		if r := do("delete", a, b); r.code != http.StatusOK || env.commentStatus(a) != "gone" || env.commentStatus(b) != "gone" {
			t.Errorf("delete spam: %d %s", r.code, r.body)
		}
		var n int
		_ = env.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'comment.delete' AND entity_id = $1`,
			fmt.Sprint(a)).Scan(&n)
		if n != 1 {
			t.Errorf("delete audit entries: %d", n)
		}
		if r := do("explode", b); r.code != http.StatusBadRequest {
			t.Errorf("unknown action: %d", r.code)
		}
		if r := env.do("editor", http.MethodPost, "/comments/bulk", `{"ids":[],"action":"approve"}`); r.code != http.StatusBadRequest {
			t.Errorf("no ids: %d", r.code)
		}
	})

	t.Run("reply", func(t *testing.T) {
		var parent int64
		if err := env.pool.QueryRow(context.Background(), `SELECT id FROM comments WHERE legacy_key = 'wp:comment:999'`).Scan(&parent); err != nil {
			t.Fatal(err)
		}
		var out struct {
			ID     int64  `json:"id"`
			Status string `json:"status"`
		}
		r := env.do("editor", http.MethodPost, fmt.Sprintf("/comments/%d/reply", parent), `{"body":"Staff reply from the API"}`)
		r.json(t, &out)
		if r.code != http.StatusCreated || out.Status != "approved" {
			t.Fatalf("reply: %d %s", r.code, r.body)
		}
		var uid string
		_ = env.pool.QueryRow(context.Background(), `SELECT user_id::text FROM comments WHERE id = $1`, out.ID).Scan(&uid)
		if uid != env.ids["editor"].String() {
			t.Errorf("reply user %q", uid)
		}
		if r := env.do("editor", http.MethodPost, fmt.Sprintf("/comments/%d/reply", parent), `{"body":"x"}`); r.code != http.StatusBadRequest {
			t.Errorf("short reply: %d", r.code)
		}
		if r := env.do("editor", http.MethodPost, "/comments/999999999/reply", `{"body":"hello there"}`); r.code != http.StatusNotFound {
			t.Errorf("missing parent: %d", r.code)
		}
	})
}

func TestSpamBlocklistAPI(t *testing.T) {
	env, _ := newCommentsEnv(t)
	body := `{"entries":{"keyword":[" Casino "],"domain":["Spam.` + apiDomain + `"],"cidr":["198.51.100.9/24"],"email":["bad@` + apiDomain + `"]}}`
	var out struct {
		Entries    map[string][]string `json:"entries"`
		Classifier struct {
			Min    int  `json:"min"`
			Active bool `json:"active"`
		} `json:"classifier"`
	}
	r := env.do("admin", http.MethodPut, "/spam/blocklist", body)
	r.json(t, &out)
	if r.code != http.StatusOK || out.Entries["keyword"][0] != "casino" || out.Entries["domain"][0] != "spam."+apiDomain ||
		out.Entries["cidr"][0] != "198.51.100.0/24" || len(out.Entries["ip"]) != 0 || out.Classifier.Min != 200 {
		t.Fatalf("put: %d %s", r.code, r.body)
	}
	if r := env.do("admin", http.MethodGet, "/spam/blocklist", ""); r.code != http.StatusOK || !strings.Contains(string(r.body), "casino") {
		t.Errorf("get: %d %s", r.code, r.body)
	}
	var audits int
	_ = env.pool.QueryRow(context.Background(), `SELECT count(*) FROM audit_log WHERE action = 'spam.blocklist'`).Scan(&audits)
	if audits == 0 {
		t.Error("blocklist change not audited")
	}
	for _, bad := range []string{`{"entries":{"ip":["300.1.1.1"]}}`, `{"entries":{"other":["x"]}}`, `{"entries":{"domain":["http://x"]}}`} {
		if r := env.do("admin", http.MethodPut, "/spam/blocklist", bad); r.code != http.StatusBadRequest {
			t.Errorf("%s: %d", bad, r.code)
		}
	}
	if r := env.do("editor", http.MethodGet, "/spam/blocklist", ""); r.code != http.StatusForbidden {
		t.Errorf("editor: %d", r.code)
	}
	if r := env.do("admin", http.MethodPut, "/spam/blocklist", `{"entries":{}}`); r.code != http.StatusOK {
		t.Errorf("clear: %d", r.code)
	}
	if r := env.do("admin", http.MethodPost, "/spam/retrain", ""); r.code != http.StatusAccepted {
		t.Errorf("retrain: %d %s", r.code, r.body)
	}
}

func TestDashboardPendingComments(t *testing.T) {
	env, postID := newCommentsEnv(t)
	env.addComment(postID, "pending", "Dashboard pending comment")
	var d struct {
		Pending *struct {
			Count  int64 `json:"count"`
			Latest []struct {
				PostTitle string `json:"post_title"`
			} `json:"latest"`
		} `json:"pending_comments"`
	}
	env.do("editor", http.MethodGet, "/dashboard", "").json(t, &d)
	if d.Pending == nil || d.Pending.Count < 1 || len(d.Pending.Latest) == 0 || d.Pending.Latest[0].PostTitle == "" {
		t.Fatalf("editor dashboard: %+v", d.Pending)
	}
	d.Pending = nil
	env.do("author", http.MethodGet, "/dashboard", "").json(t, &d)
	if d.Pending != nil {
		t.Error("author sees pending comments")
	}
}
