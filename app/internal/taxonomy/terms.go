package taxonomy

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"

	"afsc/internal/audit"
	"afsc/internal/site"
)

type Term struct {
	ID          int64   `json:"id"`
	Taxonomy    string  `json:"taxonomy"`
	Name        string  `json:"name"`
	Slug        string  `json:"slug"`
	Description string  `json:"description"`
	ParentID    *int64  `json:"parent_id"`
	Count       int64   `json:"count"`
	URL         *string `json:"url"`
}

type TermInput struct {
	Taxonomy    string `json:"taxonomy"`
	Name        string `json:"name"`
	Slug        string `json:"slug"`
	Description string `json:"description"`
	ParentID    *int64 `json:"parent_id"`
}

const (
	termCols = `t.id, t.taxonomy, t.name, t.slug, t.description, t.parent_id,
  (SELECT count(*) FROM post_terms pt JOIN posts p ON p.id = pt.post_id WHERE pt.term_id = t.id AND p.status <> 'trash')`
	sqlTerm      = `SELECT ` + termCols + ` FROM terms t WHERE t.id = $1`
	sqlTermsByID = `SELECT ` + termCols + ` FROM terms t WHERE t.id = ANY($1::bigint[])`
	sqlTerms     = `
SELECT ` + termCols + `, count(*) OVER () FROM terms t
WHERE ($1::text = '' OR t.taxonomy = $1) AND ($2::text = '' OR t.name ILIKE $2 OR t.slug ILIKE $2)
ORDER BY t.taxonomy, lower(t.name), t.id
LIMIT $3 OFFSET $4`
	sqlSlugTaken = `
SELECT EXISTS (SELECT 1 FROM terms WHERE taxonomy = $1 AND parent_id IS NOT DISTINCT FROM $2 AND slug = $3 AND id <> $4)`
	// sqlIsAncestor reports whether $2 is $1 or one of its ancestors
	sqlIsAncestor = `
WITH RECURSIVE up AS (
  SELECT id, parent_id FROM terms WHERE id = $1
  UNION
  SELECT t.id, t.parent_id FROM terms t JOIN up ON t.id = up.parent_id
)
SELECT EXISTS (SELECT 1 FROM up WHERE id = $2)`
)

