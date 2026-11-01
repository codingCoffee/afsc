// Package ratelimit is a Postgres-backed failure limiter, so every replica sees the same counts (§7.1).
package ratelimit

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Policy locks a key once it reaches Max failures inside Window. Each further lockout doubles, up to MaxLock.
// A lockout streak resets when the key has had no lockout for 24 h.
type Policy struct {
	Max     int
	Window  time.Duration
	MaxLock time.Duration
}

var (
	Account = Policy{Max: 5, Window: 15 * time.Minute, MaxLock: 4 * time.Hour}
	IP      = Policy{Max: 50, Window: 15 * time.Minute, MaxLock: 4 * time.Hour}
)

type Limiter struct {
	DB  *pgxpool.Pool
	Now func() time.Time
}

// now is truncated to Postgres' microsecond precision, so durations read back from the DB are exact.
func (l *Limiter) now() time.Time {
	if l.Now != nil {
		return l.Now().Truncate(time.Microsecond)
	}
	return time.Now().Truncate(time.Microsecond)
}

// Locked returns how long key stays locked, or 0.
func (l *Limiter) Locked(ctx context.Context, key string) (time.Duration, error) {
	var until time.Time
	err := l.DB.QueryRow(ctx, `SELECT until FROM login_lockouts WHERE key = $1`, key).Scan(&until)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	if d := until.Sub(l.now()); d > 0 {
		return d, nil
	}
	return 0, nil
}

// Fail records a failure for key and returns the lockout it triggered, or 0.
func (l *Limiter) Fail(ctx context.Context, key string, p Policy) (time.Duration, error) {
	now := l.now()
	tx, err := l.DB.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('afsc_rl:' || $1::text))`, key); err != nil {
		return 0, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO login_failures (key, at) VALUES ($1, $2)`, key, now); err != nil {
		return 0, err
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM login_failures WHERE key = $1 AND at > $2`, key,
		now.Add(-p.Window)).Scan(&n); err != nil {
		return 0, err
	}
	if n < p.Max {
		return 0, tx.Commit(ctx)
	}
	var count int
	var updated time.Time
	err = tx.QueryRow(ctx, `SELECT count, updated_at FROM login_lockouts WHERE key = $1`, key).Scan(&count, &updated)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && now.Sub(updated) > 24*time.Hour) {
		count, err = 0, nil
	}
	if err != nil {
		return 0, err
	}
	count++
	lock := LockFor(p, count)
	if _, err := tx.Exec(ctx, `
INSERT INTO login_lockouts (key, until, count, updated_at) VALUES ($1, $2, $3, $4)
ON CONFLICT (key) DO UPDATE SET until = EXCLUDED.until, count = EXCLUDED.count, updated_at = EXCLUDED.updated_at`,
		key, now.Add(lock), count, now); err != nil {
		return 0, err
	}
	// the window starts over after a lockout
	if _, err := tx.Exec(ctx, `DELETE FROM login_failures WHERE key = $1`, key); err != nil {
		return 0, err
	}
	return lock, tx.Commit(ctx)
}

// LockFor is the lockout length of the nth consecutive lockout.
func LockFor(p Policy, n int) time.Duration {
	d := p.Window
	for i := 1; i < n && d < p.MaxLock; i++ {
		d *= 2
	}
	return min(d, p.MaxLock)
}

// Reset clears failures after a success. The lockout streak stays, so it still doubles next time.
func (l *Limiter) Reset(ctx context.Context, key string) error {
	_, err := l.DB.Exec(ctx, `DELETE FROM login_failures WHERE key = $1`, key)
	return err
}

// Prune drops failures and lockouts nobody needs any more.
func (l *Limiter) Prune(ctx context.Context) error {
	now := l.now()
	if _, err := l.DB.Exec(ctx, `DELETE FROM login_failures WHERE at < $1`, now.Add(-24*time.Hour)); err != nil {
		return err
	}
	_, err := l.DB.Exec(ctx, `DELETE FROM login_lockouts WHERE until < $1 AND updated_at < $1`, now.Add(-24*time.Hour))
	return err
}
