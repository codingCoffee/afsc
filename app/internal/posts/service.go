package posts

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/content"
	"afsc/internal/indexnow"
	"afsc/internal/jobs"
	"afsc/internal/plugins"
	"afsc/internal/redirects"
	"afsc/internal/site"
)

// Service runs every content write in one transaction per call.
type Service struct {
	DB        *pgxpool.Pool
	Site      *site.Site
	Redirects *redirects.Store
	Logf      func(format string, args ...any)
	Now       func() time.Time
}

func New(pool *pgxpool.Pool, st *site.Site, logf func(string, ...any)) *Service {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	return &Service{DB: pool, Site: st, Redirects: &redirects.Store{DB: pool, Router: st.Router()}, Logf: logf, Now: time.Now}
}

type row struct {
	ID                int64
	LegacyKey         string
	Type              string
	Status            string
	Slug              string
	ParentID          *int64
	Title             string
	Excerpt           string
	Content           []byte
	Template          *string
	FeaturedMediaID   *int64
	PublishedAt       *time.Time
	Sticky            bool
	MenuOrder         int
	CommentStatus     string
	Version           int
	CreatedAt         time.Time
	UpdatedAt         time.Time
	AuthorIDs         []int64
	CategoryIDs       []int64
	TagIDs            []int64
	PrimaryCategoryID *int64
	Videos            bool
}

const sqlRow = `
SELECT p.id, coalesce(p.legacy_key, ''), p.type, p.status, p.slug, p.parent_id, p.title, p.excerpt, p.content,
       p.template, p.featured_media_id, p.published_at, p.sticky, p.menu_order, p.comment_status, p.version,
       p.created_at, p.updated_at,
       ARRAY(SELECT author_id FROM post_authors WHERE post_id = p.id ORDER BY position, author_id),
       ARRAY(SELECT pt.term_id FROM post_terms pt JOIN terms t ON t.id = pt.term_id
             WHERE pt.post_id = p.id AND t.taxonomy = 'category' ORDER BY pt.term_id),
       ARRAY(SELECT pt.term_id FROM post_terms pt JOIN terms t ON t.id = pt.term_id
             WHERE pt.post_id = p.id AND t.taxonomy = 'tag' ORDER BY pt.term_id),
       (SELECT term_id FROM post_terms WHERE post_id = p.id AND is_primary),
       EXISTS (SELECT 1 FROM post_videos WHERE post_id = p.id)
FROM posts p WHERE p.id = $1`

func loadRow(ctx context.Context, db dbtx, id int64) (*row, error) {
	r := &row{}
	err := db.QueryRow(ctx, sqlRow, id).Scan(&r.ID, &r.LegacyKey, &r.Type, &r.Status, &r.Slug, &r.ParentID, &r.Title,
		&r.Excerpt, &r.Content, &r.Template, &r.FeaturedMediaID, &r.PublishedAt, &r.Sticky, &r.MenuOrder,
		&r.CommentStatus, &r.Version, &r.CreatedAt, &r.UpdatedAt, &r.AuthorIDs, &r.CategoryIDs, &r.TagIDs,
		&r.PrimaryCategoryID, &r.Videos)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	}
	return r, err
}

// lockRow locks the post for the rest of tx, then loads it.
func lockRow(ctx context.Context, tx pgx.Tx, id int64) (*row, error) {
	var v int
	if err := tx.QueryRow(ctx, `SELECT version FROM posts WHERE id = $1 FOR UPDATE`, id).Scan(&v); errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound()
	} else if err != nil {
		return nil, err
	}
	return loadRow(ctx, tx, id)
}

// ref matches site's refCols: the primary category, else the lowest-id one.
func (r *row) ref() site.PostRef {
	ref := site.PostRef{ID: r.ID, LegacyKey: r.LegacyKey, Type: r.Type, Slug: r.Slug, Status: r.Status, PublishedAt: r.PublishedAt}
	if r.ParentID != nil {
		ref.ParentID = *r.ParentID
	}
	if r.PrimaryCategoryID != nil {
		ref.CategoryID = *r.PrimaryCategoryID
	} else if len(r.CategoryIDs) > 0 {
		ref.CategoryID = r.CategoryIDs[0]
	}
	return ref
}

func (r *row) state() *site.PostState {
	if r == nil {
		return nil
	}
	return &site.PostState{Ref: r.ref(), TermIDs: slices.Concat(r.CategoryIDs, r.TagIDs), AuthorIDs: r.AuthorIDs, Videos: r.Videos}
}

