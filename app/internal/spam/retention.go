package spam

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const retentionBatch = 5000

// RetentionResult counts what one retention run changed.
type RetentionResult struct {
	SpamDeleted int64
	IPCleared   int64
}

// Archive stores the token sets of the given spam comments in spam_archive inside tx, so deleting them keeps
// their training signal and a re-import doesn't bring them back (Q1). Non-spam ids are ignored.
func Archive(ctx context.Context, tx pgx.Tx, ids []int64) error {
	rows, err := tx.Query(ctx, `
SELECT coalesce(legacy_key, 'afsc:comment:' || id), body, author_url, author_email
FROM comments WHERE id = ANY($1) AND status = 'spam'`, ids)
	if err != nil {
		return err
	}
	var keys []string
	var sets [][]string
	var key, body, authorURL, email string
	if _, err := pgx.ForEachRow(rows, []any{&key, &body, &authorURL, &email}, func() error {
		keys = append(keys, key)
		sets = append(sets, Tokens(body, authorURL, email))
		return nil
	}); err != nil {
		return err
	}
	for i := range keys {
		if _, err := tx.Exec(ctx, `INSERT INTO spam_archive (comment_key, tokens) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
			keys[i], sets[i]); err != nil {
			return err
		}
	}
	return nil
}

// Retain deletes spam older than spamDays (archiving its tokens first) and clears IP and user agent on comments
// older than ipDays (the comments.retention job, §11). Both run in batches so no transaction holds many locks.
func Retain(ctx context.Context, pool *pgxpool.Pool, now time.Time, spamDays, ipDays int) (RetentionResult, error) {
	var res RetentionResult
	spamBefore := now.AddDate(0, 0, -spamDays)
	for {
		n, err := retainSpamBatch(ctx, pool, spamBefore)
		res.SpamDeleted += n
		if err != nil || n < retentionBatch {
			if err != nil {
				return res, err
			}
			break
		}
	}
	ipBefore := now.AddDate(0, 0, -ipDays)
	for {
		tag, err := pool.Exec(ctx, `
UPDATE comments SET ip = NULL, user_agent = NULL
WHERE id IN (SELECT id FROM comments WHERE created_at < $1 AND (ip IS NOT NULL OR user_agent IS NOT NULL) LIMIT $2)`,
			ipBefore, retentionBatch)
		if err != nil {
			return res, err
		}
		res.IPCleared += tag.RowsAffected()
		if tag.RowsAffected() < retentionBatch {
			break
		}
	}
	return res, nil
}

func retainSpamBatch(ctx context.Context, pool *pgxpool.Pool, before time.Time) (int64, error) {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback(ctx)
	rows, err := tx.Query(ctx, `
SELECT id FROM comments WHERE status = 'spam' AND created_at < $1 ORDER BY id LIMIT $2 FOR UPDATE SKIP LOCKED`,
		before, retentionBatch)
	if err != nil {
		return 0, err
	}
	ids, err := pgx.CollectRows(rows, pgx.RowTo[int64])
	if err != nil || len(ids) == 0 {
		return 0, err
	}
	if err := Archive(ctx, tx, ids); err != nil {
		return 0, err
	}
	tag, err := tx.Exec(ctx, `DELETE FROM comments WHERE id = ANY($1)`, ids)
	if err != nil {
		return 0, err
	}
	return tag.RowsAffected(), tx.Commit(ctx)
}