func likePattern(q string) string {
	if q = strings.TrimSpace(q); q == "" {
		return ""
	}
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

func scanTerm(row pgx.Row, extra ...any) (*Term, error) {
	t := &Term{}
	err := row.Scan(append([]any{&t.ID, &t.Taxonomy, &t.Name, &t.Slug, &t.Description, &t.ParentID, &t.Count}, extra...)...)
	return t, err
}

// termURLs fills in public archive URLs; category paths include every ancestor's slug.
func (s *Service) termURLs(ctx context.Context, terms []*Term) error {
	perm, err := s.perm()
	if err != nil {
		return err
	}
	var cats map[int64]catNode
	for _, t := range terms {
		path := t.Slug
		if t.Taxonomy == "category" {
			if cats == nil {
				if cats, err = s.categories(ctx); err != nil {
					return err
				}
			}
			path = catPath(cats, t.ID)
		}
		t.URL = s.abs(perm.Term(t.Taxonomy, path))
	}
	return nil
}

type catNode struct {
	parent int64
	slug   string
}

func (s *Service) categories(ctx context.Context) (map[int64]catNode, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, coalesce(parent_id, 0), slug FROM terms WHERE taxonomy = 'category'`)
	if err != nil {
		return nil, err
	}
	out := map[int64]catNode{}
	var id int64
	var n catNode
	_, err = pgx.ForEachRow(rows, []any{&id, &n.parent, &n.slug}, func() error {
		out[id] = n
		return nil
	})
	return out, err
}

func catPath(cats map[int64]catNode, id int64) string {
	var slugs []string
	for depth := 0; id != 0 && depth < 64; depth++ {
		n, ok := cats[id]
		if !ok {
			break
		}
		slugs = append([]string{n.slug}, slugs...)
		id = n.parent
	}
	return strings.Join(slugs, "/")
}

// ListTerms pages through terms; size <= 0 returns all of them.
func (s *Service) ListTerms(ctx context.Context, taxonomy, q string, page, size int) ([]*Term, int64, error) {
	limit, offset := size, (page-1)*size
	if size <= 0 {
		limit, offset = 1_000_000, 0
	}
	rows, err := s.DB.Query(ctx, sqlTerms, taxonomy, likePattern(q), limit, offset)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := []*Term{}
	var total int64
	for rows.Next() {
		t, err := scanTerm(rows, &total)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, err
	}
	return out, total, s.termURLs(ctx, out)
}

func (s *Service) GetTerm(ctx context.Context, id int64) (*Term, error) {
	t, err := scanTerm(s.DB.QueryRow(ctx, sqlTerm, id))
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("term")
	}
	if err != nil {
		return nil, err
	}
	return t, s.termURLs(ctx, []*Term{t})
}

// validate checks in against the stored term cur (nil when creating) and decides the slug.
func (s *Service) validateTerm(ctx context.Context, tx pgx.Tx, id int64, in *TermInput, cur *Term) error {
	in.Name, in.Description = strings.TrimSpace(in.Name), strings.TrimSpace(in.Description)
	switch {
	case in.Taxonomy != "category" && in.Taxonomy != "tag":
		return badField("taxonomy", "must be category or tag")
	case cur != nil && cur.Taxonomy != in.Taxonomy:
		return badField("taxonomy", "can't change; merge into a term of the other taxonomy instead")
	}
	if err := checkLen("name", in.Name, 1, 200); err != nil {
		return err
	}
	if err := checkLen("description", in.Description, 0, 5000); err != nil {
		return err
	}
	if in.ParentID != nil {
		if in.Taxonomy == "tag" {
			return badField("parent_id", "tags have no parent")
		}
		var taxonomy string
		err := tx.QueryRow(ctx, `SELECT taxonomy FROM terms WHERE id = $1`, *in.ParentID).Scan(&taxonomy)
		if errors.Is(err, pgx.ErrNoRows) || (err == nil && taxonomy != "category") {
			return badField("parent_id", "the parent category doesn't exist")
		}
		if err != nil {
			return err
		}
		if cur != nil {
			var cycle bool
			if err := tx.QueryRow(ctx, sqlIsAncestor, *in.ParentID, id).Scan(&cycle); err != nil {
				return err
			}
			if cycle {
				return badField("parent_id", "a category can't sit inside itself or one of its subcategories")
			}
		}
	}
	current := ""
	if cur != nil {
		current = cur.Slug
		if !sameParent(cur.ParentID, in.ParentID) {
			current = "" // the old slug may clash at the new level
			if in.Slug == "" {
				in.Slug = cur.Slug
			}
		}
	}
	slug, err := pickSlug(in.Slug, current, in.Name, func(slug string) (bool, error) {
		var taken bool
		err := tx.QueryRow(ctx, sqlSlugTaken, in.Taxonomy, in.ParentID, slug, id).Scan(&taken)
		return taken, err
	})
	in.Slug = slug
	return err
}

func sameParent(a, b *int64) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && *a == *b)
}

// CreateTerm adds a term. A new term has no posts, so only the snapshot needs reloading.
func (s *Service) CreateTerm(ctx context.Context, in TermInput, act Actor) (*Term, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := s.validateTerm(ctx, tx, 0, &in, nil); err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRow(ctx, `
INSERT INTO terms (taxonomy, slug, name, description, parent_id) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
		in.Taxonomy, in.Slug, in.Name, in.Description, in.ParentID).Scan(&id)
	if isUnique(err) {
		return nil, conflict("slug", "the slug %q is already used", in.Slug)
	}
	if err != nil {
		return nil, err
	}
	if err := act.audit(ctx, tx, "term.create", "term", id, in); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	return s.GetTerm(ctx, id)
}