func (s *Service) visible(r *row) bool {
	if r == nil {
		return false
	}
	ref := r.ref()
	return ref.Visible(s.Now())
}

func postKey(id int64) string { return "post:" + strconv.FormatInt(id, 10) }

var templateRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)

// Save creates (id 0) or updates a post through the whole save pipeline. force overwrites a version conflict.
func (s *Service) Save(ctx context.Context, a Actor, id int64, in Input, force bool) (*Detail, error) {
	var beforeURLs map[string]string
	if id != 0 {
		var err error
		if beforeURLs, err = s.Site.PostURLs(ctx, id); err != nil {
			return nil, err
		}
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var cur *row
	if id != 0 {
		if cur, err = lockRow(ctx, tx, id); err != nil {
			return nil, err
		}
		if cur.Status == "trash" {
			return nil, fail(http.StatusConflict, "trashed", "", "restore the post from trash before editing it")
		}
		if err := s.checkVersion(ctx, tx, a, cur, in.Version, force); err != nil {
			return nil, err
		}
		if _, err := canEdit(ctx, tx, a, cur); err != nil {
			return nil, err
		}
		in.Type = cur.Type
	} else if in.Type != "post" && in.Type != "page" {
		return nil, invalid("type", "type must be post or page")
	} else if in.Type == "page" && !a.can(authz.EditPages) {
		return nil, noCap("only editors and admins can create pages")
	}

	d, err := s.prepare(ctx, tx, a, cur, &in)
	if err != nil {
		return nil, err
	}
	after := d.after
	if err := s.write(ctx, tx, cur, d); err != nil {
		return nil, mapDBError(err)
	}
	if err := SaveSEO(ctx, tx, "post", after.ID, in.SEO); err != nil {
		return nil, mapDBError(err)
	}
	seo, err := LoadSEO(ctx, tx, "post", after.ID)
	if err != nil {
		return nil, err
	}
	kind := "manual"
	if after.Status == "published" {
		kind = "publish"
	}
	if err := addRevision(ctx, tx, after.ID, kind, &seo, a); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM post_revisions WHERE post_id = $1 AND user_id = $2 AND kind = 'autosave'`,
		after.ID, a.ref()); err != nil {
		return nil, err
	}

	ev := s.Site.PostChange(cur.state(), after.state())
	if d.authorCreated {
		ev.Refresh = true
	}
	if err := site.Notify(ctx, tx, ev); err != nil {
		return nil, err
	}
	if err := s.enqueuePing(ctx, tx, cur, after, seo.RobotsNoindex != nil && *seo.RobotsNoindex); err != nil {
		return nil, err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: a.ref(), Action: action(cur, after), EntityType: "post",
		EntityID: strconv.FormatInt(after.ID, 10), Diff: changes(cur, after), IP: a.IP}); err != nil {
		return nil, err
	}
	if err := s.emitChange(ctx, tx, cur, after, true); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, mapDBError(err)
	}
	s.afterCommit(ctx, a, beforeURLs, cur, after)
	return s.Get(ctx, a, after.ID)
}

func (s *Service) checkVersion(ctx context.Context, tx pgx.Tx, a Actor, cur *row, v *int, force bool) error {
	if v == nil {
		return invalid("version", "version is required")
	}
	if *v == cur.Version {
		return nil
	}
	if !force {
		e := fail(http.StatusConflict, "conflict", "", "someone else saved this post since you opened it")
		e.CurrentVersion, e.ModifiedAt = &cur.Version, &cur.UpdatedAt
		var name string
		err := tx.QueryRow(ctx, `
SELECT coalesce(nullif(u.display_name, ''), u.username) FROM post_revisions r JOIN users u ON u.id = r.user_id
WHERE r.post_id = $1 AND r.kind <> 'autosave' ORDER BY r.id DESC LIMIT 1`, cur.ID).Scan(&name)
		if err == nil {
			e.ModifiedBy = &name
		} else if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		return e
	}
	if !a.can(authz.EditOthers) {
		return noCap("only editors and admins can overwrite someone else's changes")
	}
	return nil
}

type draft struct {
	after         *row
	doc           *content.Node
	media         map[int64]content.MediaInfo
	authorCreated bool
}

// prepare validates in and resolves everything the post row will hold: status, date, content, parent, slug,
// authors and terms.
func (s *Service) prepare(ctx context.Context, tx pgx.Tx, a Actor, cur *row, in *Input) (*draft, error) {
	now := s.Now()
	after := &row{Type: in.Type, Excerpt: in.Excerpt, Sticky: in.Sticky, MenuOrder: in.MenuOrder,
		FeaturedMediaID: in.FeaturedMediaID, PublishedAt: in.PublishedAt, CommentStatus: in.CommentStatus}
	if cur != nil {
		after.ID, after.LegacyKey, after.Version, after.CreatedAt = cur.ID, cur.LegacyKey, cur.Version, cur.CreatedAt
	}
	after.Title = strings.TrimSpace(in.Title)
	switch {
	case utf8.RuneCountInString(after.Title) > 500:
		return nil, invalid("title", "title must be at most 500 characters")
	case utf8.RuneCountInString(after.Excerpt) > 5000:
		return nil, invalid("excerpt", "excerpt must be at most 5000 characters")
	}
	if after.CommentStatus == "" {
		after.CommentStatus = "open"
	} else if after.CommentStatus != "open" && after.CommentStatus != "closed" {
		return nil, invalid("comment_status", "comment_status must be open or closed")
	}
	if in.Template != nil && *in.Template != "" {
		if !templateRe.MatchString(*in.Template) {
			return nil, invalid("template", "template must be a template name such as \"wide\"")
		}
		after.Template = in.Template
	}

	after.Status = in.Status
	switch after.Status {
	case "published":
		if after.PublishedAt == nil {
			after.PublishedAt = &now
		} else if after.PublishedAt.After(now) {
			after.Status = "scheduled"
		}
	case "scheduled":
		if after.PublishedAt == nil || !after.PublishedAt.After(now) {
			return nil, invalid("published_at", "a scheduled post needs a date in the future")
		}
	}
	if err := checkStatus(a, after.Status); err != nil {
		return nil, err
	}

	var stored []byte
	if cur != nil {
		stored = cur.Content
	}
	doc, normalized, media, err := s.validateContent(ctx, tx, a, in.Content, stored)
	if err != nil {
		return nil, err
	}
	after.Content = normalized

	if in.ParentID != nil {
		if in.Type != "page" {
			return nil, invalid("parent_id", "only pages have a parent")
		}
		if err := checkParent(ctx, tx, after.ID, *in.ParentID); err != nil {
			return nil, err
		}
		after.ParentID = in.ParentID
	}
	slug := Slugify(in.Slug)
	if strings.TrimSpace(in.Slug) == "" {
		slug = Slugify(after.Title)
		if cur != nil && cur.Slug != "" {
			slug = cur.Slug
		}
	}
	if slug == "" {
		slug = "untitled"
	}
	if cur == nil || slug != cur.Slug || !sameID(cur.ParentID, after.ParentID) {
		if slug, err = UniqueSlug(ctx, tx, in.Type, after.ParentID, slug, after.ID); err != nil {
			return nil, err
		}
	}
	after.Slug = slug

	d := &draft{after: after, doc: doc, media: media}
	authorIDs := dedupe(in.AuthorIDs)
	if len(authorIDs) == 0 || !a.can(authz.EditOthers) {
		me, created, err := ensureAuthor(ctx, tx, a)
		if err != nil {
			return nil, err
		}
		d.authorCreated = created
		if len(authorIDs) == 0 {
			authorIDs = []int64{me}
		} else if !slices.Contains(authorIDs, me) {
			return nil, invalid("author_ids", "you must stay an author of your own post")
		}
	}
	var n int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM authors WHERE id = ANY($1::bigint[])`, authorIDs).Scan(&n); err != nil {
		return nil, err
	}
	if n != len(authorIDs) {
		return nil, invalid("author_ids", "unknown author")
	}
	after.AuthorIDs = authorIDs

	cats, tags := dedupe(in.CategoryIDs), dedupe(in.TagIDs)
	if err := checkTerms(ctx, tx, cats, "category"); err != nil {
		return nil, err
	}
	if err := checkTerms(ctx, tx, tags, "tag"); err != nil {
		return nil, err
	}
	slices.Sort(cats)
	slices.Sort(tags)
	if in.PrimaryCategoryID != nil && !slices.Contains(cats, *in.PrimaryCategoryID) {
		return nil, invalid("primary_category_id", "the primary category must be one of the post's categories")
	}
	after.CategoryIDs, after.TagIDs, after.PrimaryCategoryID = cats, tags, in.PrimaryCategoryID
	return d, nil
}

