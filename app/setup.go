package main

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"
	"golang.org/x/crypto/bcrypt"

	"afsc/internal/audit"
	"afsc/internal/db"
)

// ensureSetupToken makes sure a setup token exists while there are no users (§7.1). It returns the token only when
// this call created it, so exactly one replica logs it. A non-empty envToken (AFSC_SETUP_TOKEN) always wins.
func ensureSetupToken(ctx context.Context, pool *pgxpool.Pool, envToken string) (string, error) {
	var users int64
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users); err != nil {
		return "", err
	}
	if users > 0 {
		_, err := pool.Exec(ctx, `DELETE FROM setup_tokens`)
		return "", err
	}
	if envToken != "" {
		_, err := pool.Exec(ctx, `
INSERT INTO setup_tokens (id, token_hash) VALUES (1, $1)
ON CONFLICT (id) DO UPDATE SET token_hash = EXCLUDED.token_hash`, hashToken(envToken))
		return "", err
	}
	raw, hash, err := newRefreshToken()
	if err != nil {
		return "", err
	}
	tag, err := pool.Exec(ctx, `INSERT INTO setup_tokens (id, token_hash) VALUES (1, $1) ON CONFLICT (id) DO NOTHING`, hash)
	if err != nil || tag.RowsAffected() == 0 {
		return "", err
	}
	return raw, nil
}

func (h *authHandler) setupNeeded(ctx context.Context, q db.DBTX) (bool, error) {
	var users int64
	err := q.QueryRow(ctx, `SELECT count(*) FROM users`).Scan(&users)
	return users == 0, err
}

// setupStatus answers 200 while setup is open and 404 afterwards, like the setup endpoint itself.
func (h *authHandler) setupStatus(c *echo.Context) error {
	needed, err := h.setupNeeded(c.Request().Context(), h.pool)
	if err != nil {
		return err
	}
	if !needed {
		return jsonError(c, http.StatusNotFound, "not found")
	}
	return c.JSON(http.StatusOK, map[string]bool{"needed": true})
}

func (h *authHandler) setup(c *echo.Context) error {
	var req struct {
		Token string `json:"token"`
		newUserRequest
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	ipKey := "ip:" + clientIP(c)
	if d, err := h.checkLocks(ctx, ipKey); err != nil {
		return err
	} else if d > 0 {
		return tooMany(c, d)
	}
	if field, msg := req.normalize(h.cfg.strictPasswords); field != "" {
		return fieldError(c, http.StatusBadRequest, field, msg)
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
	q := h.q.WithTx(tx)
	// one setup at a time: the loser sees a user and gets 404
	if err := q.LockUserSignup(ctx); err != nil {
		return err
	}
	if needed, err := h.setupNeeded(ctx, tx); err != nil {
		return err
	} else if !needed {
		return jsonError(c, http.StatusNotFound, "not found")
	}
	var stored []byte
	err = tx.QueryRow(ctx, `SELECT token_hash FROM setup_tokens WHERE id = 1`).Scan(&stored)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	if len(stored) == 0 || subtle.ConstantTimeCompare(stored, hashToken(req.Token)) != 1 {
		if err := h.fail(c, ""); err != nil {
			return err
		}
		return fieldError(c, http.StatusForbidden, "token", "the setup token is wrong; find it in the server log")
	}

	hashStr := string(hash)
	user, err := q.CreateUserWithRole(ctx, db.CreateUserWithRoleParams{
		Username:     req.Username,
		Email:        req.Email,
		PasswordHash: &hashStr,
		FirstName:    req.FirstName,
		LastName:     req.LastName,
		Role:         "admin",
	})
	if err != nil {
		if resp, ok := uniqueViolation(c, err); ok {
			return resp
		}
		return err
	}
	if req.DisplayName != "" {
		if _, err := tx.Exec(ctx, `UPDATE users SET display_name = $2 WHERE id = $1`, user.ID, req.DisplayName); err != nil {
			return err
		}
		user.DisplayName = req.DisplayName
	}
	if _, err := tx.Exec(ctx, `DELETE FROM setup_tokens`); err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: nullID(user.ID), Action: "setup", EntityType: "user",
		EntityID: user.ID.String(), IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return h.startSession(c, http.StatusCreated, user, "login.success")
}
