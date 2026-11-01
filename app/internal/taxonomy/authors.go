package taxonomy

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"afsc/internal/content"
	"afsc/internal/media"
)

type Author struct {
	ID            int64             `json:"id"`
	Slug          string            `json:"slug"`
	DisplayName   string            `json:"display_name"`
	Bio           string            `json:"bio"`
	Website       string            `json:"website"`
	Social        map[string]string `json:"social"`
	AvatarMediaID *int64            `json:"avatar_media_id"`
	AvatarURL     *string           `json:"avatar_url"`
	UserID        *string           `json:"user_id"`
	UserName      *string           `json:"user_name"`
	PostCount     int64             `json:"post_count"`
	URL           *string           `json:"url"`
}

type AuthorInput struct {
	Slug          string            `json:"slug"`
	DisplayName   string            `json:"display_name"`
	Bio           string            `json:"bio"`
	Website       string            `json:"website"`
	Social        map[string]string `json:"social"`
	AvatarMediaID *int64            `json:"avatar_media_id"`
	UserID        *string           `json:"user_id"`
}

const (
	authorCols = `a.id, a.slug, a.display_name, a.bio, a.website, a.social, a.avatar_media_id, a.user_id::text,
  coalesce(nullif(u.display_name, ''), u.username),
  (SELECT count(*) FROM post_authors pa JOIN posts p ON p.id = pa.post_id WHERE pa.author_id = a.id AND p.status <> 'trash'),
  m.filename, m.mime, m.width, m.height, encode(m.sha256, 'hex')`
	authorFrom = ` FROM authors a LEFT JOIN users u ON u.id = a.user_id LEFT JOIN media m ON m.id = a.avatar_media_id`
	sqlAuthor  = `SELECT ` + authorCols + authorFrom + ` WHERE a.id = $1`
	sqlAuthors = `SELECT ` + authorCols + `, count(*) OVER ()` + authorFrom + `
WHERE ($1::text = '' OR a.display_name ILIKE $1 OR a.slug ILIKE $1)
ORDER BY lower(a.display_name), a.id
LIMIT $2 OFFSET $3`
)

var socialKeyRe = regexp.MustCompile(`^[a-z0-9_-]{1,30}$`)

func (s *Service) scanAuthor(row pgx.Row, extra ...any) (*Author, error) {
	a := &Author{}
	var filename, mime, sha *string
	var w, h *int
	dst := []any{&a.ID, &a.Slug, &a.DisplayName, &a.Bio, &a.Website, &a.Social, &a.AvatarMediaID, &a.UserID, &a.UserName,
		&a.PostCount, &filename, &mime, &w, &h, &sha}
	if err := row.Scan(append(dst, extra...)...); err != nil {
		return nil, err
	}
	if a.Social == nil {
		a.Social = map[string]string{}
	}
	if a.AvatarMediaID != nil && filename != nil && sha != nil && len(*sha) >= 8 {
		info := content.MediaInfo{ID: *a.AvatarMediaID, Filename: *filename, Version: (*sha)[:8], Mime: *mime}
		if w != nil && h != nil {
			info.Width, info.Height = *w, *h
		}
		var sizes *content.ImageSizes
		if m, err := s.manifest(); err == nil {
			sizes = m.Images()
		}
		a.AvatarURL = media.ThumbURL(info, sizes)
	}
	if s.Site.Settings().Permalinks.AuthorArchivesEnabled {
		if perm, err := s.perm(); err == nil {
			a.URL = s.abs(perm.Author(a.Slug))
		}
	}
	return a, nil
}

