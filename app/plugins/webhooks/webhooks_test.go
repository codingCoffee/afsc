package webhooks

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/plugins"
)

func TestSeal(t *testing.T) {
	k1, k2 := []byte(strings.Repeat("a", 32)), []byte(strings.Repeat("b", 32))
	s := newSecret()
	if !strings.HasPrefix(s, "whsec_") || len(s) != len("whsec_")+43 || hint(s) != s[len(s)-4:] {
		t.Fatalf("secret %q", s)
	}
	enc, err := seal(k1, 7, s)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := unseal(k1, 7, enc); err != nil || got != s {
		t.Errorf("round trip: %q %v", got, err)
	}
	if _, err := unseal(k2, 7, enc); !errors.Is(err, ErrSecretUnreadable) {
		t.Errorf("wrong key: %v", err)
	}
	if _, err := unseal(k1, 8, enc); !errors.Is(err, ErrSecretUnreadable) {
		t.Errorf("another endpoint's id: %v", err)
	}
}

func TestBackoff(t *testing.T) {
	for attempt, want := range map[int]time.Duration{1: time.Minute, 2: 4 * time.Minute, 3: 16 * time.Minute,
		5: 256 * time.Minute, 6: 12 * time.Hour, 7: 12 * time.Hour} {
		for range 50 {
			if d := Backoff(attempt); d < want*3/4 || d >= want*5/4 {
				t.Fatalf("attempt %d: %s outside %s ±25%%", attempt, d, want)
			}
		}
	}
}

type env struct {
	t    *testing.T
	pool *pgxpool.Pool
	p    *Plugin
	rt   *plugins.Runtime
	srv  *httptest.Server

	mu     sync.Mutex
	status int
	reqs   []*http.Request
	bodies []string
}

func newEnv(t *testing.T) *env {
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{t: t, pool: pool, status: http.StatusOK}
	e.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.mu.Lock()
		e.reqs, e.bodies = append(e.reqs, r), append(e.bodies, string(b))
		status := e.status
		e.mu.Unlock()
		if status == http.StatusMovedPermanently {
			http.Redirect(w, r, "/elsewhere", status)
			return
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(strings.Repeat("x", 3000)))
	}))
	client := e.srv.Client()
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	e.p = &Plugin{Client: client, Now: time.Now}
	e.rt, err = plugins.Build(ctx, plugins.Config{DB: pool, BaseURL: "https://blog.example.com",
		Secret: []byte(strings.Repeat("k", 32)), Logf: t.Logf}, []plugins.Plugin{e.p})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		e.srv.Close()
		_, _ = pool.Exec(ctx, `DELETE FROM jobs WHERE kind = $1 AND (payload->>'delivery_id')::bigint IN
			(SELECT d.id FROM plg_webhooks_deliveries d JOIN plg_webhooks_endpoints e ON e.id = d.endpoint_id
			 WHERE e.description = 'webhooks test')`, DeliverKind)
		_, _ = pool.Exec(ctx, `DELETE FROM plg_webhooks_endpoints WHERE description = 'webhooks test'`)
		pool.Close()
	})
	return e
}

// endpoint inserts one directly, so it can point at the loopback test server.
func (e *env) endpoint(events []string, enabled bool) (int64, string) {
	e.t.Helper()
	ctx := context.Background()
	key, err := e.p.h.Key()
	if err != nil {
		e.t.Fatal(err)
	}
	var id int64
	if err := e.pool.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('plg_webhooks_endpoints', 'id'))`).Scan(&id); err != nil {
		e.t.Fatal(err)
	}
	secret := newSecret()
	enc, _ := seal(key, id, secret)
	if _, err := e.pool.Exec(ctx, `INSERT INTO plg_webhooks_endpoints (id, url, description, events, enabled, secret_enc, secret_hint)
