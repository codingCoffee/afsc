package importer

import (
  "context"
  "errors"
  "fmt"

  "github.com/jackc/pgx/v5/pgxpool"

  "afsc/internal/storage"
  "afsc/internal/theme"
)

// ErrInvalid means validation failed; the report lists why and nothing was written.
var ErrInvalid = errors.New("bundle is invalid")

type Options struct {
  BundleDir string
  DryRun    bool
  Prune     bool
  // Confirm is asked before prune deletes anything; it is never called in a dry run.
  Confirm func([]PruneItem) bool
  Theme   *theme.Manifest
  Pool    *pgxpool.Pool
  Storage storage.Storage
  Logf    func(format string, args ...any)
}

// Run validates the whole bundle, then writes it. A dry run writes inside one transaction and rolls it back.
func Run(ctx context.Context, o Options) (*Report, error) {
  if o.Logf == nil {
    o.Logf = func(string, ...any) {}
  }
  rep := newReport(o.DryRun)

  m, err := readManifest(o.BundleDir)
  if err != nil {
    rep.errorf("", "manifest", "%v", err)
    return rep, ErrInvalid
  }
  rep.Source = m.Source
  checkManifest(m, rep)
  if len(rep.Errors) > 0 {
    return rep, ErrInvalid
  }

  var allowed, locations map[string]bool
  if o.Theme != nil {
    allowed = o.Theme.AllowedClasses()
    locations = map[string]bool{}
    for _, l := range o.Theme.MenuLocations {
      locations[l] = true
    }
    if m.Theme != "" && m.Theme != o.Theme.Name {
      rep.warnf("", "manifest.theme", "bundle was validated against theme %q, importing with %q", m.Theme, o.Theme.Name)
    }
  } else {
    rep.warnf("", "theme", "no theme given; every content class will be dropped")
  }

  o.Logf("validating bundle")
  ix := validate(o.BundleDir, m, allowed, locations, rep)
  if len(rep.Errors) > 0 {
    return rep, ErrInvalid
  }
  if !o.DryRun && o.Storage == nil {
    return rep, errors.New("object storage is required unless --dry-run is set")
  }

  conn, err := o.Pool.Acquire(ctx)
  if err != nil {
    return rep, err
  }
  defer conn.Release()
  if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext('afsc_import'))`); err != nil {
    return rep, err
  }
  defer conn.Exec(context.WithoutCancel(ctx), `SELECT pg_advisory_unlock(hashtext('afsc_import'))`)

  w := &writer{conn: conn, dir: o.BundleDir, ix: ix, rep: rep, store: o.Storage, logf: o.Logf, like: m.Source + ":%"}
  if o.DryRun {
    tx, err := conn.Begin(ctx)
    if err != nil {
      return rep, err
    }
    defer tx.Rollback(context.WithoutCancel(ctx))
    w.shared = tx
  }

  if err := w.run(ctx); err != nil {
    return rep, err
  }
  if o.Prune {
    if err := w.prune(ctx, o.Confirm); err != nil {
      return rep, fmt.Errorf("prune: %w", err)
    }
  }
  return rep, nil
}
