package main

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/redirects"
	"afsc/internal/site"
)

// redirectHandler serves the redirect manager and the 404 log to admins and editors.
type redirectHandler struct {
	store *redirects.Store
	site  *site.Site
	pool  *pgxpool.Pool
}

func (h *authHandler) registerRedirects(g *echo.Group, rh *redirectHandler) {
	rh.pool = h.pool
	cp := authz.ManageContent
	h.route(g, http.MethodGet, "/redirects", cp, rh.list)
	h.route(g, http.MethodPost, "/redirects", cp, rh.create)
	h.route(g, http.MethodPost, "/redirects/test", cp, rh.test)
	h.route(g, http.MethodGet, "/redirects/export", cp, rh.export)
	h.route(g, http.MethodPost, "/redirects/import", cp, rh.importCSV)
	h.route(g, http.MethodGet, "/redirects/:id", cp, rh.get)
	h.route(g, http.MethodPut, "/redirects/:id", cp, rh.update)
	h.route(g, http.MethodDelete, "/redirects/:id", cp, rh.remove)

	h.route(g, http.MethodGet, "/not-found", cp, rh.listNotFound)
	h.route(g, http.MethodPost, "/not-found/dismiss", cp, rh.dismiss)
}

// audit records a redirect change (§7.1); entityID is the redirect id, or "" for imports.
func (rh *redirectHandler) audit(c *echo.Context, action, entityID string, diff any) error {
	if rh.pool == nil {
		return nil
	}
	return audit.Write(c.Request().Context(), rh.pool, audit.Entry{Actor: actor(c), Action: action, EntityType: "redirect",
		EntityID: entityID, Diff: diff, IP: clientIP(c)})
}

type savedResponse struct {
	Redirect     *redirects.Row `json:"redirect"`
	Warnings     []string       `json:"warnings"`
	FlattenedIDs []int64        `json:"flattened_ids"`
}

type chainResponse struct {
	Message         string          `json:"message"`
	Field           string          `json:"field"`
	Code            string          `json:"code"`
	Hops            []redirects.Hop `json:"hops"`
	FlattenedTarget *string         `json:"flattened_target"`
}

// writeErr maps redirect errors to responses; anything else is a 500.
func writeErr(c *echo.Context, err error) error {
	var fe *redirects.FieldError
	var ce *redirects.ChainError
	switch {
	case errors.Is(err, redirects.ErrNotFound):
		return jsonError(c, http.StatusNotFound, "redirect not found")
	case errors.As(err, &fe):
		status := http.StatusBadRequest
		if fe.Conflict {
			status = http.StatusConflict
		}
		return fieldError(c, status, fe.Field, fe.Message)
	case errors.As(err, &ce):
		resp := chainResponse{Message: ce.Error(), Field: "target", Code: ce.Code, Hops: ce.Hops}
		if ce.Flattened != "" {
			resp.FlattenedTarget = &ce.Flattened
		}
		return c.JSON(http.StatusConflict, resp)
	}
	return err
}

func (rh *redirectHandler) invalidate(c *echo.Context, p redirects.Purge) error {
	return rh.site.Invalidate(c.Request().Context(), site.Event{Refresh: p.Refresh, All: p.Refresh, Paths: p.Paths})
}

func userRef(c *echo.Context) *string {
	id, ok := c.Get(ctxUserID).(uuid.UUID)
	if !ok {
		return nil
	}
	s := id.String()
	return &s
}

func pathID(c *echo.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	return id, err == nil && id > 0
}

func (rh *redirectHandler) list(c *echo.Context) error {
	page, size := parsePage(c)
	q := redirects.ListQuery{Q: c.QueryParam("q"), Match: c.QueryParam("match"), Origin: c.QueryParam("origin"),
		Sort: c.QueryParam("sort"), Page: int(page), Size: int(size)}
	items, total, err := rh.store.List(c.Request().Context(), q)
	if err != nil {
		return err
	}
	if items == nil {
		items = []redirects.Row{}
	}
	return c.JSON(http.StatusOK, pageResponse[redirects.Row]{Items: items, Page: page, PageSize: size, Total: total})
}

func (rh *redirectHandler) get(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusBadRequest, "invalid redirect id")
	}
	row, err := rh.store.Get(c.Request().Context(), id)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, row)
}

func (rh *redirectHandler) create(c *echo.Context) error { return rh.save(c, 0, http.StatusCreated) }

func (rh *redirectHandler) update(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusBadRequest, "invalid redirect id")
	}
	return rh.save(c, id, http.StatusOK)
}