func sameID(a, b *int64) bool { return (a == nil && b == nil) || (a != nil && b != nil && *a == *b) }

func dedupe(ids []int64) []int64 {
	out := []int64{}
	for _, id := range ids {
		if !slices.Contains(out, id) {
			out = append(out, id)
		}
	}
	return out
}

func checkTerms(ctx context.Context, db dbtx, ids []int64, taxonomy string) error {
	var n int
	if err := db.QueryRow(ctx, `SELECT count(*) FROM terms WHERE id = ANY($1::bigint[]) AND taxonomy = $2`, ids, taxonomy).Scan(&n); err != nil {
		return err
	}
	if n != len(ids) {
		field := "category_ids"
		if taxonomy == "tag" {
			field = "tag_ids"
		}
		return invalid(field, "unknown %s", taxonomy)
	}
	return nil
}

func checkParent(ctx context.Context, db dbtx, self, parent int64) error {
	var typ string
	err := db.QueryRow(ctx, `SELECT type FROM posts WHERE id = $1 AND status <> 'trash'`, parent).Scan(&typ)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && typ != "page") {
		return invalid("parent_id", "the parent must be an existing page")
	}
	if err != nil || self == 0 {
		return err
	}
	var cycle bool
	if err := db.QueryRow(ctx, `
WITH RECURSIVE up (id, parent_id) AS (
  SELECT id, parent_id FROM posts WHERE id = $1
  UNION SELECT p.id, p.parent_id FROM posts p JOIN up ON p.id = up.parent_id)
SELECT EXISTS (SELECT 1 FROM up WHERE id = $2)`, parent, self).Scan(&cycle); err != nil {
		return err
	}
	if cycle {
		return invalid("parent_id", "a page can't be placed under itself or its own subpages")
	}
	return nil
}

