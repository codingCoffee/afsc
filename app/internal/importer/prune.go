package importer

import (
  "context"
  "slices"

  "github.com/jackc/pgx/v5"
)

// pruneTables lists tables with legacy keys, children first. Queries are constants; only values are bound.
var pruneTables = []struct {
  table, plan, delete string
  keys                func(*index) []string
}{
  {"comments",
    `SELECT legacy_key FROM comments WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM comments WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.comments) }},
  {"menu_items",
    `SELECT legacy_key FROM menu_items WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM menu_items WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.menuItems) }},
  {"menus",
    `SELECT legacy_key FROM menus WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM menus WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.menus) }},
  {"redirects",
    `SELECT legacy_key FROM redirects WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM redirects WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.redirects) }},
  {"posts",
    `SELECT legacy_key FROM posts WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM posts WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.posts) }},
  {"terms",
    `SELECT legacy_key FROM terms WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM terms WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.terms) }},
  {"authors",
    `SELECT legacy_key FROM authors WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM authors WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.authors) }},
  {"media",
    `SELECT legacy_key FROM media WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM media WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.media) }},
  // admins are never pruned, so an import can't lock everyone out
  {"users",
    `SELECT legacy_key FROM users WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) AND role <> 'admin' ORDER BY legacy_key`,
    `DELETE FROM users WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) AND role <> 'admin'`,
    func(ix *index) []string { return sortedKeys(ix.users) }},
  {"legacy_records",
    `SELECT legacy_key FROM legacy_records WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[])) ORDER BY legacy_key`,
    `DELETE FROM legacy_records WHERE legacy_key LIKE $1 AND NOT (legacy_key = ANY($2::text[]))`,
    func(ix *index) []string { return sortedKeys(ix.legacy) }},
}

func sortedKeys[V any](m map[string]V) []string {
  out := make([]string, 0, len(m))
  for k := range m {
    out = append(out, k)
  }
  slices.Sort(out)
  return out
}

func (w *writer) prunePlan(ctx context.Context, tx pgx.Tx) ([]PruneItem, error) {
  var plan []PruneItem
  for _, t := range pruneTables {
    rows, err := tx.Query(ctx, t.plan, w.like, t.keys(w.ix))
    if err != nil {
      return nil, err
    }
    item := PruneItem{Table: t.table, Sample: []string{}}
    for rows.Next() {
      var k string
      if err := rows.Scan(&k); err != nil {
        rows.Close()
        return nil, err
      }
      item.Count++
      if len(item.Sample) < 10 {
        item.Sample = append(item.Sample, k)
      }
    }
    if err := rows.Err(); err != nil {
      return nil, err
    }
    if item.Count > 0 {
      plan = append(plan, item)
    }
  }
  return plan, nil
}

// prune deletes records from earlier imports of this source that are no longer in the bundle.
func (w *writer) prune(ctx context.Context, confirm func([]PruneItem) bool) error {
  return w.inTx(ctx, func(tx pgx.Tx) error {
    plan, err := w.prunePlan(ctx, tx)
    if err != nil {
      return err
    }
    w.rep.Prune = plan
    if len(plan) == 0 || w.shared != nil {
      return nil
    }
    if !confirm(plan) {
      w.logf("prune cancelled")
      w.rep.Prune = nil
      return nil
    }
    for _, t := range pruneTables {
      tag, err := tx.Exec(ctx, t.delete, w.like, t.keys(w.ix))
      if err != nil {
        return err
      }
      w.deleted(t.table, tag)
    }
    return nil
  })
}
