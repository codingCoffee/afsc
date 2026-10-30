package main

import (
  "context"
  "net/http"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"
  "github.com/labstack/echo/v5"
)

func healthHandler(pool *pgxpool.Pool) echo.HandlerFunc {
  return func(c *echo.Context) error {
    db := false
    if pool != nil {
      ctx, cancel := context.WithTimeout(c.Request().Context(), 2*time.Second)
      defer cancel()
      db = pool.Ping(ctx) == nil
    }

    status := http.StatusOK
    if !db {
      status = http.StatusServiceUnavailable
    }
    return c.JSON(status, map[string]bool{"all": db, "db": db, "api": true})
  }
}
