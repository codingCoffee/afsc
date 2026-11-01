package main

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"afsc/internal/audit"
	"afsc/internal/authz"
)

func (h *authHandler) registerProfile(g *echo.Group) {
	h.route(g, http.MethodPatch, "/me", authz.Self, h.updateMe)
	h.route(g, http.MethodPost, "/me/password", authz.Self, h.changePassword)
	h.route(g, http.MethodGet, "/me/sessions", authz.Self, h.mySessions)
	h.route(g, http.MethodPost, "/me/sessions/revoke-others", authz.Self, h.revokeOtherSessions)
}

func (h *authHandler) updateMe(c *echo.Context) error {
	var req struct {
		FirstName   string `json:"first_name"`
		LastName    string `json:"last_name"`
		DisplayName string `json:"display_name"`
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	first, last, display := strings.TrimSpace(req.FirstName), strings.TrimSpace(req.LastName), strings.TrimSpace(req.DisplayName)
	if field, msg := checkNames(first, last, display); field != "" {
		return fieldError(c, http.StatusBadRequest, field, msg)
	}
	ctx := c.Request().Context()
	if _, err := h.pool.Exec(ctx, `UPDATE users SET first_name = $2, last_name = $3, display_name = $4 WHERE id = $1`,
		currentUser(c), first, last, display); err != nil {
		return err
	}
	return h.me(c)
}

// changePassword checks the current password, sets the new one and ends every other session.
func (h *authHandler) changePassword(c *echo.Context) error {
	var req struct {
		Current string `json:"current"`
		New     string `json:"new"`
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	acctKey := "acct:pw:" + currentUser(c).String()
	if d, err := h.checkLocks(ctx, acctKey); err != nil {
		return err
	} else if d > 0 {
		return tooMany(c, d)
	}
	user, err := h.q.GetUserByID(ctx, currentUser(c))
	if err != nil {
		return err
	}
	if user.PasswordHash == nil || bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(req.Current)) != nil {
		if err := h.fail(c, acctKey); err != nil {
			return err
		}
		return fieldError(c, http.StatusBadRequest, "current", "the current password is wrong")
	}
	if msg := validatePassword(req.New, h.cfg.strictPasswords); msg != "" {
		return fieldError(c, http.StatusBadRequest, "new", msg)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.New), bcryptCost)
	if err != nil {
		return err
	}
	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, user.ID, string(hash)); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now()
WHERE user_id = $1 AND session_id <> $2 AND revoked_at IS NULL`, user.ID, currentSession(c)); err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "user.password", EntityType: "user",
		EntityID: user.ID.String(), IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (h *authHandler) mySessions(c *echo.Context) error {
	items, err := listSessions(c.Request().Context(), h.pool, currentUser(c), currentSession(c))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

func (h *authHandler) revokeOtherSessions(c *echo.Context) error {
	ctx := c.Request().Context()
	tag, err := h.pool.Exec(ctx, `UPDATE refresh_tokens SET revoked_at = now()
WHERE user_id = $1 AND session_id <> $2 AND revoked_at IS NULL`, currentUser(c), currentSession(c))
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, h.pool, audit.Entry{Actor: actor(c), Action: "session.revoke_others", EntityType: "user",
		EntityID: currentUser(c).String(), IP: clientIP(c)}); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]int64{"revoked": tag.RowsAffected()})
}

// listAudit is the read-only audit log (§10), filterable by actor, action, entity and date range.
func (h *authHandler) listAudit(c *echo.Context) error {
	page, size := parsePage(c)
	f := audit.Filter{Actor: c.QueryParam("actor"), Action: c.QueryParam("action"), EntityType: c.QueryParam("entity_type"),
		EntityID: c.QueryParam("entity_id"), Page: int(page), Size: int(size)}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"from", &f.From}, {"to", &f.To}} {
		if v := c.QueryParam(p.name); v != "" {
			t, err := time.Parse(time.RFC3339, v)
			if err != nil {
				return fieldError(c, http.StatusBadRequest, p.name, "use an RFC 3339 time")
			}
			*p.dst = &t
		}
	}
	items, total, err := audit.List(c.Request().Context(), h.pool, f)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, pageResponse[audit.Row]{Items: items, Page: page, PageSize: size, Total: total})
}