func contentError(issues []content.Issue) error {
	return &Error{Status: http.StatusUnprocessableEntity, Code: "invalid_content", Message: "the content has errors", Errors: issues}
}

// validateContent parses and validates a document (§5.1) with the theme's classes and the rawHtml rule (Q9). It
// returns the normalized document, its JSON and the media it references.
func (s *Service) validateContent(ctx context.Context, db dbtx, a Actor, raw json.RawMessage, stored []byte) (*content.Node, []byte, map[int64]content.MediaInfo, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil, nil, contentError([]content.Issue{{Message: "content is required"}})
	}
	doc, err := content.Parse(raw)
	if err != nil {
		return nil, nil, nil, contentError([]content.Issue{{Message: err.Error()}})
	}
	var issues []content.Issue
	if !a.can(authz.RawHTML) {
		issues = rawHTMLIssues(doc, stored)
	}
	media, err := loadMediaInfo(ctx, db, content.MediaIDs(doc))
	if err != nil {
		return nil, nil, nil, err
	}
	res := content.Validate(doc, content.Options{AllowedClasses: s.Site.AllowedClasses(), AllowRawHTML: true,
		MediaExists: func(id int64) bool { _, ok := media[id]; return ok }})
	if issues = append(issues, res.Errors...); len(issues) > 0 {
		return nil, nil, nil, contentError(issues)
	}
	b, err := json.Marshal(doc)
	return doc, b, media, err
}

func loadMediaInfo(ctx context.Context, db dbtx, ids []int64) (map[int64]content.MediaInfo, error) {
	out := map[int64]content.MediaInfo{}
	if len(ids) == 0 {
		return out, nil
	}
	rows, err := db.Query(ctx, `
SELECT id, filename, substr(encode(sha256, 'hex'), 1, 8), mime, coalesce(width, 0), coalesce(height, 0)
FROM media WHERE id = ANY($1::bigint[])`, ids)
	if err != nil {
		return nil, err
	}
	var m content.MediaInfo
	_, err = pgx.ForEachRow(rows, []any{&m.ID, &m.Filename, &m.Version, &m.Mime, &m.Width, &m.Height}, func() error {
		out[m.ID] = m
		return nil
	})
	return out, err
}

const (
	sqlInsertPost = `
INSERT INTO posts (id, type, status, slug, parent_id, title, excerpt, content, content_html, content_text, template,
                   featured_media_id, published_at, sticky, menu_order, comment_status)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
RETURNING version, created_at, updated_at`
	sqlUpdatePost = `
UPDATE posts SET type = $2, status = $3, slug = $4, parent_id = $5, title = $6, excerpt = $7, content = $8,
  content_html = $9, content_text = $10, template = $11, featured_media_id = $12, published_at = $13, sticky = $14,
  menu_order = $15, comment_status = $16, version = version + 1, updated_at = now()
WHERE id = $1
RETURNING version, created_at, updated_at`
	sqlInsertRevision = `
INSERT INTO post_revisions (post_id, kind, title, excerpt, content, content_text, seo, user_id)
SELECT id, $2::text, title, excerpt, content, content_text, $3::jsonb, $4::uuid FROM posts WHERE id = $1`
)

