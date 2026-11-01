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

	"afsc/internal/authz"
)

const (
	accessCookie      = "access_token"
	refreshCookie     = "refresh_token"
	accessCookiePath  = "/admin/api/"
	refreshCookiePath = "/admin/api/v1/"
	ctxUserID         = "user_id"
	ctxRole           = "role"
	ctxSessionID      = "session_id"
)

type accessClaims struct {
	Role      string `json:"role"`
	SessionID string `json:"sid"`
	jwt.RegisteredClaims
}

func (h *authHandler) signAccess(userID uuid.UUID, role string, sessionID uuid.UUID) (string, error) {
	now := time.Now()
	claims := accessClaims{
		Role:      role,
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

// setSessionCookies stores only the refresh token; the access token travels in the Authorization header.
func (h *authHandler) setSessionCookies(c *echo.Context, refresh string, expires time.Time) {
	setAuthCookie(c, refreshCookie, refresh, refreshCookiePath, max(int(time.Until(expires).Seconds()), 1))
	// drop the access cookie older clients were given
	setAuthCookie(c, accessCookie, "", accessCookiePath, -1)
}

func clearSessionCookies(c *echo.Context) {
	setAuthCookie(c, accessCookie, "", accessCookiePath, -1)
	setAuthCookie(c, refreshCookie, "", refreshCookiePath, -1)
}

// requireAuth accepts the access token only from the Authorization header, which a cross-site request can't set,
// and rejects it if its session has since been revoked.
func (h *authHandler) requireAuth(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		var token string
		if auth := c.Request().Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
			token = strings.TrimPrefix(auth, "Bearer ")
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
		c.Set(ctxRole, session.Role)
		c.Set(ctxSessionID, sessionID)
		return next(c)
	}
}

// requireCap rejects a role that lacks cap. The "capability" code tells it apart from ownership 403s.
func requireCap(cp authz.Cap) echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c *echo.Context) error {
			if role, _ := c.Get(ctxRole).(string); !authz.Can(role, cp) {
				return c.JSON(http.StatusForbidden, map[string]string{"message": "your role can't do this", "code": "capability"})
			}
			return next(c)
		}
	}
}

func currentUser(c *echo.Context) uuid.UUID {
	id, _ := c.Get(ctxUserID).(uuid.UUID)
	return id
}

func currentRole(c *echo.Context) string {
	r, _ := c.Get(ctxRole).(string)
	return r
}

func actor(c *echo.Context) uuid.NullUUID {
	id, ok := c.Get(ctxUserID).(uuid.UUID)
	return uuid.NullUUID{UUID: id, Valid: ok}
}
