package main

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/content"
	"afsc/internal/seo"
	"afsc/internal/site"
)

// seoHandler serves the SEO panel, the per-entity SEO fields, the bulk editor and manual video rows (§8.6).
// It reads d.site per request, so tests can swap in a site with a page cache.
type seoHandler struct{ d *adminDeps }

// registerSEO registers the M3f admin routes.
func (h *authHandler) registerSEO(g *echo.Group, d *adminDeps) {
	sh := &seoHandler{d: d}
	h.route(g, http.MethodPost, "/seo/resolve", authz.EditOwn, sh.resolve)
	h.route(g, http.MethodGet, "/seo/bulk", authz.ManageContent, sh.bulkList)
	h.route(g, http.MethodPatch, "/seo/bulk", authz.ManageContent, sh.bulkPatch)
	h.route(g, http.MethodGet, "/seo/videos/:post_id", authz.EditOwn, sh.getVideos)
	h.route(g, http.MethodPut, "/seo/videos/:post_id", authz.EditOwn, sh.putVideos)
	h.route(g, http.MethodGet, "/seo/:entity_type/:entity_id", authz.EditOwn, sh.get)
	h.route(g, http.MethodPut, "/seo/:entity_type/:entity_id", authz.EditOwn, sh.put)
}

func seoDeny(c *echo.Context, code string) error {
	msg := "your role can't do this"
	if code == "ownership" {
		msg = "you can only edit your own posts"
	}
	return c.JSON(http.StatusForbidden, map[string]string{"message": msg, "code": code})
}

const sqlSEOPostAccess = `
SELECT p.type, EXISTS (SELECT 1 FROM post_authors pa JOIN authors a ON a.id = pa.author_id
                       WHERE pa.post_id = p.id AND a.user_id = $2)
FROM posts p WHERE p.id = $1`

// postAccess checks that the caller may edit post id: editors and admins always, others when their linked author
// is on it; pages also need pages.edit. done is true when it has already answered.
func (sh *seoHandler) postAccess(c *echo.Context, id int64) (done bool, err error) {
	var typ string
	var owned bool
	err = sh.d.pool.QueryRow(c.Request().Context(), sqlSEOPostAccess, id, currentUser(c)).Scan(&typ, &owned)
	if errors.Is(err, pgx.ErrNoRows) {
		return true, jsonError(c, http.StatusNotFound, "post not found")
	}
	if err != nil {
		return true, err
	}
	role := currentRole(c)
	if typ == "page" && !authz.Can(role, authz.EditPages) {
		return true, seoDeny(c, "capability")
	}
	if !owned && !authz.Can(role, authz.EditOthers) {
		return true, seoDeny(c, "ownership")
	}
	return false, nil
}

// entityAccess checks the caller may edit the entity's SEO and that it exists.
func (sh *seoHandler) entityAccess(c *echo.Context, typ string, id int64) (done bool, err error) {
	switch {
	case typ == "home" && id != 0:
		return true, jsonError(c, http.StatusBadRequest, "home has entity id 0")
	case typ == "post" || typ == "term" || typ == "author":
		if id <= 0 {
			return true, jsonError(c, http.StatusBadRequest, "invalid entity id")
		}
	case typ != "home":
		return true, jsonError(c, http.StatusBadRequest, "entity_type must be post, term, author or home")
	}
	if typ == "post" {
		return sh.postAccess(c, id)
	}
	if !authz.Can(currentRole(c), authz.ManageContent) {
		return true, seoDeny(c, "capability")
	}
	if typ == "home" {
		return false, nil
	}
	var ok bool
	q := `SELECT EXISTS (SELECT 1 FROM terms WHERE id = $1)`
	if typ == "author" {
		q = `SELECT EXISTS (SELECT 1 FROM authors WHERE id = $1)`
	}
	if err := sh.d.pool.QueryRow(c.Request().Context(), q, id).Scan(&ok); err != nil {
		return true, err
	}
	if !ok {
		return true, jsonError(c, http.StatusNotFound, typ+" not found")
	}
	return false, nil
}

