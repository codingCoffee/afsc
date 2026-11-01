package main

import (
	"net/http"

	"github.com/labstack/echo/v5"

	"afsc/internal/authz"
)

const apiPrefix = "/admin/api/v1"

// route registers an admin API route with the capability it requires (§7.2). Every admin route goes through
// here, so routes_test.go can check each one against the role matrix.
func (h *authHandler) route(g *echo.Group, method, path string, cp authz.Cap, handler echo.HandlerFunc, mw ...echo.MiddlewareFunc) {
	key := method + " " + apiPrefix + path
	if _, dup := h.caps[key]; dup {
		panic("admin route registered twice: " + key)
	}
	h.caps[key] = cp
	pre := []echo.MiddlewareFunc{apiGuard(h.cfg.baseURL, rawBodyRoutes[key])}
	if cp != authz.Public {
		pre = append(pre, h.requireAuth, requireCap(cp))
	}
	mw = append(pre, mw...)
	switch method {
	case http.MethodGet:
		g.GET(path, handler, mw...)
	case http.MethodPost:
		g.POST(path, handler, mw...)
	case http.MethodPut:
		g.PUT(path, handler, mw...)
	case http.MethodPatch:
		g.PATCH(path, handler, mw...)
	case http.MethodDelete:
		g.DELETE(path, handler, mw...)
	default:
		panic("unsupported admin route method " + method)
	}
}
