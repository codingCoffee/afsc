package main

import (
  "context"
  "errors"
  "fmt"
  "log"
  "os"
  "strconv"
  "strings"

  "github.com/golang-migrate/migrate/v4"
  _ "github.com/golang-migrate/migrate/v4/database/pgx/v5"
  "github.com/golang-migrate/migrate/v4/source"
  "github.com/golang-migrate/migrate/v4/source/file"
  "github.com/urfave/cli/v3"
)

const migrationsURL = "file://db/migrations"

func migrateCmd() *cli.Command {
  return &cli.Command{
    Name:  "migrate",
    Usage: "run database migrations",
    Commands: []*cli.Command{
      {
        Name:      "up",
        Usage:     "apply all migrations, or up to and including [version]",
        ArgsUsage: "[version]",
        Action:    func(ctx context.Context, cmd *cli.Command) error { return migrateAction(cmd, true) },
      },
      {
        Name:      "down",
        Usage:     "revert all migrations, or down to [version] (exclusive)",
        ArgsUsage: "[version]",
        Action:    func(ctx context.Context, cmd *cli.Command) error { return migrateAction(cmd, false) },
      },
    },
  }
}

func migrateAction(cmd *cli.Command, up bool) error {
  var target *uint
  if arg := cmd.Args().First(); arg != "" {
    v, err := strconv.ParseUint(arg, 10, 64)
    if err != nil {
      return fmt.Errorf("invalid migration version %q", arg)
    }
    t := uint(v)
    target = &t
  }
  return runMigrations(target, up)
}

func runMigrations(target *uint, up bool) error {
  dbURL := os.Getenv("DATABASE_URL")
  if dbURL == "" {
    return errors.New("DATABASE_URL is not set")
  }
  for _, prefix := range []string{"postgres://", "postgresql://"} {
    if strings.HasPrefix(dbURL, prefix) {
      dbURL = "pgx5://" + strings.TrimPrefix(dbURL, prefix)
      break
    }
  }

  src, err := (&file.File{}).Open(migrationsURL)
  if err != nil {
    return fmt.Errorf("failed to open migrations: %w", err)
  }
  m, err := migrate.NewWithSourceInstance("file", src, dbURL)
  if err != nil {
    return fmt.Errorf("failed to connect to database: %w", err)
  }
  defer m.Close()

  if target != nil && *target > 0 && !versionExists(src, *target) {
    return fmt.Errorf("migration %04d does not exist", *target)
  }

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
