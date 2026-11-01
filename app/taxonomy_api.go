package main

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/labstack/echo/v5"

	"afsc/internal/authz"
	"afsc/internal/redirects"
	"afsc/internal/taxonomy"
)

type taxHandler struct{ svc *taxonomy.Service }

// registerTaxonomy registers the M3c admin routes.
func (h *authHandler) registerTaxonomy(g *echo.Group, d *adminDeps) {
	th := &taxHandler{svc: &taxonomy.Service{DB: d.pool, Site: d.site,
		Redirects: &redirects.Store{DB: d.pool, Router: d.site.Router()}, ThemeDir: d.themeDir}}
	edit, manage := authz.EditOwn, authz.ManageContent

	h.route(g, http.MethodGet, "/terms", edit, th.listTerms)
	h.route(g, http.MethodPost, "/terms", edit, th.createTerm)
	h.route(g, http.MethodGet, "/terms/:id", edit, th.getTerm)
	h.route(g, http.MethodPut, "/terms/:id", manage, th.updateTerm)
	h.route(g, http.MethodDelete, "/terms/:id", manage, th.deleteTerm)
	h.route(g, http.MethodPost, "/terms/:id/merge", manage, th.mergeTerm)

	h.route(g, http.MethodGet, "/authors", edit, th.listAuthors)
	h.route(g, http.MethodPost, "/authors", manage, th.createAuthor)
	h.route(g, http.MethodGet, "/authors/:id", edit, th.getAuthor)
	h.route(g, http.MethodPut, "/authors/:id", manage, th.updateAuthor)
	h.route(g, http.MethodDelete, "/authors/:id", manage, th.deleteAuthor)

	h.route(g, http.MethodGet, "/menus", manage, th.listMenus)
	h.route(g, http.MethodPost, "/menus", manage, th.createMenu)
	h.route(g, http.MethodGet, "/menus/:id", manage, th.getMenu)
	h.route(g, http.MethodPut, "/menus/:id", manage, th.saveMenu)
	h.route(g, http.MethodDelete, "/menus/:id", manage, th.deleteMenu)
	h.route(g, http.MethodPut, "/menu-locations", manage, th.setLocations)
}

func taxActor(c *echo.Context) taxonomy.Actor {
	return taxonomy.Actor{User: actor(c), IP: clientIP(c)}
}

func taxErr(c *echo.Context, err error) error {
	var te *taxonomy.Error
	if errors.As(err, &te) {
		return mediaJSONError(c, te.Status, te.Message, te.Code, te.Field, te.Extra)
	}
	return err
}

// taxPage reads ?page, ?page_size and ?all=1 (every row, for trees and pickers).
func taxPage(c *echo.Context) (page, size int32, all bool) {
	page, size = parsePage(c)
	return page, size, c.QueryParam("all") == "1"
}

func taxList[T any](c *echo.Context, items []T, total int64, page, size int32, all bool) error {
	if all {
		page, size = 1, int32(len(items))
	}
	return c.JSON(http.StatusOK, pageResponse[T]{Items: items, Page: page, PageSize: size, Total: total})
}

func (th *taxHandler) listTerms(c *echo.Context) error {
	page, size, all := taxPage(c)
	n := int(size)
	if all {
		n = 0
	}
	items, total, err := th.svc.ListTerms(c.Request().Context(), c.QueryParam("taxonomy"), c.QueryParam("q"), int(page), n)
	if err != nil {
		return err
	}
	return taxList(c, items, total, page, size, all)
}

func (th *taxHandler) getTerm(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "term not found")
	}
	t, err := th.svc.GetTerm(c.Request().Context(), id)
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, t)
}

// createTerm is open to every editor role so the post editor can add tags; categories need content.manage.
func (th *taxHandler) createTerm(c *echo.Context) error {
	var in taxonomy.TermInput
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	if in.Taxonomy != "tag" && !authz.Can(currentRole(c), authz.ManageContent) {
		return c.JSON(http.StatusForbidden, map[string]string{"message": "your role can't add categories", "code": "capability"})
	}
	t, err := th.svc.CreateTerm(c.Request().Context(), in, taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusCreated, t)
}

