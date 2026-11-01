package site

import (
  "context"
  "fmt"

  "github.com/jackc/pgx/v5"
  "github.com/jackc/pgx/v5/pgxpool"

  "afsc/internal/content"
)

const (
  sqlRerenderBatch = `SELECT id, coalesce(legacy_key, ''), content FROM posts WHERE id > $1 ORDER BY id LIMIT 500`
  sqlRerenderSave  = `
UPDATE posts SET content_html = $2, content_text = $3
WHERE id = $1 AND (content_html, content_text) IS DISTINCT FROM ($2, $3)`
)

// RerenderAll re-renders every post's stored content_html and content_text with the given node partials
// (nil for built-ins) and image sizes. It returns how many posts changed. updated_at is left alone.
func RerenderAll(ctx context.Context, db *pgxpool.Pool, parts content.Partials, images *content.ImageSizes) (int, error) {
  media, err := queryMedia(ctx, db, sqlMedia)
  if err != nil {
    return 0, err
  }
  lookup := func(id int64) (content.MediaInfo, bool) {
    m, ok := media[id]
    return m.MediaInfo, ok
  }
  changed := 0
  var after int64
  for {
    type item struct {
      id   int64
      key  string
      body []byte
    }
    rows, err := db.Query(ctx, sqlRerenderBatch, after)
    if err != nil {
      return changed, err
    }
    items, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (item, error) {
      var it item
      return it, r.Scan(&it.id, &it.key, &it.body)
    })
    if err != nil || len(items) == 0 {
      return changed, err
    }
    batch := &pgx.Batch{}
    for _, it := range items {
      doc, err := content.Parse(it.body)
      if err != nil {
        return changed, fmt.Errorf("post %d: %w", it.id, err)
      }
      h, txt, err := content.Render(doc, content.RenderContext{PostID: wpID(it.id, it.key, "post"), Media: lookup, Partials: parts,
        Images: images})
      if err != nil {
        return changed, fmt.Errorf("post %d: %w", it.id, err)
      }
      batch.Queue(sqlRerenderSave, it.id, h, txt)
      after = it.id
    }
    br := db.SendBatch(ctx, batch)
    for range items {
      tag, err := br.Exec()
      if err != nil {
        br.Close()
        return changed, err
      }
      changed += int(tag.RowsAffected())
    }
    if err := br.Close(); err != nil {
      return changed, err
    }
  }
}
