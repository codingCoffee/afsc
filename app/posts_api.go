package main

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v5"

	"afsc/internal/authz"
	"afsc/internal/posts"
	"afsc/internal/site"
)

const postsMaxBody = 8 << 20

type postsHandler struct {
	svc      *posts.Service
	site     *site.Site
	themeDir string
}

// registerPosts registers the M3b admin routes.
func (h *authHandler) registerPosts(g *echo.Group, d *adminDeps) {
	ph := &postsHandler{svc: posts.New(d.pool, d.site, log.Printf), site: d.site, themeDir: d.themeDir}
	cp := authz.EditOwn
	h.route(g, http.MethodGet, "/posts", cp, ph.list)
	h.route(g, http.MethodPost, "/posts", cp, ph.create)
	h.route(g, http.MethodGet, "/posts/slug-check", cp, ph.slugCheck)
	h.route(g, http.MethodPost, "/posts/bulk", cp, ph.bulk)
	h.route(g, http.MethodGet, "/posts/:id", cp, ph.get)
	h.route(g, http.MethodPut, "/posts/:id", cp, ph.update)
	h.route(g, http.MethodPatch, "/posts/:id", cp, ph.patch)
	h.route(g, http.MethodDelete, "/posts/:id", authz.EditOthers, ph.remove)
	h.route(g, http.MethodPost, "/posts/:id/autosave", cp, ph.autosave)
	h.route(g, http.MethodPost, "/posts/:id/preview", cp, ph.preview)
	h.route(g, http.MethodGet, "/posts/:id/revisions", cp, ph.revisions)
	h.route(g, http.MethodPost, "/posts/:id/trash", cp, ph.trash)
	h.route(g, http.MethodPost, "/posts/:id/restore", cp, ph.restore)
	h.route(g, http.MethodGet, "/revisions/:id", cp, ph.revision)
	h.route(g, http.MethodGet, "/revisions/:id/diff", cp, ph.diff)
	h.route(g, http.MethodGet, "/templates", cp, ph.templates)
}

func postsActor(c *echo.Context) posts.Actor {
	return posts.Actor{User: currentUser(c), Role: currentRole(c), IP: clientIP(c)}
}

// postsErr writes a service refusal as its JSON; anything else is a 500.
func postsErr(c *echo.Context, err error) error {
	var pe *posts.Error
	if errors.As(err, &pe) {
		return c.JSON(pe.Status, pe)
	}
	return err
}

func postsID(c *echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

func postsNotFound(c *echo.Context) error {
	return c.JSON(http.StatusNotFound, map[string]string{"message": "not found", "code": "not_found"})
}

func postsDecode(c *echo.Context, v any) error {
	if err := decodeJSONLimit(c, v, postsMaxBody); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body: "+err.Error())
	}
	return nil
}

func postsTime(v string) (*time.Time, bool) {
	if v == "" {
		return nil, true
	}
	if t, err := time.Parse(time.RFC3339, v); err == nil {
		return &t, true
	}
	if t, err := time.Parse(time.DateOnly, v); err == nil {
		return &t, true
	}
	return nil, false
}

func (ph *postsHandler) list(c *echo.Context) error {
	page, size := parsePage(c)
	f := posts.ListFilter{Type: c.QueryParam("type"), Status: c.QueryParam("status"), Q: c.QueryParam("q"),
		Sort: c.QueryParam("sort"), Mine: c.QueryParam("mine") == "1", Page: int(page), Size: int(size)}
	for _, p := range []struct {
		name string
		dst  *int64
	}{{"author", &f.AuthorID}, {"term", &f.TermID}} {
		if v := c.QueryParam(p.name); v != "" {
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fieldError(c, http.StatusBadRequest, p.name, "must be an id")
			}
			*p.dst = n
		}
	}
	var ok bool
	if f.From, ok = postsTime(c.QueryParam("from")); !ok {
		return fieldError(c, http.StatusBadRequest, "from", "use a date or an RFC 3339 time")
	}
	if f.To, ok = postsTime(c.QueryParam("to")); !ok {
		return fieldError(c, http.StatusBadRequest, "to", "use a date or an RFC 3339 time")
	}
	items, total, err := ph.svc.List(c.Request().Context(), postsActor(c), f)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, pageResponse[posts.ListItem]{Items: items, Page: page, PageSize: size, Total: total})
}

func (ph *postsHandler) create(c *echo.Context) error {
	var in posts.Input
	if err := postsDecode(c, &in); err != nil {
		return err
	}
	d, err := ph.svc.Save(c.Request().Context(), postsActor(c), 0, in, false)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusCreated, d)
}

