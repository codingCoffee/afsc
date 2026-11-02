package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"afsc/internal/authz"
	"afsc/internal/content"
	"afsc/internal/media"
	"afsc/internal/site"
	"afsc/internal/storage"
	"afsc/internal/theme"
)

type mediaHandler struct{ d *adminDeps }

// registerMedia registers the M3c admin routes.
func (h *authHandler) registerMedia(g *echo.Group, d *adminDeps) {
	mh := &mediaHandler{d: d}
	h.route(g, http.MethodGet, "/media", authz.EditOwn, mh.list)
	h.route(g, http.MethodPost, "/media", authz.UploadMedia, mh.upload)
	h.route(g, http.MethodGet, "/media/legacy", authz.ManageContent, mh.legacy)
	h.route(g, http.MethodGet, "/media/:id", authz.EditOwn, mh.get)
	h.route(g, http.MethodPatch, "/media/:id", authz.UploadMedia, mh.update)
	h.route(g, http.MethodPut, "/media/:id/file", authz.UploadMedia, mh.replace)
	h.route(g, http.MethodDelete, "/media/:id", authz.ManageContent, mh.remove)
}

// mediaImages reads the active theme's image sizes; thumbnails fall back to originals when it can't.
func mediaImages(themeDir string) func() *content.ImageSizes {
	return func() *content.ImageSizes {
		m, err := theme.LoadManifest(themeDir)
		if err != nil {
			return nil
		}
		return m.Images()
	}
}

// admin builds the library service. ok is false when the storage can't stream or delete (it only matters to writes).
func (mh *mediaHandler) admin() (*media.Admin, bool) {
	sm, ok := mh.d.storage.(storage.Manager)
	return &media.Admin{DB: mh.d.pool, Storage: sm, Images: mediaImages(mh.d.themeDir), Logf: log.Printf,
		Plugins: mh.d.plugins, BaseURL: mh.d.baseURL}, ok
}

func (mh *mediaHandler) writer(c *echo.Context) (*media.Admin, error) {
	a, ok := mh.admin()
	if !ok {
		return nil, jsonError(c, http.StatusServiceUnavailable, "media storage is read-only")
	}
	return a, nil
}

func mediaActor(c *echo.Context) media.Actor {
	return media.Actor{User: actor(c), IP: clientIP(c), Manage: authz.Can(currentRole(c), authz.ManageContent)}
}

// mediaJSONError writes {message, code?, field?, ...extra}.
func mediaJSONError(c *echo.Context, status int, msg, code, field string, extra map[string]any) error {
	body := map[string]any{"message": msg}
	for k, v := range extra {
		body[k] = v
	}
	if code != "" {
		body["code"] = code
	}
	if field != "" {
		body["field"] = field
	}
	return c.JSON(status, body)
}

func mediaErr(c *echo.Context, err error) error {
	var me *media.Error
	if errors.As(err, &me) {
		return mediaJSONError(c, me.Status, me.Message, me.Code, me.Field, me.Extra)
	}
	return err
}

func (mh *mediaHandler) readUpload(c *echo.Context) (media.Upload, bool) {
	r := c.Request()
	name := r.Header.Get("X-Filename")
	if n, err := url.PathUnescape(name); err == nil {
		name = n
	}
	up := media.Upload{Body: r.Body, Size: r.ContentLength, Filename: name,
		AllowSVG: authz.Can(currentRole(c), authz.RawHTML), Limits: mh.d.site.Settings().Media}
	return up, r.ContentLength >= 0
}

const mediaSQLPostStates = `
SELECT p.id, coalesce(p.legacy_key, ''), p.type, p.slug, p.status, p.published_at, coalesce(p.parent_id, 0),
  coalesce((SELECT pt.term_id FROM post_terms pt JOIN terms t ON t.id = pt.term_id
            WHERE pt.post_id = p.id AND t.taxonomy = 'category' ORDER BY pt.is_primary DESC, t.id LIMIT 1), 0),
  coalesce((SELECT array_agg(pt.term_id) FROM post_terms pt WHERE pt.post_id = p.id), '{}'),
  coalesce((SELECT array_agg(pa.author_id ORDER BY pa.position) FROM post_authors pa WHERE pa.post_id = p.id), '{}'),
  EXISTS (SELECT 1 FROM post_videos v WHERE v.post_id = p.id)
FROM posts p WHERE p.id = ANY($1::bigint[]) AND p.status = 'published'`

