package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/mail"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/db"
	"afsc/internal/plugins"
	"afsc/internal/ratelimit"
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
	refreshTTL      time.Duration // absolute session lifetime
	idleTTL         time.Duration // a session unused this long ends; 0 means refreshTTL
	strictPasswords bool
	baseURL         string // AFSC_PUBLIC_BASE_URL: the only accepted Origin, and the base of invite links
}

type authHandler struct {
	pool    *pgxpool.Pool
	q       *db.Queries
	cfg     authConfig
	limiter *ratelimit.Limiter
	caps    map[string]authz.Cap // "METHOD /admin/api/v1/path" → capability, filled by route()
	now     func() time.Time
	plugins *plugins.Runtime // receives user.login; may be nil
}

type userResponse struct {
	ID          uuid.UUID `json:"id"`
	Username    string    `json:"username"`
	Email       string    `json:"email"`
	FirstName   string    `json:"first_name"`
	LastName    string    `json:"last_name"`
	DisplayName string    `json:"display_name"`
	Role        string    `json:"role"`
	CreatedAt   time.Time `json:"created_at"`
	Caps        []string  `json:"caps"`
}

type sessionResponse struct {
	AccessToken string       `json:"access_token"`
	ExpiresIn   int          `json:"expires_in"`
	User        userResponse `json:"user"`
}

func newAuthHandler(pool *pgxpool.Pool, cfg authConfig) *authHandler {
	if cfg.idleTTL <= 0 || cfg.idleTTL > cfg.refreshTTL {
		cfg.idleTTL = cfg.refreshTTL
	}
	return &authHandler{pool: pool, q: db.New(pool), cfg: cfg, limiter: &ratelimit.Limiter{DB: pool},
		caps: map[string]authz.Cap{}, now: time.Now}
}

func (h *authHandler) register(g *echo.Group) {
	h.route(g, http.MethodPost, "/signin", authz.Public, h.signin)
	h.route(g, http.MethodPost, "/token_refresh", authz.Public, h.tokenRefresh)
	h.route(g, http.MethodPost, "/signout", authz.Public, h.signout)
	h.route(g, http.MethodGet, "/setup/status", authz.Public, h.setupStatus)
	h.route(g, http.MethodPost, "/setup", authz.Public, h.setup)
	h.route(g, http.MethodGet, "/invites/:token", authz.Public, h.getInvite)
	h.route(g, http.MethodPost, "/invites/:token", authz.Public, h.acceptInvite)

	h.route(g, http.MethodGet, "/me", authz.Self, h.me)
	h.registerProfile(g)
	h.registerAdmin(g)
	h.route(g, http.MethodGet, "/audit", authz.ReadAudit, h.listAudit)
}