VALUES ($1, $2, 'webhooks test', $3, $4, $5, $6)`, id, e.srv.URL+"/hook", events, enabled, enc, hint(secret)); err != nil {
		e.t.Fatal(err)
	}
	return id, secret
}

func (e *env) deliveries(endpointID int64) []int64 {
	e.t.Helper()
	rows, err := e.pool.Query(context.Background(), `SELECT id FROM plg_webhooks_deliveries WHERE endpoint_id = $1 ORDER BY id`, endpointID)
	if err != nil {
		e.t.Fatal(err)
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		_ = rows.Scan(&id)
		out = append(out, id)
	}
	return out
}

func (e *env) state(id int64) (status string, attempts int, code *int, snippet *string) {
	e.t.Helper()
	if err := e.pool.QueryRow(context.Background(), `SELECT status, attempts, last_status_code, response_snippet
FROM plg_webhooks_deliveries WHERE id = $1`, id).Scan(&status, &attempts, &code, &snippet); err != nil {
		e.t.Fatal(err)
	}
	return
}

func (e *env) run(id int64) error {
	b, _ := json.Marshal(map[string]int64{"delivery_id": id})
	return e.p.deliver(context.Background(), b)
}

func event(name string) plugins.Envelope[json.RawMessage] {
	return plugins.Envelope[json.RawMessage]{ID: uuid.New(), Event: name, OccurredAt: time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC),
		Data: json.RawMessage(`{"id":42,"title":"Hello"}`)}
}

func TestFanOut(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	sub, _ := e.endpoint([]string{"post.saved", "post.published"}, true)
	other, _ := e.endpoint([]string{"term.saved"}, true)
	off, _ := e.endpoint([]string{"post.saved"}, false)
	ev := event("post.saved")
	for range 2 {
		if err := e.p.fanOut(ctx, ev); err != nil {
			t.Fatal(err)
		}
	}
	if n := len(e.deliveries(sub)); n != 1 {
		t.Errorf("subscribed endpoint got %d deliveries, want 1 after a re-run", n)
	}
	if len(e.deliveries(other)) != 0 || len(e.deliveries(off)) != 0 {
		t.Error("unsubscribed or disabled endpoint got a delivery")
	}
	var jobsN int
	_ = e.pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE kind = $1 AND (payload->>'delivery_id')::bigint = $2`,
		DeliverKind, e.deliveries(sub)[0]).Scan(&jobsN)
	if jobsN != 1 {
		t.Errorf("%d deliver jobs", jobsN)
	}
}

func TestDeliver(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, secret := e.endpoint([]string{"post.saved"}, true)
	ev := event("post.saved")
	if err := e.p.fanOut(ctx, ev); err != nil {
		t.Fatal(err)
	}
	d := e.deliveries(id)[0]

	e.status = http.StatusInternalServerError
	if err := e.run(d); err == nil {
		t.Error("500 must return an error so the job retries")
	}
	if st, n, code, _ := e.state(d); st != "pending" || n != 1 || code == nil || *code != 500 {
		t.Errorf("after 500: %s %d %v", st, n, code)
	}
	e.status = http.StatusTooManyRequests
	if err := e.run(d); err == nil {
		t.Error("429 must retry")
	}
	e.status = http.StatusOK
	if err := e.run(d); err != nil {
		t.Fatal(err)
	}
	st, n, _, snippet := e.state(d)
	if st != "succeeded" || n != 3 || snippet == nil || len(*snippet) != snippetBytes {
		t.Errorf("after 200: %s %d snippet %d", st, n, len(*snippet))
	}
	if err := e.run(d); err != nil || len(e.reqs) != 3 {
		t.Errorf("a finished delivery was sent again: %v, %d requests", err, len(e.reqs))
	}

	r, body := e.reqs[2], e.bodies[2]
	if body != `{"event":"post.saved","occurred_at":"2026-11-02T10:00:00Z","data":{"id":42,"title":"Hello"}}` {
		t.Errorf("body %s", body)
	}
	for k, want := range map[string]string{"Content-Type": "application/json", "User-Agent": userAgent,
		"X-AFSC-Event": "post.saved", "X-AFSC-Event-Id": ev.ID.String()} {
		if got := r.Header.Get(k); got != want {
			t.Errorf("%s = %q, want %q", k, got, want)
		}
	}
	if r.Header.Get("X-AFSC-Delivery-Id") == "" {
		t.Error("no delivery id header")
	}
	sig := r.Header.Get("X-AFSC-Signature")
	ts := strings.TrimPrefix(strings.SplitN(sig, ",", 2)[0], "t=")
	var unix int64
	_ = json.Unmarshal([]byte(ts), &unix)
	if sig != Sign(secret, unix, []byte(body)) || time.Since(time.Unix(unix, 0)) > time.Minute {
		t.Errorf("signature %s", sig)
	}

	for status, want := range map[int]string{http.StatusNotFound: "failed", http.StatusMovedPermanently: "failed"} {
		if err := e.p.fanOut(ctx, event("post.saved")); err != nil {
			t.Fatal(err)
		}
		ds := e.deliveries(id)
		e.status = status
		if err := e.run(ds[len(ds)-1]); err != nil {
			t.Errorf("%d returned an error (it must not retry): %v", status, err)
		}
		if st, _, _, _ := e.state(ds[len(ds)-1]); st != want {
			t.Errorf("%d: %s", status, st)
		}
	}

	if err := e.p.fanOut(ctx, event("post.saved")); err != nil {
		t.Fatal(err)
	}
	ds := e.deliveries(id)
	last := ds[len(ds)-1]
	_, _ = e.pool.Exec(ctx, `UPDATE plg_webhooks_deliveries SET attempts = $2 WHERE id = $1`, last, MaxAttempts-1)
	e.status = http.StatusBadGateway
	if err := e.run(last); err != nil {
		t.Errorf("the last attempt must not retry: %v", err)
	}
	if st, n, _, _ := e.state(last); st != "failed" || n != MaxAttempts {
		t.Errorf("after the last attempt: %s %d", st, n)
	}
}