// UpdateTerm saves a term. A slug or parent change moves its archive (and its subcategories', and post URLs under
// a {category} permalink), so the old URLs get 301s and both URL sets are purged.
func (s *Service) UpdateTerm(ctx context.Context, id int64, in TermInput, act Actor) (*Term, error) {
	cur, err := s.GetTerm(ctx, id)
	if err != nil {
		return nil, err
	}
	before, err := s.Site.TermURLs(ctx, id)
	if err != nil {
		return nil, err
	}
	ev, err := s.Site.TermChange(ctx, id)
	if err != nil {
		return nil, err
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `SELECT 1 FROM terms WHERE id = $1 FOR UPDATE`, id); err != nil {
		return nil, err
	}
	if err := s.validateTerm(ctx, tx, id, &in, cur); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `
UPDATE terms SET slug = $2, name = $3, description = $4, parent_id = $5, updated_at = now() WHERE id = $1`,
		id, in.Slug, in.Name, in.Description, in.ParentID)
	if isUnique(err) {
		return nil, conflict("slug", "the slug %q is already used", in.Slug)
	}
	if err != nil {
		return nil, err
	}
	diff := map[string]audit.Change{}
	for k, c := range map[string]audit.Change{"name": {From: cur.Name, To: in.Name}, "slug": {From: cur.Slug, To: in.Slug},
		"description": {From: cur.Description, To: in.Description}, "parent_id": {From: cur.ParentID, To: in.ParentID}} {
		if k == "parent_id" && sameParent(cur.ParentID, in.ParentID) || k != "parent_id" && c.From == c.To {
			continue
		}
		diff[k] = c
	}
	if err := act.audit(ctx, tx, "term.update", "term", id, diff); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	if cur.Slug != in.Slug || !sameParent(cur.ParentID, in.ParentID) {
		after, err := s.Site.TermURLs(ctx, id)
		if err != nil {
			return nil, err
		}
		red, err := s.redirect(ctx, before, after, act)
		if err != nil {
			return nil, err
		}
		ev = ev.Merge(red)
	}
	now, err := s.Site.TermChange(ctx, id)
	if err != nil {
		return nil, err
	}
	if err := s.Site.Invalidate(ctx, ev.Merge(now)); err != nil {
		return nil, err
	}
	return s.GetTerm(ctx, id)
}

// reparent moves the subcategories of id up to parent before id goes away.
func reparent(ctx context.Context, tx pgx.Tx, id int64, parent *int64) error {
	_, err := tx.Exec(ctx, `UPDATE terms SET parent_id = $2, updated_at = now() WHERE parent_id = $1`, id, parent)
	if isUnique(err) {
		return conflict("", "a subcategory has the same slug as a category it would move next to; rename it first")
	}
	return err
}

func dropTermRefs(ctx context.Context, tx pgx.Tx, id int64) error {
	_, err := tx.Exec(ctx, `DELETE FROM seo WHERE entity_type = 'term' AND entity_id = $1`, id)
	if err == nil {
		_, err = tx.Exec(ctx, `DELETE FROM terms WHERE id = $1`, id)
	}
	return err
}

