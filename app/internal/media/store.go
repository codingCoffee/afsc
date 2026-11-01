package media

import (
  "context"
  "errors"
  "time"

  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"

  "afsc/internal/content"
)

// Record is a media row as the handler needs it.
type Record struct {
  ID        int64
  Key       string // storage key
  Filename  string
  Mime      string
  Bytes     int64
  SHA256    string // hex
  Width     int
  Height    int
  FocalX    *float64
  FocalY    *float64
  UpdatedAt time.Time
}

func (r *Record) Version() string { return r.SHA256[:8] }

func (r *Record) focal() *[2]float64 {
  if r.FocalX == nil || r.FocalY == nil {
    return nil
  }
  return &[2]float64{*r.FocalX, *r.FocalY}
}

func (r *Record) Info() content.MediaInfo {
  return content.MediaInfo{ID: r.ID, Filename: r.Filename, Version: r.Version(), Mime: r.Mime, Width: r.Width, Height: r.Height}
}

// Legacy is a media_legacy_paths row with its media.
type Legacy struct {
  Path    string
  Variant string // orig or resize
  Width   int
  Height  int
  Crop    bool
  Media   Record
}

// Store is the database side of the handler; PG implements it.
type Store interface {
  Media(ctx context.Context, id int64) (*Record, error)
  LegacyPath(ctx context.Context, paths []string) (*Legacy, error)
  AddHits(ctx context.Context, hits map[string]Hit) error
}

type Hit struct {
  N    int64
  Last time.Time
}

const (
  mediaCols = `m.id, m.storage_key, m.filename, m.mime, m.bytes, encode(m.sha256, 'hex'),
  coalesce(m.width, 0), coalesce(m.height, 0), m.focal_x::float8, m.focal_y::float8, m.updated_at`
  sqlMediaByID = `SELECT ` + mediaCols + ` FROM media m WHERE m.id = $1`
  sqlLegacy    = `
SELECT lp.path, lp.variant, coalesce(lp.width, 0), coalesce(lp.height, 0), lp.crop, ` + mediaCols + `
FROM media_legacy_paths lp JOIN media m ON m.id = lp.media_id
WHERE lp.path = ANY($1::text[])
ORDER BY array_position($1::text[], lp.path)
LIMIT 1`
  sqlAddHits = `
UPDATE media_legacy_paths AS lp
SET hits = lp.hits + u.n, last_hit_at = greatest(lp.last_hit_at, u.last)
FROM unnest($1::text[], $2::bigint[], $3::timestamptz[]) AS u(path, n, last)
WHERE lp.path = u.path`
)

type PG struct{ DB *pgxpool.Pool }

func recordDest(r *Record) []any {
  return []any{&r.ID, &r.Key, &r.Filename, &r.Mime, &r.Bytes, &r.SHA256, &r.Width, &r.Height, &r.FocalX, &r.FocalY, &r.UpdatedAt}
}

func (p *PG) Media(ctx context.Context, id int64) (*Record, error) {
  r := &Record{}
  err := p.DB.QueryRow(ctx, sqlMediaByID, id).Scan(recordDest(r)...)
  if errors.Is(err, pgx.ErrNoRows) {
    return nil, nil
  }
  return r, err
}

func (p *PG) LegacyPath(ctx context.Context, paths []string) (*Legacy, error) {
  l := &Legacy{}
  dst := append([]any{&l.Path, &l.Variant, &l.Width, &l.Height, &l.Crop}, recordDest(&l.Media)...)
  err := p.DB.QueryRow(ctx, sqlLegacy, paths).Scan(dst...)
  if errors.Is(err, pgx.ErrNoRows) {
    return nil, nil
  }
  return l, err
}

func (p *PG) AddHits(ctx context.Context, hits map[string]Hit) error {
  paths := make([]string, 0, len(hits))
  ns := make([]int64, 0, len(hits))
  lasts := make([]time.Time, 0, len(hits))
  for path, h := range hits {
    paths, ns, lasts = append(paths, path), append(ns, h.N), append(lasts, h.Last)
  }
  _, err := p.DB.Exec(ctx, sqlAddHits, paths, ns, lasts)
  return err
}
