package main

import (
	"context"
	"html/template"
	"log"
	"net/http"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/labstack/echo/v5"

	"afsc/internal/plugins"
	"afsc/internal/theme"
	registry "afsc/plugins"
)

// buildPlugins initialises every compiled-in plugin. pool and secret may be nil (theme check, migrate, dry runs).
func buildPlugins(ctx context.Context, pool *pgxpool.Pool, baseURL string, secret []byte) (*plugins.Runtime, error) {
	return plugins.Build(ctx, plugins.Config{DB: pool, BaseURL: baseURL, Secret: secret, Logf: log.Printf}, registry.All())
}

// themeOptions are the plugin funcs and head/body filters for a theme loaded outside the site (theme check,
// import). Plugins see their default settings there.
func themeOptions(rt *plugins.Runtime) theme.Options {
	opts := theme.Options{Funcs: rt.Funcs()}
	html := func(f plugins.Filter[template.HTML, *theme.PageData]) func(*theme.PageData, template.HTML) template.HTML {
		if !rt.HasFilter(f.Name) {
			return nil
		}
		return func(d *theme.PageData, h template.HTML) template.HTML {
			return plugins.ApplyFilter(context.Background(), rt, f, h, d)
		}
	}
	opts.HeadFilter, opts.BodyFilter = html(plugins.HeadTags), html(plugins.BodyEnd)
	return opts
}

// pluginPublic serves /_plugins/… with the public security headers. Request bodies are capped at 1 MB.
func pluginPublic(rt *plugins.Runtime, headers func(http.ResponseWriter)) echo.HandlerFunc {
	return echo.WrapHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		headers(w)
		rt.ServePublic(w, r)
	}))
}

// registerPluginAdmin mounts every plugin admin route through route(), so it gets auth, CSRF, a capability and
// the capability test. A disabled plugin's routes answer 404.
func (h *authHandler) registerPluginAdmin(g *echo.Group, rt *plugins.Runtime) {
	for _, r := range rt.AdminRoutes() {
		var names []string
		for seg := range strings.SplitSeq(r.Path, "/") {
			if name, ok := strings.CutPrefix(seg, ":"); ok {
				names = append(names, name)
			}
		}
		h.route(g, r.Method, r.Path, r.Cap, func(c *echo.Context) error {
			if !rt.Enabled(r.Plugin) {
				return jsonError(c, http.StatusNotFound, "this plugin is switched off")
			}
			params := map[string]string{}
			for _, n := range names {
				params[n] = c.Param(n)
			}
			a := plugins.Admin{User: currentUser(c), Role: currentRole(c), IP: clientIP(c), Params: params}
			req := c.Request().WithContext(plugins.WithAdmin(c.Request().Context(), a))
			r.Handler.ServeHTTP(c.Response(), req)
			return nil
		})
	}
}
