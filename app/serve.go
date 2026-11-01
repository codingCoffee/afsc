package main

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"github.com/labstack/echo/v5/middleware"
	"github.com/urfave/cli/v3"

	"afsc/internal/comments"
	"afsc/internal/imgproxy"
	"afsc/internal/site"
	"afsc/internal/spam"
	"afsc/internal/storage"
)

const nuxtURL = "http://localhost:3000"

func serveCmd() *cli.Command {
	return &cli.Command{
		Name:  "serve",
		Usage: "run the public site, the admin and its API",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "api-host", Value: "0.0.0.0", Sources: cli.EnvVars("AFSC_API_HOST")},
			&cli.IntFlag{Name: "api-port", Value: 1381, Sources: cli.EnvVars("AFSC_API_PORT")},
			&cli.BoolFlag{Name: "migrate", Usage: "run all pending migrations before serving"},
			&cli.IntFlag{Name: "access-token-expiry-mins", Value: 5, Sources: cli.EnvVars("AFSC_ACCESS_TOKEN_EXPIRY_MINS")},
			&cli.IntFlag{Name: "refresh-token-expiry-days", Value: 7, Usage: "absolute session lifetime", Sources: cli.EnvVars("AFSC_REFRESH_TOKEN_EXPIRY_DAYS")},
			&cli.IntFlag{Name: "session-idle-hours", Value: 12, Usage: "a session unused this long ends", Sources: cli.EnvVars("AFSC_SESSION_IDLE_HOURS")},
			&cli.IntFlag{Name: "page-cache-mb", Value: 128, Usage: "in-process page cache size; 0 disables it", Sources: cli.EnvVars("AFSC_PAGE_CACHE_MB")},
			&cli.BoolFlag{Name: "no-worker", Usage: "don't run jobs here (run `afsc worker` separately)"},
			jobConcurrencyFlag,
		},
		Action: serve,
	}
}