// mediaPurge drops the cached pages of every post showing a changed file, or everything past 200 posts.
// refresh reloads the snapshot too, which holds author avatars.
func (mh *mediaHandler) purge(ctx context.Context, posts []int64, refresh bool) error {
	ev := site.Event{Refresh: refresh}
	if len(posts) > 200 {
		ev.All = true
		return mh.d.site.Invalidate(ctx, ev)
	}
	rows, err := mh.d.pool.Query(ctx, mediaSQLPostStates, posts)
	if err != nil {
		return err
	}
	var ps site.PostState
	_, err = pgx.ForEachRow(rows, []any{&ps.Ref.ID, &ps.Ref.LegacyKey, &ps.Ref.Type, &ps.Ref.Slug, &ps.Ref.Status,
		&ps.Ref.PublishedAt, &ps.Ref.ParentID, &ps.Ref.CategoryID, &ps.TermIDs, &ps.AuthorIDs, &ps.Videos}, func() error {
		st := ps
		ev = ev.Merge(mh.d.site.PostChange(&st, &st))
		return nil
	})
	if err != nil {
		return err
	}
	return mh.d.site.Invalidate(ctx, ev)
}

func (mh *mediaHandler) list(c *echo.Context) error {
	page, size := parsePage(c)
	a, _ := mh.admin()
	items, total, err := a.List(c.Request().Context(), media.ListQuery{Q: c.QueryParam("q"), Type: c.QueryParam("type"),
		Legacy: c.QueryParam("legacy") == "1", Page: int(page), Size: int(size)})
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, pageResponse[media.Item]{Items: items, Page: page, PageSize: size, Total: total})
}

func (mh *mediaHandler) get(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "media not found")
	}
	a, _ := mh.admin()
	d, err := a.Get(c.Request().Context(), id)
	if err != nil {
		return mediaErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (mh *mediaHandler) upload(c *echo.Context) error {
	a, err := mh.writer(c)
	if a == nil {
		return err
	}
	up, ok := mh.readUpload(c)
	if !ok {
		return jsonError(c, http.StatusLengthRequired, "send the file with a Content-Length")
	}
	it, created, err := a.Create(c.Request().Context(), up, mediaActor(c))
	if err != nil {
		return mediaErr(c, err)
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	return c.JSON(status, it)
}

func (mh *mediaHandler) update(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "media not found")
	}
	var p media.Patch
	if err := decodeJSON(c, &p); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	a, _ := mh.admin()
	ctx := c.Request().Context()
	it, posts, err := a.Update(ctx, id, p, mediaActor(c))
	if err != nil {
		return mediaErr(c, err)
	}
	if err := mh.purge(ctx, posts, false); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, it)
}

func (mh *mediaHandler) replace(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "media not found")
	}
	a, err := mh.writer(c)
	if a == nil {
		return err
	}
	up, ok := mh.readUpload(c)
	if !ok {
		return jsonError(c, http.StatusLengthRequired, "send the file with a Content-Length")
	}
	ctx := c.Request().Context()
	it, posts, err := a.Replace(ctx, id, up, c.QueryParam("confirm") == "1", mediaActor(c))
	if err != nil {
		return mediaErr(c, err)
	}
	if err := mh.purge(ctx, posts, true); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, it)
}

func (mh *mediaHandler) remove(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "media not found")
	}
	a, err := mh.writer(c)
	if a == nil {
		return err
	}
	ctx := c.Request().Context()
	posts, err := a.Delete(ctx, id, c.QueryParam("confirm") == "1", mediaActor(c))
	if err != nil {
		return mediaErr(c, err)
	}
	if err := mh.purge(ctx, posts, true); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (mh *mediaHandler) legacy(c *echo.Context) error {
	page, size := parsePage(c)
	a, _ := mh.admin()
	rows, total, err := a.Legacy(c.Request().Context(), int(page), int(size))
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, pageResponse[media.LegacyRow]{Items: rows, Page: page, PageSize: size, Total: total})
}
