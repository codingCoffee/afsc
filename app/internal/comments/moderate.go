package comments

import (
	"context"
	"errors"
	"slices"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"afsc/internal/spam"
)

var Statuses = []string{"pending", "approved", "spam", "trash"}

// ErrNotDeletable is returned when a permanent delete includes a comment outside spam and trash.
var ErrNotDeletable = errors.New("only spam and trashed comments can be deleted permanently")

// Changed is one comment a moderation call touched.
type Changed struct {
	ID     int64  `json:"id"`
	PostID int64  `json:"post_id"`
	From   string `json:"from"`
	To     string `json:"to"`
}

type modRow struct {
	id, postID             int64
	status                 string
	body, authorURL, email string
}

func lockRows(ctx context.Context, tx pgx.Tx, ids []int64) ([]modRow, error) {
	rows, err := tx.Query(ctx, `
SELECT id, post_id, status, body, author_url, author_email FROM comments WHERE id = ANY($1) ORDER BY id FOR UPDATE`, ids)
	if err != nil {
		return nil, err
	}
	var out []modRow
	var r modRow
	_, err = pgx.ForEachRow(rows, []any{&r.id, &r.postID, &r.status, &r.body, &r.authorURL, &r.email}, func() error {
		out = append(out, r)
		return nil
	})
	return out, err
}

// SetStatus moves comments to status, trains the classifier on label changes and purges the pages of posts
// whose visible comments changed. Comments already in status are skipped.
func (s *Service) SetStatus(ctx context.Context, ids []int64, status string) ([]Changed, error) {
	if !slices.Contains(Statuses, status) {
		return nil, &ValidationError{Fields: map[string]string{"status": "unknown status"}}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := lockRows(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	var changed []Changed
	var train []spam.Change
	var moved []int64
	var purge []int64
	for _, r := range rows {
		if r.status == status {
			continue
		}
		changed = append(changed, Changed{ID: r.id, PostID: r.postID, From: r.status, To: status})
		moved = append(moved, r.id)
		train = append(train, spam.Change{Tokens: spam.Tokens(r.body, r.authorURL, r.email), From: r.status, To: status})
		if (r.status == "approved" || status == "approved") && !slices.Contains(purge, r.postID) {
			purge = append(purge, r.postID)
		}
	}
	if len(moved) == 0 {
		return changed, nil
	}
	if _, err := tx.Exec(ctx, `UPDATE comments SET status = $2, updated_at = now() WHERE id = ANY($1)`, moved, status); err != nil {
		return nil, err
	}
	if err := spam.Train(ctx, tx, train); err != nil {
		return nil, err
	}
	if len(purge) > 0 {
		if err := s.purge(ctx, tx, purge); err != nil {
			return nil, err
		}
	}
	if err := s.emitStatus(ctx, tx, changed); err != nil {
		return nil, err
	}
	return changed, tx.Commit(ctx)
}

// Delete permanently removes spam and trashed comments. Spam is archived first, so its training signal stays.
// Replies to a deleted comment become top-level (parent_id ON DELETE SET NULL).
func (s *Service) Delete(ctx context.Context, ids []int64) ([]Changed, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	rows, err := lockRows(ctx, tx, ids)
	if err != nil {
		return nil, err
	}
	var out []Changed
	var del []int64
	for _, r := range rows {
		if r.status != "spam" && r.status != "trash" {
			return nil, ErrNotDeletable
		}
		out = append(out, Changed{ID: r.id, PostID: r.postID, From: r.status, To: "deleted"})
		del = append(del, r.id)
	}
	if len(del) == 0 {
		return out, nil
	}
	if err := spam.Archive(ctx, tx, del); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM comments WHERE id = ANY($1)`, del); err != nil {
		return nil, err
	}
	if err := s.emitStatus(ctx, tx, out); err != nil {
		return nil, err
	}
	return out, tx.Commit(ctx)
}

// Reply posts an approved comment as a signed-in user. Staff replies skip the spam pipeline and the closed check.
func (s *Service) Reply(ctx context.Context, user uuid.UUID, parentID int64, body string) (*Result, error) {
	body, verr := validBody(body)
	if verr != nil {
		return nil, verr
	}
	var postID int64
	if err := s.DB.QueryRow(ctx, `SELECT post_id FROM comments WHERE id = $1`, parentID).Scan(&postID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	pid, err := s.resolveParent(ctx, s.DB, postID, parentID, s.discussion().ThreadDepth)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	res := &Result{PostID: postID, ParentID: pid, Status: "approved"}
	var email, authorURL, name string
	err = tx.QueryRow(ctx, `
WITH u AS (
  SELECT u.id, u.email, coalesce(nullif(a.display_name, ''), nullif(u.display_name, ''), u.username) AS name,
         coalesce(a.website, '') AS website
  FROM users u LEFT JOIN authors a ON a.user_id = u.id WHERE u.id = $1
)
INSERT INTO comments (post_id, parent_id, author_name, author_email, author_url, user_id, body, status)
SELECT $2::bigint, nullif($3::bigint, 0), u.name, u.email, u.website, u.id, $4::text, 'approved' FROM u
RETURNING id, author_email, author_url, author_name`, user, postID, pid, body).Scan(&res.ID, &email, &authorURL, &name)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	if err := spam.Train(ctx, tx, []spam.Change{{Tokens: spam.Tokens(body, authorURL, email), To: "approved"}}); err != nil {
		return nil, err
	}
	if err := s.purge(ctx, tx, []int64{postID}); err != nil {
		return nil, err
	}
	if err := s.emitCreated(ctx, tx, res, name); err != nil {
		return nil, err
	}
	return res, tx.Commit(ctx)
}
