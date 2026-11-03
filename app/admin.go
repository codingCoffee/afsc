package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/db"
)

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

// adminUser is a row in the Users screen.
type adminUser struct {
	ID            uuid.UUID  `json:"id"`
	Username      string     `json:"username"`
	Email         string     `json:"email"`
	FirstName     string     `json:"first_name"`
	LastName      string     `json:"last_name"`
	DisplayName   string     `json:"display_name"`
	Role          string     `json:"role"`
	CreatedAt     time.Time  `json:"created_at"`
	LastLoginAt   *time.Time `json:"last_login_at"`
	DisabledAt    *time.Time `json:"disabled_at"`
	HasPassword   bool       `json:"has_password"`
	InvitePending bool       `json:"invite_pending"`
	Sessions      int64      `json:"sessions"`
}

type sessionRow struct {
	SessionID uuid.UUID `json:"session_id"`
	UserAgent string    `json:"user_agent"`
	IP        string    `json:"ip"`
	StartedAt time.Time `json:"started_at"`
	LastSeen  time.Time `json:"last_seen"`
	ExpiresAt time.Time `json:"expires_at"`
	Current   bool      `json:"current"`
}

func (h *authHandler) registerAdmin(g *echo.Group) {
	h.route(g, http.MethodGet, "/users", authz.ManageUsers, h.listUsers)
	h.route(g, http.MethodPost, "/users", authz.ManageUsers, h.createUser)
	h.route(g, http.MethodGet, "/users/:id", authz.ManageUsers, h.getUser)
	h.route(g, http.MethodPatch, "/users/:id", authz.ManageUsers, h.updateUser)
	h.route(g, http.MethodDelete, "/users/:id", authz.ManageUsers, h.deleteUser)
	h.route(g, http.MethodPost, "/users/:id/disable", authz.ManageUsers, h.disableUser)
	h.route(g, http.MethodPost, "/users/:id/enable", authz.ManageUsers, h.enableUser)
	h.route(g, http.MethodPost, "/users/:id/invite", authz.ManageUsers, h.reinviteUser)
	h.route(g, http.MethodGet, "/users/:id/sessions", authz.ManageUsers, h.userSessions)
	h.route(g, http.MethodPost, "/users/:id/sessions/revoke", authz.ManageUsers, h.revokeUserSessions)
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

const sqlAdminUsers = `
SELECT u.id, u.username, u.email, u.first_name, u.last_name, u.display_name, u.role, u.created_at, u.last_login_at,
       u.disabled_at, u.password_hash IS NOT NULL,
       EXISTS (SELECT 1 FROM user_invites i WHERE i.user_id = u.id AND i.used_at IS NULL AND i.expires_at > now()),
       (SELECT count(DISTINCT rt.session_id) FROM refresh_tokens rt
        WHERE rt.user_id = u.id AND rt.revoked_at IS NULL AND rt.expires_at > now()),
       count(*) OVER ()
FROM users u
WHERE ($1 = '' OR u.username ILIKE $1 OR u.email ILIKE $1 OR u.display_name ILIKE $1)
  AND ($2 = '' OR u.role = $2)
  AND ($3::uuid IS NULL OR u.id = $3)
ORDER BY u.created_at ASC, u.id ASC
LIMIT $4 OFFSET $5`

func (h *authHandler) queryUsers(ctx context.Context, q, role string, id *uuid.UUID, page, size int32) ([]adminUser, int64, error) {
	like := ""
	if q = strings.TrimSpace(q); q != "" {
		like = "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
	}
	rows, err := h.pool.Query(ctx, sqlAdminUsers, like, role, id, size, (page-1)*size)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []adminUser{}
	var total int64
	for rows.Next() {
		var u adminUser
		if err := rows.Scan(&u.ID, &u.Username, &u.Email, &u.FirstName, &u.LastName, &u.DisplayName, &u.Role,
			&u.CreatedAt, &u.LastLoginAt, &u.DisabledAt, &u.HasPassword, &u.InvitePending, &u.Sessions, &total); err != nil {
			return nil, 0, err
		}
		out = append(out, u)
	}
	return out, total, rows.Err()
}

func (h *authHandler) listUsers(c *echo.Context) error {
	page, size := parsePage(c)
	items, total, err := h.queryUsers(c.Request().Context(), c.QueryParam("q"), c.QueryParam("role"), nil, page, size)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, pageResponse[adminUser]{Items: items, Page: page, PageSize: size, Total: total})
}