func serve(ctx context.Context, cmd *cli.Command) error {
	if cmd.Bool("migrate") {
		if err := runMigrations(nil, true); err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	}

	secret := os.Getenv("AFSC_JWT_SECRET")
	if len(secret) < 32 {
		return errors.New("AFSC_JWT_SECRET must be set and at least 32 bytes long")
	}
	accessMins, refreshDays, idleHours := cmd.Int("access-token-expiry-mins"), cmd.Int("refresh-token-expiry-days"),
		cmd.Int("session-idle-hours")
	if accessMins < 1 || refreshDays < 1 || idleHours < 1 {
		return errors.New("token expiries must be at least 1")
	}
	trusted, err := trustedProxies()
	if err != nil {
		return err
	}
	cacheMB := cmd.Int("page-cache-mb")
	if cacheMB < 0 || cacheMB > 1<<20 {
		return errors.New("AFSC_PAGE_CACHE_MB must be between 0 and 1048576")
	}
	jobConcurrency := cmd.Int("job-concurrency")
	if jobConcurrency < 1 || jobConcurrency > 64 {
		return errors.New("AFSC_JOB_CONCURRENCY must be between 1 and 64")
	}
	previewKey, err := site.PreviewKey([]byte(secret))
	if err != nil {
		return err
	}
	commentSigner, err := spam.NewSigner([]byte(secret))
	if err != nil {
		return err
	}

	dbURL := os.Getenv("AFSC_DATABASE_URL")
	if dbURL == "" {
		return errors.New("AFSC_DATABASE_URL is not set")
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
	themeDir := os.Getenv("AFSC_THEME_DIR")
	if themeDir == "" {
		return errors.New("AFSC_THEME_DIR is not set")
	}
	dev := os.Getenv("AFSC_ENV") == "dev"
	s3cfg, err := storage.S3ConfigFromEnv()
	if err != nil {
		return err
	}
	store, err := storage.NewS3(ctx, s3cfg)
	if err != nil {
		return err
	}
	if err := store.Check(ctx); err != nil {
		return err
	}
	ipcfg, err := imgproxy.ConfigFromEnv(s3cfg.Bucket)
	if err != nil {
		return err
	}
	pub, err := site.New(ctx, site.Config{Pool: pool, BaseURL: baseURL, ThemeDir: themeDir, Dev: dev, Logf: log.Printf,
		Storage: store, Imgproxy: imgproxy.New(ipcfg), CacheBytes: int64(cacheMB) << 20, PreviewKey: previewKey,
		CommentSigner: commentSigner})
	if err != nil {
		return err
	}
	siteCtx, stopSite := context.WithCancel(ctx)
	siteDone := make(chan struct{})
	go func() {
		pub.Run(siteCtx)
		close(siteDone)
	}()
	defer func() {
		stopSite()
		<-siteDone
	}()
	if !cmd.Bool("no-worker") {
		jobsCtx, stopJobs := context.WithCancel(ctx)
		jobsDone := make(chan struct{})
		go func() {
			newJobsRunner(pool, pub, baseURL, jobConcurrency).run(jobsCtx)
			close(jobsDone)
		}()
		defer func() {
			stopJobs()
			<-jobsDone
		}()
	}

	setupToken, err := ensureSetupToken(ctx, pool, os.Getenv("AFSC_SETUP_TOKEN"))
	if err != nil {
		return fmt.Errorf("setup token: %w", err)
	}
	if setupToken != "" {
		log.Printf("no users yet: open %s/admin/ and use this one-time setup token: %s", baseURL, setupToken)
	}

	fsys, err := clientFS()
	if err != nil {
		return err
	}

	e := echo.New()

	e.Use(middleware.RequestLogger())
	e.Use(middleware.Recover())

	if fsys != nil {
		csp, err := adminCSP(fsys)
		if err != nil {
			return fmt.Errorf("admin CSP: %w", err)
		}
		h := echo.WrapHandler(spaHandler(fsys))
		e.GET("/admin", h, adminHeaders(csp))
		e.GET("/admin/*", h, adminHeaders(csp))
	} else {
		if dev {
			nuxt, err := startNuxtDev()
			if err != nil {
				return err
			}
			defer stopNuxtDev(nuxt)
		}

		target, err := url.Parse(nuxtURL)
		if err != nil {
			return err
		}
		e.Use(middleware.ProxyWithConfig(middleware.ProxyConfig{
			Balancer: middleware.NewRandomBalancer([]*middleware.ProxyTarget{{URL: target}}),
			Skipper: func(c *echo.Context) bool {
				p := c.Request().URL.Path
				isAdmin := p == "/admin" || strings.HasPrefix(p, "/admin/")
				return !isAdmin || strings.HasPrefix(p, "/admin/api/")
			},
		}))
	}

	public := echo.WrapHandler(pub)
	for _, path := range []string{"/", "/*"} {
		e.GET(path, public)
		e.HEAD(path, public)
	}
	e.POST(site.CommentsPath, postComment(comments.New(pool, pub, commentSigner, log.Printf), pub, baseURL),
		clientIPMiddleware(trusted))

	auth := newAuthHandler(pool, authConfig{
		secret:          []byte(secret),
		accessTTL:       time.Duration(accessMins) * time.Minute,
		refreshTTL:      time.Duration(refreshDays) * 24 * time.Hour,
		idleTTL:         time.Duration(idleHours) * time.Hour,
		strictPasswords: !dev,
		baseURL:         baseURL,
	})
	mountAdmin(e, auth, &adminDeps{pool: pool, site: pub, storage: store, baseURL: baseURL, themeDir: themeDir, dev: dev},
		clientIPMiddleware(trusted), adminHeaders(apiCSP))

	addr := net.JoinHostPort(cmd.String("api-host"), strconv.Itoa(cmd.Int("api-port")))
	sc := echo.StartConfig{Address: addr}
	if err := sc.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// publicBaseURL reads AFSC_PUBLIC_BASE_URL, the origin used for absolute URLs. The Host header is never trusted.
func publicBaseURL() (string, error) {
	raw := os.Getenv("AFSC_PUBLIC_BASE_URL")
	u, err := url.Parse(raw)
	if raw == "" || err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" ||
		(u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.Fragment != "" {
		return "", errors.New("AFSC_PUBLIC_BASE_URL must be an http(s) origin such as https://blog.example.com")
	}
	return u.Scheme + "://" + u.Host, nil
}

// spaHandler serves the generated Nuxt site under /admin, falling back to index.html for client-side routes.
func spaHandler(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.StripPrefix("/admin", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.Trim(r.URL.Path, "/")
		if p != "" {
			if _, err := fs.Stat(fsys, p); err != nil {
				r.URL.Path = "/"
			}
		}
		files.ServeHTTP(w, r)
	}))
}

func startNuxtDev() (*exec.Cmd, error) {
	c := exec.Command("bun", "run", "dev")
	c.Dir = "client"
	c.Stdout = os.Stdout
	c.Stderr = os.Stderr
	// own process group so the whole bun -> nuxt tree can be killed together
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := c.Start(); err != nil {
		return nil, err
	}
	log.Printf("started nuxt dev server (pid %d)", c.Process.Pid)
	return c, nil
}

func stopNuxtDev(c *exec.Cmd) {
	if err := syscall.Kill(-c.Process.Pid, syscall.SIGTERM); err != nil {
		log.Printf("failed to stop nuxt dev server: %v", err)
		return
	}
	_ = c.Wait()
	log.Printf("stopped nuxt dev server")
}
