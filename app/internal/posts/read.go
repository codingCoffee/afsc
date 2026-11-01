package posts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"afsc/internal/authz"
	"afsc/internal/content"
)

// Get is the editor's view of a post, with the actor's autosave when it is newer than the post.
func (s *Service) Get(ctx context.Context, a Actor, id int64) (*Detail, error) {
	r, err := loadRow(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	d := &Detail{ID: r.ID, Type: r.Type, Status: r.Status, Title: r.Title, Slug: r.Slug, PublishedAt: r.PublishedAt,
		Excerpt: r.Excerpt, Content: r.Content, ParentID: r.ParentID, Template: r.Template, FeaturedMediaID: r.FeaturedMediaID,
		AuthorIDs: r.AuthorIDs, CategoryIDs: r.CategoryIDs, PrimaryCategoryID: r.PrimaryCategoryID, TagIDs: r.TagIDs,
		CommentStatus: r.CommentStatus, Sticky: r.Sticky, MenuOrder: r.MenuOrder, Version: r.Version,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	if d.SEO, err = LoadSEO(ctx, s.DB, "post", id); err != nil {
		return nil, err
	}
	authors, err := s.authorRefs(ctx, []int64{id})
	if err != nil {
		return nil, err
	}
	d.Authors = authors[id]
	if d.Authors == nil {
		d.Authors = []AuthorRef{}
	}
	if s.visible(r) {
		ref := r.ref()
		u := s.Site.PostURL(&ref)
		d.URL = &u
	}
	if r.FeaturedMediaID != nil {
		if d.FeaturedMedia, err = s.mediaItem(ctx, *r.FeaturedMediaID); err != nil {
			return nil, err
		}
	}
	if a.User != uuid.Nil {
		as := &Autosave{}
		var seo []byte
		err := s.DB.QueryRow(ctx, `
SELECT id, title, excerpt, content, seo, created_at FROM post_revisions
WHERE post_id = $1 AND user_id = $2 AND kind = 'autosave' AND created_at > $3`, id, a.User, r.UpdatedAt).
			Scan(&as.RevisionID, &as.Title, &as.Excerpt, &as.Content, &seo, &as.SavedAt)
		switch {
		case err == nil:
			if seo != nil {
				as.SEO = &SeoFields{}
				if err := json.Unmarshal(seo, as.SEO); err != nil {
					return nil, err
				}
			}
			d.Autosave = as
		case !errors.Is(err, pgx.ErrNoRows):
			return nil, err
		}
	}
	me, err := actorAuthor(ctx, s.DB, a)
	if err != nil {
		return nil, err
	}
	own := me != 0 && slices.Contains(r.AuthorIDs, me)
	pages := r.Type != "page" || a.can(authz.EditPages)
	d.CanPublish = pages && (a.can(authz.EditOthers) || (own && a.can(authz.PublishOwn)))
	d.CanOverwrite = a.can(authz.EditOthers)
	return d, nil
}

func (s *Service) authorRefs(ctx context.Context, ids []int64) (map[int64][]AuthorRef, error) {
	rows, err := s.DB.Query(ctx, `
SELECT pa.post_id, a.id, a.display_name, a.slug FROM post_authors pa JOIN authors a ON a.id = pa.author_id
WHERE pa.post_id = ANY($1::bigint[]) ORDER BY pa.post_id, pa.position, a.id`, ids)
	if err != nil {
		return nil, err
	}
	out := map[int64][]AuthorRef{}
	var postID int64
	var ar AuthorRef
	_, err = pgx.ForEachRow(rows, []any{&postID, &ar.ID, &ar.DisplayName, &ar.Slug}, func() error {
		out[postID] = append(out[postID], ar)
		return nil
	})
	return out, err
}

const sqlMediaItem = `
SELECT m.id, m.filename, m.mime, m.bytes, m.width, m.height, m.title, m.alt, m.caption, m.description, m.focal_x,
       m.focal_y, substr(encode(m.sha256, 'hex'), 1, 8), m.library_visible,
       (SELECT count(*) FROM media_legacy_paths lp WHERE lp.media_id = m.id), u.username, m.created_at
FROM media m LEFT JOIN users u ON u.id = m.uploaded_by
WHERE m.id = $1`

func (s *Service) mediaItem(ctx context.Context, id int64) (*MediaItem, error) {
	m := &MediaItem{}
	var version string
	err := s.DB.QueryRow(ctx, sqlMediaItem, id).Scan(&m.ID, &m.Filename, &m.Mime, &m.Bytes, &m.Width, &m.Height, &m.Title,
		&m.Alt, &m.Caption, &m.Description, &m.FocalX, &m.FocalY, &version, &m.LibraryVisible, &m.LegacyPathCount,
		&m.UploadedBy, &m.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	info := content.MediaInfo{ID: m.ID, Filename: m.Filename, Version: version, Mime: m.Mime}
	if m.Width != nil && m.Height != nil {
		info.Width, info.Height = *m.Width, *m.Height
	}
	m.URL = content.MediaURL(info, "orig")
	if v, _, _, ok := s.Site.ImageSizes().Variant(info, "thumbnail"); ok {
		u := content.MediaURL(info, v)
		m.ThumbURL = &u
	}
	return m, nil
}

// ListFilter is GET /posts.
type ListFilter struct {
	Type     string
	Status   string // "" = everything but trash
	AuthorID int64
	TermID   int64
	Mine     bool
	Q        string
	From, To *time.Time
	Sort     string // date, -date (default), title, -title, modified, -modified
	Page     int
	Size     int
}

var sorts = map[string]string{
	"-date":     "coalesce(p.published_at, p.updated_at) DESC, p.id DESC",
	"date":      "coalesce(p.published_at, p.updated_at) ASC, p.id ASC",
	"title":     "lower(p.title) ASC, p.id ASC",
	"-title":    "lower(p.title) DESC, p.id DESC",
	"modified":  "p.updated_at ASC, p.id ASC",
	"-modified": "p.updated_at DESC, p.id DESC",
}

const sqlList = `
SELECT p.id, p.type, p.status, p.title, p.slug, p.published_at, p.updated_at, p.sticky, p.version,
       EXISTS (SELECT 1 FROM post_authors pa WHERE pa.post_id = p.id AND pa.author_id = $9),
       count(*) OVER ()
FROM posts p
WHERE p.type = $1
  AND CASE WHEN $2 = '' THEN p.status <> 'trash' ELSE p.status = $2 END
  AND ($3::bigint = 0 OR EXISTS (SELECT 1 FROM post_authors pa WHERE pa.post_id = p.id AND pa.author_id = $3))
  AND ($4::bigint = 0 OR EXISTS (SELECT 1 FROM post_terms pt WHERE pt.post_id = p.id AND pt.term_id = $4))
  AND ($5 = '' OR p.title ILIKE $6 OR p.search @@ websearch_to_tsquery('english', $5))
  AND ($7::timestamptz IS NULL OR p.published_at >= $7)
  AND ($8::timestamptz IS NULL OR p.published_at < $8)
ORDER BY `

// List returns one page of posts or pages for the admin list.
func (s *Service) List(ctx context.Context, a Actor, f ListFilter) ([]ListItem, int64, error) {
	if f.Type != "post" && f.Type != "page" {
		f.Type = "post"
	}
	if f.Status != "" && f.Status != "trash" && !slices.Contains(statuses, f.Status) {
		return nil, 0, invalid("status", "unknown status")
	}
	order, ok := sorts[f.Sort]
	if !ok {
		order = sorts["-date"]
	}
	me, err := actorAuthor(ctx, s.DB, a)
	if err != nil {
		return nil, 0, err
	}
	author := f.AuthorID
	if f.Mine {
		author = me
		if me == 0 {
			author = -1
		}
	}
	q := strings.TrimSpace(f.Q)
	rows, err := s.DB.Query(ctx, sqlList+order+` LIMIT $10 OFFSET $11`, f.Type, f.Status, author, f.TermID, q,
		"%"+likeEscape(q)+"%", f.From, f.To, me, f.Size, (f.Page-1)*f.Size)
	if err != nil {
		return nil, 0, err
	}
	items := []ListItem{}
	var total int64
	var it ListItem
	var own bool
	if _, err := pgx.ForEachRow(rows, []any{&it.ID, &it.Type, &it.Status, &it.Title, &it.Slug, &it.PublishedAt,
		&it.UpdatedAt, &it.Sticky, &it.Version, &own, &total}, func() error {
		x := it
		x.CanEdit = canEditListed(a, &x, own)
		items = append(items, x)
		return nil
	}); err != nil {
		return nil, 0, err
	}
	if len(items) == 0 {
		return items, total, nil
	}
	ids := make([]int64, len(items))
	for i, x := range items {
		ids[i] = x.ID
	}
	authors, err := s.authorRefs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	terms, err := s.termRefs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	urls, err := s.Site.VisibleURLs(ctx, ids)
	if err != nil {
		return nil, 0, err
	}
	for i := range items {
		x := &items[i]
		x.Authors, x.Categories, x.Tags = authors[x.ID], []TermRef{}, []TermRef{}
		if x.Authors == nil {
			x.Authors = []AuthorRef{}
		}
		for _, t := range terms[x.ID] {
			if t.Taxonomy == "category" {
				x.Categories = append(x.Categories, t)
			} else {
				x.Tags = append(x.Tags, t)
			}
		}
		if u, ok := urls[x.ID]; ok {
			x.URL = &u
		}
	}
	return items, total, nil
}

// canEditListed approximates canEdit for list rows: any non-draft status counts as published for contributors.
func canEditListed(a Actor, it *ListItem, own bool) bool {
	switch {
	case it.Type == "page" && !a.can(authz.EditPages):
		return false
	case a.can(authz.EditOthers):
		return true
	case !own:
		return false
	}
	return a.can(authz.PublishOwn) || it.Status == "draft" || it.Status == "pending" || it.Status == "trash"
}

func (s *Service) termRefs(ctx context.Context, ids []int64) (map[int64][]TermRef, error) {
	rows, err := s.DB.Query(ctx, `
SELECT pt.post_id, t.id, t.name, t.slug, t.taxonomy FROM post_terms pt JOIN terms t ON t.id = pt.term_id
WHERE pt.post_id = ANY($1::bigint[]) ORDER BY pt.post_id, lower(t.name), t.id`, ids)
	if err != nil {
		return nil, err
	}
	out := map[int64][]TermRef{}
	var postID int64
	var t TermRef
	_, err = pgx.ForEachRow(rows, []any{&postID, &t.ID, &t.Name, &t.Slug, &t.Taxonomy}, func() error {
		out[postID] = append(out[postID], t)
		return nil
	})
	return out, err
}

// SlugCheck reports whether slug is free for a post of typ under parent, and a free variant.
func (s *Service) SlugCheck(ctx context.Context, typ, slug string, parent *int64, exclude int64) (bool, string, error) {
	if typ != "post" && typ != "page" {
		return false, "", invalid("type", "type must be post or page")
	}
	base := Slugify(slug)
	if base == "" {
		base = "untitled"
	}
	free, err := UniqueSlug(ctx, s.DB, typ, parent, base, exclude)
	if err != nil {
		return false, "", err
	}
	return free == slug, free, nil
}

// PatchInput is the quick edit: only the fields sent change. SetPublishedAt marks published_at as sent.
type PatchInput struct {
	Version        int
	Title          *string
	Slug           *string
	Status         *string
	PublishedAt    *time.Time
	SetPublishedAt bool
	CategoryIDs    []int64
	TagIDs         []int64
}

func inputOf(d *Detail) Input {
	seo := d.SEO
	return Input{Type: d.Type, Version: &d.Version, Title: d.Title, Slug: d.Slug, Status: d.Status,
		PublishedAt: d.PublishedAt, Excerpt: d.Excerpt, Content: d.Content, ParentID: d.ParentID, Template: d.Template,
		FeaturedMediaID: d.FeaturedMediaID, AuthorIDs: d.AuthorIDs, CategoryIDs: d.CategoryIDs,
		PrimaryCategoryID: d.PrimaryCategoryID, TagIDs: d.TagIDs, CommentStatus: d.CommentStatus, Sticky: d.Sticky,
		MenuOrder: d.MenuOrder, SEO: &seo}
}

// Patch applies a quick edit through Save.
func (s *Service) Patch(ctx context.Context, a Actor, id int64, p PatchInput) (*Detail, error) {
	d, err := s.Get(ctx, a, id)
	if err != nil {
		return nil, err
	}
	in := inputOf(d)
	in.Version = &p.Version
	if p.Title != nil {
		in.Title = *p.Title
	}
	if p.Slug != nil {
		in.Slug = *p.Slug
	}
	if p.Status != nil {
		in.Status = *p.Status
	}
	if p.SetPublishedAt {
		in.PublishedAt = p.PublishedAt
	}
	if p.CategoryIDs != nil {
		in.CategoryIDs = p.CategoryIDs
		if in.PrimaryCategoryID != nil && !slices.Contains(p.CategoryIDs, *in.PrimaryCategoryID) {
			in.PrimaryCategoryID = nil
		}
	}
	if p.TagIDs != nil {
		in.TagIDs = p.TagIDs
	}
	return s.Save(ctx, a, id, in, false)
}

type BulkInput struct {
	IDs       []int64 `json:"ids"`
	Action    string  `json:"action"`
	TermIDs   []int64 `json:"term_ids"`
	AuthorIDs []int64 `json:"author_ids"`
	Gone      string  `json:"gone"`
}

type BulkResult struct {
	ID      int64  `json:"id"`
	OK      bool   `json:"ok"`
	Message string `json:"message,omitempty"`
}

// Bulk runs one service call per id and reports each outcome. Unexpected errors stop the batch.
func (s *Service) Bulk(ctx context.Context, a Actor, in BulkInput) ([]BulkResult, error) {
	if len(in.IDs) > 100 {
		return nil, invalid("ids", "at most 100 posts at a time")
	}
	var cats, tags []int64
	if in.Action == "set_terms" || in.Action == "add_terms" {
		rows, err := s.DB.Query(ctx, `SELECT id, taxonomy FROM terms WHERE id = ANY($1::bigint[])`, dedupe(in.TermIDs))
		if err != nil {
			return nil, err
		}
		var id int64
		var tax string
		if _, err := pgx.ForEachRow(rows, []any{&id, &tax}, func() error {
			if tax == "category" {
				cats = append(cats, id)
			} else {
				tags = append(tags, id)
			}
			return nil
		}); err != nil {
			return nil, err
		}
	}
	out := []BulkResult{}
	for _, id := range dedupe(in.IDs) {
		var err error
		switch in.Action {
		case "trash":
			_, err = s.Trash(ctx, a, id, in.Gone, "")
		case "restore":
			_, err = s.Restore(ctx, a, id)
		case "publish", "set_terms", "add_terms", "set_authors":
			err = s.bulkEdit(ctx, a, id, in.Action, cats, tags, in.AuthorIDs)
		default:
			return nil, invalid("action", "unknown bulk action")
		}
		var pe *Error
		switch {
		case err == nil:
			out = append(out, BulkResult{ID: id, OK: true})
		case errors.As(err, &pe):
			out = append(out, BulkResult{ID: id, Message: pe.Message})
		default:
			return nil, err
		}
	}
	return out, nil
}

func (s *Service) bulkEdit(ctx context.Context, a Actor, id int64, action string, cats, tags, authors []int64) error {
	d, err := s.Get(ctx, a, id)
	if err != nil {
		return err
	}
	in := inputOf(d)
	switch action {
	case "publish":
		if d.Status == "published" {
			return nil
		}
		in.Status = "published"
	case "set_terms":
		in.CategoryIDs, in.TagIDs, in.PrimaryCategoryID = cats, tags, nil
	case "add_terms":
		in.CategoryIDs, in.TagIDs = append(in.CategoryIDs, cats...), append(in.TagIDs, tags...)
	case "set_authors":
		if len(authors) == 0 {
			return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid", Field: "author_ids", Message: "choose at least one author"}
		}
		in.AuthorIDs = authors
	}
	_, err = s.Save(ctx, a, id, in, false)
	return err
}