// ListAuthors pages through authors; size <= 0 returns all of them.
func (s *Service) ListAuthors(ctx context.Context, q string, page, size int) ([]*Author, int64, error) {
	limit, offset := size, (page-1)*size
	if size <= 0 {
		limit, offset = 1_000_000, 0
	}
	rows, err := s.DB.Query(ctx, sqlAuthors, likePattern(q), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Author{}
	var total int64
	for rows.Next() {
		a, err := s.scanAuthor(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, a)
	}
	return out, total, rows.Err()
}

func (s *Service) GetAuthor(ctx context.Context, id int64) (*Author, error) {
	a, err := s.scanAuthor(s.DB.QueryRow(ctx, sqlAuthor, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("author")
	}
	return a, err
}

// validateAuthor normalises in and checks it against cur (nil when creating). linkUsers is false for callers
// without users.manage: their user_id is ignored and the stored link kept.
func (s *Service) validateAuthor(ctx context.Context, tx pgx.Tx, id int64, in *AuthorInput, cur *Author, linkUsers bool) error {
	in.DisplayName, in.Bio, in.Website = strings.TrimSpace(in.DisplayName), strings.TrimSpace(in.Bio), strings.TrimSpace(in.Website)
	if err := checkLen("display_name", in.DisplayName, 1, 200); err != nil {
		return err
	}
	if err := checkLen("bio", in.Bio, 0, 5000); err != nil {
		return err
	}
	if in.Website != "" && !httpURL(in.Website) {
		return badField("website", "must be an http or https URL")
	}
	if len(in.Social) > 20 {
		return badField("social", "at most 20 profiles")
	}
	for k, v := range in.Social {
		if !socialKeyRe.MatchString(k) {
			return badField("social", "%q isn't a valid profile name", k)
		}
		if !httpURL(v) {
			return badField("social", "the %s profile must be an http or https URL", k)
		}
	}
	if in.Social == nil {
		in.Social = map[string]string{}
	}
	if in.AvatarMediaID != nil {
		var mime string
		err := tx.QueryRow(ctx, `SELECT mime FROM media WHERE id = $1`, *in.AvatarMediaID).Scan(&mime)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && !strings.HasPrefix(mime, "image/")) {
			return badField("avatar_media_id", "pick an image from the media library")
		}
		if err != nil {
			return err
		}
	}
	if !linkUsers {
		in.UserID = nil
		if cur != nil {
			in.UserID = cur.UserID
		}
	} else if in.UserID != nil {
		uid, err := uuid.Parse(*in.UserID)
		if err != nil {
			return badField("user_id", "not a user id")
		}
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM users WHERE id = $1)`, uid).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return badField("user_id", "the user doesn't exist")
		}
		var other string
		err = tx.QueryRow(ctx, `SELECT display_name FROM authors WHERE user_id = $1 AND id <> $2`, uid, id).Scan(&other)
		if err == nil {
			return conflict("user_id", "this user is already linked to %s", other)
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
	}
	current := ""
	if cur != nil {
		current = cur.Slug
	}
	slug, err := pickSlug(in.Slug, current, in.DisplayName, func(slug string) (bool, error) {
		var taken bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM authors WHERE slug = $1 AND id <> $2)`, slug, id).Scan(&taken)
		return taken, err
	})
	in.Slug = slug
	return err
}

// nullUUID converts a validated user id.
func nullUUID(s *string) uuid.NullUUID {
	if s == nil {
		return uuid.NullUUID{}
	}
	id, err := uuid.Parse(*s)
	return uuid.NullUUID{UUID: id, Valid: err == nil}
}

func authorUnique(err error, in AuthorInput) error {
	if strings.Contains(err.Error(), "user_id") {
		return conflict("user_id", "this user is already linked to another author")
	}
	return conflict("slug", "the slug %q is already used", in.Slug)
}