func userParam(c *echo.Context) (uuid.UUID, error) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return uuid.Nil, jsonError(c, http.StatusBadRequest, "invalid user id")
	}
	return id, nil
}

func (h *authHandler) getUser(c *echo.Context) error {
	id, err := userParam(c)
	if err != nil {
		return err
	}
	items, _, err := h.queryUsers(c.Request().Context(), "", "", &id, 1, 1)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		return jsonError(c, http.StatusNotFound, "user not found")
	}
	return c.JSON(http.StatusOK, items[0])
}

var usernameStrip = regexp.MustCompile(`[^a-z0-9_.-]+`)

// deriveUsername turns an email's local part into a free username.
func deriveUsername(ctx context.Context, q db.DBTX, email string) (string, error) {
	base := strings.ToLower(email)
	if i := strings.IndexByte(base, '@'); i >= 0 {
		base = base[:i]
	}
	base = strings.Trim(usernameStrip.ReplaceAllString(base, "-"), "-.")
	if len(base) > 26 {
		base = base[:26]
	}
	for len(base) < 3 {
		base += "x"
	}
	for i := 0; i < 1000; i++ {
		name := base
		if i > 0 {
			name = fmt.Sprintf("%s-%d", base, i+1)
		}
		var taken bool
		if err := q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE username = $1)`, name).Scan(&taken); err != nil {
			return "", err
		}
		if !taken {
			return name, nil
		}
	}
	return "", errors.New("no free username")
}

// createUser makes an account with no password and returns a one-time invite link (§7.1). The link is shown
// once; the admin sends it.
func (h *authHandler) createUser(c *echo.Context) error {
	var req struct {
		Email       string `json:"email"`
		Username    string `json:"username"`
		DisplayName string `json:"display_name"`
		FirstName   string `json:"first_name"`
		LastName    string `json:"last_name"`
		Role        string `json:"role"`
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	req.Email = strings.ToLower(strings.TrimSpace(req.Email))
	req.Username = strings.ToLower(strings.TrimSpace(req.Username))
	req.DisplayName, req.FirstName, req.LastName = strings.TrimSpace(req.DisplayName), strings.TrimSpace(req.FirstName),
		strings.TrimSpace(req.LastName)
	if field, msg := checkEmail(req.Email); field != "" {
		return fieldError(c, http.StatusBadRequest, field, msg)
	}
	if field, msg := checkNames(req.FirstName, req.LastName, req.DisplayName); field != "" {
		return fieldError(c, http.StatusBadRequest, field, msg)
	}
	if req.Role == "" {
		req.Role = "contributor"
	}
	if !authz.ValidRole(req.Role) {
		return fieldError(c, http.StatusBadRequest, "role", "must be admin, editor, author or contributor")
	}
	if req.Username != "" && !usernameRe.MatchString(req.Username) {
		return fieldError(c, http.StatusBadRequest, "username", "3-32 characters: a-z, 0-9, _ . -")
	}

	ctx := c.Request().Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if req.Username == "" {
		if req.Username, err = deriveUsername(ctx, tx, req.Email); err != nil {
			return err
		}
	}
	user, err := h.q.WithTx(tx).CreateUserWithRole(ctx, db.CreateUserWithRoleParams{
		Username: req.Username, Email: req.Email, FirstName: req.FirstName, LastName: req.LastName, Role: req.Role,
	})
	if err != nil {
		if resp, ok := uniqueViolation(c, err); ok {
			return resp
		}
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET display_name = $2 WHERE id = $1`, user.ID, req.DisplayName); err != nil {
		return err
	}
	raw, err := createInvite(ctx, tx, user.ID, actor(c))
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "user.create", EntityType: "user",
		EntityID: user.ID.String(), Diff: map[string]string{"email": req.Email, "role": req.Role}, IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	items, _, err := h.queryUsers(ctx, "", "", &user.ID, 1, 1)
	if err != nil || len(items) == 0 {
		return err
	}
	return c.JSON(http.StatusCreated, map[string]any{"user": items[0], "invite_url": inviteURL(h.cfg.baseURL+h.cfg.basePath, raw),
		"expires_at": time.Now().Add(inviteTTL)})
}

