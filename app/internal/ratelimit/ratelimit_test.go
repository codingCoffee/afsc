package ratelimit

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestLockFor(t *testing.T) {
	p := Policy{Max: 5, Window: 15 * time.Minute, MaxLock: 4 * time.Hour}
	want := []time.Duration{15 * time.Minute, 30 * time.Minute, time.Hour, 2 * time.Hour, 4 * time.Hour, 4 * time.Hour}
	for i, w := range want {
		if got := LockFor(p, i+1); got != w {
			t.Errorf("lockout %d: %v, want %v", i+1, got, w)
		}
	}
}

func TestLimiterSharedAcrossInstances(t *testing.T) {
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
	key := "acct:test-" + uuid.NewString()
	t.Cleanup(func() {
		pool.Exec(context.Background(), `DELETE FROM login_failures WHERE key = $1`, key)
		pool.Exec(context.Background(), `DELETE FROM login_lockouts WHERE key = $1`, key)
	})
	now := time.Now()
	clock := func() time.Time { return now }
	a, b := &Limiter{DB: pool, Now: clock}, &Limiter{DB: pool, Now: clock}
	for i := 0; i < 4; i++ {
		l := a
		if i%2 == 1 {
			l = b
		}
		if lock, err := l.Fail(ctx, key, Account); err != nil || lock != 0 {
			t.Fatalf("failure %d: %v %v", i+1, lock, err)
		}
	}
	if lock, err := b.Fail(ctx, key, Account); err != nil || lock != 15*time.Minute {
		t.Fatalf("5th failure: %v %v", lock, err)
	}
	if d, _ := a.Locked(ctx, key); d != 15*time.Minute {
		t.Fatalf("locked for %v on the other instance", d)
	}
	now = now.Add(16 * time.Minute)
	if d, _ := a.Locked(ctx, key); d != 0 {
		t.Fatalf("still locked: %v", d)
	}
	for i := 0; i < 4; i++ {
		a.Fail(ctx, key, Account)
	}
	if lock, _ := a.Fail(ctx, key, Account); lock != 30*time.Minute {
		t.Fatalf("second lockout should double: %v", lock)
	}
}
