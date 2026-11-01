package main

import (
	"net/http"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"afsc/internal/authz"
	"afsc/internal/redirects"
	"afsc/internal/site"
	"afsc/internal/storage"
)

// adminDeps is everything the admin API handlers share.
type adminDeps struct {
	pool     *pgxpool.Pool
	site     *site.Site
	storage  storage.Storage
	baseURL  string
	themeDir string
	dev      bool
}

// mountAdmin registers the whole admin JSON API under /admin/api/v1. Tests call it too, so they see the same
// routes, guards and capabilities as production.
func mountAdmin(e *echo.Echo, h *authHandler, d *adminDeps, mw ...echo.MiddlewareFunc) *echo.Group {
	api := e.Group(apiPrefix, mw...)
	h.route(api, http.MethodGet, "/ht", authz.Public, healthHandler(d.pool))
	h.register(api)
	h.registerRedirects(api, &redirectHandler{store: &redirects.Store{DB: d.pool, Router: d.site.Router()}, site: d.site})
	h.registerPosts(api, d)
	h.registerMedia(api, d)
	h.registerTaxonomy(api, d)
	h.registerSettings(api, d)
	h.registerSEO(api, d)
	h.registerComments(api, d)
	return api
}