func (s *Service) CreateAuthor(ctx context.Context, in AuthorInput, linkUsers bool, act Actor) (*Author, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := s.validateAuthor(ctx, tx, 0, &in, nil, linkUsers); err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
INSERT INTO authors (slug, display_name, bio, website, social, avatar_media_id, user_id)
VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING id`,
		in.Slug, in.DisplayName, in.Bio, in.Website, in.Social, in.AvatarMediaID, nullUUID(in.UserID)).Scan(&id)
	if isUnique(err) {
		return nil, authorUnique(err, in)
	}
	if err != nil {
		return nil, err
	}
	if err := act.audit(ctx, tx, "author.create", "author", id, in); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	return s.GetAuthor(ctx, id)
}

// UpdateAuthor saves an author; a slug change redirects the old archive.
func (s *Service) UpdateAuthor(ctx context.Context, id int64, in AuthorInput, linkUsers bool, act Actor) (*Author, error) {
	cur, err := s.GetAuthor(ctx, id)
	if err != nil {
		return nil, err
	}
	before := s.Site.AuthorURLs(id)
	ev, err := s.Site.AuthorChange(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM authors WHERE id = $1 FOR UPDATE`, id); err != nil {
		return nil, err
	}
	if err := s.validateAuthor(ctx, tx, id, &in, cur, linkUsers); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
UPDATE authors SET slug = $2, display_name = $3, bio = $4, website = $5, social = $6, avatar_media_id = $7,
  user_id = $8, updated_at = now()
WHERE id = $1`, id, in.Slug, in.DisplayName, in.Bio, in.Website, in.Social, in.AvatarMediaID, nullUUID(in.UserID))
	if isUnique(err) {
		return nil, authorUnique(err, in)
	}
	if err != nil {
		return nil, err
	}
	if err := act.audit(ctx, tx, "author.update", "author", id, map[string]any{"before": cur, "after": in}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	red, err := s.redirect(ctx, before, s.Site.AuthorURLs(id), act)
	if err != nil {
		return nil, err
	}
	now, err := s.Site.AuthorChange(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.Site.Invalidate(ctx, ev.Merge(now).Merge(red)); err != nil {
		return nil, err
	}
	return s.GetAuthor(ctx, id)
}

// DeleteAuthor refuses while posts credit the author, unless reassign names another author to take them over;
// then the old archive redirects to the new one.
func (s *Service) DeleteAuthor(ctx context.Context, id int64, reassign *int64, act Actor) error {
	cur, err := s.GetAuthor(ctx, id)
	if err != nil {
		return err
	}
	if reassign != nil && *reassign == id {
		return badField("reassign", "pick a different author")
	}
	before := s.Site.AuthorURLs(id)
	ev, err := s.Site.AuthorChange(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var n int64
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM post_authors WHERE author_id = $1`, id).Scan(&n); err != nil {
		return err
	}
	if n > 0 && reassign == nil {
		return &Error{Status: http.StatusConflict, Code: "has_posts", Message: "this author has posts; pick an author to take them over",
			Extra: map[string]any{"post_count": n}}
	}
	if reassign != nil {
		var exists bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM authors WHERE id = $1)`, *reassign).Scan(&exists); err != nil {
			return err
		}
		if !exists {
			return badField("reassign", "the author to reassign to doesn't exist")
		}
		if _, err := tx.Exec(ctx, `
INSERT INTO post_authors (post_id, author_id, position)
SELECT post_id, $2, position FROM post_authors WHERE author_id = $1
ON CONFLICT DO NOTHING`, id, *reassign); err != nil {
			return err
		}
	}
	if _, err := tx.Exec(ctx, `DELETE FROM seo WHERE entity_type = 'author' AND entity_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM authors WHERE id = $1`, id); err != nil {
		return err
	}
	if err := act.audit(ctx, tx, "author.delete", "author", id, map[string]any{"author": cur, "reassign": reassign, "posts": n}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	extra := map[string]string{}
	if reassign != nil {
		if err := s.refresh(ctx); err != nil {
			return err
		}
		if p, ok := s.Site.AuthorURLs(*reassign)["author:"+strconv.FormatInt(*reassign, 10)]; ok {
			extra["author:"+strconv.FormatInt(id, 10)] = p
		}
		now, err := s.Site.AuthorChange(ctx, *reassign)
		if err != nil {
			return err
		}
		ev = ev.Merge(now)
	}
	return s.settle(ctx, before, extra, ev, act)
}
