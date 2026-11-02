package main

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"expvar"
	"fmt"
	"io/fs"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"afsc/internal/metrics"
	"afsc/internal/plugins"
	"afsc/internal/site"
)

// setupLogging sends every log line, including log.Printf, through slog: JSON in production, text in dev.
func setupLogging(dev bool) {
	var h slog.Handler = slog.NewJSONHandler(os.Stderr, nil)
	if dev {
		h = slog.NewTextHandler(os.Stderr, nil)
	}
	slog.SetDefault(slog.New(h))
}

// migSet is one golang-migrate version table and the newest version this binary embeds for it.
type migSet struct {
	Name  string // "core" or "plugin <id>"
	Table string
	Want  uint
}

// migrationSets lists core and every plugin's migrations with their embedded latest versions.
func migrationSets(rt *plugins.Runtime) ([]migSet, error) {
	want, err := latestVersion(migrationsFS, "db/migrations")
	if err != nil {
		return nil, fmt.Errorf("core migrations: %w", err)
	}
	sets := []migSet{{Name: "core", Table: "schema_migrations", Want: want}}
	for _, m := range rt.Migrations() {
		want, err := latestVersion(m.FS, ".")
		if err != nil {
			return nil, fmt.Errorf("plugin %s migrations: %w", m.Plugin, err)
		}
		sets = append(sets, migSet{Name: "plugin " + m.Plugin, Table: m.VersionTable(), Want: want})
	}
	return sets, nil
}

var upFileRe = regexp.MustCompile(`^([0-9]+)_.*\.up\.sql$`)

func latestVersion(fsys fs.FS, dir string) (uint, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return 0, err
	}
	var latest uint64
	for _, e := range entries {
		if m := upFileRe.FindStringSubmatch(e.Name()); m != nil {
			v, err := strconv.ParseUint(m[1], 10, 64)
			if err != nil {
				return 0, err
			}
			latest = max(latest, v)
		}
	}
	return uint(latest), nil
}

// migState is what the database records for a set. Have is -1 when nothing has been applied.
type migState struct {
	migSet
	Have  int64
	Dirty bool
}

func (s migState) current() bool { return !s.Dirty && s.Have >= int64(s.Want) }

func (s migState) String() string {
	have := "nothing applied"
	if s.Have >= 0 {
		have = fmt.Sprintf("at %04d", s.Have)
	}
	if s.Dirty {
		have += " (dirty)"
	}
	return fmt.Sprintf("%s %s, binary has %04d", s.Name, have, s.Want)
}