func (rh *redirectHandler) save(c *echo.Context, id int64, status int) error {
	var in redirects.Input
	if err := decodeJSON(c, &in); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	saved, err := rh.store.Save(ctx, id, in, c.QueryParam("flatten") == "1", userRef(c))
	if err != nil {
		return writeErr(c, err)
	}
	if err := rh.invalidate(c, saved.Purge); err != nil {
		return err
	}
	row, err := rh.store.Get(ctx, saved.ID)
	if err != nil {
		return err
	}
	action := "redirect.update"
	if id == 0 {
		action = "redirect.create"
	}
	if err := rh.audit(c, action, strconv.FormatInt(saved.ID, 10), map[string]any{"redirect": row,
		"flattened_ids": saved.Flattened}); err != nil {
		return err
	}
	if saved.Warnings == nil {
		saved.Warnings = []string{}
	}
	return c.JSON(status, savedResponse{Redirect: row, Warnings: saved.Warnings, FlattenedIDs: saved.Flattened})
}

func (rh *redirectHandler) remove(c *echo.Context) error {
	id, ok := pathID(c)
	if !ok {
		return jsonError(c, http.StatusBadRequest, "invalid redirect id")
	}
	before, err := rh.store.Get(c.Request().Context(), id)
	if err != nil {
		return writeErr(c, err)
	}
	p, err := rh.store.Delete(c.Request().Context(), id)
	if err != nil {
		return writeErr(c, err)
	}
	if err := rh.invalidate(c, p); err != nil {
		return err
	}
	if err := rh.audit(c, "redirect.delete", strconv.FormatInt(id, 10), map[string]any{"redirect": before}); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

func (rh *redirectHandler) test(c *echo.Context) error {
	var req struct {
		Match  string   `json:"match"`
		Source string   `json:"source"`
		Target string   `json:"target"`
		Paths  []string `json:"paths"`
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	res, err := redirects.Test(req.Match, req.Source, req.Target, req.Paths, rh.site.Router())
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, map[string]any{"results": res})
}

func (rh *redirectHandler) export(c *echo.Context) error {
	var buf bytes.Buffer
	if err := rh.store.Export(c.Request().Context(), &buf); err != nil {
		return err
	}
	c.Response().Header().Set("Content-Disposition", `attachment; filename="redirects.csv"`)
	return c.Blob(http.StatusOK, "text/csv; charset=utf-8", buf.Bytes())
}

func (rh *redirectHandler) importCSV(c *echo.Context) error {
	body, err := io.ReadAll(http.MaxBytesReader(nil, c.Request().Body, redirects.MaxCSVBytes+1))
	if err != nil || len(body) > redirects.MaxCSVBytes {
		return jsonError(c, http.StatusRequestEntityTooLarge, "the file must be at most 5 MB")
	}
	lines, lineErrs, err := redirects.ReadCSV(bytes.NewReader(body))
	if err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid CSV: "+err.Error())
	}
	if len(lineErrs) > 0 {
		return c.JSON(http.StatusUnprocessableEntity, redirects.ImportReport{Errors: lineErrs, Warnings: []redirects.LineError{}})
	}
	rep, err := rh.store.Import(c.Request().Context(), lines, c.QueryParam("flatten") == "1", userRef(c))
	if err != nil {
		return err
	}
	if len(rep.Errors) > 0 {
		return c.JSON(http.StatusUnprocessableEntity, rep)
	}
	if err := rh.invalidate(c, rep.Purge); err != nil {
		return err
	}
	if err := rh.audit(c, "redirect.import", "", map[string]any{"lines": len(lines)}); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, rep)
}

func (rh *redirectHandler) listNotFound(c *echo.Context) error {
	page, size := parsePage(c)
	items, total, err := rh.store.ListNotFound(c.Request().Context(), c.QueryParam("q"), c.QueryParam("sort"), int(page), int(size))
	if err != nil {
		return err
	}
	if items == nil {
		items = []redirects.NotFound{}
	}
	return c.JSON(http.StatusOK, pageResponse[redirects.NotFound]{Items: items, Page: page, PageSize: size, Total: total})
}

func (rh *redirectHandler) dismiss(c *echo.Context) error {
	var req struct {
		Paths []string `json:"paths"`
	}
	if err := decodeJSON(c, &req); err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	n, err := rh.store.Dismiss(c.Request().Context(), req.Paths)
	if err != nil {
		return writeErr(c, err)
	}
	return c.JSON(http.StatusOK, map[string]int64{"dismissed": n})
}
