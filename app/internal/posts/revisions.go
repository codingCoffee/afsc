package posts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"

	"afsc/internal/content"
)

type RevisionItem struct {
	ID        int64     `json:"id"`
	Kind      string    `json:"kind"`
	UserName  *string   `json:"user_name"`
	CreatedAt time.Time `json:"created_at"`
	Title     string    `json:"title"`
}

type Revision struct {
	RevisionItem
	PostID  int64           `json:"post_id"`
	Excerpt string          `json:"excerpt"`
	Content json.RawMessage `json:"content"`
	SEO     *SeoFields      `json:"seo"`
}

const revisionCols = `r.id, r.kind, coalesce(nullif(u.display_name, ''), u.username), r.created_at, r.title`

// Revisions lists a post's revisions, newest first, without content.
func (s *Service) Revisions(ctx context.Context, postID int64) ([]RevisionItem, error) {
	if _, err := loadRow(ctx, s.DB, postID); err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT `+revisionCols+`
FROM post_revisions r LEFT JOIN users u ON u.id = r.user_id
WHERE r.post_id = $1 ORDER BY r.created_at DESC, r.id DESC`, postID)
	if err != nil {
		return nil, err
	}
	out := []RevisionItem{}
	var it RevisionItem
	_, err = pgx.ForEachRow(rows, []any{&it.ID, &it.Kind, &it.UserName, &it.CreatedAt, &it.Title}, func() error {
		out = append(out, it)
		return nil
	})
	return out, err
}

func revisionNotFound() *Error {
	return fail(http.StatusNotFound, "not_found", "", "revision not found")
}

// Revision returns one revision with its content.
func (s *Service) Revision(ctx context.Context, id int64) (*Revision, error) {
	r := &Revision{}
	var seo []byte
	err := s.DB.QueryRow(ctx, `SELECT `+revisionCols+`, r.post_id, r.excerpt, r.content, r.seo
FROM post_revisions r LEFT JOIN users u ON u.id = r.user_id WHERE r.id = $1`, id).Scan(&r.ID, &r.Kind, &r.UserName,
		&r.CreatedAt, &r.Title, &r.PostID, &r.Excerpt, &r.Content, &seo)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, revisionNotFound()
	}
	if err != nil {
		return nil, err
	}
	if seo != nil {
		r.SEO = &SeoFields{}
		if err := json.Unmarshal(seo, r.SEO); err != nil {
			return nil, err
		}
	}
	return r, nil
}

type diffSide struct {
	postID      int64
	title, text string
	doc         []byte
}

// Diff compares revision id with against: another revision of the same post, or "current" (the saved post). The
// operations turn the revision into against.
func (s *Service) Diff(ctx context.Context, id int64, against string) (*RevisionDiff, error) {
	load := func(sql string, id int64) (*diffSide, error) {
		d := &diffSide{}
		err := s.DB.QueryRow(ctx, sql, id).Scan(&d.postID, &d.title, &d.text, &d.doc)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, revisionNotFound()
		}
		return d, err
	}
	const revSQL = `SELECT post_id, title, content_text, content FROM post_revisions WHERE id = $1`
	a, err := load(revSQL, id)
	if err != nil {
		return nil, err
	}
	var b *diffSide
	if against == "" || against == "current" {
		b, err = load(`SELECT id, title, content_text, content FROM posts WHERE id = $1`, a.postID)
	} else {
		other, perr := strconv.ParseInt(against, 10, 64)
		if perr != nil {
			return nil, invalid("against", "against must be a revision id or current")
		}
		b, err = load(revSQL, other)
	}
	if err != nil {
		return nil, err
	}
	if b.postID != a.postID {
		return nil, invalid("against", "both revisions must belong to the same post")
	}
	da, _ := content.Parse(a.doc)
	db, _ := content.Parse(b.doc)
	return &RevisionDiff{Title: WordDiff(a.title, b.title), Text: WordDiff(a.text, b.text), Blocks: BlockDiff(da, db)}, nil
}

// Preview returns a signed preview link for a post, of revisionID (one of its revisions) or of its saved content
// when revisionID is 0.
func (s *Service) Preview(ctx context.Context, postID, revisionID int64) (string, time.Time, error) {
	r, err := loadRow(ctx, s.DB, postID)
	if err != nil {
		return "", time.Time{}, err
	}
	if r.Status == "trash" {
		return "", time.Time{}, fail(http.StatusConflict, "trashed", "", "restore the post from trash to preview it")
	}
	if revisionID != 0 {
		var ok bool
		if err := s.DB.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM post_revisions WHERE id = $1 AND post_id = $2)`,
			revisionID, postID).Scan(&ok); err != nil {
			return "", time.Time{}, err
		}
		if !ok {
			return "", time.Time{}, revisionNotFound()
		}
	}
	u, exp := s.Site.PreviewURL(postID, revisionID)
	return u, exp, nil
}
