package posts

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"afsc/internal/authz"
)

// actorAuthor is the author linked to the actor's user, 0 when there is none.
func actorAuthor(ctx context.Context, db dbtx, a Actor) (int64, error) {
	if a.User == uuid.Nil {
		return 0, nil
	}
	var id int64
	err := db.QueryRow(ctx, `SELECT id FROM authors WHERE user_id = $1`, a.User).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	}
	return id, err
}

// ensureAuthor returns the actor's author, creating one linked to their user on their first post (Q7).
func ensureAuthor(ctx context.Context, db dbtx, a Actor) (id int64, created bool, err error) {
	if id, err = actorAuthor(ctx, db, a); id != 0 || err != nil {
		return id, false, err
	}
	var username, display string
	if err := db.QueryRow(ctx, `SELECT username, display_name FROM users WHERE id = $1`, a.User).Scan(&username, &display); err != nil {
		return 0, false, err
	}
	if display == "" {
		display = username
	}
	base := Slugify(username)
	if base == "" {
		base = "author"
	}
	rows, err := db.Query(ctx, `SELECT slug FROM authors WHERE slug = $1 OR slug LIKE $2`, base, likeEscape(base)+"-%")
	if err != nil {
		return 0, false, err
	}
	taken, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		return 0, false, err
	}
	slug := base
	for n := 2; slices.Contains(taken, slug); n++ {
		slug = base + "-" + strconv.Itoa(n)
	}
	err = db.QueryRow(ctx, `INSERT INTO authors (slug, display_name, user_id) VALUES ($1, $2, $3) RETURNING id`,
		slug, display, a.User).Scan(&id)
	return id, err == nil, err
}

func everPublished(ctx context.Context, db dbtx, r *row) (bool, error) {
	if r.Status == "published" || r.Status == "scheduled" || r.Status == "private" {
		return true, nil
	}
	var ok bool
	err := db.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM post_revisions WHERE post_id = $1 AND kind = 'publish')`, r.ID).Scan(&ok)
	return ok, err
}

// canEdit checks type, ownership and the contributor rule for changing an existing post. It returns whether the
// post is the actor's own.
func canEdit(ctx context.Context, db dbtx, a Actor, r *row) (bool, error) {
	if r.Type == "page" && !a.can(authz.EditPages) {
		return false, noCap("only editors and admins can edit pages")
	}
	me, err := actorAuthor(ctx, db, a)
	if err != nil {
		return false, err
	}
	own := me != 0 && slices.Contains(r.AuthorIDs, me)
	if a.can(authz.EditOthers) {
		return own, nil
	}
	if !own {
		return false, denied("you can only edit your own posts")
	}
	if !a.can(authz.PublishOwn) {
		pub, err := everPublished(ctx, db, r)
		if err != nil {
			return false, err
		}
		if pub {
			return false, denied("a published post can only be changed by its author or an editor")
		}
	}
	return true, nil
}

// checkStatus applies §7.2 to the status a save ends in. Ownership is already checked.
func checkStatus(a Actor, to string) error {
	if !slices.Contains(statuses, to) {
		return invalid("status", "status must be one of draft, pending, scheduled, published, private")
	}
	if a.can(authz.PublishOwn) || to == "draft" || to == "pending" {
		return nil
	}
	return &Error{Status: http.StatusForbidden, Code: "capability", Field: "status",
		Message: "contributors can save drafts and submit them for review"}
}