func (ph *postsHandler) get(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	d, err := ph.svc.Get(c.Request().Context(), postsActor(c), id)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (ph *postsHandler) update(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	var in posts.Input
	if err := postsDecode(c, &in); err != nil {
		return err
	}
	d, err := ph.svc.Save(c.Request().Context(), postsActor(c), id, in, c.QueryParam("force") == "1")
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (ph *postsHandler) patch(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	var req struct {
		Version     int             `json:"version"`
		Title       *string         `json:"title"`
		Slug        *string         `json:"slug"`
		Status      *string         `json:"status"`
		PublishedAt json.RawMessage `json:"published_at"`
		CategoryIDs []int64         `json:"category_ids"`
		TagIDs      []int64         `json:"tag_ids"`
	}
	if err := postsDecode(c, &req); err != nil {
		return err
	}
	p := posts.PatchInput{Version: req.Version, Title: req.Title, Slug: req.Slug, Status: req.Status,
		CategoryIDs: req.CategoryIDs, TagIDs: req.TagIDs, SetPublishedAt: len(req.PublishedAt) > 0}
	if p.SetPublishedAt {
		if err := json.Unmarshal(req.PublishedAt, &p.PublishedAt); err != nil {
			return fieldError(c, http.StatusBadRequest, "published_at", "use an RFC 3339 time or null")
		}
	}
	d, err := ph.svc.Patch(c.Request().Context(), postsActor(c), id, p)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (ph *postsHandler) remove(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	if err := ph.svc.Delete(c.Request().Context(), postsActor(c), id); err != nil {
		return postsErr(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (ph *postsHandler) autosave(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	var in posts.AutosaveInput
	if err := postsDecode(c, &in); err != nil {
		return err
	}
	as, err := ph.svc.Autosave(c.Request().Context(), postsActor(c), id, in)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"revision_id": as.RevisionID, "saved_at": as.SavedAt})
}

func (ph *postsHandler) preview(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	var req struct {
		RevisionID *int64 `json:"revision_id"`
	}
	if err := postsDecode(c, &req); err != nil {
		return err
	}
	var rev int64
	if req.RevisionID != nil {
		rev = *req.RevisionID
	}
	u, exp, err := ph.svc.Preview(c.Request().Context(), id, rev)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"url": u, "expires_at": exp})
}

func (ph *postsHandler) revisions(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	items, err := ph.svc.Revisions(c.Request().Context(), id)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, items)
}

func (ph *postsHandler) revision(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	r, err := ph.svc.Revision(c.Request().Context(), id)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, r)
}

func (ph *postsHandler) diff(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	d, err := ph.svc.Diff(c.Request().Context(), id, c.QueryParam("against"))
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (ph *postsHandler) trash(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	var req struct {
		Gone   string `json:"gone"`
		Target string `json:"target"`
	}
	if err := postsDecode(c, &req); err != nil {
		return err
	}
	d, err := ph.svc.Trash(c.Request().Context(), postsActor(c), id, req.Gone, req.Target)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (ph *postsHandler) restore(c *echo.Context) error {
	id, ok := postsID(c)
	if !ok {
		return postsNotFound(c)
	}
	d, err := ph.svc.Restore(c.Request().Context(), postsActor(c), id)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, d)
}

func (ph *postsHandler) bulk(c *echo.Context) error {
	var in posts.BulkInput
	if err := postsDecode(c, &in); err != nil {
		return err
	}
	res, err := ph.svc.Bulk(c.Request().Context(), postsActor(c), in)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"results": res})
}

func (ph *postsHandler) slugCheck(c *echo.Context) error {
	var parent *int64
	if v := c.QueryParam("parent_id"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fieldError(c, http.StatusBadRequest, "parent_id", "must be an id")
		}
		parent = &n
	}
	exclude, _ := strconv.ParseInt(c.QueryParam("exclude"), 10, 64)
	typ := c.QueryParam("type")
	if typ == "" {
		typ = "post"
	}
	ok, suggestion, err := ph.svc.SlugCheck(c.Request().Context(), typ, c.QueryParam("slug"), parent, exclude)
	if err != nil {
		return postsErr(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"available": ok, "suggestion": suggestion})
}

// templates lists the theme's page-<name>.html files, which a page's template field selects (§6.2). Posts have
// no template overrides in the hierarchy.
func (ph *postsHandler) templates(c *echo.Context) error {
	entries, err := os.ReadDir(filepath.Join(ph.themeDir, "templates"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	pages := []string{}
	for _, e := range entries {
		if name, ok := strings.CutPrefix(e.Name(), "page-"); ok && !e.IsDir() && strings.HasSuffix(name, ".html") {
			pages = append(pages, strings.TrimSuffix(name, ".html"))
		}
	}
	slices.Sort(pages)
	classes := []string{}
	for cls := range ph.site.AllowedClasses() {
		classes = append(classes, cls)
	}
	slices.Sort(classes)
	return c.JSON(http.StatusOK, map[string][]string{"post": {}, "page": pages, "allowed_classes": classes})
}
