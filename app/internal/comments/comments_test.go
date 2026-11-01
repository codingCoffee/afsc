package comments

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/importer"
	"afsc/internal/site"
	"afsc/internal/spam"
	"afsc/internal/theme"
)

type discard struct{}

func (discard) Put(_ context.Context, _ string, body io.ReadSeeker, _ int64, _ string) error {
	_, err := io.Copy(io.Discard, body)
	return err
}

// testDomain marks every comment these tests create, so cleanup can find them.
const testDomain = "m4test.example"

type env struct {
	t      *testing.T
	ctx    context.Context
	pool   *pgxpool.Pool
	site   *site.Site
	svc    *Service
	signer *spam.Signer
	now    time.Time
	postID int64 // wp:post:123, published and open
	pageID int64 // wp:post:2
}

func newEnv(t *testing.T) *env {
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
	signer, _ := spam.NewSigner([]byte("test-secret"))
	e := &env{t: t, ctx: ctx, pool: pool, site: st, signer: signer, now: time.Now()}
	e.svc = New(pool, st, signer, t.Logf)
	e.svc.Now = func() time.Time { return e.now }
	for key, dst := range map[string]*int64{"wp:post:123": &e.postID, "wp:post:2": &e.pageID} {
		if err := pool.QueryRow(ctx, `SELECT id FROM posts WHERE legacy_key = $1`, key).Scan(dst); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		bg := context.Background()
		for _, sql := range []string{
			`DELETE FROM spam_archive WHERE comment_key IN (SELECT 'afsc:comment:' || id FROM comments WHERE author_email LIKE '%@' || $1)`,
			`DELETE FROM comments WHERE author_email LIKE '%@' || $1`,
			`DELETE FROM spam_archive WHERE comment_key LIKE 'afsc:comment:%' AND tokens @> ARRAY['from:' || $1]`,
			`DELETE FROM spam_blocklist WHERE value LIKE '%' || $1`,
			`UPDATE posts SET comment_status = 'open' WHERE legacy_key = 'wp:post:123' AND $1 <> ''`,
		} {
			if _, err := pool.Exec(bg, sql, testDomain); err != nil {
				t.Error(err)
			}
		}
		if _, err := spam.Retrain(bg, pool); err != nil {
			t.Error(err)
		}
	})
	return e
}

var seq int

// input is a valid submission from a fresh email, with a token issued a minute ago.
func (e *env) input(body string) Input {
	seq++
	return Input{PostID: e.postID, Token: e.signer.FormToken(e.postID, e.now.Add(-time.Minute)),
		Name: "Tester", Email: fmt.Sprintf("u%d-%d@%s", seq, time.Now().UnixNano(), testDomain), Body: body,
		IP: fmt.Sprintf("203.0.113.%d", seq%250+1), UserAgent: "test"}
}

func (e *env) mustCreate(in Input) *Result {
	e.t.Helper()
	r, err := e.svc.Create(e.ctx, in)
	if err != nil {
		e.t.Fatalf("create: %v", describe(err))
	}
	return r
}

func describe(err error) string {
	var v *ValidationError
	if errors.As(err, &v) {
		return fmt.Sprintf("%v %v", err, v.Fields)
	}
	return err.Error()
}

// insert writes a comment directly, bypassing the pipeline.
func (e *env) insert(parent int64, status, body string, created time.Time) int64 {
	e.t.Helper()
	var id int64
	if err := e.pool.QueryRow(e.ctx, `
INSERT INTO comments (post_id, parent_id, author_name, author_email, body, status, ip, user_agent, created_at)
VALUES ($1, nullif($2::bigint, 0), 'Direct', $3, $4, $5, '192.0.2.200', 'ua', $6) RETURNING id`,
		e.postID, parent, fmt.Sprintf("direct%d@%s", time.Now().UnixNano(), testDomain), body, status, created).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	return id
}

func (e *env) status(id int64) string {
	e.t.Helper()
	var s string
	if err := e.pool.QueryRow(e.ctx, `SELECT status FROM comments WHERE id = $1`, id).Scan(&s); err != nil {
		e.t.Fatal(err)
	}
	return s
}