// write renders the document and stores the post row, its authors, terms, auto videos and media references.
func (s *Service) write(ctx context.Context, tx pgx.Tx, cur *row, d *draft) error {
	after := d.after
	sql := sqlUpdatePost
	if cur == nil {
		sql = sqlInsertPost
		if err := tx.QueryRow(ctx, `SELECT nextval(pg_get_serial_sequence('posts', 'id'))`).Scan(&after.ID); err != nil {
			return err
		}
	}
	html, text, err := s.Site.RenderContent(ctx, after.ID, after.LegacyKey, d.doc)
	if err != nil {
		return err
	}
	if err := tx.QueryRow(ctx, sql, after.ID, after.Type, after.Status, after.Slug, after.ParentID, after.Title,
		after.Excerpt, string(after.Content), html, text, after.Template, after.FeaturedMediaID, after.PublishedAt,
		after.Sticky, after.MenuOrder, after.CommentStatus).Scan(&after.Version, &after.CreatedAt, &after.UpdatedAt); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM post_authors WHERE post_id = $1`, after.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO post_authors (post_id, author_id, position)
SELECT $1, a, o - 1 FROM unnest($2::bigint[]) WITH ORDINALITY AS u(a, o)`, after.ID, after.AuthorIDs); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM post_terms WHERE post_id = $1`, after.ID); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO post_terms (post_id, term_id, is_primary)
SELECT $1, t, coalesce(t = $3::bigint, false) FROM unnest($2::bigint[]) AS u(t)`,
		after.ID, slices.Concat(after.CategoryIDs, after.TagIDs), after.PrimaryCategoryID); err != nil {
		return err
	}
	base := s.Site.BaseURL()
	videos := autoVideos(d.doc, func(id int64) string {
		if m, ok := d.media[id]; ok {
			return base + content.MediaURL(m, "orig")
		}
		return ""
	})
	if err := syncDerived(ctx, tx, after.ID, after.Title, videos, mediaIDs(d.doc, after.FeaturedMediaID)); err != nil {
		return err
	}
	return tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM post_videos WHERE post_id = $1)`, after.ID).Scan(&after.Videos)
}

// addRevision snapshots the post row as just written.
func addRevision(ctx context.Context, tx pgx.Tx, id int64, kind string, seo *SeoFields, a Actor) error {
	b, err := json.Marshal(seo)
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, sqlInsertRevision, id, kind, string(b), a.ref())
	return err
}

func mapDBError(err error) error {
	var pe *pgconn.PgError
	if !errors.As(err, &pe) {
		return err
	}
	switch pe.Code {
	case "23505":
		return fail(http.StatusConflict, "slug_taken", "slug", "another post already uses this slug")
	case "23503":
		return invalid("", "a referenced media item, author or term no longer exists")
	}
	return err
}

func action(cur, after *row) string {
	switch {
	case cur == nil:
		return "post.create"
	case after.Status == "published" && cur.Status != "published":
		return "post.publish"
	case cur.Status == "published" && after.Status != "published":
		return "post.unpublish"
	}
	return "post.update"
}

func changes(cur, after *row) map[string]audit.Change {
	out := map[string]audit.Change{}
	if cur == nil {
		out["status"] = audit.Change{To: after.Status}
		out["title"] = audit.Change{To: after.Title}
		return out
	}
	add := func(k string, from, to any, same bool) {
		if !same {
			out[k] = audit.Change{From: from, To: to}
		}
	}
	add("title", cur.Title, after.Title, cur.Title == after.Title)
	add("slug", cur.Slug, after.Slug, cur.Slug == after.Slug)
	add("status", cur.Status, after.Status, cur.Status == after.Status)
	add("published_at", cur.PublishedAt, after.PublishedAt,
		(cur.PublishedAt == nil) == (after.PublishedAt == nil) && (cur.PublishedAt == nil || cur.PublishedAt.Equal(*after.PublishedAt)))
	add("parent_id", cur.ParentID, after.ParentID, sameID(cur.ParentID, after.ParentID))
	return out
}

// enqueuePing queues an IndexNow ping (Q25) for the public URLs a change touches: the URL before (unpublish, trash,
// move) and after (publish, update, move).
func (s *Service) enqueuePing(ctx context.Context, tx pgx.Tx, before, after *row, noindex bool) error {
	if noindex || !s.Site.Settings().SEO.IndexNow.Enabled {
		return nil
	}
	var urls []string
	for _, r := range []*row{before, after} {
		if s.visible(r) {
			ref := r.ref()
			if u := s.Site.PostURL(&ref); !slices.Contains(urls, u) {
				urls = append(urls, u)
			}
		}
	}
	if len(urls) == 0 {
		return nil
	}
	return jobs.Enqueue(ctx, tx, indexnow.Kind, indexnow.Payload{URLs: urls}, time.Time{})
}

// afterCommit records URL moves (§10.3) once the change is visible to the snapshot, and clears an exact redirect
// that would hide a post that just went live.
func (s *Service) afterCommit(ctx context.Context, a Actor, beforeURLs map[string]string, cur, after *row) {
	ctx = context.WithoutCancel(ctx)
	was, is := s.visible(cur), s.visible(after)
	if !is {
		return
	}
	key := postKey(after.ID)
	if was {
		b, n := cur.ref(), after.ref()
		if s.Site.PostURL(&b) == s.Site.PostURL(&n) {
			return
		}
		if err := s.Site.Invalidate(ctx, site.Event{Refresh: true}); err != nil {
			s.Logf("posts: refresh after moving post %d: %v", after.ID, err)
		}
		afterURLs, err := s.Site.PostURLs(ctx, after.ID)
		if err != nil {
			s.Logf("posts: URLs of moved post %d: %v", after.ID, err)
			return
		}
		moves := redirects.Moves(beforeURLs, afterURLs)
		if len(moves) == 0 {
			return
		}
		res, err := s.Redirects.SlugChange(ctx, moves, a.userRef())
		if err != nil {
			s.Logf("posts: redirects for moved post %d: %v", after.ID, err)
			return
		}
		s.purge(ctx, res.Purge)
		s.pingMoves(ctx, moves, beforeURLs[key])
		return
	}
	urls, err := s.Site.PostURLs(ctx, after.ID)
	if err == nil && urls[key] != "" {
		err = s.clearExact(ctx, urls[key])
	}
	if err != nil {
		s.Logf("posts: clearing redirects at published post %d: %v", after.ID, err)
	}
}

// pingMoves queues IndexNow for moved subpages; the page's own URLs went with its save.
func (s *Service) pingMoves(ctx context.Context, moves []redirects.Move, own string) {
	if !s.Site.Settings().SEO.IndexNow.Enabled {
		return
	}
	var urls []string
	for _, m := range moves {
		if m.Old != own {
			urls = append(urls, s.abs(m.Old), s.abs(m.New))
		}
	}
	if len(urls) == 0 {
		return
	}
	if err := jobs.Enqueue(ctx, s.DB, indexnow.Kind, indexnow.Payload{URLs: urls}, time.Time{}); err != nil {
		s.Logf("posts: queue IndexNow ping: %v", err)
	}
}

func (s *Service) abs(path string) string {
	return s.Site.BaseURL() + (&url.URL{Path: path}).EscapedPath()
}

// clearExact deletes an exact redirect from path, like SlugChange does for a move's new path.
func (s *Service) clearExact(ctx context.Context, path string) error {
	var id int64
	err := s.DB.QueryRow(ctx, `SELECT id FROM redirects WHERE match = 'exact' AND source = $1`, path).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	p, err := s.Redirects.Delete(ctx, id)
	if err != nil && !errors.Is(err, redirects.ErrNotFound) {
		return err
	}
	s.purge(ctx, p)
	return nil
}

func (s *Service) purge(ctx context.Context, p redirects.Purge) {
	if err := s.Site.Invalidate(ctx, site.Event{Refresh: p.Refresh, All: p.Refresh, Paths: p.Paths}); err != nil {
		s.Logf("posts: purge: %v", err)
	}
}

// PublishDue publishes every scheduled post whose date has passed, one transaction each (the
// posts.publish_scheduled job).
func (s *Service) PublishDue(ctx context.Context) (int, error) {
	n := 0
	for {
		ok, err := s.publishOne(ctx)
		if err != nil || !ok {
			return n, err
		}
		n++
	}
}

func (s *Service) publishOne(ctx context.Context) (bool, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return false, err
	}
	defer tx.Rollback(ctx)
	var id int64
	err = tx.QueryRow(ctx, `
