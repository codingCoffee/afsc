package plugins

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/settings"
	"afsc/internal/spam"
)

type execRec struct{ args [][]any }

func (e *execRec) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	e.args = append(e.args, args)
	return pgconn.CommandTag{}, nil
}

type got struct {
	mu  sync.Mutex
	evs []Envelope[PostData]
}

func (g *got) handler(err error) func(context.Context, Envelope[PostData]) error {
	return func(_ context.Context, ev Envelope[PostData]) error {
		g.mu.Lock()
		defer g.mu.Unlock()
		g.evs = append(g.evs, ev)
		return err
	}
}

func TestEmitQueuesOneJobPerEnabledSubscriber(t *testing.T) {
	var a, b got
	rt := build(t,
		&fake{id: "aa", init: func(h *Host) {
			AddAction(h, PostSaved, 0, a.handler(nil))
			AddAction(h, PostSaved, 1, a.handler(nil))
		}},
		&fake{id: "bb", init: func(h *Host) { AddAction(h, PostSaved, 0, b.handler(nil)) }},
		&fake{id: "cc", init: func(h *Host) { AddAction(h, PostDeleted, 0, nil) }},
	)
	ctx := context.Background()
	var rec execRec
	if err := Emit(ctx, rt, &rec, PostPublished, PostData{ID: 1}); err != nil || len(rec.args) != 0 {
		t.Fatalf("no subscribers wrote %d rows: %v", len(rec.args), err)
	}
	if err := Emit(ctx, nil, &rec, PostSaved, PostData{ID: 1}); err != nil || len(rec.args) != 0 {
		t.Fatal("a nil runtime wrote")
	}
	if err := Emit(ctx, rt, &rec, PostSaved, PostData{ID: 7, Title: "Hi"}); err != nil {
		t.Fatal(err)
	}
	if len(rec.args) != 1 {
		t.Fatalf("one insert per emit, got %d", len(rec.args))
	}
	args := rec.args[0]
	if args[0] != ActionKind || args[2] != actionAttempts || strings.Join(args[3].([]string), ",") != "aa,bb" {
		t.Errorf("insert args: %v", args)
	}
	var env Envelope[PostData]
	if err := json.Unmarshal([]byte(args[1].(string)), &env); err != nil || env.Event != "post.saved" || env.Data.ID != 7 ||
		env.ID.String() == "" || env.OccurredAt.IsZero() {
		t.Errorf("envelope %s: %v", args[1], err)
	}

	withSettings(rt, func(s *settings.All) { s.Plugins.Enabled = map[string]bool{"bb": false} })
	rec.args = nil
	if err := Emit(ctx, rt, &rec, PostSaved, PostData{ID: 7}); err != nil || strings.Join(rec.args[0][3].([]string), ",") != "aa" {
		t.Errorf("a disabled subscriber got a job: %v %v", rec.args, err)
	}
}

func actionPayload(t *testing.T, plugin string, ev any) json.RawMessage {
	env, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(actionJob{Plugin: plugin, Envelope: env})
	return b
}

func TestRunAction(t *testing.T) {
	var a, b got
	boom := errors.New("boom")
	rt := build(t,
		&fake{id: "aa", init: func(h *Host) {
			AddAction(h, PostSaved, 5, a.handler(nil))
			AddAction(h, PostSaved, 0, a.handler(nil))
		}},
		&fake{id: "bb", init: func(h *Host) { AddAction(h, PostSaved, 0, b.handler(boom)) }},
		&fake{id: "cc", init: func(h *Host) {
			AddAction(h, PostSaved, 0, func(context.Context, Envelope[PostData]) error { panic("broken") })
		}},
	)
	run := rt.Jobs()[ActionKind].Handler
	if run == nil {
		t.Fatal("no plugins.action handler")
	}
	ctx := context.Background()
	ev := Envelope[PostData]{Event: "post.saved", OccurredAt: time.Now(), Data: PostData{ID: 3}}
	if err := run(ctx, actionPayload(t, "aa", ev)); err != nil || len(a.evs) != 2 || a.evs[0].Data.ID != 3 {
		t.Errorf("aa: %v %+v", err, a.evs)
	}
	if len(b.evs) != 0 {
		t.Error("a job for aa ran bb's handler")
	}
	if err := run(ctx, actionPayload(t, "bb", ev)); !errors.Is(err, boom) {
		t.Errorf("bb error not returned for a retry: %v", err)
	}
	if err := run(ctx, actionPayload(t, "cc", ev)); err == nil || !strings.Contains(err.Error(), "panic") {
		t.Errorf("cc panic: %v", err)
	}
	withSettings(rt, func(s *settings.All) { s.Plugins.Enabled = map[string]bool{"bb": false} })
	if err := run(ctx, actionPayload(t, "bb", ev)); err != nil {
		t.Errorf("a disabled plugin's job should no-op: %v", err)
	}

	if rt := build(t, &fake{id: "aa"}); rt.Jobs()[ActionKind].Handler != nil {
		t.Error("plugins.action registered without actions")
	}
	if _, err := Build(ctx, Config{}, []Plugin{&fake{id: "aa", init: func(h *Host) {
		AddAction(h, Event[PostData]{"post.nope"}, 0, a.handler(nil))
	}}}); err == nil {
		t.Error("an unknown event registered")
	}
}