func seoEntityParams(c *echo.Context) (string, int64, bool) {
	id, err := strconv.ParseInt(c.Param("entity_id"), 10, 64)
	return c.Param("entity_type"), id, err == nil
}

func (sh *seoHandler) get(c *echo.Context) error {
	typ, id, ok := seoEntityParams(c)
	if !ok {
		return jsonError(c, http.StatusBadRequest, "invalid entity id")
	}
	if done, err := sh.entityAccess(c, typ, id); done {
		return err
	}
	f, err := seo.LoadFields(c.Request().Context(), sh.d.pool, typ, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, f)
}

// seoFieldsError answers a failed Normalize, or a missing image.
func seoFieldsError(c *echo.Context, err error) error {
	var fe *seo.FieldError
	if errors.As(err, &fe) {
		return fieldError(c, http.StatusBadRequest, fe.Field, fe.Message)
	}
	return err
}

// checkMedia rejects image ids that name no media row.
func (sh *seoHandler) checkMedia(ctx context.Context, f *seo.Fields) error {
	for name, id := range map[string]*int64{"og_image_media_id": f.OGImageMediaID, "twitter_image_media_id": f.TwitterImageMediaID} {
		if id == nil {
			continue
		}
		var ok bool
		if err := sh.d.pool.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM media WHERE id = $1)`, *id).Scan(&ok); err != nil {
			return err
		}
		if !ok {
			return &seo.FieldError{Field: name, Message: "no such media"}
		}
	}
	return nil
}

// seoDiff lists the SeoFields that differ, as audit changes keyed by JSON name.
func seoDiff(before, after any) map[string]audit.Change {
	asMap := func(v any) map[string]any {
		b, _ := json.Marshal(v)
		m := map[string]any{}
		_ = json.Unmarshal(b, &m)
		return m
	}
	a, b := asMap(before), asMap(after)
	out := map[string]audit.Change{}
	for k, v := range b {
		if !reflect.DeepEqual(a[k], v) {
			out[k] = audit.Change{From: a[k], To: v}
		}
	}
	return out
}

// purgeEvent is what an SEO change to the entity invalidates.
func (sh *seoHandler) purgeEvent(ctx context.Context, typ string, id int64) (site.Event, error) {
	switch typ {
	case "post":
		return sh.d.site.SEOChange(ctx, []int64{id}, false)
	case "term":
		return sh.d.site.TermChange(ctx, id)
	case "author":
		return sh.d.site.AuthorChange(ctx, id)
	}
	return site.Event{Paths: []string{"/"}}, nil
}

func (sh *seoHandler) put(c *echo.Context) error {
	typ, id, ok := seoEntityParams(c)
	if !ok {
		return jsonError(c, http.StatusBadRequest, "invalid entity id")
	}
	if done, err := sh.entityAccess(c, typ, id); done {
		return err
	}
	var f seo.Fields
	if err := decodeJSON(c, &f); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	if err := f.Normalize(); err != nil {
		return seoFieldsError(c, err)
	}
	if err := sh.checkMedia(ctx, &f); err != nil {
		return seoFieldsError(c, err)
	}
	tx, err := sh.d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	before, err := seo.LoadFields(ctx, tx, typ, id)
	if err != nil {
		return err
	}
	if err := seo.SaveFields(ctx, tx, typ, id, &f); err != nil {
		return err
	}
	if diff := seoDiff(before, &f); len(diff) > 0 {
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "seo.update", EntityType: typ,
			EntityID: strconv.FormatInt(id, 10), Diff: diff, IP: clientIP(c)}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	ev, err := sh.purgeEvent(ctx, typ, id)
	if err != nil {
		return err
	}
	if err := sh.d.site.Invalidate(ctx, ev); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, f)
}

// seoDraftInput is SeoResolveInput.post: the editor's unsaved fields.
type seoDraftInput struct {
	Type              string          `json:"type"`
	Title             string          `json:"title"`
	Slug              string          `json:"slug"`
	Excerpt           string          `json:"excerpt"`
	Content           json.RawMessage `json:"content"`
	FeaturedMediaID   *int64          `json:"featured_media_id"`
	CategoryIDs       []int64         `json:"category_ids"`
	PrimaryCategoryID *int64          `json:"primary_category_id"`
	PublishedAt       *time.Time      `json:"published_at"`
	ParentID          *int64          `json:"parent_id"`
}

type seoOG struct {
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Image       *string `json:"image"`
	Type        string  `json:"type"`
}

type seoTwitter struct {
	Card        string  `json:"card"`
	Title       string  `json:"title"`
	Description string  `json:"description"`
	Image       *string `json:"image"`
}

type seoCrumb struct {
	Name string `json:"name"`
	URL  string `json:"url"`
}

type seoResolvedResponse struct {
	Title       string                `json:"title"`
	Description string                `json:"description"`
	Canonical   string                `json:"canonical"`
	Robots      string                `json:"robots"`
	OG          seoOG                 `json:"og"`
	Twitter     seoTwitter            `json:"twitter"`
	URL         string                `json:"url"`
	Breadcrumbs []seoCrumb            `json:"breadcrumbs"`
	Checks      []seo.Check           `json:"checks"`
	Suggestions []site.LinkSuggestion `json:"suggestions"`
	UnknownVars []string              `json:"unknown_vars"`
}

func seoOptional(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func seoDeref[T any](p *T) T {
	var zero T
	if p == nil {
		return zero
	}
	return *p
}

func (sh *seoHandler) resolve(c *echo.Context) error {
	var req struct {
		EntityType string         `json:"entity_type"`
		EntityID   int64          `json:"entity_id"`
		Post       *seoDraftInput `json:"post"`
		SEO        seo.Fields     `json:"seo"`
	}
	if err := decodeJSONLimit(c, &req, 8<<20); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	typ, id := req.EntityType, req.EntityID
	if typ == "post" && id == 0 {
		if req.Post == nil {
			return fieldError(c, http.StatusBadRequest, "post", "a new post needs its draft fields")
		}
		if req.Post.Type == "page" && !authz.Can(currentRole(c), authz.EditPages) {
			return seoDeny(c, "capability")
		}
	} else if done, err := sh.entityAccess(c, typ, id); done {
		return err
	}
	f := req.SEO
	if err := f.Normalize(); err != nil {
		return seoFieldsError(c, err)
	}
	var draft *site.SEODraft
	if p := req.Post; p != nil && typ == "post" {
		draft = &site.SEODraft{Type: p.Type, Title: p.Title, Slug: p.Slug, Excerpt: p.Excerpt, CategoryIDs: p.CategoryIDs,
			FeaturedMediaID: seoDeref(p.FeaturedMediaID), PrimaryCategoryID: seoDeref(p.PrimaryCategoryID),
			PublishedAt: p.PublishedAt, ParentID: seoDeref(p.ParentID)}
		if len(p.Content) > 0 && string(p.Content) != "null" {
			doc, err := content.Parse(p.Content)
			if err != nil {
				return fieldError(c, http.StatusBadRequest, "post.content", err.Error())
			}
			draft.Content = doc
		}
	}
	ctx := c.Request().Context()
	res, err := sh.d.site.ResolveSEO(ctx, typ, id, draft, &f)
	if errors.Is(err, site.ErrSEONotFound) {
		return jsonError(c, http.StatusNotFound, typ+" not found")
	}
	if err != nil {
		return err
	}

	h, m := res.Head, res.Meta
	kp := seoDeref(f.FocusKeyphrase)
	out := seoResolvedResponse{Title: h.Title, Description: h.Description, Canonical: h.Canonical, Robots: h.Robots,
		OG: seoOG{Title: m["og:title"], Description: m["og:description"], Image: seoOptional(m["og:image"]), Type: m["og:type"]},
		Twitter: seoTwitter{Card: m["twitter:card"], Title: cmp.Or(m["twitter:title"], m["og:title"]),
			Description: cmp.Or(m["twitter:description"], m["og:description"]),
			Image:       seoOptional(cmp.Or(m["twitter:image"], m["og:image"]))},
		URL: res.URL, Breadcrumbs: []seoCrumb{}, Suggestions: []site.LinkSuggestion{}, UnknownVars: []string{}}
	for _, cr := range res.Crumbs {
		out.Breadcrumbs = append(out.Breadcrumbs, seoCrumb{Name: cr.Name, URL: cr.URL})
	}
	if len(res.Unknown) > 0 {
		out.UnknownVars = slices.Compact(slices.Sorted(slices.Values(res.Unknown)))
	}
	out.Checks = seo.Checks(seo.CheckInput{Title: h.Title, Description: h.Description, Doc: res.Doc, Slug: res.Slug,
		Keyphrase: kp, BaseURL: sh.d.baseURL})
	if typ == "post" {
		more, err := sh.duplicateChecks(ctx, id, cmp.Or(seoDeref(f.Title), res.PostTitle), seoDeref(f.Description), kp)
		if err != nil {
			return err
		}
		out.Checks = append(out.Checks, more...)
		if out.Suggestions, err = sh.d.site.LinkSuggestions(ctx, res.PostTitle+" "+kp, id, 10); err != nil {
			return err
		}
	}
	return c.JSON(http.StatusOK, out)
}

const (
	sqlSEODupTitle = `
SELECT p.title FROM posts p LEFT JOIN seo s ON s.entity_type = 'post' AND s.entity_id = p.id
WHERE p.id <> $1 AND p.status <> 'trash' AND lower(coalesce(nullif(btrim(s.title), ''), p.title)) = lower(btrim($2))
ORDER BY p.id LIMIT 3`
	sqlSEODupDescription = `
SELECT p.title FROM posts p JOIN seo s ON s.entity_type = 'post' AND s.entity_id = p.id
WHERE p.id <> $1 AND p.status <> 'trash' AND lower(btrim(s.description)) = lower(btrim($2))
ORDER BY p.id LIMIT 3`
	sqlSEODupKeyphrase = `
SELECT p.title FROM posts p JOIN seo s ON s.entity_type = 'post' AND s.entity_id = p.id
WHERE p.id <> $1 AND p.status <> 'trash' AND lower(btrim(s.focus_keyphrase)) = lower(btrim($2))
ORDER BY p.id LIMIT 3`
)

// duplicateChecks compares with other posts that aren't trashed: the SEO title (the override, else the post
// title), the description override and the focus keyphrase. Empty values are skipped.
func (sh *seoHandler) duplicateChecks(ctx context.Context, id int64, title, desc, kp string) ([]seo.Check, error) {
	var out []seo.Check
	for _, q := range []struct {
		id, sql, value, what string
	}{
		{"duplicate_title", sqlSEODupTitle, title, "SEO title"},
		{"duplicate_description", sqlSEODupDescription, desc, "meta description"},
		{"keyphrase_used", sqlSEODupKeyphrase, kp, "focus keyphrase"},
	} {
		if strings.TrimSpace(q.value) == "" {
			continue
		}
		rows, err := sh.d.pool.Query(ctx, q.sql, id, q.value)
		if err != nil {
			return nil, err
		}
		titles, err := pgx.CollectRows(rows, pgx.RowTo[string])
		if err != nil {
			return nil, err
		}
		if len(titles) == 0 {
			out = append(out, seo.Check{ID: q.id, Status: seo.OK, Message: "No other post has this " + q.what + "."})
			continue
		}
		out = append(out, seo.Check{ID: q.id, Status: seo.Warn,
			Message: "Other posts use this " + q.what + ": “" + strings.Join(titles, "”, “") + "”."})
	}
	return out, nil
}

type seoBulkRow struct {
	PostID              int64   `json:"post_id"`
	Type                string  `json:"type"`
	PostTitle           string  `json:"post_title"`
	Status              string  `json:"status"`
	URL                 *string `json:"url"`
	Title               *string `json:"title"`
	Description         *string `json:"description"`
	ResolvedTitle       string  `json:"resolved_title"`
	ResolvedDescription string  `json:"resolved_description"`
}

const sqlSEOBulk = `
SELECT p.id, p.type, p.title, p.status, nullif(btrim(s.title), ''), nullif(btrim(s.description), ''), count(*) OVER ()
FROM posts p LEFT JOIN seo s ON s.entity_type = 'post' AND s.entity_id = p.id
WHERE p.status <> 'trash' AND ($1 = '' OR p.type = $1)
  AND ($2 <> 'title' OR coalesce(btrim(s.title), '') = '')
  AND ($2 <> 'description' OR coalesce(btrim(s.description), '') = '')
  AND ($3 = '' OR p.title ILIKE $4)
ORDER BY p.updated_at DESC, p.id DESC
LIMIT $5 OFFSET $6`

func seoLike(q string) string {
	return "%" + strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q) + "%"
}

func (sh *seoHandler) bulkList(c *echo.Context) error {
	typ, missing, q := c.QueryParam("type"), c.QueryParam("missing"), strings.TrimSpace(c.QueryParam("q"))
	if typ != "" && typ != "post" && typ != "page" {
		return fieldError(c, http.StatusBadRequest, "type", "type must be post or page")
	}
	if missing != "" && missing != "title" && missing != "description" {
		return fieldError(c, http.StatusBadRequest, "missing", "missing must be title or description")
	}
	page, size := parsePage(c)
	ctx := c.Request().Context()
	rows, err := sh.d.pool.Query(ctx, sqlSEOBulk, typ, missing, q, seoLike(q), size, (page-1)*size)
	if err != nil {
		return err
	}
	items := []seoBulkRow{}
	var total int64
	var r seoBulkRow
	if _, err := pgx.ForEachRow(rows, []any{&r.PostID, &r.Type, &r.PostTitle, &r.Status, &r.Title, &r.Description, &total},
		func() error {
			items = append(items, r)
			r = seoBulkRow{}
			return nil
		}); err != nil {
		return err
	}
	ids := make([]int64, len(items))
	for i, it := range items {
		ids[i] = it.PostID
	}
	sums, err := sh.d.site.SEOSummaries(ctx, ids)
	if err != nil {
		return err
	}
	for i := range items {
		s := sums[items[i].PostID]
		items[i].ResolvedTitle, items[i].ResolvedDescription, items[i].URL = s.Title, s.Description, seoOptional(s.URL)
	}
	return c.JSON(http.StatusOK, pageResponse[seoBulkRow]{Items: items, Page: page, PageSize: size, Total: total})
}

// seoNullable decodes an optional nullable string: absent leaves set false, null sets it with nil.
func seoNullable(raw json.RawMessage) (set bool, v *string, err error) {
	if len(raw) == 0 {
		return false, nil, nil
	}
	if string(raw) == "null" {
		return true, nil, nil
	}
	var s string
	err = json.Unmarshal(raw, &s)
	return true, &s, err
}

const sqlSEOBulkSave = `
INSERT INTO seo (entity_type, entity_id, title, description) VALUES ('post', $1, $2, $3)
ON CONFLICT (entity_type, entity_id) DO UPDATE SET
  title = CASE WHEN $4 THEN EXCLUDED.title ELSE seo.title END,
  description = CASE WHEN $5 THEN EXCLUDED.description ELSE seo.description END,
  updated_at = now()`

func (sh *seoHandler) bulkPatch(c *echo.Context) error {
	var req struct {
		Rows []struct {
			PostID      int64           `json:"post_id"`
			Title       json.RawMessage `json:"title"`
			Description json.RawMessage `json:"description"`
		} `json:"rows"`
	}
	if err := decodeJSONLimit(c, &req, 1<<20); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	if len(req.Rows) == 0 || len(req.Rows) > 100 {
		return fieldError(c, http.StatusBadRequest, "rows", "send 1-100 rows")
	}
	type change struct {
		id          int64
		setT, setD  bool
		title, desc *string
	}
	changes := make([]change, 0, len(req.Rows))
	ids := make([]int64, 0, len(req.Rows))
	for i, r := range req.Rows {
		field := "rows." + strconv.Itoa(i)
		if r.PostID <= 0 || slices.Contains(ids, r.PostID) {
			return fieldError(c, http.StatusBadRequest, field+".post_id", "invalid or repeated post id")
		}
		ch := change{id: r.PostID}
		var errT, errD error
		ch.setT, ch.title, errT = seoNullable(r.Title)
		ch.setD, ch.desc, errD = seoNullable(r.Description)
		if errT != nil || errD != nil {
			return fieldError(c, http.StatusBadRequest, field, "title and description must be strings or null")
		}
		f := seo.Fields{Title: ch.title, Description: ch.desc}
		if err := f.Normalize(); err != nil {
			var fe *seo.FieldError
			if errors.As(err, &fe) {
				return fieldError(c, http.StatusBadRequest, field+"."+fe.Field, fe.Message)
			}
			return err
		}
		ch.title, ch.desc = f.Title, f.Description
		changes, ids = append(changes, ch), append(ids, r.PostID)
	}

	ctx := c.Request().Context()
	var found int
	if err := sh.d.pool.QueryRow(ctx, `SELECT count(*) FROM posts WHERE id = ANY($1::bigint[]) AND status <> 'trash'`,
		ids).Scan(&found); err != nil {
		return err
	}
	if found != len(ids) {
		return jsonError(c, http.StatusNotFound, "some posts don't exist or are in the trash")
	}
	tx, err := sh.d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	for _, ch := range changes {
		before, err := seo.LoadFields(ctx, tx, "post", ch.id)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, sqlSEOBulkSave, ch.id, ch.title, ch.desc, ch.setT, ch.setD); err != nil {
			return err
		}
		diff := map[string]audit.Change{}
		if ch.setT && seoDeref(before.Title) != seoDeref(ch.title) {
			diff["title"] = audit.Change{From: before.Title, To: ch.title}
		}
		if ch.setD && seoDeref(before.Description) != seoDeref(ch.desc) {
			diff["description"] = audit.Change{From: before.Description, To: ch.desc}
		}
		if len(diff) == 0 {
			continue
		}
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "seo.update", EntityType: "post",
			EntityID: strconv.FormatInt(ch.id, 10), Diff: diff, IP: clientIP(c)}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	ev, err := sh.d.site.SEOChange(ctx, ids, false)
	if err != nil {
		return err
	}
	if err := sh.d.site.Invalidate(ctx, ev); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]int{"updated": len(changes)})
}

type seoVideo struct {
	ID              *int64     `json:"id"`
	URL             string     `json:"url"`
	Provider        string     `json:"provider"`
	Title           string     `json:"title"`
	Description     string     `json:"description"`
	ThumbnailURL    string     `json:"thumbnail_url"`
	DurationSeconds *int       `json:"duration_seconds"`
	UploadDate      *time.Time `json:"upload_date"`
	FamilyFriendly  *bool      `json:"family_friendly"`
	Source          string     `json:"source"`
}

const (
	sqlSEOVideos = `
SELECT id, url, provider, title, description, thumbnail_url, duration_seconds, upload_date, family_friendly, source
FROM post_videos WHERE post_id = $1
ORDER BY CASE source WHEN 'manual' THEN 0 WHEN 'import' THEN 1 ELSE 2 END, id`
	// A manual row that overrides an auto or imported one keeps that row's provider and id, so embeds still resolve.
	sqlSEOVideoInsert = `
INSERT INTO post_videos (post_id, provider, external_id, url, title, description, thumbnail_url, duration_seconds,
                         upload_date, family_friendly, source)
SELECT $1::bigint, coalesce(o.provider, $2::text), coalesce(o.external_id, ''), $3::text, $4::text, $5::text, $6::text,
       $7::int, $8::timestamptz, $9::bool, 'manual'
FROM (SELECT 1) one
LEFT JOIN LATERAL (SELECT provider, external_id FROM post_videos
                   WHERE post_id = $1::bigint AND url = $3::text AND source <> 'manual'
                   ORDER BY source = 'import' DESC, id LIMIT 1) o ON true`
)

func seoPostParam(c *echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("post_id"), 10, 64)
	return id, err == nil && id > 0
}

func (sh *seoHandler) videos(ctx context.Context, postID int64) ([]seoVideo, error) {
	rows, err := sh.d.pool.Query(ctx, sqlSEOVideos, postID)
	if err != nil {
		return nil, err
	}
	out, err := pgx.CollectRows(rows, pgx.RowToStructByPos[seoVideo])
	if out == nil {
		out = []seoVideo{}
	}
	return out, err
}

func (sh *seoHandler) getVideos(c *echo.Context) error {
	id, ok := seoPostParam(c)
	if !ok {
		return jsonError(c, http.StatusBadRequest, "invalid post id")
	}
	if done, err := sh.postAccess(c, id); done {
		return err
	}
	items, err := sh.videos(c.Request().Context(), id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}

func seoHTTPURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != ""
}

// putVideos replaces the post's manual video rows with items. Auto and imported rows stay; a manual row with the
// same URL wins over them in JSON-LD and the video sitemap.
func (sh *seoHandler) putVideos(c *echo.Context) error {
	id, ok := seoPostParam(c)
	if !ok {
		return jsonError(c, http.StatusBadRequest, "invalid post id")
	}
	if done, err := sh.postAccess(c, id); done {
		return err
	}
	var req struct {
		Items []seoVideo `json:"items"`
	}
	if err := decodeJSONLimit(c, &req, 1<<20); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	if len(req.Items) > 50 {
		return fieldError(c, http.StatusBadRequest, "items", "at most 50 videos")
	}
	seen := map[string]bool{}
	for i := range req.Items {
		v := &req.Items[i]
		field := "items." + strconv.Itoa(i)
		v.URL, v.Title, v.Description = strings.TrimSpace(v.URL), strings.TrimSpace(v.Title), strings.TrimSpace(v.Description)
		v.ThumbnailURL, v.Provider = strings.TrimSpace(v.ThumbnailURL), strings.ToLower(strings.TrimSpace(v.Provider))
		switch {
		case len(v.URL) > 2048 || !seoHTTPURL(v.URL):
			return fieldError(c, http.StatusBadRequest, field+".url", "must be an http(s) URL")
		case seen[v.URL]:
			return fieldError(c, http.StatusBadRequest, field+".url", "listed twice")
		case len(v.Title) > 300 || len(v.Description) > 5000 || len(v.Provider) > 40:
			return fieldError(c, http.StatusBadRequest, field, "title, description or provider is too long")
		case v.ThumbnailURL != "" && (len(v.ThumbnailURL) > 2048 || !seoHTTPURL(v.ThumbnailURL)):
			return fieldError(c, http.StatusBadRequest, field+".thumbnail_url", "must be an http(s) URL")
		case v.DurationSeconds != nil && *v.DurationSeconds < 0:
			return fieldError(c, http.StatusBadRequest, field+".duration_seconds", "must be 0 or more")
		}
		seen[v.URL] = true
	}

	ctx := c.Request().Context()
	before, err := sh.videos(ctx, id)
	if err != nil {
		return err
	}
	tx, err := sh.d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM post_videos WHERE post_id = $1 AND source = 'manual'`, id); err != nil {
		return err
	}
	for _, v := range req.Items {
		if _, err := tx.Exec(ctx, sqlSEOVideoInsert, id, v.Provider, v.URL, v.Title, v.Description, v.ThumbnailURL,
			v.DurationSeconds, v.UploadDate, v.FamilyFriendly); err != nil {
			return err
		}
	}
	var manual []seoVideo
	for _, v := range before {
		if v.Source == "manual" {
			manual = append(manual, v)
		}
	}
	if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "seo.videos", EntityType: "post",
		EntityID: strconv.FormatInt(id, 10), Diff: audit.Change{From: len(manual), To: len(req.Items)},
		IP: clientIP(c)}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	ev, err := sh.d.site.SEOChange(ctx, []int64{id}, true)
	if err != nil {
		return err
	}
	if err := sh.d.site.Invalidate(ctx, ev); err != nil {
		return err
	}
	items, err := sh.videos(ctx, id)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"items": items})
}