SELECT id FROM posts WHERE status = 'scheduled' AND published_at <= now()
ORDER BY published_at, id LIMIT 1 FOR UPDATE SKIP LOCKED`).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	cur, err := loadRow(ctx, tx, id)
	if err != nil {
		return false, err
	}
	after := *cur
	after.Status = "published"
	if err := tx.QueryRow(ctx, `UPDATE posts SET status = 'published', version = version + 1, updated_at = now()
WHERE id = $1 RETURNING version, updated_at`, id).Scan(&after.Version, &after.UpdatedAt); err != nil {
		return false, err
	}
	seo, err := LoadSEO(ctx, tx, "post", id)
	if err != nil {
		return false, err
	}
	if err := addRevision(ctx, tx, id, "publish", &seo, Actor{}); err != nil {
		return false, err
	}
	if err := site.Notify(ctx, tx, s.Site.PostChange(cur.state(), after.state())); err != nil {
		return false, err
	}
	if err := s.enqueuePing(ctx, tx, cur, &after, seo.RobotsNoindex != nil && *seo.RobotsNoindex); err != nil {
		return false, err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Action: "post.publish", EntityType: "post", EntityID: strconv.FormatInt(id, 10),
		Diff: map[string]audit.Change{"status": {From: "scheduled", To: "published"}}}); err != nil {
		return false, err
	}
	if err := s.emitChange(ctx, tx, cur, &after, false); err != nil {
		return false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return false, err
	}
	s.afterCommit(ctx, Actor{}, nil, cur, &after)
	return true, nil
}

// Trash moves a post to trash. For a published post, gone decides what its URL does next (Q10): "410", "301" to
// target, or "none" (a plain 404).
func (s *Service) Trash(ctx context.Context, a Actor, id int64, gone, target string) (*Detail, error) {
	beforeURLs, err := s.Site.PostURLs(ctx, id)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	cur, err := lockRow(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if cur.Status == "trash" {
		return nil, fail(http.StatusConflict, "trashed", "", "the post is already in trash")
	}
	if _, err := canEdit(ctx, tx, a, cur); err != nil {
		return nil, err
	}
	var rule *redirects.Input
	if cur.Status == "published" {
		in := redirects.Input{Source: beforeURLs[postKey(id)], Match: "exact", Note: fmt.Sprintf("trashed post #%d", id)}
		switch gone {
		case "410":
			in.Status = http.StatusGone
		case "301":
			in.Status, in.Target = http.StatusMovedPermanently, &target
		case "none":
		default:
			return nil, invalid("gone", `gone must be "410", "301" or "none" for a published post`)
		}
		if in.Status != 0 && in.Source != "" {
			if _, err := redirects.Normalize(in, s.Site.Router()); err != nil {
				var fe *redirects.FieldError
				if errors.As(err, &fe) {
					return nil, invalid("target", "%s", fe.Message)
				}
				return nil, err
			}
			rule = &in
		}
	}
	after := *cur
	after.Status = "trash"
	if err := tx.QueryRow(ctx, `UPDATE posts SET status = 'trash', version = version + 1, updated_at = now() WHERE id = $1