func (h *authHandler) reinviteUser(c *echo.Context) error {
	id, err := userParam(c)
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	target, err := h.q.WithTx(tx).GetUserByID(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return jsonError(c, http.StatusNotFound, "user not found")
	}
	if err != nil {
		return err
	}
	if target.DisabledAt != nil {
		return jsonError(c, http.StatusConflict, "enable the account before inviting it")
	}
	raw, err := createInvite(ctx, tx, id, actor(c))
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "user.invite", EntityType: "user",
		EntityID: id.String(), IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"invite_url": inviteURL(h.cfg.baseURL+h.cfg.basePath, raw),
		"expires_at": time.Now().Add(inviteTTL)})
}

// lockedTarget loads a user under the signup lock, so last-admin checks can't race.
func (h *authHandler) lockedTarget(ctx context.Context, tx pgx.Tx, id uuid.UUID) (db.User, error) {
	q := h.q.WithTx(tx)
	if err := q.LockUserSignup(ctx); err != nil {
		return db.User{}, err
	}
	return q.GetUserByID(ctx, id)
}

// otherActiveAdmins counts enabled admins other than id.
func otherActiveAdmins(ctx context.Context, tx pgx.Tx, id uuid.UUID) (int64, error) {
	var n int64
	err := tx.QueryRow(ctx, `SELECT count(*) FROM users WHERE role = 'admin' AND disabled_at IS NULL AND id <> $1`, id).Scan(&n)
	return n, err
}