func TestDeliverCancelledAndPrune(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, _ := e.endpoint([]string{"post.saved"}, true)
	if err := e.p.fanOut(ctx, event("post.saved")); err != nil {
		t.Fatal(err)
	}
	d := e.deliveries(id)[0]
	_, _ = e.pool.Exec(ctx, `UPDATE plg_webhooks_endpoints SET enabled = false WHERE id = $1`, id)
	if err := e.run(d); err != nil {
		t.Fatal(err)
	}
	if st, _, _, _ := e.state(d); st != "cancelled" || len(e.reqs) != 0 {
		t.Errorf("disabled endpoint: %s, %d requests", st, len(e.reqs))
	}

	_, _ = e.pool.Exec(ctx, `UPDATE plg_webhooks_deliveries SET created_at = now() - interval '31 days' WHERE id = $1`, d)
	_, _ = e.pool.Exec(ctx, `UPDATE plg_webhooks_endpoints SET enabled = true WHERE id = $1`, id)
	if err := e.p.fanOut(ctx, event("post.saved")); err != nil {
		t.Fatal(err)
	}
	if err := e.p.prune(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if ds := e.deliveries(id); len(ds) != 1 || ds[0] == d {
		t.Errorf("prune kept %v", ds)
	}

	_, _ = e.pool.Exec(ctx, `DELETE FROM plg_webhooks_endpoints WHERE id = $1`, id)
	if err := e.run(d); err != nil {
		t.Errorf("a deleted endpoint's delivery: %v", err)
	}
}

func TestUnreadableSecretFails(t *testing.T) {
	e := newEnv(t)
	ctx := context.Background()
	id, _ := e.endpoint([]string{"post.saved"}, true)
	_, _ = e.pool.Exec(ctx, `UPDATE plg_webhooks_endpoints SET secret_enc = '\x00' WHERE id = $1`, id)
	if err := e.p.fanOut(ctx, event("post.saved")); err != nil {
		t.Fatal(err)
	}
	d := e.deliveries(id)[0]
	if err := e.run(d); err != nil {
		t.Fatal(err)
	}
	var st, msg string
	_ = e.pool.QueryRow(ctx, `SELECT status, last_error FROM plg_webhooks_deliveries WHERE id = $1`, d).Scan(&st, &msg)
	if st != "failed" || !strings.Contains(msg, "rotate") || len(e.reqs) != 0 {
		t.Errorf("unreadable secret: %s %q", st, msg)
	}
}