type newUserRequest struct {
	Username    string `json:"username"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	FirstName   string `json:"first_name"`
	LastName    string `json:"last_name"`
	DisplayName string `json:"display_name"`
}

// normalize trims/lowercases fields in place and returns the invalid field and message, if any.
func (r *newUserRequest) normalize(strictPasswords bool) (string, string) {
	r.Username = strings.ToLower(strings.TrimSpace(r.Username))
	r.Email = strings.ToLower(strings.TrimSpace(r.Email))
	r.FirstName = strings.TrimSpace(r.FirstName)
	r.LastName = strings.TrimSpace(r.LastName)
	r.DisplayName = strings.TrimSpace(r.DisplayName)

	if !usernameRe.MatchString(r.Username) {
		return "username", "3-32 characters: a-z, 0-9, _ . -"
	}
	if field, msg := checkEmail(r.Email); field != "" {
		return field, msg
	}
	if field, msg := checkNames(r.FirstName, r.LastName, r.DisplayName); field != "" {
		return field, msg
	}
	if msg := validatePassword(r.Password, strictPasswords); msg != "" {
		return "password", msg
	}
	return "", ""
}

func checkEmail(email string) (string, string) {
	if addr, err := mail.ParseAddress(email); err != nil || addr.Address != email || len(email) > 254 {
		return "email", "enter a valid email address"
	}
	return "", ""
}

func checkNames(first, last, display string) (string, string) {
	switch {
	case utf8.RuneCountInString(first) > 100:
		return "first_name", "must be at most 100 characters"
	case utf8.RuneCountInString(last) > 100:
		return "last_name", "must be at most 100 characters"
	case utf8.RuneCountInString(display) > 200:
		return "display_name", "must be at most 200 characters"
	}
	return "", ""
}

func uniqueViolation(c *echo.Context, err error) (error, bool) {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		return nil, false
	}
	switch pgErr.ConstraintName {
	case "users_username_key":
		return fieldError(c, http.StatusConflict, "username", "username is already taken"), true
	case "users_email_key":
		return fieldError(c, http.StatusConflict, "email", "email is already registered"), true
	}
	return nil, false
}

// tooMany answers 429 with Retry-After for a locked key.
func tooMany(c *echo.Context, d time.Duration) error {
	c.Response().Header().Set("Retry-After", strconv.Itoa(int(d.Seconds())+1))
	return jsonError(c, http.StatusTooManyRequests, "too many attempts, try again later")
}

// checkLocks returns the longest lockout among keys.
func (h *authHandler) checkLocks(ctx context.Context, keys ...string) (time.Duration, error) {
	var longest time.Duration
	for _, k := range keys {
		d, err := h.limiter.Locked(ctx, k)
		if err != nil {
			return 0, err
		}
		longest = max(longest, d)
	}
	return longest, nil
}

// fail records a failed attempt against the IP and, when given, the account key.
func (h *authHandler) fail(c *echo.Context, acctKey string) error {
	ctx := c.Request().Context()
	if _, err := h.limiter.Fail(ctx, "ip:"+clientIP(c), ratelimit.IP); err != nil {
		return err
	}
	if acctKey != "" {
		lock, err := h.limiter.Fail(ctx, acctKey, ratelimit.Account)
		if err != nil {
			return err
		}
		if lock > 0 {
			return audit.Write(ctx, h.pool, audit.Entry{Action: "login.lockout", EntityType: "login", EntityID: acctKey,
				Diff: map[string]string{"duration": lock.String()}, IP: clientIP(c)})
		}
	}
	return nil
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
	acctKey := "acct:" + login
	if d, err := h.checkLocks(ctx, "ip:"+clientIP(c), acctKey); err != nil {
		return err
	} else if d > 0 {
		return tooMany(c, d)
	}

	user, err := h.q.GetUserByLogin(ctx, login)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	ok := false
	switch {
	case err != nil, user.PasswordHash == nil, user.DisabledAt != nil:
		// imported users have no password until they accept an invite
		_ = bcrypt.CompareHashAndPassword(dummyHash, []byte(req.Password))
	default:
		ok = bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(req.Password)) == nil
	}
	if !ok {
		entry := audit.Entry{Action: "login.failure", EntityType: "login", EntityID: login, IP: clientIP(c)}
		if err == nil {
			entry.EntityType, entry.EntityID = "user", user.ID.String()
		}
		if err := audit.Write(ctx, h.pool, entry); err != nil {
			return err
		}
		if err := h.fail(c, acctKey); err != nil {
			return err
		}
		return jsonError(c, http.StatusUnauthorized, "invalid credentials")
	}

	if err := h.limiter.Reset(ctx, acctKey); err != nil {
		return err
	}
	if cost, err := bcrypt.Cost([]byte(*user.PasswordHash)); err == nil && cost != bcryptCost {
		if hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost); err == nil {
			if _, err := h.pool.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, user.ID, string(hash)); err != nil {
				return err
			}
		}
	}
	return h.startSession(c, http.StatusOK, user, "login.success")
}

// startSession signs user in on a new session, records the login and writes action to the audit log.
func (h *authHandler) startSession(c *echo.Context, status int, user db.User, action string) error {
	ctx := c.Request().Context()
	if _, err := h.pool.Exec(ctx, `UPDATE users SET last_login_at = now() WHERE id = $1`, user.ID); err != nil {
		return err
	}
	sessionID := uuid.New()
	_, refresh, expires, err := h.issueRefresh(c, h.pool, user.ID, sessionID, h.now())
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, h.pool, audit.Entry{Actor: uuid.NullUUID{UUID: user.ID, Valid: true}, Action: action,
		EntityType: "user", EntityID: user.ID.String(), IP: clientIP(c)}); err != nil {
		return err
	}
	if err := plugins.Emit(ctx, h.plugins, h.pool, plugins.UserLogin,
		plugins.LoginData{UserID: user.ID, Username: user.Username, Role: user.Role}); err != nil {
		return err
	}
	return h.respondSession(c, status, user, refresh, sessionID, expires)
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
		clearSessionCookies(c)
		// only a token that was already rotated means replay; signout/blacklist revocations are just dead tokens
		if !old.ReplacedBy.Valid {
			return jsonError(c, http.StatusUnauthorized, "session revoked")
		}
		if err := q.RevokeUserRefreshTokens(ctx, old.UserID); err != nil {
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		log.Printf("refresh token reuse detected for user %s, revoked all sessions", old.UserID)
		return jsonError(c, http.StatusUnauthorized, "refresh token reused, all sessions revoked")
	}
	if h.now().After(old.ExpiresAt) {
		clearSessionCookies(c)
		return jsonError(c, http.StatusUnauthorized, "session expired")
	}

	user, err := q.GetUserByID(ctx, old.UserID)
	if err != nil {
		return err
	}
	if user.DisabledAt != nil {
		clearSessionCookies(c)
		return jsonError(c, http.StatusUnauthorized, "account disabled")
	}
	var started time.Time
	if err := tx.QueryRow(ctx, `SELECT session_started_at FROM refresh_tokens WHERE id = $1`, old.ID).Scan(&started); err != nil {
		return err
	}
	newID, refresh, expires, err := h.issueRefresh(c, tx, user.ID, old.SessionID, started)
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
	return h.respondSession(c, http.StatusOK, user, refresh, old.SessionID, expires)
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
	user, err := h.q.GetUserByID(c.Request().Context(), currentUser(c))
	if errors.Is(err, pgx.ErrNoRows) {
		return jsonError(c, http.StatusUnauthorized, "user no longer exists")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, toUserResponse(user))
}

// sessionExpiry is when a session's next refresh token expires: after the idle timeout, but never past the
// session's absolute lifetime.
func (h *authHandler) sessionExpiry(started time.Time) time.Time {
	idle := h.now().Add(h.cfg.idleTTL)
	if abs := started.Add(h.cfg.refreshTTL); abs.Before(idle) {
		return abs
	}
	return idle
}

func (h *authHandler) issueRefresh(c *echo.Context, tx db.DBTX, userID, sessionID uuid.UUID, started time.Time) (uuid.UUID, string, time.Time, error) {
	ctx := c.Request().Context()
	raw, hash, err := newRefreshToken()
	if err != nil {
		return uuid.Nil, "", time.Time{}, err
	}
	expires := h.sessionExpiry(started)
	id, err := db.New(tx).CreateRefreshToken(ctx, db.CreateRefreshTokenParams{
		UserID:    userID,
		SessionID: sessionID,
		TokenHash: hash,
		UserAgent: c.Request().UserAgent(),
		Ip:        clientIP(c),
		ExpiresAt: expires,
	})
	if err != nil {
		return uuid.Nil, "", time.Time{}, err
	}
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET session_started_at = $2 WHERE id = $1`, id, started); err != nil {
		return uuid.Nil, "", time.Time{}, err
	}
	return id, raw, expires, nil
}

