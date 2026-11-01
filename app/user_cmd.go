package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/db"
)

// userCmd creates users and invite links from the shell. Links are printed for the operator to send; nothing is
// emailed.
func userCmd() *cli.Command {
	return &cli.Command{
		Name:  "user",
		Usage: "create users and invite links",
		Commands: []*cli.Command{
			{
				Name:  "create",
				Usage: "create a user with no password and print an invite link",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "email", Required: true},
					&cli.StringFlag{Name: "role", Value: "contributor", Usage: "admin, editor, author or contributor"},
					&cli.StringFlag{Name: "username", Usage: "defaults to the email's local part"},
					&cli.StringFlag{Name: "display-name"},
				},
				Action: userCreate,
			},
			{
				Name:   "invite",
				Usage:  "print a new invite link for an existing user (voids older unused links)",
				Flags:  []cli.Flag{&cli.StringFlag{Name: "email", Required: true}},
				Action: userInvite,
			},
		},
	}
}

func cmdPool(ctx context.Context) (*pgxpool.Pool, string, error) {
	dbURL := os.Getenv("AFSC_DATABASE_URL")
	if dbURL == "" {
		return nil, "", errors.New("AFSC_DATABASE_URL is not set")
	}
	base, err := publicBaseURL()
	if err != nil {
		return nil, "", err
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return nil, "", fmt.Errorf("invalid AFSC_DATABASE_URL: %w", err)
	}
	return pool, base, nil
}

func userCreate(ctx context.Context, cmd *cli.Command) error {
	email := strings.ToLower(strings.TrimSpace(cmd.String("email")))
	if field, msg := checkEmail(email); field != "" {
		return fmt.Errorf("--email: %s", msg)
	}
	role := cmd.String("role")
	if !authz.ValidRole(role) {
		return errors.New("--role must be admin, editor, author or contributor")
	}
	username := strings.ToLower(strings.TrimSpace(cmd.String("username")))
	if username != "" && !usernameRe.MatchString(username) {
		return errors.New("--username: 3-32 characters: a-z, 0-9, _ . -")
	}
	pool, base, err := cmdPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()

	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if username == "" {
		if username, err = deriveUsername(ctx, tx, email); err != nil {
			return err
		}
	}
	user, err := db.New(tx).CreateUserWithRole(ctx, db.CreateUserWithRoleParams{Username: username, Email: email, Role: role})
	if err != nil {
		return err
	}
	if name := strings.TrimSpace(cmd.String("display-name")); name != "" {
		if _, err := tx.Exec(ctx, `UPDATE users SET display_name = $2 WHERE id = $1`, user.ID, name); err != nil {
			return err
		}
	}
	raw, err := createInvite(ctx, tx, user.ID, uuid.NullUUID{})
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Action: "user.create", EntityType: "user", EntityID: user.ID.String(),
		Diff: map[string]string{"email": email, "role": role, "via": "cli"}}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("created %s (%s) as %s\ninvite link, valid 72 h:\n%s\n", username, email, role, inviteURL(base, raw))
	return nil
}

func userInvite(ctx context.Context, cmd *cli.Command) error {
	email := strings.ToLower(strings.TrimSpace(cmd.String("email")))
	pool, base, err := cmdPool(ctx)
	if err != nil {
		return err
	}
	defer pool.Close()
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var id uuid.UUID
	var disabled bool
	err = tx.QueryRow(ctx, `SELECT id, disabled_at IS NOT NULL FROM users WHERE email = $1`, email).Scan(&id, &disabled)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("no user with email %s", email)
	}
	if err != nil {
		return err
	}
	if disabled {
		return fmt.Errorf("%s is disabled; enable it in Admin → Users first", email)
	}
	raw, err := createInvite(ctx, tx, id, uuid.NullUUID{})
	if err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Action: "user.invite", EntityType: "user", EntityID: id.String(),
		Diff: map[string]string{"via": "cli"}}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	fmt.Printf("invite link for %s, valid 72 h:\n%s\n", email, inviteURL(base, raw))
	return nil
}