RETURNING updated_at`, id).Scan(&after.UpdatedAt); err != nil {
		return nil, err
	}
	seo, err := LoadSEO(ctx, tx, "post", id)
	if err != nil {
		return nil, err
	}
	if err := site.Notify(ctx, tx, s.Site.PostChange(cur.state(), nil)); err != nil {
		return nil, err
	}
	if err := s.enqueuePing(ctx, tx, cur, nil, seo.RobotsNoindex != nil && *seo.RobotsNoindex); err != nil {
		return nil, err
	}
	diff := changes(cur, &after)
	if rule != nil {
		diff["gone"] = audit.Change{To: gone}
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: a.ref(), Action: "post.trash", EntityType: "post",
		EntityID: strconv.FormatInt(id, 10), Diff: diff, IP: a.IP}); err != nil {
		return nil, err
	}
	if err := s.emitChange(ctx, tx, cur, &after, false); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if rule != nil {
		if err := s.goneRule(ctx, a, *rule); err != nil {
			s.Logf("posts: redirect for trashed post %d: %v", id, err)
		}
	}
	return s.Get(ctx, a, id)
}

// goneRule creates the trash rule, replacing an exact rule already at that source.
func (s *Service) goneRule(ctx context.Context, a Actor, in redirects.Input) error {
	ctx = context.WithoutCancel(ctx)
	var existing int64
	err := s.DB.QueryRow(ctx, `SELECT id FROM redirects WHERE match = 'exact' AND source = $1`, in.Source).Scan(&existing)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	saved, err := s.Redirects.Save(ctx, existing, in, true, a.userRef())
	if err != nil {
		return err
	}
	s.purge(ctx, saved.Purge)
	return nil
}

// Restore takes a post out of trash as a draft.
func (s *Service) Restore(ctx context.Context, a Actor, id int64) (*Detail, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	cur, err := lockRow(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if cur.Status != "trash" {
		return nil, fail(http.StatusConflict, "not_trashed", "", "the post is not in trash")
	}
	if _, err := canEdit(ctx, tx, a, cur); err != nil {
		return nil, err
	}
	after := *cur
	after.Status = "draft"
	if err := tx.QueryRow(ctx, `UPDATE posts SET status = 'draft', version = version + 1, updated_at = now() WHERE id = $1
