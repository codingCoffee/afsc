package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if dbURL == "" {
		t.Skip("AFSC_TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(context.Background(), dbURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// testKind is a kind no other test or process handles, removed after the test.
func testKind(t *testing.T, pool *pgxpool.Pool) string {
	kind := "test." + uuid.NewString()[:8]
	t.Cleanup(func() {
		if _, err := pool.Exec(context.Background(), `DELETE FROM jobs WHERE kind = $1`, kind); err != nil {
			t.Error(err)
		}
	})
	return kind
}

func waitFor(t *testing.T, what string, limit time.Duration, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(limit); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", what)
}

func pending(t *testing.T, pool *pgxpool.Pool, kind string) int {
	var n int
	if err := pool.QueryRow(context.Background(), `SELECT count(*) FROM jobs WHERE kind = $1 AND done_at IS NULL`, kind).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestTwoWorkersNeverRunTheSameJob(t *testing.T) {
	pool := testPool(t)
	kind := testKind(t, pool)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	const n = 60
	for i := range n {
		if err := Enqueue(ctx, pool, kind, map[string]int{"i": i}, time.Time{}); err != nil {
			t.Fatal(err)
		}
	}
	var mu sync.Mutex
	runs := map[int]int{}
	handler := func(_ context.Context, p json.RawMessage) error {
		var v struct{ I int }
		if err := json.Unmarshal(p, &v); err != nil {
			return err
		}
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		runs[v.I]++
		mu.Unlock()
		return nil
	}
	var wg sync.WaitGroup
	for range 2 {
		w := &Worker{DB: pool, Concurrency: 4, Poll: 20 * time.Millisecond}
		w.Handle(kind, handler)
		wg.Go(func() { w.Run(ctx) })
	}
	waitFor(t, "all jobs", 20*time.Second, func() bool { return pending(t, pool, kind) == 0 })
	cancel()
	wg.Wait()
	mu.Lock()
	defer mu.Unlock()
	if len(runs) != n {
		t.Errorf("%d distinct jobs ran, want %d", len(runs), n)
	}
	for i, c := range runs {
		if c != 1 {
			t.Errorf("job %d ran %d times", i, c)
		}
	}
}

func TestRetriesUntilMaxAttempts(t *testing.T) {
	pool := testPool(t)
	kind := testKind(t, pool)
	ctx := context.Background()
	if err := Enqueue(ctx, pool, kind, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var backoffs []int
	w := &Worker{DB: pool, Backoff: func(attempt int) time.Duration {
		backoffs = append(backoffs, attempt)
		return 0
	}}
	w.Handle(kind, func(context.Context, json.RawMessage) error {
		calls.Add(1)
		return errors.New("boom")
	})
	for range 20 {
		if ran, err := w.RunOnce(ctx); err != nil {
			t.Fatal(err)
		} else if !ran {
			break
		}
	}
	var attempts int
	var lastErr *string
	var done *time.Time
	if err := pool.QueryRow(ctx, `SELECT attempts, last_error, done_at FROM jobs WHERE kind = $1`, kind).Scan(&attempts, &lastErr, &done); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 8 || attempts != 8 || done == nil || lastErr == nil || *lastErr != "boom" {
		t.Errorf("calls %d attempts %d done %v err %v", calls.Load(), attempts, done, lastErr)
	}
	if len(backoffs) != 7 || backoffs[0] != 1 || backoffs[6] != 7 {
		t.Errorf("backoff attempts %v", backoffs)
	}
}

func TestBackoffFailedJobWaits(t *testing.T) {
	pool := testPool(t)
	kind := testKind(t, pool)
	ctx := context.Background()
	if err := Enqueue(ctx, pool, kind, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	w := &Worker{DB: pool}
	w.Handle(kind, func(context.Context, json.RawMessage) error { return errors.New("later") })
	if ran, err := w.RunOnce(ctx); !ran || err != nil {
		t.Fatalf("ran %v err %v", ran, err)
	}
	if ran, err := w.RunOnce(ctx); ran || err != nil {
		t.Errorf("a failed job ran again before its backoff: %v %v", ran, err)
	}
	var secs float64
	if err := pool.QueryRow(ctx, `SELECT extract(epoch FROM run_at - now())::float8 FROM jobs WHERE kind = $1`, kind).Scan(&secs); err != nil {
		t.Fatal(err)
	}
	if wait := time.Duration(secs * float64(time.Second)); wait < 5*time.Second || wait > 15*time.Second {
		t.Errorf("first retry in %s, want 7.5-12.5 s", wait)
	}
}

func TestDefaultBackoff(t *testing.T) {
	for attempt, want := range map[int]time.Duration{1: 10 * time.Second, 2: 20 * time.Second, 4: 80 * time.Second, 20: time.Hour} {
		for range 50 {
			if d := DefaultBackoff(attempt); d < want*3/4 || d >= want*5/4 {
				t.Fatalf("attempt %d: %s outside %s ±25%%", attempt, d, want)
			}
		}
	}
}

func TestPanicIsRecorded(t *testing.T) {
	pool := testPool(t)
	kind := testKind(t, pool)
	ctx := context.Background()
	if err := Enqueue(ctx, pool, kind, nil, time.Time{}); err != nil {
		t.Fatal(err)
	}
	w := &Worker{DB: pool}
	w.Handle(kind, func(context.Context, json.RawMessage) error { panic("handler exploded") })
	if ran, err := w.RunOnce(ctx); !ran || err != nil {
		t.Fatalf("ran %v err %v", ran, err)
	}
	var lastErr string
	if err := pool.QueryRow(ctx, `SELECT last_error FROM jobs WHERE kind = $1`, kind).Scan(&lastErr); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lastErr, "panic: handler exploded") {
		t.Errorf("last_error = %q", lastErr)
	}
}

func TestOneSchedulerLeadsAndAnotherTakesOver(t *testing.T) {
	pool := testPool(t)
	kind := testKind(t, pool)
	key := int64(time.Now().UnixNano())
	mk := func() (*Scheduler, context.CancelFunc, chan struct{}) {
		s := &Scheduler{DB: pool, LockKey: key, Retry: 100 * time.Millisecond,
			Schedules: []Schedule{{Kind: kind, Every: time.Hour}}}
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			s.Run(ctx)
			close(done)
		}()
		return s, cancel, done
	}
	a, stopA, doneA := mk()
	b, stopB, doneB := mk()
	defer func() {
		stopA()
		stopB()
		<-doneA
		<-doneB
	}()
	waitFor(t, "a leader", 5*time.Second, func() bool { return a.Leading() || b.Leading() })
	time.Sleep(300 * time.Millisecond)
	if a.Leading() == b.Leading() {
		t.Fatalf("leading: a %v, b %v", a.Leading(), b.Leading())
	}
	if n := pending(t, pool, kind); n != 1 {
		t.Errorf("%d pending jobs after the leader started, want 1", n)
	}
	leader, follower, stop, done := a, b, stopA, doneA
	if b.Leading() {
		leader, follower, stop, done = b, a, stopB, doneB
	}
	stop()
	<-done
	if leader.Leading() {
		t.Error("stopped scheduler still leads")
	}
	waitFor(t, "takeover", 5*time.Second, follower.Leading)
	if n := pending(t, pool, kind); n != 1 {
		t.Errorf("the new leader enqueued a duplicate: %d pending", n)
	}
}