func TestCreate(t *testing.T) {
	e := newEnv(t)
	r := e.mustCreate(e.input("A thoughtful first comment."))
	if r.Status != "pending" {
		t.Fatalf("untrusted comment: %s, want pending", r.Status)
	}
	var ip, ua *string
	var reasons []byte
	if err := e.pool.QueryRow(e.ctx, `SELECT host(ip), user_agent, spam_reasons FROM comments WHERE id = $1`, r.ID).
		Scan(&ip, &ua, &reasons); err != nil {
		t.Fatal(err)
	}
	if ip == nil || ua == nil || string(reasons) == "" {
		t.Fatalf("ip %v ua %v reasons %s", ip, ua, reasons)
	}

	t.Run("bad token", func(t *testing.T) {
		in := e.input("Hello there")
		in.Token = e.signer.FormToken(e.pageID, e.now.Add(-time.Minute))
		if _, err := e.svc.Create(e.ctx, in); !errors.Is(err, ErrBadToken) {
			t.Fatalf("token for another post: %v", err)
		}
		in.Token = "garbage"
		if _, err := e.svc.Create(e.ctx, in); !errors.Is(err, ErrBadToken) {
			t.Fatalf("garbage token: %v", err)
		}
	})
	t.Run("validation", func(t *testing.T) {
		in := e.input("x")
		in.Email = "not-an-email"
		in.URL = "javascript:alert(1)"
		in.Name = ""
		_, err := e.svc.Create(e.ctx, in)
		var v *ValidationError
		if !errors.As(err, &v) || len(v.Fields) != 4 {
			t.Fatalf("want 4 field errors, got %v", describe(err))
		}
	})
	t.Run("honeypot and too fast are stored as spam", func(t *testing.T) {
		in := e.input("I am a bot")
		in.Honeypot = "http://bot.example"
		if r := e.mustCreate(in); r.Status != "spam" {
			t.Fatalf("honeypot: %s", r.Status)
		}
		in = e.input("I am fast")
		in.Token = e.signer.FormToken(e.postID, e.now.Add(-time.Second))
		if r := e.mustCreate(in); r.Status != "spam" {
			t.Fatalf("too fast: %s", r.Status)
		}
	})
	t.Run("closed", func(t *testing.T) {
		if _, err := e.pool.Exec(e.ctx, `UPDATE posts SET comment_status = 'closed' WHERE id = $1`, e.postID); err != nil {
			t.Fatal(err)
		}
		defer e.pool.Exec(e.ctx, `UPDATE posts SET comment_status = 'open' WHERE id = $1`, e.postID)
		if _, err := e.svc.Create(e.ctx, e.input("Is this closed?")); !errors.Is(err, ErrClosed) {
			t.Fatalf("closed post: %v", err)
		}
	})
}

func TestRateLimit(t *testing.T) {
	e := newEnv(t)
	email := fmt.Sprintf("rate%d@%s", time.Now().UnixNano(), testDomain)
	for i := range 3 {
		in := e.input(fmt.Sprintf("Comment number %d from one email", i))
		in.Email = email
		e.mustCreate(in)
	}
	in := e.input("One more from the same email")
	in.Email = email
	if _, err := e.svc.Create(e.ctx, in); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("4th by email: %v", err)
	}
	for i := range 5 {
		in := e.input(fmt.Sprintf("Comment number %d from one address", i))
		in.IP = "198.18.0.9"
		e.mustCreate(in)
	}
	in = e.input("One more from the same address")
	in.IP = "198.18.0.9"
	if _, err := e.svc.Create(e.ctx, in); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("6th by IP: %v", err)
	}
}