func (h *authHandler) updateUser(c *echo.Context) error {
	id, err := userParam(c)
	if err != nil {
		return err
	}
	var req struct {
		Role        *string `json:"role"`
		DisplayName *string `json:"display_name"`
		FirstName   *string `json:"first_name"`
		LastName    *string `json:"last_name"`
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	if req.Role != nil && !authz.ValidRole(*req.Role) {
		return fieldError(c, http.StatusBadRequest, "role", "must be admin, editor, author or contributor")
	}
	ctx := c.Request().Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	target, err := h.lockedTarget(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return jsonError(c, http.StatusNotFound, "user not found")
	}
	if err != nil {
		return err
	}
	diff := map[string]audit.Change{}
	first, last, display := target.FirstName, target.LastName, target.DisplayName
	if req.FirstName != nil {
		first = strings.TrimSpace(*req.FirstName)
	}
	if req.LastName != nil {
		last = strings.TrimSpace(*req.LastName)
	}
	if req.DisplayName != nil {
		display = strings.TrimSpace(*req.DisplayName)
	}
	if field, msg := checkNames(first, last, display); field != "" {
		return fieldError(c, http.StatusBadRequest, field, msg)
	}
	for k, v := range map[string][2]string{"first_name": {target.FirstName, first}, "last_name": {target.LastName, last},
		"display_name": {target.DisplayName, display}} {
		if v[0] != v[1] {
			diff[k] = audit.Change{From: v[0], To: v[1]}
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET first_name = $2, last_name = $3, display_name = $4 WHERE id = $1`,
		id, first, last, display); err != nil {
		return err
	}
	if req.Role != nil && *req.Role != target.Role {
		if target.Role == "admin" {
			if n, err := otherActiveAdmins(ctx, tx, id); err != nil {
				return err
			} else if n == 0 {
				return jsonError(c, http.StatusConflict, "can't demote the last admin")
			}
		}
		if _, err := tx.Exec(ctx, `UPDATE users SET role = $2 WHERE id = $1`, id, *req.Role); err != nil {
			return err
		}
		// a privilege change ends every session, so new tokens carry the new role
		if err := h.q.WithTx(tx).RevokeUserRefreshTokens(ctx, id); err != nil {
			return err
		}
		diff["role"] = audit.Change{From: target.Role, To: *req.Role}
	}
	if len(diff) > 0 {
		action := "user.update"
		if _, ok := diff["role"]; ok {
			action = "user.role"
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: action, EntityType: "user",
			EntityID: id.String(), Diff: diff, IP: clientIP(c)}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.getUser(c)
}

func (h *authHandler) disableUser(c *echo.Context) error { return h.setDisabled(c, true) }
func (h *authHandler) enableUser(c *echo.Context) error  { return h.setDisabled(c, false) }

func (h *authHandler) setDisabled(c *echo.Context, disable bool) error {
	id, err := userParam(c)
	if err != nil {
		return err
	}
	if disable && id == currentUser(c) {
		return jsonError(c, http.StatusConflict, "you can't disable your own account")
	}
	ctx := c.Request().Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	target, err := h.lockedTarget(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return jsonError(c, http.StatusNotFound, "user not found")
	}
	if err != nil {
		return err
	}
	if disable && target.Role == "admin" {
		if n, err := otherActiveAdmins(ctx, tx, id); err != nil {
			return err
		} else if n == 0 {
			return jsonError(c, http.StatusConflict, "can't disable the last admin")
		}
	}
	action := "user.enable"
	if disable {
		action = "user.disable"
		if _, err := tx.Exec(ctx, `UPDATE users SET disabled_at = now() WHERE id = $1 AND disabled_at IS NULL`, id); err != nil {
			return err
		}
		if err := h.q.WithTx(tx).RevokeUserRefreshTokens(ctx, id); err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, `DELETE FROM user_invites WHERE user_id = $1 AND used_at IS NULL`, id); err != nil {
			return err
		}
	} else if _, err := tx.Exec(ctx, `UPDATE users SET disabled_at = NULL WHERE id = $1`, id); err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: action, EntityType: "user", EntityID: id.String(),
		IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.getUser(c)
}

func (h *authHandler) deleteUser(c *echo.Context) error {
	id, err := userParam(c)
	if err != nil {
		return err
	}
	if id == currentUser(c) {
		return jsonError(c, http.StatusConflict, "you can't delete your own account")
	}

	ctx := c.Request().Context()
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	target, err := h.lockedTarget(ctx, tx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return jsonError(c, http.StatusNotFound, "user not found")
	}
	if err != nil {
		return err
	}
	if target.Role == "admin" {
		if n, err := otherActiveAdmins(ctx, tx, id); err != nil {
			return err
		} else if n == 0 {
			return jsonError(c, http.StatusConflict, "can't delete the last admin")
		}
	}
	if _, err := h.q.WithTx(tx).DeleteUser(ctx, id); err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "user.delete", EntityType: "user",
		EntityID: id.String(), Diff: map[string]string{"username": target.Username, "email": target.Email}, IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

const sqlSessions = `
SELECT DISTINCT ON (session_id) session_id, user_agent, ip, session_started_at, created_at, expires_at
FROM refresh_tokens
WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()
ORDER BY session_id, created_at DESC`

func listSessions(ctx context.Context, q db.DBTX, userID, current uuid.UUID) ([]sessionRow, error) {
	rows, err := q.Query(ctx, sqlSessions, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []sessionRow{}
	for rows.Next() {
		var s sessionRow
		if err := rows.Scan(&s.SessionID, &s.UserAgent, &s.IP, &s.StartedAt, &s.LastSeen, &s.ExpiresAt); err != nil {
			return nil, err
		}
		s.Current = s.SessionID == current
		out = append(out, s)
	}
	return out, rows.Err()
}

func currentSession(c *echo.Context) uuid.UUID {
	id, _ := c.Get(ctxSessionID).(uuid.UUID)
	return id
}

func (h *authHandler) userSessions(c *echo.Context) error {
	id, err := userParam(c)
	if err != nil {
		return err
	}
	items, err := listSessions(c.Request().Context(), h.pool, id, currentSession(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

// revokeUserSessions ends one session (session_id) or all of them (no body).
func (h *authHandler) revokeUserSessions(c *echo.Context) error {
	id, err := userParam(c)
	if err != nil {
		return err
	}
	var req struct {
		SessionID *uuid.UUID `json:"session_id"`
	}
	if c.Request().ContentLength != 0 {
		if err := decodeJSON(c, &req); err != nil {
			return jsonError(c, http.StatusBadRequest, "invalid request body")
		}
	}
	ctx := c.Request().Context()
	var n int64
	if req.SessionID != nil {
		tag, err := h.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now()
WHERE user_id = $1 AND session_id = $2 AND revoked_at IS NULL`, id, *req.SessionID)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
	} else {
		tag, err := h.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now() WHERE user_id = $1 AND revoked_at IS NULL`, id)
		if err != nil {
			return err
		}
		n = tag.RowsAffected()
	}
	diff := map[string]any{"all": req.SessionID == nil}
	if req.SessionID != nil {
		diff["session_id"] = req.SessionID.String()
	}
	if err := audit.Write(ctx, h.pool, audit.Entry{Actor: actor(c), Action: "session.revoke", EntityType: "user",
		EntityID: id.String(), Diff: diff, IP: clientIP(c)}); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]int64{"revoked": n})
}