func migrationStates(ctx context.Context, pool *pgxpool.Pool, sets []migSet) ([]migState, error) {
	out := make([]migState, 0, len(sets))
	for _, set := range sets {
		st := migState{migSet: set, Have: -1}
		var exists bool
		if err := pool.QueryRow(ctx, `SELECT to_regclass($1) IS NOT NULL`, set.Table).Scan(&exists); err != nil {
			return nil, err
		}
		if exists {
			err := pool.QueryRow(ctx, `SELECT version, dirty FROM `+pgx.Identifier{set.Table}.Sanitize()+` LIMIT 1`).
				Scan(&st.Have, &st.Dirty)
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return nil, err
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// requireMigrations refuses to run on a database that is behind this binary or dirty. A database ahead of it (an
// older binary during a rolling deploy) only logs a warning: migrations are additive, so old code keeps working.
func requireMigrations(ctx context.Context, pool *pgxpool.Pool, sets []migSet) error {
	states, err := migrationStates(ctx, pool, sets)
	if err != nil {
		return fmt.Errorf("check migrations: %w", err)
	}
	var bad []string
	for _, s := range states {
		switch {
		case !s.current():
			bad = append(bad, s.String())
		case s.Have > int64(s.Want):
			log.Printf("warning: database is ahead of this binary: %s", s)
		}
	}
	if len(bad) > 0 {
		return fmt.Errorf("database migrations are not current: %s; run `afsc migrate up` (or start with --migrate)%s",
			strings.Join(bad, "; "), dirtyHint(states))
	}
	return nil
}

func dirtyHint(states []migState) string {
	for _, s := range states {
		if s.Dirty {
			return ". A dirty version means a migration failed half-way: fix the schema by hand, then force the version (docs/ops.md)"
		}
	}
	return ""
}

// healthz answers 200 while the process is up. It touches nothing else, so a slow database never restarts it.
func healthz(c *echo.Context) error {
	c.Response().Header().Set("Cache-Control", "no-store")
	return c.JSON(http.StatusOK, map[string]string{"status": "ok"})
}

// readyz answers 200 when the database answers within 2 s and every migration set is current, else 503.
func readyz(pool *pgxpool.Pool, sets []migSet) echo.HandlerFunc {
	return func(c *echo.Context) error {
		c.Response().Header().Set("Cache-Control", "no-store")
		ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
		defer cancel()
		res := map[string]string{"db": "ok", "migrations": "ok"}
		if err := pool.Ping(ctx); err != nil {
			res["db"], res["migrations"] = "down", "unknown"
			return c.JSON(http.StatusServiceUnavailable, res)
		}
		states, err := migrationStates(ctx, pool, sets)
		if err != nil {
			res["migrations"] = "unknown"
			return c.JSON(http.StatusServiceUnavailable, res)
		}
		for _, s := range states {
			if !s.current() {
				res["migrations"] = "behind"
				if s.Dirty {
					res["migrations"] = "dirty"
				}
				return c.JSON(http.StatusServiceUnavailable, res)
			}
		}
		return c.JSON(http.StatusOK, res)
	}
}

var publishOnce sync.Once

// serveMetrics serves expvar at /debug/vars on addr until ctx is done and returns the bound address. It's a
// separate listener: the public server never routes /debug/vars. pub may be nil.
func serveMetrics(ctx context.Context, addr string, pool *pgxpool.Pool, pub *site.Site) (string, error) {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return "", fmt.Errorf("AFSC_METRICS_ADDR %q: %w", addr, err)
	}
	if ip := net.ParseIP(host); host == "" || (ip != nil && ip.IsUnspecified()) {
		log.Printf("warning: AFSC_METRICS_ADDR %s listens on every interface; keep the port off the internet", addr)
	}
	publishOnce.Do(func() {
		expvar.Publish("afsc_page_cache_size", expvar.Func(func() any {
			if pub == nil {
				return map[string]int64{"entries": 0, "bytes": 0}
			}
			n, b := pub.CacheStats()
			return map[string]int64{"entries": int64(n), "bytes": b}
		}))
		expvar.Publish("afsc_jobs_pending", expvar.Func(func() any { return pendingJobs(pool) }))
	})
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return "", fmt.Errorf("metrics listener: %w", err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET /debug/vars", expvar.Handler())
	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("metrics listener: %v", err)
		}
	}()
	log.Printf("metrics on http://%s/debug/vars", ln.Addr())
	return ln.Addr().String(), nil
}

// pendingJobs counts unfinished jobs by kind, read when /debug/vars is scraped.
func pendingJobs(pool *pgxpool.Pool) any {
	if pool == nil {
		return map[string]int64{}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rows, err := pool.Query(ctx, `SELECT kind, count(*) FROM jobs WHERE done_at IS NULL GROUP BY kind`)
	if err != nil {
		return map[string]string{"error": "query failed"}
	}
	out := map[string]int64{}
	var kind string
	var n int64
	if _, err := pgx.ForEachRow(rows, []any{&kind, &n}, func() error {
		out[kind] = n
		return nil
	}); err != nil {
		return map[string]string{"error": "query failed"}
	}
	return out
}

var requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{8,64}$`)

// tokenPaths are path prefixes whose next segment is a secret (invite tokens); logs show it as "redacted".
var tokenPaths = []string{"/admin/invite/", "/admin/api/v1/invites/"}

// logPath is the path as logged: never the query string (preview tokens, search terms), and no invite tokens.
func logPath(p string) string {
	for _, prefix := range tokenPaths {
		if rest, ok := strings.CutPrefix(p, prefix); ok && rest != "" {
			if _, tail, found := strings.Cut(rest, "/"); found {
				return prefix + "redacted/" + tail
			}
			return prefix + "redacted"
		}
	}
	return p
}

// withRequestLog gives every request an X-Request-ID (a well-formed one from a proxy is kept), logs one line per
// request and counts responses by status class. Successful probes aren't logged.
func withRequestLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := r.Header.Get("X-Request-ID")
		if !requestIDRe.MatchString(id) {
			b := make([]byte, 8)
			_, _ = rand.Read(b)
			id = hex.EncodeToString(b)
			r.Header.Set("X-Request-ID", id)
		}
		w.Header().Set("X-Request-ID", id)
		rec := &statusRecorder{ResponseWriter: w}
		next.ServeHTTP(rec, r)
		status := rec.status
		if status == 0 {
			status = http.StatusOK
		}
		metrics.Requests.Add(strconv.Itoa(status/100)+"xx", 1)
		if (r.URL.Path == "/healthz" || r.URL.Path == "/readyz") && status < 500 {
			return
		}
		slog.LogAttrs(r.Context(), slog.LevelInfo, "request",
			slog.String("request_id", id),
			slog.String("method", r.Method),
			slog.String("path", logPath(r.URL.Path)),
			slog.Int("status", status),
			slog.Int64("bytes", rec.bytes),
			slog.Float64("latency_ms", float64(time.Since(start).Microseconds())/1000))
	})
}

// statusRecorder notes the status and size; Unwrap keeps http.ResponseController (flush, hijack) working.
type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(b)
	s.bytes += int64(n)
	return n, err
}

func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

func (s *statusRecorder) Flush() { _ = http.NewResponseController(s.ResponseWriter).Flush() }

func (s *statusRecorder) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return http.NewResponseController(s.ResponseWriter).Hijack()
}

// listen serves h on addr until ctx is done, then drains for up to 10 s.
func listen(ctx context.Context, addr string, h http.Handler) error {
	srv := &http.Server{Addr: addr, Handler: h, ReadHeaderTimeout: 10 * time.Second}
	done := make(chan struct{})
	go func() {
		defer close(done)
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("listening on %s", addr)
	err := srv.ListenAndServe()
	if errors.Is(err, http.ErrServerClosed) {
		<-done
		return nil
	}
	return err
}
