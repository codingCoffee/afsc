package main

import (
  "encoding/json"
  "errors"
  "log"
  "net/http"
  "net/mail"
  "regexp"
  "strings"
  "time"
  "unicode"
  "unicode/utf8"

  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgconn"
  "github.com/jackc/pgx/v5/pgxpool"
  "github.com/labstack/echo/v5"
  "github.com/labstack/echo/v5/middleware"
  "golang.org/x/crypto/bcrypt"

  "afsc/internal/db"
)

const bcryptCost = 12

var (
  usernameRe = regexp.MustCompile(`^[a-z0-9_.-]{3,32}$`)
  // compared against when a login doesn't exist, so response time doesn't reveal valid accounts
  dummyHash, _ = bcrypt.GenerateFromPassword([]byte("afsc-timing-equaliser"), bcryptCost)
)

type authConfig struct {
  secret          []byte
  accessTTL       time.Duration
  refreshTTL      time.Duration
  strictPasswords bool
}

type authHandler struct {
  pool *pgxpool.Pool
  q    *db.Queries
  cfg  authConfig
}

type userResponse struct {
  ID        uuid.UUID `json:"id"`
  Username  string    `json:"username"`
  Email     string    `json:"email"`
  FirstName string    `json:"first_name"`
  LastName  string    `json:"last_name"`
  UserType  string    `json:"user_type"`
}

type sessionResponse struct {
  AccessToken string       `json:"access_token"`
  ExpiresIn   int          `json:"expires_in"`
  User        userResponse `json:"user"`
}

func newAuthHandler(pool *pgxpool.Pool, cfg authConfig) *authHandler {
  return &authHandler{pool: pool, q: db.New(pool), cfg: cfg}
}

func (h *authHandler) register(g *echo.Group) {
  limiter := middleware.RateLimiter(middleware.NewRateLimiterMemoryStoreWithConfig(
    middleware.RateLimiterMemoryStoreConfig{Rate: 1.0 / 6, Burst: 5, ExpiresIn: 3 * time.Minute},
  ))

  g.POST("/signup", h.signup, limiter)
  g.POST("/signin", h.signin, limiter)
  g.POST("/token_refresh", h.tokenRefresh)
  g.POST("/signout", h.signout)
  g.GET("/me", h.me, h.requireAuth)
}

func (h *authHandler) signup(c *echo.Context) error {
  var req struct {
    Username  string `json:"username"`
    Email     string `json:"email"`
    Password  string `json:"password"`
    FirstName string `json:"first_name"`
    LastName  string `json:"last_name"`
  }
  if err := decodeJSON(c, &req); err != nil {
    return jsonError(c, http.StatusBadRequest, "invalid request body")
  }

  req.Username = strings.ToLower(strings.TrimSpace(req.Username))
  req.Email = strings.ToLower(strings.TrimSpace(req.Email))
  req.FirstName = strings.TrimSpace(req.FirstName)
  req.LastName = strings.TrimSpace(req.LastName)

  if !usernameRe.MatchString(req.Username) {
    return fieldError(c, http.StatusBadRequest, "username", "3-32 characters: a-z, 0-9, _ . -")
  }
  if addr, err := mail.ParseAddress(req.Email); err != nil || addr.Address != req.Email || len(req.Email) > 254 {
    return fieldError(c, http.StatusBadRequest, "email", "enter a valid email address")
  }
  if utf8.RuneCountInString(req.FirstName) > 100 || utf8.RuneCountInString(req.LastName) > 100 {
    return jsonError(c, http.StatusBadRequest, "names must be at most 100 characters")
  }
  if msg := validatePassword(req.Password, h.cfg.strictPasswords); msg != "" {
    return fieldError(c, http.StatusBadRequest, "password", msg)
  }

  hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
  if err != nil {
    return err
  }

  ctx := c.Request().Context()
  tx, err := h.pool.Begin(ctx)
  if err != nil {
    return err
  }
  defer tx.Rollback(ctx)
  q := h.q.WithTx(tx)

  if err := q.LockUserSignup(ctx); err != nil {
    return err
  }
  user, err := q.CreateUser(ctx, db.CreateUserParams{
    Username:     req.Username,
    Email:        req.Email,
    PasswordHash: string(hash),
    FirstName:    req.FirstName,
    LastName:     req.LastName,
  })
  if err != nil {
    var pgErr *pgconn.PgError
    if errors.As(err, &pgErr) && pgErr.Code == "23505" {
      switch pgErr.ConstraintName {
      case "users_username_key":
        return fieldError(c, http.StatusConflict, "username", "username is already taken")
      case "users_email_key":
        return fieldError(c, http.StatusConflict, "email", "email is already registered")
      }
    }
    return err
  }

  _, refresh, err := h.issueRefresh(c, q, user.ID)
  if err != nil {
    return err
  }
  if err := tx.Commit(ctx); err != nil {
    return err
  }
  return h.respondSession(c, http.StatusCreated, user, refresh)
}

func (h *authHandler) signin(c *echo.Context) error {
  var req struct {
    Login    string `json:"login"`
    Password string `json:"password"`
  }
  if err := decodeJSON(c, &req); err != nil {
    return jsonError(c, http.StatusBadRequest, "invalid request body")
  }
  login := strings.ToLower(strings.TrimSpace(req.Login))
  if login == "" || req.Password == "" {
    return jsonError(c, http.StatusBadRequest, "login and password are required")
  }

  ctx := c.Request().Context()
  user, err := h.q.GetUserByLogin(ctx, login)
  if errors.Is(err, pgx.ErrNoRows) {
    _ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
    return jsonError(c, http.StatusUnauthorized, "invalid credentials")
  }
  if err != nil {
    return err
  }
  if bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)) != nil {
    return jsonError(c, http.StatusUnauthorized, "invalid credentials")
  }

  _, refresh, err := h.issueRefresh(c, h.q, user.ID)
  if err != nil {
    return err
  }
  return h.respondSession(c, http.StatusOK, user, refresh)
}

