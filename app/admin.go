package main

import (
  "errors"
  "net/http"
  "strconv"
  "time"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/labstack/echo/v5"
  "golang.org/x/crypto/bcrypt"

  "afsc/internal/db"
)

var validRoles = map[string]bool{"admin": true, "editor": true, "author": true, "contributor": true}

const (
  defaultPageSize = 20
  maxPageSize     = 100
  maxPage         = 100_000
)

type pageResponse[T any] struct {
  Items    []T   `json:"items"`
  Page     int32 `json:"page"`
  PageSize int32 `json:"page_size"`
  Total    int64 `json:"total"`
}

type tokenResponse struct {
  ID        uuid.UUID  `json:"id"`
  SessionID uuid.UUID  `json:"session_id"`
  UserID    uuid.UUID  `json:"user_id"`
  Username  string     `json:"username"`
  UserAgent string     `json:"user_agent"`
  IP        string     `json:"ip"`
  CreatedAt time.Time  `json:"created_at"`
  ExpiresAt time.Time  `json:"expires_at"`
  RevokedAt *time.Time `json:"revoked_at"`
  Status    string     `json:"status"`
  Current   bool       `json:"current"`
}

func (h *authHandler) registerAdmin(g *echo.Group) {
  admin := []echo.MiddlewareFunc{h.requireAuth, requireAdmin}

  g.GET("/users", h.listUsers, admin...)
  g.POST("/users", h.createUser, admin...)
  g.DELETE("/users/:id", h.deleteUser, admin...)

  g.GET("/tokens", h.listTokens, admin...)
  g.POST("/tokens/:id/blacklist", h.blacklistToken, admin...)
  g.DELETE("/tokens/expired", h.cleanupExpiredTokens, admin...)
}

func parsePage(c *echo.Context) (int32, int32) {
  page, err := strconv.Atoi(c.QueryParam("page"))
  if err != nil || page < 1 {
    page = 1
  }
  size, err := strconv.Atoi(c.QueryParam("page_size"))
  if err != nil || size < 1 {
    size = defaultPageSize
  }
  return int32(min(page, maxPage)), int32(min(size, maxPageSize))
}

func (h *authHandler) listUsers(c *echo.Context) error {
  ctx := c.Request().Context()
  page, size := parsePage(c)

  total, err := h.q.CountUsers(ctx)
  if err != nil {
    return err
  }
  rows, err := h.q.ListUsers(ctx, db.ListUsersParams{Lim: size, Off: (page - 1) * size})
  if err != nil {
    return err
  }

  items := make([]userResponse, 0, len(rows))
  for _, u := range rows {
    items = append(items, toUserResponse(u))
  }
  return c.JSON(http.StatusOK, pageResponse[userResponse]{Items: items, Page: page, PageSize: size, Total: total})
}

func (h *authHandler) createUser(c *echo.Context) error {
  var req newUserRequest
  if err := decodeJSON(c, &req); err != nil {
    return jsonError(c, http.StatusBadRequest, "invalid request body")
  }
  if field, msg := req.normalize(h.cfg.strictPasswords); field != "" {
    return fieldError(c, http.StatusBadRequest, field, msg)
  }
  if req.Role == "" {
    req.Role = "contributor"
  }
  if !validRoles[req.Role] {
    return fieldError(c, http.StatusBadRequest, "role", "must be admin, editor, author or contributor")
  }

  hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
  if err != nil {
    return err
  }
  hashStr := string(hash)
  user, err := h.q.CreateUserWithRole(c.Request().Context(), db.CreateUserWithRoleParams{
    Username:     req.Username,
    Email:        req.Email,
    PasswordHash: &hashStr,
    FirstName:    req.FirstName,
    LastName:     req.LastName,
    Role:         req.Role,
  })
  if err != nil {
    if resp, ok := uniqueViolation(c, err); ok {
      return resp
    }
    return err
  }
  return c.JSON(http.StatusCreated, toUserResponse(user))
}

func (h *authHandler) deleteUser(c *echo.Context) error {
  id, err := uuid.Parse(c.Param("id"))
  if err != nil {
    return jsonError(c, http.StatusBadRequest, "invalid user id")
  }
  if self, _ := c.Get(ctxUserID).(uuid.UUID); self == id {
    return jsonError(c, http.StatusConflict, "you can't delete your own account")
  }

  ctx := c.Request().Context()
  tx, err := h.pool.Begin(ctx)
  if err != nil {
    return err
  }
  defer tx.Rollback(ctx)
  q := h.q.WithTx(tx)

  // same lock as signup, so concurrent deletes can't race past the last-admin check
  if err := q.LockUserSignup(ctx); err != nil {
    return err
  }
  target, err := q.GetUserByID(ctx, id)
  if errors.Is(err, pgx.ErrNoRows) {
    return jsonError(c, http.StatusNotFound, "user not found")
  }
  if err != nil {
    return err
  }
  if target.Role == "admin" {
    admins, err := q.CountAdmins(ctx)
    if err != nil {
      return err
    }
    if admins <= 1 {
      return jsonError(c, http.StatusConflict, "can't delete the last admin")
    }
  }

  if _, err := q.DeleteUser(ctx, id); err != nil {
    return err
  }
  if err := tx.Commit(ctx); err != nil {
    return err
  }
  return c.NoContent(http.StatusNoContent)
}

func (h *authHandler) listTokens(c *echo.Context) error {
  ctx := c.Request().Context()
  page, size := parsePage(c)
  activeOnly := c.QueryParam("status") != "all"
  current, _ := c.Get(ctxSessionID).(uuid.UUID)

  total, err := h.q.CountRefreshTokens(ctx, activeOnly)
  if err != nil {
    return err
  }
  rows, err := h.q.ListRefreshTokens(ctx, db.ListRefreshTokensParams{ActiveOnly: activeOnly, Lim: size, Off: (page - 1) * size})
  if err != nil {
    return err
  }

  now := time.Now()
  items := make([]tokenResponse, 0, len(rows))
  for _, t := range rows {
    status := "active"
    switch {
    case t.RevokedAt != nil:
      status = "revoked"
    case now.After(t.ExpiresAt):
      status = "expired"
    }
    items = append(items, tokenResponse{
      ID:        t.ID,
      SessionID: t.SessionID,
      UserID:    t.UserID,
      Username:  t.Username,
      UserAgent: t.UserAgent,
      IP:        t.Ip,
      CreatedAt: t.CreatedAt,
      ExpiresAt: t.ExpiresAt,
      RevokedAt: t.RevokedAt,
      Status:    status,
      Current:   status == "active" && t.SessionID == current,
    })
  }
  return c.JSON(http.StatusOK, pageResponse[tokenResponse]{Items: items, Page: page, PageSize: size, Total: total})
}

func (h *authHandler) blacklistToken(c *echo.Context) error {
  id, err := uuid.Parse(c.Param("id"))
  if err != nil {
    return jsonError(c, http.StatusBadRequest, "invalid token id")
  }
  n, err := h.q.RevokeSessionByTokenID(c.Request().Context(), id)
  if err != nil {
    return err
  }
  if n == 0 {
    return jsonError(c, http.StatusNotFound, "token not found or already revoked")
  }
  return c.NoContent(http.StatusNoContent)
}

func (h *authHandler) cleanupExpiredTokens(c *echo.Context) error {
  n, err := h.q.DeleteExpiredRefreshTokens(c.Request().Context())
  if err != nil {
    return err
  }
  return c.JSON(http.StatusOK, map[string]int64{"deleted": n})
}
