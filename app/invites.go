package main

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"afsc/internal/audit"
	"afsc/internal/db"
)

const inviteTTL = 72 * time.Hour

func nullID(id uuid.UUID) uuid.NullUUID { return uuid.NullUUID{UUID: id, Valid: true} }

// createInvite replaces any unused invite for userID with a new one and returns the raw token. Tokens are random
// 32 bytes, stored hashed, valid 72 h and single-use (§7.1).
func createInvite(ctx context.Context, q db.DBTX, userID uuid.UUID, by uuid.NullUUID) (string, error) {
	raw, hash, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	if _, err := q.Exec(ctx, `DELETE FROM user_invites WHERE user_id = $1 AND used_at IS NULL`, userID); err != nil {
		return "", err
	}
	_, err = q.Exec(ctx, `INSERT INTO user_invites (token_hash, user_id, created_by, expires_at) VALUES ($1, $2, $3, $4)`,
		hash, userID, by, time.Now().Add(inviteTTL))
	return raw, err
}

func inviteURL(baseURL, raw string) string {
	return strings.TrimSuffix(baseURL, "/") + "/admin/invite/" + raw
}

type inviteInfo struct {
	UserID      uuid.UUID `json:"-"`
	Email       string    `json:"email"`
	Username    string    `json:"username"`
	DisplayName string    `json:"display_name"`
	ExpiresAt   time.Time `json:"expires_at"`
	usedAt      *time.Time
	disabledAt  *time.Time
}

var errInvite = errors.New("invite not found")

func lookupInvite(ctx context.Context, q db.DBTX, raw string, forUpdate bool) (*inviteInfo, error) {
	sql := `
SELECT u.id, u.email, u.username, u.display_name, i.expires_at, i.used_at, u.disabled_at
FROM user_invites i JOIN users u ON u.id = i.user_id WHERE i.token_hash = $1`
	if forUpdate {
		sql += ` FOR UPDATE OF i`
	}
	var in inviteInfo
	err := q.QueryRow(ctx, sql, hashToken(raw)).Scan(&in.UserID, &in.Email, &in.Username, &in.DisplayName, &in.ExpiresAt,
		&in.usedAt, &in.disabledAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, errInvite
	}
	if err != nil {
		return nil, err
	}
	if in.usedAt != nil || in.disabledAt != nil || time.Now().After(in.ExpiresAt) {
		return nil, errInvite
	}
	return &in, nil
}

func (h *authHandler) getInvite(c *echo.Context) error {
	ctx := c.Request().Context()
	if d, err := h.checkLocks(ctx, "ip:"+clientIP(c)); err != nil {
		return err
	} else if d > 0 {
		return tooMany(c, d)
	}
	in, err := lookupInvite(ctx, h.pool, c.Param("token"), false)
	if errors.Is(err, errInvite) {
		if err := h.fail(c, ""); err != nil {
			return err
		}
		return jsonError(c, http.StatusNotFound, "this invite link is invalid, used or expired")
	}
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, in)
}

// acceptInvite sets the password, uses up the invite, ends any other sessions and signs the user in.
func (h *authHandler) acceptInvite(c *echo.Context) error {
	var req struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	if d, err := h.checkLocks(ctx, "ip:"+clientIP(c)); err != nil {
		return err
	} else if d > 0 {
		return tooMany(c, d)
	}
	if msg := validatePassword(req.Password, h.cfg.strictPasswords); msg != "" {
		return fieldError(c, http.StatusBadRequest, "password", msg)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcryptCost)
	if err != nil {
		return err
	}

	tx, err := h.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	in, err := lookupInvite(ctx, tx, c.Param("token"), true)
	if errors.Is(err, errInvite) {
		if err := h.fail(c, ""); err != nil {
			return err
		}
		return jsonError(c, http.StatusNotFound, "this invite link is invalid, used or expired")
	}
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE user_invites SET used_at = now() WHERE token_hash = $1`, hashToken(c.Param("token"))); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `UPDATE users SET password_hash = $2 WHERE id = $1`, in.UserID, string(hash)); err != nil {
		return err
	}
	if err := h.q.WithTx(tx).RevokeUserRefreshTokens(ctx, in.UserID); err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: nullID(in.UserID), Action: "invite.accept", EntityType: "user",
		EntityID: in.UserID.String(), IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	user, err := h.q.GetUserByID(ctx, in.UserID)
	if err != nil {
		return err
	}
	return h.startSession(c, http.StatusOK, user, "login.success")
}