func TestEmitFollowsTheTransaction(t *testing.T) {
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	var a got
	rt := build(t, &fake{id: "txtest", init: func(h *Host) { AddAction(h, PostSaved, 0, a.handler(nil)) }})
	marker := "tx-" + time.Now().Format("150405.000000")
	count := func() int {
		var n int
		if err := pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = $1 AND payload->'envelope'->'data'->>'title' = $2`,
			ActionKind, marker).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	t.Cleanup(func() {
		_, _ = pool.Exec(ctx, `DELETE FROM jobs WHERE kind = $1 AND payload->'envelope'->'data'->>'title' = $2`, ActionKind, marker)
	})

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Emit(ctx, rt, tx, PostSaved, PostData{ID: 1, Title: marker}); err != nil {
		t.Fatal(err)
	}
	_ = tx.Rollback(ctx)
	if n := count(); n != 0 {
		t.Fatalf("rolled-back emit left %d jobs", n)
	}

	tx, err = pool.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := Emit(ctx, rt, tx, PostSaved, PostData{ID: 1, Title: marker}); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatal(err)
	}
	var plugin string
	var attempts int
	if err := pool.QueryRow(ctx, `SELECT payload->>'plugin', max_attempts FROM jobs WHERE kind = $1
		AND payload->'envelope'->'data'->>'title' = $2`, ActionKind, marker).Scan(&plugin, &attempts); err != nil {
		t.Fatal(err)
	}
	if plugin != "txtest" || attempts != actionAttempts || count() != 1 {
		t.Errorf("committed emit: plugin %q, max_attempts %d, %d jobs", plugin, attempts, count())
	}
}

func verdictAdd(check string, delta int) func(context.Context, spam.Verdict, spam.Input) (spam.Verdict, error) {
	return func(_ context.Context, v spam.Verdict, _ spam.Input) (spam.Verdict, error) {
		return v.Merge(spam.Verdict{Score: delta, Reasons: []spam.Reason{{Check: check, Delta: delta}}}), nil
	}
}

func TestCheckComment(t *testing.T) {
	ctx := context.Background()
	base := &fake{id: "base", init: func(h *Host) { AddFilter(h, CommentCheck, 0, verdictAdd("base", 2)) }}
	extra := &fake{id: "extra", init: func(h *Host) { AddFilter(h, CommentCheck, 1, verdictAdd("extra", 3)) }}

	v := build(t, base, extra).CheckComment(ctx, spam.Input{})
	if v.Score != 5 || len(v.Reasons) != 2 || v.ForcePending {
		t.Errorf("chain: %+v", v)
	}
	if v := (*Runtime)(nil).CheckComment(ctx, spam.Input{}); v.Score != 0 || v.ForcePending {
		t.Errorf("nil runtime: %+v", v)
	}

	hard := &fake{id: "hard", init: func(h *Host) {
		AddFilter(h, CommentCheck, 0, func(_ context.Context, v spam.Verdict, _ spam.Input) (spam.Verdict, error) {
			v.Hard = "spam"
			return v, nil
		})
	}}
	if v := build(t, hard, extra).CheckComment(ctx, spam.Input{}); v.Hard != "spam" || v.Score != 0 {
		t.Errorf("hard verdict didn't stop the chain: %+v", v)
	}

	for name, fn := range map[string]func(context.Context, spam.Verdict, spam.Input) (spam.Verdict, error){
		"error": func(context.Context, spam.Verdict, spam.Input) (spam.Verdict, error) {
			return spam.Verdict{}, errors.New("down")
		},
		"panic": func(context.Context, spam.Verdict, spam.Input) (spam.Verdict, error) { panic("broken") },
		"timeout": func(ctx context.Context, v spam.Verdict, _ spam.Input) (spam.Verdict, error) {
			<-ctx.Done()
			return v, nil
		},
	} {
		bad := &fake{id: "bad", init: func(h *Host) { AddFilter(h, CommentCheck, 1, fn) }}
		v := build(t, base, bad, extra).CheckComment(ctx, spam.Input{})
		last := v.Reasons[len(v.Reasons)-1]
		if !v.ForcePending || v.Score != 2 || last.Check != "check_failed" || !strings.HasPrefix(last.Detail, "bad: ") {
			t.Errorf("%s: not fail-closed: %+v", name, v)
		}
	}

	rt := build(t, base, extra)
	withSettings(rt, func(s *settings.All) { s.Plugins.Enabled = map[string]bool{"extra": false} })
	if v := rt.CheckComment(ctx, spam.Input{}); v.Score != 2 {
		t.Errorf("disabled check ran: %+v", v)
	}
}
