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
)

const nuxtURL = "http://localhost:3000"

func serveCmd() *cli.Command {
  return &cli.Command{
    Name:  "serve",
    Usage: "run the API server and proxy everything else to Nuxt",
    Flags: []cli.Flag{
      &cli.StringFlag{Name: "api-host", Value: "0.0.0.0", Sources: cli.EnvVars("API_HOST")},
      &cli.IntFlag{Name: "api-port", Value: 1381, Sources: cli.EnvVars("API_PORT")},
      &cli.BoolFlag{Name: "migrate", Usage: "run all pending migrations before serving"},
      &cli.IntFlag{Name: "access-token-expiry-mins", Value: 5, Sources: cli.EnvVars("ACCESS_TOKEN_EXPIRY_MINS")},
      &cli.IntFlag{Name: "refresh-token-expiry-days", Value: 7, Sources: cli.EnvVars("REFRESH_TOKEN_EXPIRY_DAYS")},
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

  secret := os.Getenv("JWT_SECRET")
  if len(secret) < 32 {
    return errors.New("JWT_SECRET must be set and at least 32 bytes long")
  }
  accessMins, refreshDays := cmd.Int("access-token-expiry-mins"), cmd.Int("refresh-token-expiry-days")
  if accessMins < 1 || refreshDays < 1 {
    return errors.New("token expiries must be at least 1")
  }

  dbURL := os.Getenv("DATABASE_URL")
  if dbURL == "" {
    return errors.New("DATABASE_URL is not set")
  }
  pool, err := pgxpool.New(ctx, dbURL)
  if err != nil {
    return fmt.Errorf("invalid DATABASE_URL: %w", err)
  }
  defer pool.Close()

  fsys, err := clientFS()
  if err != nil {
    return err
  }

  e := echo.New()

  e.Use(middleware.RequestLogger())
  e.Use(middleware.Recover())

  if fsys != nil {
    h := echo.WrapHandler(spaHandler(fsys))
    e.GET("/admin", h)
    e.GET("/admin/*", h)
  } else {
    if os.Getenv("APP_ENV") == "dev" {
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
        return p == "/" || strings.HasPrefix(p, "/admin/api/")
      },
    }))
  }

  e.GET("/", func(c *echo.Context) error {
    return c.JSON(http.StatusOK, map[string]string{"message": "Hello, World!"})
  })

  api := e.Group("/admin/api/v1")
  api.GET("/ht", healthHandler(pool))
  newAuthHandler(pool, authConfig{
    secret:          []byte(secret),
    accessTTL:       time.Duration(accessMins) * time.Minute,
    refreshTTL:      time.Duration(refreshDays) * 24 * time.Hour,
    strictPasswords: os.Getenv("APP_ENV") != "dev",
  }).register(api)

  addr := net.JoinHostPort(cmd.String("api-host"), strconv.Itoa(cmd.Int("api-port")))
  sc := echo.StartConfig{Address: addr}
  if err := sc.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
    return err
  }
  return nil
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