func (th *taxHandler) updateTerm(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "term not found")
	}
	var in taxonomy.TermInput
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	t, err := th.svc.UpdateTerm(c.Request().Context(), id, in, taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, t)
}

func (th *taxHandler) deleteTerm(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "term not found")
	}
	if err := th.svc.DeleteTerm(c.Request().Context(), id, taxActor(c)); err != nil {
		return taxErr(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (th *taxHandler) mergeTerm(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "term not found")
	}
	var in struct {
		Into int64 `json:"into"`
	}
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	t, err := th.svc.MergeTerm(c.Request().Context(), id, in.Into, taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, t)
}

func (th *taxHandler) listAuthors(c *echo.Context) error {
	page, size, all := taxPage(c)
	n := int(size)
	if all {
		n = 0
	}
	items, total, err := th.svc.ListAuthors(c.Request().Context(), c.QueryParam("q"), int(page), n)
	if err != nil {
		return err
	}
	return taxList(c, items, total, page, size, all)
}

func (th *taxHandler) getAuthor(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "author not found")
	}
	a, err := th.svc.GetAuthor(c.Request().Context(), id)
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, a)
}

// linking an author to a user account is reserved to users.manage; others keep the stored link
func taxLinkUsers(c *echo.Context) bool { return authz.Can(currentRole(c), authz.ManageUsers) }

func (th *taxHandler) createAuthor(c *echo.Context) error {
	var in taxonomy.AuthorInput
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	a, err := th.svc.CreateAuthor(c.Request().Context(), in, taxLinkUsers(c), taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusCreated, a)
}

func (th *taxHandler) updateAuthor(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "author not found")
	}
	var in taxonomy.AuthorInput
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	a, err := th.svc.UpdateAuthor(c.Request().Context(), id, in, taxLinkUsers(c), taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, a)
}

func (th *taxHandler) deleteAuthor(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "author not found")
	}
	var reassign *int64
	if s := c.QueryParam("reassign"); s != "" {
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil || n <= 0 {
			return fieldError(c, http.StatusBadRequest, "reassign", "not an author id")
		}
		reassign = &n
	}
	if err := th.svc.DeleteAuthor(c.Request().Context(), id, reassign, taxActor(c)); err != nil {
		return taxErr(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (th *taxHandler) listMenus(c *echo.Context) error {
	m, err := th.svc.ListMenus(c.Request().Context())
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, m)
}

func (th *taxHandler) getMenu(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "menu not found")
	}
	m, err := th.svc.GetMenu(c.Request().Context(), id)
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, m)
}

func (th *taxHandler) createMenu(c *echo.Context) error {
	var in struct {
		Name string `json:"name"`
		Slug string `json:"slug"`
	}
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	m, err := th.svc.CreateMenu(c.Request().Context(), taxonomy.MenuInput{Name: in.Name, Slug: in.Slug}, taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusCreated, m)
}

func (th *taxHandler) saveMenu(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "menu not found")
	}
	var in taxonomy.MenuInput
	if err := decodeJSONLimit(c, &in, 1<<20); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	m, err := th.svc.SaveMenu(c.Request().Context(), id, in, taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, m)
}

func (th *taxHandler) deleteMenu(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusNotFound, "menu not found")
	}
	if err := th.svc.DeleteMenu(c.Request().Context(), id, taxActor(c)); err != nil {
		return taxErr(c, err)
	}
	return c.NoContent(http.StatusNoContent)
}

func (th *taxHandler) setLocations(c *echo.Context) error {
	var in struct {
		Locations map[string]*int64 `json:"locations"`
	}
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	m, err := th.svc.SetLocations(c.Request().Context(), in.Locations, taxActor(c))
	if err != nil {
		return taxErr(c, err)
	}
	return c.JSON(http.StatusOK, m)
}