RETURNING updated_at`, id).Scan(&after.UpdatedAt); err != nil {
		return nil, err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: a.ref(), Action: "post.restore", EntityType: "post",
		EntityID: strconv.FormatInt(id, 10), Diff: map[string]audit.Change{"status": {From: "trash", To: "draft"}}, IP: a.IP}); err != nil {
		return nil, err
	}
	if err := s.emitChange(ctx, tx, cur, &after, true); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Get(ctx, a, id)
}

// Delete removes a trashed post for good (editor+).
func (s *Service) Delete(ctx context.Context, a Actor, id int64) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	cur, err := lockRow(ctx, tx, id)
	if err != nil {
		return err
	}
	if !a.can(authz.EditOthers) || (cur.Type == "page" && !a.can(authz.EditPages)) {
		return noCap("only editors and admins can delete posts permanently")
	}
	if cur.Status != "trash" {
		return fail(http.StatusConflict, "not_trashed", "", "only posts in trash can be deleted permanently")
	}
	if _, err := tx.Exec(ctx, `DELETE FROM seo WHERE entity_type = 'post' AND entity_id = $1`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM posts WHERE id = $1`, id); err != nil {
		return err
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: a.ref(), Action: "post.delete", EntityType: "post",
		EntityID: strconv.FormatInt(id, 10), Diff: map[string]any{"title": cur.Title, "slug": cur.Slug, "type": cur.Type}, IP: a.IP}); err != nil {
		return err
	}
	if err := plugins.Emit(ctx, s.Site.Plugins(), tx, plugins.PostDeleted,
		plugins.PostDeletedData{ID: id, Type: cur.Type, Slug: cur.Slug}); err != nil {
		return err
	}
	return tx.Commit(ctx)
}

type AutosaveInput struct {
	Title   string          `json:"title"`
	Excerpt string          `json:"excerpt"`
	Content json.RawMessage `json:"content"`
	SEO     *SeoFields      `json:"seo"`
}

const sqlAutosave = `
INSERT INTO post_revisions (post_id, kind, title, excerpt, content, content_text, seo, user_id)
VALUES ($1, 'autosave', $2, $3, $4, $5, $6, $7)
ON CONFLICT (post_id, user_id) WHERE kind = 'autosave' DO UPDATE SET
  title = EXCLUDED.title, excerpt = EXCLUDED.excerpt, content = EXCLUDED.content,
  content_text = EXCLUDED.content_text, seo = EXCLUDED.seo, created_at = now()
RETURNING id, created_at`

// Autosave upserts the actor's autosave revision. It never touches the post row or the cache.
func (s *Service) Autosave(ctx context.Context, a Actor, id int64, in AutosaveInput) (*Autosave, error) {
	if a.User == uuid.Nil {
		return nil, noCap("autosave needs a user")
	}
	cur, err := loadRow(ctx, s.DB, id)
	if err != nil {
		return nil, err
	}
	if cur.Status == "trash" {
		return nil, fail(http.StatusConflict, "trashed", "", "restore the post from trash before editing it")
	}
	if _, err := canEdit(ctx, s.DB, a, cur); err != nil {
		return nil, err
	}
	doc, normalized, _, err := s.validateContent(ctx, s.DB, a, in.Content, cur.Content)
	if err != nil {
		return nil, err
	}
	_, text, err := s.Site.RenderContent(ctx, cur.ID, cur.LegacyKey, doc)
	if err != nil {
		return nil, err
	}
	var seo any
	if in.SEO != nil {
		b, err := json.Marshal(in.SEO)
		if err != nil {
			return nil, err
		}
		seo = string(b)
	}
	out := &Autosave{Title: in.Title, Excerpt: in.Excerpt, Content: normalized, SEO: in.SEO}
	if err := s.DB.QueryRow(ctx, sqlAutosave, id, in.Title, in.Excerpt, string(normalized), text, seo, a.User).
		Scan(&out.RevisionID, &out.SavedAt); err != nil {
		return nil, err
	}
	return out, nil
}
