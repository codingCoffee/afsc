package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"slices"
	"sync"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"afsc/internal/indexnow"
	"afsc/internal/jobs"
	"afsc/internal/plugins"
	"afsc/internal/posts"
	"afsc/internal/ratelimit"
	"afsc/internal/settings"
	"afsc/internal/site"
	"afsc/internal/spam"
)

var jobConcurrencyFlag = &cli.IntFlag{Name: "job-concurrency", Value: 4, Usage: "jobs run at once by this process",
	Sources: cli.EnvVars("AFSC_JOB_CONCURRENCY")}

func workerCmd() *cli.Command {
	return &cli.Command{
		Name:  "worker",
		Usage: "run only the job worker and scheduler",
		Flags: []cli.Flag{jobConcurrencyFlag,
			&cli.BoolFlag{Name: "migrate", Usage: "run all pending migrations before starting"}},
		Action: runWorker,
	}
}

func runWorker(ctx context.Context, cmd *cli.Command) error {
	setupLogging(os.Getenv("AFSC_ENV") == "dev")
	n := cmd.Int("job-concurrency")
	if n < 1 || n > 64 {
		return errors.New("AFSC_JOB_CONCURRENCY must be between 1 and 64")
	}
	dbURL := os.Getenv("AFSC_DATABASE_URL")
	if dbURL == "" {
		return errors.New("AFSC_DATABASE_URL is not set")
	}
	if cmd.Bool("migrate") {
		if err := runAllMigrations(ctx); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("invalid AFSC_DATABASE_URL: %w", err)
	}
	defer pool.Close()
	baseURL, err := publicBaseURL()
	if err != nil {
		return err
	}
	secret := os.Getenv("AFSC_JWT_SECRET")
	if len(secret) < 32 {
		return errors.New("AFSC_JWT_SECRET must be set and at least 32 bytes long (plugins derive keys from it)")
	}
	themeDir := os.Getenv("AFSC_THEME_DIR")
	if themeDir == "" {
		return errors.New("AFSC_THEME_DIR is not set")
	}
	rt, err := buildPlugins(ctx, pool, baseURL, []byte(secret))
	if err != nil {
		return err
	}
	sets, err := migrationSets(rt)
	if err != nil {
		return err
	}
	if err := requireMigrations(ctx, pool, sets); err != nil {
		return err
	}
	pub, err := site.New(ctx, site.Config{Pool: pool, BaseURL: baseURL, ThemeDir: themeDir, Logf: log.Printf, Plugins: rt})
	if err != nil {
		return err
	}
	if m := os.Getenv("AFSC_METRICS_ADDR"); m != "" {
		if _, err := serveMetrics(ctx, m, pool, pub); err != nil {
			return err
		}
	}
	var wg sync.WaitGroup
	wg.Go(func() { pub.Run(ctx) })
	newJobsRunner(pool, pub, baseURL, n, rt).run(ctx)
	wg.Wait()
	return nil
}

// jobsSchedules are the interval jobs the scheduler leader enqueues.
var jobsSchedules = []jobs.Schedule{
	{Kind: "posts.publish_scheduled", Every: 30 * time.Second},
	{Kind: "not_found.prune", Every: 24 * time.Hour},
	{Kind: "auth.prune", Every: 24 * time.Hour},
	{Kind: "jobs.prune", Every: 24 * time.Hour},
	{Kind: "spam.retrain", Every: 24 * time.Hour},
	{Kind: "comments.retention", Every: 24 * time.Hour},
}

type jobsRunner struct {
	worker    *jobs.Worker
	scheduler *jobs.Scheduler
	pinger    *indexnow.Pinger
}

func newJobsRunner(pool *pgxpool.Pool, pub *site.Site, baseURL string, concurrency int, rt *plugins.Runtime) *jobsRunner {
	r := &jobsRunner{
		worker:    &jobs.Worker{DB: pool, Concurrency: concurrency, Logf: log.Printf},
		scheduler: &jobs.Scheduler{DB: pool, Schedules: slices.Concat(jobsSchedules, rt.Schedules()), Logf: log.Printf},
		pinger: &indexnow.Pinger{BaseURL: baseURL,
			Settings: func() settings.IndexNow { return pub.Settings().SEO.IndexNow }},
	}
	svc := posts.New(pool, pub, log.Printf)
	r.worker.Handle("posts.publish_scheduled", func(ctx context.Context, _ json.RawMessage) error {
		n, err := svc.PublishDue(ctx)
		if n > 0 {
			log.Printf("jobs: published %d scheduled posts", n)
		}
		return err
	})
	r.worker.Handle("not_found.prune", func(ctx context.Context, _ json.RawMessage) error { return pub.PruneNotFound(ctx) })
	r.worker.Handle("auth.prune", func(ctx context.Context, _ json.RawMessage) error { return jobsAuthPrune(ctx, pool) })
	r.worker.Handle("jobs.prune", func(ctx context.Context, _ json.RawMessage) error {
		_, err := jobs.Prune(ctx, pool, 7*24*time.Hour)
		return err
	})
	r.worker.Handle("spam.retrain", func(ctx context.Context, _ json.RawMessage) error {
		n, err := spam.Retrain(ctx, pool)
		if err == nil {
			log.Printf("jobs: spam classifier retrained on %d spam and %d ham comments", n.Spam, n.Ham)
		}
		return err
	})
	r.worker.Handle("comments.retention", func(ctx context.Context, _ json.RawMessage) error {
		d := pub.Settings().Discussion
		res, err := spam.Retain(ctx, pool, time.Now(), d.SpamRetentionDays, d.CommentIPRetentionDays)
		if res.SpamDeleted > 0 || res.IPCleared > 0 {
			log.Printf("jobs: comment retention deleted %d spam, cleared IP/UA on %d comments", res.SpamDeleted, res.IPCleared)
		}
		return err
	})
	r.worker.Handle(indexnow.Kind, func(ctx context.Context, p json.RawMessage) error { return r.pinger.Handle(ctx, p) })
	for kind, j := range rt.Jobs() {
		r.worker.HandleWith(kind, j.Handler, j.Options)
	}
	return r
}

// run works and schedules until ctx is done.
func (r *jobsRunner) run(ctx context.Context) {
	var wg sync.WaitGroup
	wg.Go(func() { r.worker.Run(ctx) })
	wg.Go(func() { r.scheduler.Run(ctx) })
	wg.Wait()
}

// jobsAuthPrune drops expired sessions, used or expired invites and old login failures.
func jobsAuthPrune(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `DELETE FROM refresh_tokens WHERE expires_at < now() - interval '1 day'`); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx, `DELETE FROM user_invites WHERE used_at IS NOT NULL OR expires_at < now()`); err != nil {
		return err
	}
	return (&ratelimit.Limiter{DB: pool}).Prune(ctx)
}
