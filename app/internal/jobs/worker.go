package jobs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"os"
	"runtime/debug"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// Handler runs one job. A returned error (or a panic) schedules a retry until max_attempts is reached.
type Handler func(ctx context.Context, payload json.RawMessage) error

type Worker struct {
	DB          *pgxpool.Pool
	Concurrency int                             // goroutines; default 1
	Poll        time.Duration                   // idle wait between polls; default 1 s
	Timeout     time.Duration                   // per-job limit, and when a crashed worker's lock is reclaimed; default 5 min
	Backoff     func(attempt int) time.Duration // default DefaultBackoff
	Logf        func(format string, args ...any)

	id       string
	handlers map[string]Handler
}

// Handle registers h for kind. Call before Run.
func (w *Worker) Handle(kind string, h Handler) {
	if w.handlers == nil {
		w.handlers = map[string]Handler{}
	}
	w.handlers[kind] = h
}

// DefaultBackoff doubles from 10 s up to 1 h, with ±25 % jitter so retries of a failing kind spread out.
func DefaultBackoff(attempt int) time.Duration {
	d := 10 * time.Second
	for i := 1; i < attempt && d < time.Hour; i++ {
		d *= 2
	}
	d = min(d, time.Hour)
	return d*3/4 + rand.N(d/2)
}

func (w *Worker) defaults() {
	w.Concurrency = max(w.Concurrency, 1)
	if w.Poll <= 0 {
		w.Poll = time.Second
	}
	if w.Timeout <= 0 {
		w.Timeout = 5 * time.Minute
	}
	if w.Backoff == nil {
		w.Backoff = DefaultBackoff
	}
	if w.Logf == nil {
		w.Logf = func(string, ...any) {}
	}
	if w.id == "" {
		host, _ := os.Hostname()
		w.id = fmt.Sprintf("%s:%d:%s", host, os.Getpid(), uuid.NewString()[:8])
	}
}

// Run polls for due jobs on Concurrency goroutines until ctx is done, then waits for running jobs.
func (w *Worker) Run(ctx context.Context) {
	w.defaults()
	var wg sync.WaitGroup
	for range w.Concurrency {
		wg.Go(func() {
			for ctx.Err() == nil {
				ran, err := w.runOnce(ctx)
				if err != nil && ctx.Err() == nil {
					w.Logf("jobs: %v", err)
				}
				if ran && err == nil {
					continue
				}
				wait := w.Poll/2 + rand.N(w.Poll)
				select {
				case <-ctx.Done():
				case <-time.After(wait):
				}
			}
		})
	}
	wg.Wait()
}

const sqlClaim = `
UPDATE jobs SET locked_at = now(), locked_by = $1, attempts = attempts + 1
WHERE id = (
  SELECT id FROM jobs
  WHERE done_at IS NULL AND run_at <= now() AND kind = ANY($2::text[])
    AND (locked_at IS NULL OR locked_at < now() - make_interval(secs => $3))
  ORDER BY run_at, id
  FOR UPDATE SKIP LOCKED
  LIMIT 1)
RETURNING id, kind, payload, attempts, max_attempts`

// RunOnce claims and runs one due job. It reports whether there was one.
func (w *Worker) RunOnce(ctx context.Context) (bool, error) {
	w.defaults()
	return w.runOnce(ctx)
}

func (w *Worker) runOnce(ctx context.Context) (bool, error) {
	kinds := make([]string, 0, len(w.handlers))
	for k := range w.handlers {
		kinds = append(kinds, k)
	}
	slices.Sort(kinds)
	var id int64
	var kind string
	var payload []byte
	var attempts, maxAttempts int
	err := w.DB.QueryRow(ctx, sqlClaim, w.id, kinds, (2*w.Timeout).Seconds()).Scan(&id, &kind, &payload, &attempts, &maxAttempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}

	runErr := w.call(ctx, w.handlers[kind], payload)
	// record the outcome even when ctx was cancelled mid-job
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	switch {
	case runErr == nil:
		_, err = w.DB.Exec(rctx, `UPDATE jobs SET done_at = now(), locked_at = NULL, last_error = NULL WHERE id = $1`, id)
	case attempts >= maxAttempts:
		w.Logf("jobs: %s #%d failed for good after %d attempts: %v", kind, id, attempts, runErr)
		_, err = w.DB.Exec(rctx, `UPDATE jobs SET done_at = now(), locked_at = NULL, last_error = $2 WHERE id = $1`, id, runErr.Error())
	default:
		_, err = w.DB.Exec(rctx, `
UPDATE jobs SET run_at = now() + make_interval(secs => $3), locked_at = NULL, locked_by = NULL, last_error = $2
WHERE id = $1`, id, runErr.Error(), w.Backoff(attempts).Seconds())
	}
	if err != nil {
		return true, fmt.Errorf("record %s #%d: %w", kind, id, err)
	}
	return true, nil
}

func (w *Worker) call(ctx context.Context, h Handler, payload []byte) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("panic: %v\n%s", r, debug.Stack())
		}
	}()
	ctx, cancel := context.WithTimeout(ctx, w.Timeout)
	defer cancel()
	return h(ctx, payload)
}
