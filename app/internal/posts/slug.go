package posts

import (
	"context"
	"fmt"
	"strings"
	"unicode"

	"github.com/jackc/pgx/v5"
)

const maxSlug = 200

// Slugify lowercases s and joins its letter and digit runs with dashes. Apostrophes vanish ("don't" → "dont").
func Slugify(s string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.ToLower(s) {
		switch {
		case r == '\'' || r == '’':
		case unicode.IsLetter(r) || unicode.IsDigit(r):
			if b.Len()+4 > maxSlug {
				return strings.TrimRight(b.String(), "-")
			}
			b.WriteRune(r)
			dash = false
		case b.Len() > 0 && !dash:
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

func likeEscape(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

// UniqueSlug returns base, or base-2, base-3… if a post of the same type under the same parent (trash included)
// already uses it. exclude is the post being saved.
func UniqueSlug(ctx context.Context, db dbtx, typ string, parent *int64, base string, exclude int64) (string, error) {
	rows, err := db.Query(ctx, `
SELECT slug FROM posts
WHERE type = $1 AND parent_id IS NOT DISTINCT FROM $2 AND id <> $3 AND (slug = $4 OR slug LIKE $5)`,
		typ, parent, exclude, base, likeEscape(base)+"-%")
	if err != nil {
		return "", err
	}
	taken, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return "", err
	}
	set := make(map[string]bool, len(taken))
	for _, s := range taken {
		set[s] = true
	}
	if !set[base] {
		return base, nil
	}
	for n := 2; ; n++ {
		if c := fmt.Sprintf("%s-%d", base, n); !set[c] {
			return c, nil
		}
	}
}