func (h *authHandler) tokenRefresh(c *echo.Context) error {
  ck, err := c.Cookie(refreshCookie)
  if err != nil || ck.Value == "" {
    return jsonError(c, http.StatusUnauthorized, "no refresh token")
  }

  ctx := c.Request().Context()
  tx, err := h.pool.Begin(ctx)
  if err != nil {
    return err
  }
  defer tx.Rollback(ctx)
  q := h.q.WithTx(tx)

  old, err := q.GetRefreshTokenByHashForUpdate(ctx, hashToken(ck.Value))
  if errors.Is(err, pgx.ErrNoRows) {
    clearSessionCookies(c)
    return jsonError(c, http.StatusUnauthorized, "invalid refresh token")
  }
  if err != nil {
    return err
  }

  if old.RevokedAt != nil {
    if err := q.RevokeUserRefreshTokens(ctx, old.UserID); err != nil {
      return err
    }
    if err := tx.Commit(ctx); err != nil {
      return err
    }
    log.Printf("refresh token reuse detected for user %s, revoked all sessions", old.UserID)
    clearSessionCookies(c)
    return jsonError(c, http.StatusUnauthorized, "refresh token reused, all sessions revoked")
  }
  if time.Now().After(old.ExpiresAt) {
    clearSessionCookies(c)
    return jsonError(c, http.StatusUnauthorized, "refresh token expired")
  }

  user, err := q.GetUserByID(ctx, old.UserID)
  if err != nil {
    return err
  }
  newID, refresh, err := h.issueRefresh(c, q, user.ID)
  if err != nil {
    return err
  }
  if err := q.RevokeRefreshToken(ctx, db.RevokeRefreshTokenParams{
    ID:         old.ID,
    ReplacedBy: uuid.NullUUID{UUID: newID, Valid: true},
  }); err != nil {
    return err
  }
  if err := tx.Commit(ctx); err != nil {
    return err
  }
  return h.respondSession(c, http.StatusOK, user, refresh)
}

func (h *authHandler) signout(c *echo.Context) error {
  if ck, err := c.Cookie(refreshCookie); err == nil && ck.Value != "" {
    if err := h.q.RevokeRefreshTokenByHash(c.Request().Context(), hashToken(ck.Value)); err != nil {
      return err
    }
  }
  clearSessionCookies(c)
  return c.NoContent(http.StatusNoContent)
}

func (h *authHandler) me(c *echo.Context) error {
  userID, _ := c.Get(ctxUserID).(uuid.UUID)
  user, err := h.q.GetUserByID(c.Request().Context(), userID)
  if errors.Is(err, pgx.ErrNoRows) {
    return jsonError(c, http.StatusUnauthorized, "user no longer exists")
  }
  if err != nil {
    return err
  }
  return c.JSON(http.StatusOK, toUserResponse(user))
}

func (h *authHandler) issueRefresh(c *echo.Context, q *db.Queries, userID uuid.UUID) (uuid.UUID, string, error) {
  raw, hash, err := newRefreshToken()
  if err != nil {
    return uuid.Nil, "", err
  }
  id, err := q.CreateRefreshToken(c.Request().Context(), db.CreateRefreshTokenParams{
    UserID:    userID,
    TokenHash: hash,
    UserAgent: c.Request().UserAgent(),
    Ip:        c.RealIP(),
    ExpiresAt: time.Now().Add(h.cfg.refreshTTL),
  })
  return id, raw, err
}

func (h *authHandler) respondSession(c *echo.Context, status int, user db.User, refresh string) error {
  access, err := h.signAccess(user.ID, user.UserType)
  if err != nil {
    return err
  }
  h.setSessionCookies(c, access, refresh)
  return c.JSON(status, sessionResponse{
    AccessToken: access,
    ExpiresIn:   int(h.cfg.accessTTL.Seconds()),
    User:        toUserResponse(user),
  })
}

func toUserResponse(u db.User) userResponse {
  return userResponse{
    ID:        u.ID,
    Username:  u.Username,
    Email:     u.Email,
    FirstName: u.FirstName,
    LastName:  u.LastName,
    UserType:  u.UserType,
  }
}

func validatePassword(pw string, strict bool) string {
  if pw == "" {
    return "password is required"
  }
  if len(pw) > 72 {
    return "password must be at most 72 bytes"
  }
  if !strict {
    return ""
  }

  var upper, lower, digit, special bool
  for _, r := range pw {
    switch {
    case unicode.IsUpper(r):
      upper = true
    case unicode.IsLower(r):
      lower = true
    case unicode.IsDigit(r):
      digit = true
    case unicode.IsPunct(r) || unicode.IsSymbol(r):
      special = true
    }
  }
  if utf8.RuneCountInString(pw) < 8 || !upper || !lower || !digit || !special {
    return "password needs 8+ characters with an uppercase, a lowercase, a number and a special character"
  }
  return ""
}

func decodeJSON(c *echo.Context, v any) error {
  dec := json.NewDecoder(http.MaxBytesReader(nil, c.Request().Body, 1<<16))
  dec.DisallowUnknownFields()
  return dec.Decode(v)
}

func jsonError(c *echo.Context, status int, msg string) error {
  return c.JSON(status, map[string]string{"message": msg})
}

func fieldError(c *echo.Context, status int, field, msg string) error {
  return c.JSON(status, map[string]string{"message": msg, "field": field})
}
