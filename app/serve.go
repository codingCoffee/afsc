package main

import (
  "context"
  "errors"
  "log"
  "net"
  "net/http"
  "net/url"
  "os"
  "os/exec"
  "strconv"
  "strings"
  "syscall"

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
    },
    Action: serve,
  }
}

func serve(ctx context.Context, cmd *cli.Command) error {
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

  e := echo.New()

  e.Use(middleware.RequestLogger())
  e.Use(middleware.Recover())
  e.Use(middleware.ProxyWithConfig(middleware.ProxyConfig{
    Balancer: middleware.NewRandomBalancer([]*middleware.ProxyTarget{{URL: target}}),
    Skipper: func(c *echo.Context) bool {
      p := c.Request().URL.Path
      return p == "/" || strings.HasPrefix(p, "/admin/api/")
    },
  }))

  e.GET("/", func(c *echo.Context) error {
    return c.JSON(http.StatusOK, map[string]string{"message": "Hello, World!"})
  })

  _ = e.Group("/admin/api/v1")

  addr := net.JoinHostPort(cmd.String("api-host"), strconv.Itoa(cmd.Int("api-port")))
  sc := echo.StartConfig{Address: addr}
  if err := sc.Start(ctx, e); err != nil && !errors.Is(err, http.ErrServerClosed) {
    return err
  }
  return nil
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
