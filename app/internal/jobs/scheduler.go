package jobs

import (
	"context"
	"encoding/json"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// DefaultLockKey is the advisory lock the scheduler leader holds ("afscschd").
const DefaultLockKey int64 = 0x6166736373636864

// Schedule enqueues Kind every Every. A run is skipped while an earlier one of the same kind is still pending.
type Schedule struct {
	Kind    string
	Every   time.Duration
	Payload any
}

// Scheduler enqueues interval jobs. Only the replica holding the advisory lock on its own connection leads; when
// that connection drops, Postgres releases the lock and another replica takes over on its next try.
type Scheduler struct {
	DB        *pgxpool.Pool
	Schedules []Schedule
	LockKey   int64         // default DefaultLockKey
	Retry     time.Duration // how often a follower tries for the lock and the leader checks its conn; default 10 s
	Logf      func(format string, args ...any)

	leading atomic.Bool
}

func (s *Scheduler) Leading() bool { return s.leading.Load() }

// Run competes for leadership until ctx is done. Every schedule fires as soon as this replica leads.
func (s *Scheduler) Run(ctx context.Context) {
	if s.LockKey == 0 {
		s.LockKey = DefaultLockKey
	}
	if s.Retry <= 0 {
		s.Retry = 10 * time.Second
	}
	if s.Logf == nil {
		s.Logf = func(string, ...any) {}
	}
	for ctx.Err() == nil {
		if err := s.lead(ctx); err != nil && ctx.Err() == nil {
			s.Logf("jobs: scheduler: %v", err)
		}
		select {
		case <-ctx.Done():
		case <-time.After(s.Retry):
		}
	}
}

const sqlEnqueueOnce = `
INSERT INTO jobs (kind, payload)
SELECT $1::text, $2::jsonb WHERE NOT EXISTS (SELECT 1 FROM jobs WHERE kind = $1 AND done_at IS NULL)`

// lead takes the lock on a dedicated connection and runs the schedules while it holds it.
func (s *Scheduler) lead(ctx context.Context) error {
	conn, err := pgx.ConnectConfig(ctx, s.DB.Config().ConnConfig.Copy())
	if err != nil {
		return err
	}
	defer func() {
		cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = conn.Close(cctx)
	}()
	var got bool
	if err := conn.QueryRow(ctx, `SELECT pg_try_advisory_lock($1)`, s.LockKey).Scan(&got); err != nil || !got {
		return err
	}
	s.leading.Store(true)
	defer s.leading.Store(false)
	s.Logf("jobs: this replica now runs the scheduler")

	payloads := make([][]byte, len(s.Schedules))
	next := make([]time.Time, len(s.Schedules))
	for i, sc := range s.Schedules {
		if payloads[i], err = json.Marshal(sc.Payload); err != nil {
			return err
		}
	}
	check := time.NewTicker(s.Retry)
	defer check.Stop()
	for {
		now := time.Now()
		wake := now.Add(s.Retry)
		for i, sc := range s.Schedules {
			if !now.Before(next[i]) {
				if _, err := conn.Exec(ctx, sqlEnqueueOnce, sc.Kind, payloads[i]); err != nil {
					return err
				}
				next[i] = now.Add(sc.Every)
			}
			if next[i].Before(wake) {
				wake = next[i]
			}
		}
		select {
		case <-ctx.Done():
			return nil
		case <-check.C:
			if err := conn.Ping(ctx); err != nil {
				return err
			}
		case <-time.After(time.Until(wake)):
		}
	}
}
