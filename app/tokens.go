package main

import (
  "crypto/rand"
  "crypto/sha256"
  "encoding/base64"
  "errors"
  "net/http"
  "strings"
  "time"

  "github.com/golang-jwt/jwt/v5"
  "github.com/google/uuid"
  "github.com/jackc/pgx/v5"
  "github.com/labstack/echo/v5"
)

const (
  accessCookie      = "access_token"
  refreshCookie     = "refresh_token"
  accessCookiePath  = "/admin/api/"
  refreshCookiePath = "/admin/api/v1/"
  ctxUserID         = "user_id"
  ctxUserType       = "user_type"
  ctxSessionID      = "session_id"
)

type accessClaims struct {
  UserType  string `json:"user_type"`
  SessionID string `json:"sid"`
  jwt.RegisteredClaims
}

func (h *authHandler) signAccess(userID uuid.UUID, userType string, sessionID uuid.UUID) (string, error) {
  now := time.Now()
  claims := accessClaims{
    UserType:  userType,
    SessionID: sessionID.String(),
    RegisteredClaims: jwt.RegisteredClaims{
      Subject:   userID.String(),
      IssuedAt:  jwt.NewNumericDate(now),
      ExpiresAt: jwt.NewNumericDate(now.Add(h.cfg.accessTTL)),
    },
  }
  return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(h.cfg.secret)
}

func (h *authHandler) parseAccess(token string) (uuid.UUID, uuid.UUID, error) {
  claims := &accessClaims{}
  _, err := jwt.ParseWithClaims(token, claims, func(*jwt.Token) (any, error) {
    return h.cfg.secret, nil
  }, jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}), jwt.WithExpirationRequired())
  if err != nil {
    return uuid.Nil, uuid.Nil, err
  }
  userID, err := uuid.Parse(claims.Subject)
  if err != nil {
    return uuid.Nil, uuid.Nil, err
  }
  sessionID, err := uuid.Parse(claims.SessionID)
  if err != nil {
    return uuid.Nil, uuid.Nil, err
  }
  return userID, sessionID, nil
}

func newRefreshToken() (string, []byte, error) {
  b := make([]byte, 32)
  if _, err := rand.Read(b); err != nil {
    return "", nil, err
  }
  raw := base64.RawURLEncoding.EncodeToString(b)
  return raw, hashToken(raw), nil
}

func hashToken(raw string) []byte {
  sum := sha256.Sum256([]byte(raw))
  return sum[:]
}

func setAuthCookie(c *echo.Context, name, value, path string, maxAge int) {
  c.SetCookie(&http.Cookie{
    Name:     name,
    Value:    value,
    Path:     path,
    MaxAge:   maxAge,
    HttpOnly: true,
    Secure:   true,
    SameSite: http.SameSiteStrictMode,
  })
}

func (h *authHandler) setSessionCookies(c *echo.Context, access, refresh string) {
  setAuthCookie(c, accessCookie, access, accessCookiePath, int(h.cfg.accessTTL.Seconds()))
  setAuthCookie(c, refreshCookie, refresh, refreshCookiePath, int(h.cfg.refreshTTL.Seconds()))
}

func clearSessionCookies(c *echo.Context) {
  setAuthCookie(c, accessCookie, "", accessCookiePath, -1)
  setAuthCookie(c, refreshCookie, "", refreshCookiePath, -1)
}

// requireAuth accepts the access token from the Authorization header, falling back to the httpOnly cookie,
// and rejects it if its session has since been revoked.
func (h *authHandler) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
  return func(c *echo.Context) error {
    var token string
    if auth := c.Request().Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
      token = strings.TrimPrefix(auth, "Bearer ")
    } else if ck, err := c.Cookie(accessCookie); err == nil {
      token = ck.Value
    }
    if token == "" {
      return jsonError(c, http.StatusUnauthorized, "not signed in")
    }

    userID, sessionID, err := h.parseAccess(token)
    if err != nil {
      msg := "invalid access token"
      if errors.Is(err, jwt.ErrTokenExpired) {
        msg = "access token expired"
      }
      return jsonError(c, http.StatusUnauthorized, msg)
    }

    session, err := h.q.GetActiveSession(c.Request().Context(), sessionID)
    if errors.Is(err, pgx.ErrNoRows) || (err == nil && session.ID != userID) {
      return jsonError(c, http.StatusUnauthorized, "session revoked")
    }
    if err != nil {
      return err
    }

    c.Set(ctxUserID, userID)
    c.Set(ctxUserType, session.UserType)
    c.Set(ctxSessionID, sessionID)
    return next(c)
  }
}

func requireAdmin(next echo.HandlerFunc) echo.HandlerFunc {
  return func(c *echo.Context) error {
    if userType, _ := c.Get(ctxUserType).(string); userType != "admin" {
      return jsonError(c, http.StatusForbidden, "admins only")
    }
    return next(c)
  }
}