// DeleteTerm removes a term from every post. Its subcategories move up a level, and their moved archives redirect.
func (s *Service) DeleteTerm(ctx context.Context, id int64, act Actor) error {
	cur, err := s.GetTerm(ctx, id)
	if err != nil {
		return err
	}
	before, err := s.Site.TermURLs(ctx, id)
	if err != nil {
		return err
	}
	ev, err := s.Site.TermChange(ctx, id)
	if err != nil {
		return err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if err := reparent(ctx, tx, id, cur.ParentID); err != nil {
		return err
	}
	if err := dropTermRefs(ctx, tx, id); err != nil {
		return err
	}
	if err := act.audit(ctx, tx, "term.delete", "term", id, cur); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.settle(ctx, before, nil, ev, act)
}

// settle runs after a commit that removed an entity: reload the snapshot, redirect what moved (extra adds keys
// whose URL now belongs to another entity), and purge.
func (s *Service) settle(ctx context.Context, before, extra map[string]string, ev site.Event, act Actor) error {
	if err := s.refresh(ctx); err != nil {
		return err
	}
	after, err := s.urlsNow(ctx, before)
	if err != nil {
		return err
	}
	for k, v := range extra {
		after[k] = v
	}
	red, err := s.redirect(ctx, before, after, act)
	if err != nil {
		return err
	}
	return s.Site.Invalidate(ctx, ev.Merge(red))
}

// MergeTerm files every post of src under dst, moves src's subcategories under dst, deletes src and redirects its
// archive to dst's. A post whose primary category was src gets dst as primary.
func (s *Service) MergeTerm(ctx context.Context, src, dst int64, act Actor) (*Term, error) {
	if src == dst {
		return nil, badField("into", "pick a different term")
	}
	from, err := s.GetTerm(ctx, src)
	if err != nil {
		return nil, err
	}
	into, err := s.GetTerm(ctx, dst)
	if isNotFound(err) {
		return nil, badField("into", "the target term doesn't exist")
	}
	if err != nil {
		return nil, err
	}
	if from.Taxonomy != into.Taxonomy {
		return nil, badField("into", "merge into a term of the same taxonomy")
	}
	before, err := s.Site.TermURLs(ctx, src)
	if err != nil {
		return nil, err
	}
	evSrc, err := s.Site.TermChange(ctx, src)
	if err != nil {
		return nil, err
	}
	evDst, err := s.Site.TermChange(ctx, dst)
	if err != nil {
		return nil, err
	}

	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var inside bool
	if err := tx.QueryRow(ctx, sqlIsAncestor, dst, src).Scan(&inside); err != nil {
		return nil, err
	}
	if inside {
		return nil, badField("into", "can't merge a category into one of its own subcategories")
	}
	rows, err := tx.Query(ctx, `DELETE FROM post_terms WHERE term_id = $1 RETURNING post_id, is_primary`, src)
	if err != nil {
		return nil, err
	}
	var posts []int64
	var primary []bool
	var p int64
	var pr bool
	if _, err := pgx.ForEachRow(rows, []any{&p, &pr}, func() error {
		posts, primary = append(posts, p), append(primary, pr)
		return nil
	}); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO post_terms (post_id, term_id, is_primary)
SELECT u.post_id, $3, u.is_primary FROM unnest($1::bigint[], $2::bool[]) AS u(post_id, is_primary)
ON CONFLICT (post_id, term_id) DO UPDATE SET is_primary = post_terms.is_primary OR excluded.is_primary`,
		posts, primary, dst); err != nil {
		return nil, err
	}
	if err := reparent(ctx, tx, src, &dst); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE menu_items SET target_id = $2, updated_at = now() WHERE kind = 'term' AND target_id = $1`,
		src, dst); err != nil {
		return nil, err
	}
	if err := dropTermRefs(ctx, tx, src); err != nil {
		return nil, err
	}
	if err := act.audit(ctx, tx, "term.merge", "term", src, map[string]any{"term": from, "into": dst, "posts": len(posts)}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}

	if err := s.refresh(ctx); err != nil {
		return nil, err
	}
	dstURLs, err := s.Site.TermURLs(ctx, dst)
	if err != nil {
		return nil, err
	}
	extra := map[string]string{}
	if p, ok := dstURLs[termKey(dst)]; ok {
		extra[termKey(src)] = p
	}
	evNow, err := s.Site.TermChange(ctx, dst)
	if err != nil {
		return nil, err
	}
	if err := s.settle(ctx, before, extra, evSrc.Merge(evDst).Merge(evNow), act); err != nil {
		return nil, err
	}
	return s.GetTerm(ctx, dst)
}

func termKey(id int64) string { return "term:" + strconv.FormatInt(id, 10) }

func isNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == http.StatusNotFound
}
