// Package jobs is a Postgres job queue: Enqueue inside the caller's transaction, a Worker that runs due jobs and a
// Scheduler that enqueues interval jobs from a single leader.
package jobs

import (
	"context"
	"encoding/json"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
)

type Execer interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// Enqueue adds a job that runs at runAt (now when zero). Pass the caller's tx so the job commits with the change.
func Enqueue(ctx context.Context, db Execer, kind string, payload any, runAt time.Time) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	var at *time.Time
	if !runAt.IsZero() {
		at = &runAt
	}
	_, err = db.Exec(ctx, `INSERT INTO jobs (kind, payload, run_at) VALUES ($1, $2, coalesce($3, now()))`, kind, b, at)
	return err
}

// Prune deletes finished jobs older than age.
func Prune(ctx context.Context, db Execer, age time.Duration) (int64, error) {
	tag, err := db.Exec(ctx, `DELETE FROM jobs WHERE done_at < now() - make_interval(secs => $1)`, age.Seconds())
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), nil
}