func TestDuplicateTrustedAndBlocklist(t *testing.T) {
	e := newEnv(t)
	body := fmt.Sprintf("Unique body %d for duplicate testing", time.Now().UnixNano())
	first := e.mustCreate(e.input(body))
	if _, err := e.svc.SetStatus(e.ctx, []int64{first.ID}, "approved"); err != nil {
		t.Fatal(err)
	}
	dup := e.mustCreate(e.input("  " + strings.ToUpper(body) + "\n"))
	var reasons []spam.Reason
	var raw []byte
	if err := e.pool.QueryRow(e.ctx, `SELECT spam_reasons FROM comments WHERE id = $1`, dup.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	_ = json.Unmarshal(raw, &reasons)
	if len(reasons) == 0 || reasons[0].Check != "duplicate" || dup.Status != "pending" {
		t.Fatalf("duplicate: %s %+v", dup.Status, reasons)
	}

	var email string
	if err := e.pool.QueryRow(e.ctx, `SELECT author_email FROM comments WHERE id = $1`, first.ID).Scan(&email); err != nil {
		t.Fatal(err)
	}
	in := e.input("Back again with another plain remark.")
	in.Email = email
	if r := e.mustCreate(in); r.Status != "approved" {
		t.Fatalf("trusted commenter: %s", r.Status)
	}

	if _, err := e.pool.Exec(e.ctx, `INSERT INTO spam_blocklist (kind, value) VALUES ('domain', $1)`, "bad."+testDomain); err != nil {
		t.Fatal(err)
	}
	in = e.input("Visit https://bad." + testDomain + "/offer today")
	in.Email = email
	if r := e.mustCreate(in); r.Status != "pending" {
		t.Fatalf("blocklisted link from a trusted email: %s, want pending (score 5)", r.Status)
	}
}

func TestCheckFailsClosed(t *testing.T) {
	e := newEnv(t)
	cases := map[string]CheckFunc{
		"error": func(context.Context, spam.Querier, spam.Input) (spam.Verdict, error) {
			return spam.Verdict{}, errors.New("boom")
		},
		"panic": func(context.Context, spam.Querier, spam.Input) (spam.Verdict, error) { panic("boom") },
		"timeout": func(ctx context.Context, _ spam.Querier, _ spam.Input) (spam.Verdict, error) {
			<-ctx.Done()
			time.Sleep(10 * time.Millisecond)
			return spam.Verdict{Score: -100}, nil
		},
	}
	for name, check := range cases {
		e.svc.Check = check
		r := e.mustCreate(e.input("Checking the failure mode " + name))
		var raw []byte
		if err := e.pool.QueryRow(e.ctx, `SELECT spam_reasons FROM comments WHERE id = $1`, r.ID).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		if r.Status != "pending" || !strings.Contains(string(raw), "check_failed") {
			t.Errorf("%s: status %s reasons %s", name, r.Status, raw)
		}
	}
	e.svc.Check = spam.Check
}

func TestReparentPastThreadDepth(t *testing.T) {
	e := newEnv(t)
	root := e.insert(0, "approved", "root", e.now)
	d2 := e.insert(root, "approved", "depth two", e.now)
	d3 := e.insert(d2, "approved", "depth three", e.now)
	pending := e.insert(root, "pending", "pending parent", e.now)

	in := e.input("Reply to depth two")
	in.ParentID = d2
	if r := e.mustCreate(in); r.ParentID != d2 {
		t.Fatalf("reply within depth: parent %d, want %d", r.ParentID, d2)
	}
	in = e.input("Reply to depth three")
	in.ParentID = d3
	if r := e.mustCreate(in); r.ParentID != d2 {
		t.Fatalf("reply past depth 3: parent %d, want %d", r.ParentID, d2)
	}
	in = e.input("Reply to a pending comment")
	in.ParentID = pending
	var v *ValidationError
	if _, err := e.svc.Create(e.ctx, in); !errors.As(err, &v) {
		t.Fatalf("pending parent: %v", err)
	}
}

// tokenCounts reads spam_tokens, ignoring rows with no counts (incremental training may leave them behind).
func tokenCounts(t *testing.T, e *env) map[string]spam.Counts {
	t.Helper()
	rows, err := e.pool.Query(e.ctx, `SELECT token, spam, ham FROM spam_tokens WHERE spam <> 0 OR ham <> 0`)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]spam.Counts{}
	var tok string
	var c spam.Counts
	if _, err := pgx.ForEachRow(rows, []any{&tok, &c.Spam, &c.Ham}, func() error {
		out[tok] = c
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestIncrementalTrainingMatchesRetrain(t *testing.T) {
	e := newEnv(t)
	if _, err := spam.Retrain(e.ctx, e.pool); err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for i := range 6 {
		ids = append(ids, e.mustCreate(e.input(fmt.Sprintf("Training message %d with words alpha beta gamma%d", i, i))).ID)
	}
	steps := []struct {
		ids    []int64
		status string
	}{
		{ids[:3], "spam"}, {ids[3:], "approved"}, {ids[1:2], "approved"}, {ids[3:4], "trash"},
		{ids[4:5], "spam"}, {ids[0:1], "pending"}, {ids[1:2], "spam"},
	}
	for _, s := range steps {
		if _, err := e.svc.SetStatus(e.ctx, s.ids, s.status); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := e.svc.Delete(e.ctx, []int64{ids[3], ids[4]}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.svc.Delete(e.ctx, []int64{ids[5]}); !errors.Is(err, ErrNotDeletable) {
		t.Fatalf("delete approved: %v", err)
	}
	if _, err := e.svc.Reply(e.ctx, staffUser(t, e), ids[5], "Thanks for the training data"); err != nil {
		t.Fatal(err)
	}
	incremental := tokenCounts(t, e)
	if _, err := spam.Retrain(e.ctx, e.pool); err != nil {
		t.Fatal(err)
	}
	rebuilt := tokenCounts(t, e)
	if !maps.Equal(incremental, rebuilt) {
		for k, v := range rebuilt {
			if incremental[k] != v {
				t.Errorf("token %q: incremental %+v, rebuilt %+v", k, incremental[k], v)
			}
		}
		for k, v := range incremental {
			if _, ok := rebuilt[k]; !ok {
				t.Errorf("token %q: incremental %+v, missing after rebuild", k, v)
			}
		}
	}
}

func staffUser(t *testing.T, e *env) uuid.UUID {
	t.Helper()
	name := "m4staff-" + uuid.NewString()[:8]
	var id uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `INSERT INTO users (username, email, role) VALUES ($1, $2, 'editor') RETURNING id`,
		name, name+"@"+testDomain).Scan(&id); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = e.pool.Exec(bg, `DELETE FROM comments WHERE user_id = $1`, id)
		_, _ = e.pool.Exec(bg, `DELETE FROM users WHERE id = $1`, id)
	})
	return id
}

func TestReply(t *testing.T) {
	e := newEnv(t)
	parent := e.insert(0, "approved", "question?", e.now)
	user := staffUser(t, e)
	r, err := e.svc.Reply(e.ctx, user, parent, "Staff answer")
	if err != nil {
		t.Fatal(err)
	}
	var uid uuid.UUID
	if err := e.pool.QueryRow(e.ctx, `SELECT user_id FROM comments WHERE id = $1`, r.ID).Scan(&uid); err != nil {
		t.Fatal(err)
	}
	if r.Status != "approved" || r.ParentID != parent || uid != user {
		t.Fatalf("reply: %+v user %v", r, uid)
	}
}

func TestRetention(t *testing.T) {
	e := newEnv(t)
	// a "now" ten years back keeps the sample bundle's 2021 comments out of reach
	now := time.Now().AddDate(-10, 0, 0)
	old := now.AddDate(-1, 0, 0)
	oldSpam := e.insert(0, "spam", "ancient zzretentiontoken offer", old)
	oldHam := e.insert(0, "approved", "ancient but fine", old)
	recentSpam := e.insert(0, "spam", "recent spam", now.AddDate(0, 0, -1))

	res, err := spam.Retain(e.ctx, e.pool, now, 30, 90)
	if err != nil {
		t.Fatal(err)
	}
	if res.SpamDeleted != 1 || res.IPCleared < 1 {
		t.Fatalf("first run: %+v", res)
	}
	var n int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM comments WHERE id = $1`, oldSpam).Scan(&n)
	if n != 0 || e.status(recentSpam) != "spam" {
		t.Fatalf("old spam left %d, recent spam %s", n, e.status(recentSpam))
	}
	var ip *string
	_ = e.pool.QueryRow(e.ctx, `SELECT host(ip) FROM comments WHERE id = $1`, oldHam).Scan(&ip)
	if ip != nil {
		t.Fatalf("old comment kept ip %s", *ip)
	}
	if res, err := spam.Retain(e.ctx, e.pool, now, 30, 90); err != nil || res.SpamDeleted != 0 || res.IPCleared != 0 {
		t.Fatalf("second run changed %+v (%v)", res, err)
	}
	if _, err := spam.Retrain(e.ctx, e.pool); err != nil {
		t.Fatal(err)
	}
	var c spam.Counts
	if err := e.pool.QueryRow(e.ctx, `SELECT spam, ham FROM spam_tokens WHERE token = 'zzretentiontoken'`).Scan(&c.Spam, &c.Ham); err != nil {
		t.Fatal(err)
	}
	if c.Spam != 1 {
		t.Fatalf("archived spam lost after retrain: %+v", c)
	}
	_, _ = e.pool.Exec(e.ctx, `DELETE FROM spam_archive WHERE comment_key = $1`, fmt.Sprintf("afsc:comment:%d", oldSpam))
}

// TestImportSeedsClassifier is the §19 M4 acceptance check: importing labelled comments queues spam.retrain,
// which trains the classifier until it scores held-out messages.
func TestImportSeedsClassifier(t *testing.T) {
	e := newEnv(t)
	var queued int
	if err := e.pool.QueryRow(e.ctx, `SELECT count(*) FROM jobs WHERE kind = 'spam.retrain' AND done_at IS NULL`).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued == 0 {
		t.Fatal("import did not queue spam.retrain")
	}
	msgs, err := spam.Retrain(e.ctx, e.pool)
	if err != nil {
		t.Fatal(err)
	}
	var labelled, archived int
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM comments WHERE status IN ('approved', 'spam')`).Scan(&labelled)
	_ = e.pool.QueryRow(e.ctx, `SELECT count(*) FROM spam_archive`).Scan(&archived)
	if msgs.Spam+msgs.Ham != labelled+archived {
		t.Fatalf("trained on %+v, want %d labelled + %d archived", msgs, labelled, archived)
	}
	var c spam.Counts
	if err := e.pool.QueryRow(e.ctx, `SELECT spam, ham FROM spam_tokens WHERE token = 'domain:spam.example.net'`).Scan(&c.Spam, &c.Ham); err != nil || c.Spam < 1 {
		t.Fatalf("sample spam not learnt: %+v %v", c, err)
	}

	dir := writeCorpusBundle(t, 220, 220)
	tm, _ := theme.LoadManifest("../../themes/default")
	t.Cleanup(func() {
		bg := context.Background()
		_, _ = e.pool.Exec(bg, `DELETE FROM posts WHERE legacy_key LIKE 't4:%'`)
		_, _ = e.pool.Exec(bg, `DELETE FROM spam_archive WHERE comment_key LIKE 't4:%'`)
		_, _ = e.pool.Exec(bg, `DELETE FROM legacy_records WHERE legacy_key LIKE 't4:%'`)
	})
	opts := importer.Options{BundleDir: dir, Theme: tm, Pool: e.pool, Storage: discard{}}
	if rep, err := importer.Run(e.ctx, opts); err != nil {
		t.Fatalf("corpus import: %v %+v", err, rep.Errors)
	}
	if _, err := spam.Retrain(e.ctx, e.pool); err != nil {
		t.Fatal(err)
	}
	for body, want := range map[string]int{
		"cheap casino bonus pills winner": 3,
		"thanks for the careful analysis": -3,
	} {
		tokens := spam.Tokens(body, "", "")
		corpus, err := spam.LoadCorpus(e.ctx, e.pool, tokens)
		if err != nil {
			t.Fatal(err)
		}
		if !corpus.Active() {
			t.Fatalf("classifier inactive with %+v", corpus.Messages)
		}
		got := spam.ClassifierScore(corpus.Probability(tokens))
		if (want > 0 && got < want) || (want < 0 && got != want) {
			t.Errorf("%q scored %d, want %d", body, got, want)
		}
	}
	second, err := importer.Run(e.ctx, opts)
	if err != nil {
		t.Fatal(err)
	}
	if c := second.Counts["comments"]; c != nil && (c.Created != 0 || c.Updated != 0) {
		t.Errorf("corpus re-import changed comments: %+v", *c)
	}
}

// writeCorpusBundle writes a bundle with one post and nSpam + nHam labelled comments from two vocabularies.
func writeCorpusBundle(t *testing.T, nSpam, nHam int) string {
	t.Helper()
	dir := t.TempDir()
	write := func(name string, lines []string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("manifest.json", []string{fmt.Sprintf(`{"format_version":1,"source":"t4","generated_at":"2026-01-01T00:00:00Z",`+
		`"counts":{"posts":1,"comments":%d},"theme":"default"}`, nSpam+nHam)})
	write("posts.ndjson", []string{`{"legacy_key":"t4:post:1","type":"post","status":"draft","slug":"t4-corpus",` +
		`"title":"Corpus","excerpt":"","content":{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]},` +
		`"created_at":"2026-01-01T00:00:00Z","updated_at":"2026-01-01T00:00:00Z","comment_status":"open"}`})
	var lines []string
	add := func(i int, status, name, email, body string) {
		b, _ := json.Marshal(map[string]any{"legacy_key": fmt.Sprintf("t4:comment:%d", i), "post": "t4:post:1",
			"author_name": name, "author_email": email, "author_url": "", "ip": "", "user_agent": "", "body": body,
			"status": status, "created_at": time.Now().UTC().Format(time.RFC3339)})
		lines = append(lines, string(b))
	}
	for i := range nSpam {
		add(i, "spam", "Promo", fmt.Sprintf("promo%d@pills.example", i),
			fmt.Sprintf("Cheap pills casino bonus winner click https://pills.example/%d now", i))
	}
	for i := range nHam {
		add(nSpam+i, "approved", "Reader", fmt.Sprintf("reader%d@mail.example", i),
			fmt.Sprintf("Thanks for the careful analysis of the markets, reader note %d", i))
	}
	write("comments.ndjson", lines)
	return dir
}
