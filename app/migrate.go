package main

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
	"github.com/golang-migrate/migrate/v4/source"
	"github.com/golang-migrate/migrate/v4/source/iofs"
	"github.com/urfave/cli/v3"

	"afsc/internal/plugins"
)

//go:embed db/migrations/*.sql
var migrationsFS embed.FS

var pluginFlag = &cli.StringFlag{Name: "plugin", Usage: "only this plugin's migrations"}

func migrateCmd() *cli.Command {
	return &cli.Command{
		Name:  "migrate",
		Usage: "run database migrations",
		Commands: []*cli.Command{
			{
				Name:      "up",
				Usage:     "apply all core and plugin migrations, or core (or --plugin) up to and including [version]",
				ArgsUsage: "[version]",
				Flags:     []cli.Flag{pluginFlag},
				Action:    func(ctx context.Context, cmd *cli.Command) error { return migrateAction(ctx, cmd, true) },
			},
			{
				Name:      "down",
				Usage:     "revert all core migrations (or --plugin's), or down to [version] (exclusive)",
				ArgsUsage: "[version]",
				Flags:     []cli.Flag{pluginFlag},
				Action:    func(ctx context.Context, cmd *cli.Command) error { return migrateAction(ctx, cmd, false) },
			},
		},
	}
}

func migrateAction(ctx context.Context, cmd *cli.Command, up bool) error {
	var target *uint
	if arg := cmd.Args().First(); arg != "" {
		v, err := strconv.ParseUint(arg, 10, 64)
		if err != nil {
			return fmt.Errorf("invalid migration version %q", arg)
		}
		t := uint(v)
		target = &t
	}
	if id := cmd.String("plugin"); id != "" {
		m, err := pluginMigrations(ctx, id)
		if err != nil {
			return err
		}
		return runMigrationSet("plugin "+id, m.FS, ".", m.VersionTable(), target, up)
	}
	if !up || target != nil {
		return runMigrations(target, up)
	}
	return runAllMigrations(ctx)
}

// runAllMigrations applies every core migration, then each plugin's in registry order.
func runAllMigrations(ctx context.Context) error {
	if err := runMigrations(nil, true); err != nil {
		return err
	}
	rt, err := buildPlugins(ctx, nil, "", nil)
	if err != nil {
		return err
	}
	for _, m := range rt.Migrations() {
		if err := runMigrationSet("plugin "+m.Plugin, m.FS, ".", m.VersionTable(), nil, true); err != nil {
			return err
		}
	}
	return nil
}

func pluginMigrations(ctx context.Context, id string) (plugins.Migrations, error) {
	rt, err := buildPlugins(ctx, nil, "", nil)
	if err != nil {
		return plugins.Migrations{}, err
	}
	for _, m := range rt.Migrations() {
		if m.Plugin == id {
			return m, nil
		}
	}
	return plugins.Migrations{}, fmt.Errorf("plugin %q has no migrations", id)
}

// runMigrations applies or reverts the core migrations.
func runMigrations(target *uint, up bool) error {
	return runMigrationSet("core", migrationsFS, "db/migrations", "", target, up)
}

// migrateURL turns AFSC_DATABASE_URL into a golang-migrate pgx5 URL; table selects the version table.
func migrateURL(table string) (string, error) {
	dbURL := os.Getenv("AFSC_DATABASE_URL")
	if dbURL == "" {
		return "", errors.New("AFSC_DATABASE_URL is not set")
	}
	for _, prefix := range []string{"postgres://", "postgresql://"} {
		if strings.HasPrefix(dbURL, prefix) {
			dbURL = "pgx5://" + strings.TrimPrefix(dbURL, prefix)
			break
		}
	}
	if table == "" {
		return dbURL, nil
	}
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", errors.New("AFSC_DATABASE_URL is not a URL")
	}
	q := u.Query()
	q.Set("x-migrations-table", table)
	u.RawQuery = q.Encode()
	return u.String(), nil
}

func runMigrationSet(label string, fsys fs.FS, dir, table string, target *uint, up bool) error {
	dbURL, err := migrateURL(table)
	if err != nil {
		return err
	}
	src, err := iofs.New(fsys, dir)
	if err != nil {
		return fmt.Errorf("%s: failed to open migrations: %w", label, err)
	}
	m, err := migrate.NewWithSourceInstance("iofs", src, dbURL)
	if err != nil {
		return fmt.Errorf("failed to connect to database: %w", err)
	}
	defer m.Close()

	if target != nil && *target > 0 && !versionExists(src, *target) {
		return fmt.Errorf("%s: migration %04d does not exist", label, *target)
	}
	log.Printf("Migrations: %s", label)
	if up {
		return migrateUp(m, src, target)
	}
	return migrateDown(m, src, target)
}

func migrateUp(m *migrate.Migrate, src source.Driver, target *uint) error {
	cur, hasCur, err := currentVersion(m)
	if err != nil {
		return err
	}
	if target != nil && hasCur && *target < cur {
		return fmt.Errorf("already at %04d, which is past %04d; use migrate down", cur, *target)
	}

	ran := 0
	for {
		var next uint
		if hasCur {
			next, err = src.Next(cur)
		} else {
			next, err = src.First()
		}
		if errors.Is(err, os.ErrNotExist) || (err == nil && target != nil && next > *target) {
			break
		}
		if err != nil {
			return err
		}

		log.Printf("Running migration %04d (%s)", next, migrationName(src, next))
		if err := m.Steps(1); err != nil {
			return fmt.Errorf("migration %04d failed: %w", next, err)
		}
		cur, hasCur = next, true
		ran++
	}

	logDone(ran, cur, hasCur)
	return nil
}

func migrateDown(m *migrate.Migrate, src source.Driver, target *uint) error {
	cur, hasCur, err := currentVersion(m)
	if err != nil {
		return err
	}
	if target != nil && (!hasCur || *target > cur) {
		return fmt.Errorf("current version is below %04d; use migrate up", *target)
	}

	ran := 0
	for hasCur && (target == nil || cur > *target) {
		log.Printf("Reverting migration %04d (%s)", cur, migrationName(src, cur))
		if err := m.Steps(-1); err != nil {
			return fmt.Errorf("revert of %04d failed: %w", cur, err)
		}
		if cur, hasCur, err = currentVersion(m); err != nil {
			return err
		}
		ran++
	}

	logDone(ran, cur, hasCur)
	return nil
}

func currentVersion(m *migrate.Migrate) (uint, bool, error) {
	v, dirty, err := m.Version()
	if errors.Is(err, migrate.ErrNilVersion) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if dirty {
		return 0, false, fmt.Errorf("database is dirty at %04d; fix it manually and force the version", v)
	}
	return v, true, nil
}

func versionExists(src source.Driver, v uint) bool {
	first, err := src.First()
	for err == nil {
		if first == v {
			return true
		}
		first, err = src.Next(first)
	}
	return false
}

func migrationName(src source.Driver, v uint) string {
	r, name, err := src.ReadUp(v)
	if err != nil {
		return "unknown"
	}
	r.Close()
	return name
}

func logDone(ran int, cur uint, hasCur bool) {
	switch {
	case ran == 0 && hasCur:
		log.Printf("Nothing to do, at %04d", cur)
	case ran == 0:
		log.Printf("Nothing to do, no migrations applied")
	case hasCur:
		log.Printf("Done, %d migration(s), now at %04d", ran, cur)
	default:
		log.Printf("Done, %d migration(s), all reverted", ran)
	}
}