func (h *authHandler) respondSession(c *echo.Context, status int, user db.User, refresh string, sessionID uuid.UUID, expires time.Time) error {
	access, err := h.signAccess(user.ID, user.Role, sessionID)
	if err != nil {
		return err
	}
	h.setSessionCookies(c, refresh, expires)
	return c.JSON(status, sessionResponse{
		AccessToken: access,
		ExpiresIn:   int(h.cfg.accessTTL.Seconds()),
		User:        toUserResponse(user),
	})
}

func toUserResponse(u db.User) userResponse {
	caps := []string{string(authz.Self)}
	for _, c := range authz.Caps(u.Role) {
		caps = append(caps, string(c))
	}
	return userResponse{
		ID:          u.ID,
		Username:    u.Username,
		Email:       u.Email,
		FirstName:   u.FirstName,
		LastName:    u.LastName,
		DisplayName: u.DisplayName,
		Role:        u.Role,
		CreatedAt:   u.CreatedAt,
		Caps:        caps,
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
	return decodeJSONLimit(c, v, 1<<16)
}

func decodeJSONLimit(c *echo.Context, v any, limit int64) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, c.Request().Body, limit))
	dec.DisallowUnknownFields()
	return dec.Decode(v)
}

func jsonError(c *echo.Context, status int, msg string) error {
	return c.JSON(status, map[string]string{"message": msg})
}

func fieldError(c *echo.Context, status int, field, msg string) error {
	return c.JSON(status, map[string]string{"message": msg, "field": field})
}
