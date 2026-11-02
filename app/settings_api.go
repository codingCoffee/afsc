package main

import (
	"encoding/json"
	"errors"
	"io"
	"maps"
	"net/http"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v5"

	"afsc/internal/audit"
	"afsc/internal/authz"
	"afsc/internal/plugins"
	"afsc/internal/settings"
	"afsc/internal/site"
)

// settingsAPI reads d.site per request, so tests can swap in a site with a page cache.
type settingsAPI struct {
	d *adminDeps
}

// registerSettings registers the M3e admin routes.
func (h *authHandler) registerSettings(g *echo.Group, d *adminDeps) {
	s := &settingsAPI{d: d}
	h.route(g, http.MethodGet, "/settings", authz.ManageSettings, s.get)
	h.route(g, http.MethodPut, "/settings/:group", authz.ManageSettings, s.put)
	h.route(g, http.MethodGet, "/dashboard", authz.Self, s.dashboard)
}

const (
	settingsSQLAll  = `SELECT coalesce(jsonb_object_agg(key, value), '{}'::jsonb) FROM settings`
	settingsSQLLock = `SELECT pg_advisory_xact_lock(7310001)`
	settingsSQLPut  = `
INSERT INTO settings (key, value) VALUES ($1, $2)
ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value, updated_at = now()`
)

type settingsThemeDecl struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Default any      `json:"default"`
	Options []string `json:"options,omitempty"`
}

func (s *settingsAPI) get(c *echo.Context) error {
	var raw []byte
	if err := s.d.pool.QueryRow(c.Request().Context(), settingsSQLAll).Scan(&raw); err != nil {
		return err
	}
	all, _, err := settings.Decode(raw)
	if err != nil {
		return err
	}
	values := map[string]any{}
	for _, k := range settings.Keys {
		values[k] = all.Group(k)
	}
	m := s.d.site.Theme().Manifest()
	decls := []settingsThemeDecl{}
	for _, k := range slices.Sorted(maps.Keys(m.Settings)) {
		d := m.Settings[k]
		decls = append(decls, settingsThemeDecl{Key: k, Label: d.Label, Type: d.Type, Default: d.Default, Options: d.Options})
	}
	return c.JSON(http.StatusOK, map[string]any{
		"values":          values,
		"theme":           map[string]any{"name": m.Name, "settings": decls},
		"plugins":         s.d.plugins.List(),
		"csp_report_only": all.Security.CSPReportOnly,
	})
}

func (s *settingsAPI) put(c *echo.Context) error {
	group := c.Param("group")
	if !slices.Contains(settings.Keys, group) {
		return jsonError(c, http.StatusNotFound, "unknown settings group")
	}
	body, err := io.ReadAll(http.MaxBytesReader(nil, c.Request().Body, 256<<10))
	if err != nil {
		return jsonError(c, http.StatusBadRequest, "invalid request body")
	}
	ctx := c.Request().Context()
	tx, err := s.d.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, settingsSQLLock); err != nil {
		return err
	}
	var raw []byte
	if err := tx.QueryRow(ctx, settingsSQLAll).Scan(&raw); err != nil {
		return err
	}
	cur, _, err := settings.Decode(raw)
	if err != nil {
		return err
	}
	next := cur
	if err := next.ReplaceGroup(group, body); err != nil {
		return jsonError(c, http.StatusBadRequest, group+": "+err.Error())
	}
	if err := next.Validate(); err != nil {
		return settingsValidationError(c, group, err)
	}
	if field, msg := s.checkRefs(c, &next, group); field != "" {
		return fieldError(c, http.StatusBadRequest, field, msg)
	}
	if group == "theme_settings" {
		if field, msg := s.checkThemeSettings(next.ThemeSettings); field != "" {
			return fieldError(c, http.StatusBadRequest, field, msg)
		}
	}
	if group == "plugins" {
		if field, msg := s.d.plugins.ValidateSettings(next.Plugins); field != "" {
			return fieldError(c, http.StatusBadRequest, field, msg)
		}
	}
	value := next.Group(group)
	b, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if _, err := tx.Exec(ctx, settingsSQLPut, group, json.RawMessage(b)); err != nil {
		return err
	}
	diff, err := settingsDiff(group, cur.Group(group), value)
	if err != nil {
		return err
	}
	if len(diff) > 0 {
		if err := audit.Write(ctx, tx, audit.Entry{Actor: actor(c), Action: "settings.update", EntityType: "settings",
			EntityID: group, Diff: diff, IP: clientIP(c)}); err != nil {
			return err
		}
		if err := plugins.Emit(ctx, s.d.plugins, tx, plugins.SettingsChanged,
			plugins.SettingsData{Group: group, Fields: slices.Sorted(maps.Keys(diff))}); err != nil {
			return err
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	if err := s.d.site.Invalidate(ctx, site.Event{Refresh: true, All: true}); err != nil {
		return err
	}
	return c.JSON(http.StatusOK, map[string]any{"group": group, "value": value})
}

// settingsValidationError reports the first problem in group (else the first overall) with a group-relative field.
func settingsValidationError(c *echo.Context, group string, err error) error {
	var inv settings.Invalid
	if !errors.As(err, &inv) || len(inv) == 0 {
		return jsonError(c, http.StatusBadRequest, err.Error())
	}
	p := inv[0]
	for _, x := range inv {
		if strings.HasPrefix(x, group+".") || strings.HasPrefix(x, group+":") {
			p = x
			break
		}
	}
	path, msg, _ := strings.Cut(p, ": ")
	return fieldError(c, http.StatusBadRequest, strings.TrimPrefix(strings.TrimPrefix(path, group), "."), msg)
}

// checkRefs makes sure every reference in group names an existing page or media item by id.
func (s *settingsAPI) checkRefs(c *echo.Context, a *settings.All, group string) (string, string) {
	for _, name := range slices.Sorted(maps.Keys(a.Refs())) {
		ref := *a.Refs()[name]
		if !strings.HasPrefix(name, group+".") || ref == nil || ref.IsZero() {
			continue
		}
		field := strings.TrimPrefix(name, group+".")
		if ref.ID <= 0 {
			return field, "must be an id"
		}
		sql := `SELECT EXISTS (SELECT 1 FROM media WHERE id = $1)`
		what := "media item"
		if settings.RefKind[name] == "post" {
			sql = `SELECT EXISTS (SELECT 1 FROM posts WHERE id = $1 AND type = 'page' AND status <> 'trash')`
			what = "page"
		}
		var ok bool
		if err := s.d.pool.QueryRow(c.Request().Context(), sql, ref.ID).Scan(&ok); err != nil || !ok {
			return field, "no such " + what
		}
	}
	return "", ""
}

// checkThemeSettings validates the active theme's values against its theme.json declarations. Values saved for
// other themes are kept as they are.
func (s *settingsAPI) checkThemeSettings(ts map[string]map[string]any) (string, string) {
	m := s.d.site.Theme().Manifest()
	vals := ts[m.Name]
	for _, k := range slices.Sorted(maps.Keys(vals)) {
		decl, ok := m.Settings[k]
		if !ok {
			return m.Name + "." + k, "not declared in theme.json"
		}
		if err := decl.Check(vals[k]); err != nil {
			return m.Name + "." + k, err.Error()
		}
	}
	return "", ""
}

// settingsDiff compares two group values field by field. Nested objects are flattened to dotted keys; lists are
// compared whole.
func settingsDiff(group string, before, after any) (map[string]audit.Change, error) {
	a, err := settingsFlatten(group, before)
	if err != nil {
		return nil, err
	}
	b, err := settingsFlatten(group, after)
	if err != nil {
		return nil, err
	}
	diff := map[string]audit.Change{}
	for k, v := range a {
		if w, ok := b[k]; !ok || !reflect.DeepEqual(v, w) {
			diff[k] = audit.Change{From: v, To: b[k]}
		}
	}
	for k, w := range b {
		if _, ok := a[k]; !ok {
			diff[k] = audit.Change{From: nil, To: w}
		}
	}
	return diff, nil
}

func settingsFlatten(prefix string, v any) (map[string]any, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var x any
	if err := json.Unmarshal(b, &x); err != nil {
		return nil, err
	}
	out := map[string]any{}
	var walk func(string, any)
	walk = func(p string, v any) {
		if m, ok := v.(map[string]any); ok && len(m) > 0 {
			for k, c := range m {
				walk(p+"."+k, c)
			}
			return
		}
		out[p] = v
	}
	walk(prefix, x)
	return out, nil
}

type dashAuthorRef struct {
	ID          int64  `json:"id"`
	DisplayName string `json:"display_name"`
	Slug        string `json:"slug"`
}

type dashTermRef struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Slug     string `json:"slug"`
	Taxonomy string `json:"taxonomy"`
}

type dashPost struct {
	ID          int64           `json:"id"`
	Type        string          `json:"type"`
	Status      string          `json:"status"`
	Title       string          `json:"title"`
	Slug        string          `json:"slug"`
	URL         *string         `json:"url"`
	PublishedAt *time.Time      `json:"published_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
	Authors     []dashAuthorRef `json:"authors"`
	Categories  []dashTermRef   `json:"categories"`
	Tags        []dashTermRef   `json:"tags"`
	Sticky      bool            `json:"sticky"`
	Version     int             `json:"version"`
	CanEdit     bool            `json:"can_edit"`
}

type dash404 struct {
	Path     string    `json:"path"`
	Hits     int64     `json:"hits"`
	LastSeen time.Time `json:"last_seen"`
}

// dashOwn is true when the caller's linked author ($1) is among the post's authors.
const dashOwn = `EXISTS (SELECT 1 FROM post_authors pa WHERE pa.post_id = p.id AND pa.author_id = $1)`

// dashListSQL is completed with a constant WHERE and ORDER BY; $2 is "sees every post".
const dashListSQL = `
SELECT p.id, p.type, p.status, p.title, p.slug, p.published_at, p.updated_at, p.sticky, p.version, ` + dashOwn + `,
       coalesce((SELECT jsonb_agg(jsonb_build_object('id', a.id, 'display_name', a.display_name, 'slug', a.slug) ORDER BY pa.position)
                 FROM post_authors pa JOIN authors a ON a.id = pa.author_id WHERE pa.post_id = p.id), '[]'),
       coalesce((SELECT jsonb_agg(jsonb_build_object('id', t.id, 'name', t.name, 'slug', t.slug, 'taxonomy', t.taxonomy) ORDER BY t.name)
                 FROM post_terms pt JOIN terms t ON t.id = pt.term_id WHERE pt.post_id = p.id), '[]')
FROM posts p
WHERE `

var dashLists = map[string]string{
	"my_drafts":      `p.status IN ('draft', 'pending') AND ` + dashOwn + ` ORDER BY p.updated_at DESC, p.id DESC LIMIT 10`,
	"pending_review": `p.status = 'pending' AND ($2 OR ` + dashOwn + `) ORDER BY p.updated_at DESC, p.id DESC LIMIT 10`,
	"scheduled":      `p.status = 'scheduled' AND ($2 OR ` + dashOwn + `) ORDER BY p.published_at, p.id LIMIT 10`,
}

func (s *settingsAPI) dashboard(c *echo.Context) error {
	ctx := c.Request().Context()
	role := currentRole(c)
	all := authz.Can(role, authz.EditOthers)
	var authorID int64
	if err := s.d.pool.QueryRow(ctx, `SELECT coalesce((SELECT id FROM authors WHERE user_id = $1), 0)`,
		currentUser(c)).Scan(&authorID); err != nil {
		return err
	}
	out := map[string]any{"csp_report_only": s.d.site.Settings().Security.CSPReportOnly}
	for key, where := range dashLists {
		args := []any{authorID, all}
		if key == "my_drafts" {
			args = args[:1]
		}
		items, err := s.dashPosts(c, dashListSQL+where, all, role, args...)
		if err != nil {
			return err
		}
		out[key] = items
	}

	top := []dash404{}
	if authz.Can(role, authz.ManageContent) {
		rows, err := s.d.pool.Query(ctx, `SELECT path, hits, last_seen FROM not_found_log ORDER BY hits DESC, last_seen DESC LIMIT 10`)
		if err != nil {
			return err
		}
		for rows.Next() {
			var r dash404
			if err := rows.Scan(&r.Path, &r.Hits, &r.LastSeen); err != nil {
				rows.Close()
				return err
			}
			top = append(top, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return err
		}
	}
	out["top_404s"] = top

	if authz.Can(role, authz.ModerateComments) {
		pc, err := s.pendingComments(c)
		if err != nil {
			return err
		}
		out["pending_comments"] = pc
	}

	counts := map[string]int64{"draft": 0, "pending": 0, "scheduled": 0, "published": 0, "private": 0, "trash": 0}
	rows, err := s.d.pool.Query(ctx, `SELECT p.status, count(*) FROM posts p WHERE $2 OR `+dashOwn+` GROUP BY p.status`,
		authorID, all)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var st string
		var n int64
		if err := rows.Scan(&st, &n); err != nil {
			return err
		}
		counts[st] = n
	}
	if err := rows.Err(); err != nil {
		return err
	}
	out["counts"] = counts
	return c.JSON(http.StatusOK, out)
}

type dashComment struct {
	ID         int64     `json:"id"`
	PostID     int64     `json:"post_id"`
	PostTitle  string    `json:"post_title"`
	AuthorName string    `json:"author_name"`
	Excerpt    string    `json:"excerpt"`
	CreatedAt  time.Time `json:"created_at"`
}

type dashPending struct {
	Count  int64         `json:"count"`
	Latest []dashComment `json:"latest"`
}

func (s *settingsAPI) pendingComments(c *echo.Context) (*dashPending, error) {
	ctx := c.Request().Context()
	out := &dashPending{Latest: []dashComment{}}
	if err := s.d.pool.QueryRow(ctx, `SELECT count(*) FROM comments WHERE status = 'pending'`).Scan(&out.Count); err != nil {
		return nil, err
	}
	rows, err := s.d.pool.Query(ctx, `
SELECT c.id, c.post_id, p.title, c.author_name, left(c.body, 140), c.created_at
FROM comments c JOIN posts p ON p.id = c.post_id
WHERE c.status = 'pending' ORDER BY c.created_at DESC, c.id DESC LIMIT 5`)
	if err != nil {
		return nil, err
	}
	var r dashComment
	_, err = pgx.ForEachRow(rows, []any{&r.ID, &r.PostID, &r.PostTitle, &r.AuthorName, &r.Excerpt, &r.CreatedAt}, func() error {
		out.Latest = append(out.Latest, r)
		return nil
	})
	return out, err
}

func (s *settingsAPI) dashPosts(c *echo.Context, sql string, all bool, role string, args ...any) ([]dashPost, error) {
	rows, err := s.d.pool.Query(c.Request().Context(), sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []dashPost{}
	for rows.Next() {
		var p dashPost
		var own bool
		var authors, terms []byte
		if err := rows.Scan(&p.ID, &p.Type, &p.Status, &p.Title, &p.Slug, &p.PublishedAt, &p.UpdatedAt, &p.Sticky,
			&p.Version, &own, &authors, &terms); err != nil {
			return nil, err
		}
		var ts []dashTermRef
		if err := json.Unmarshal(authors, &p.Authors); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(terms, &ts); err != nil {
			return nil, err
		}
		p.Categories, p.Tags = []dashTermRef{}, []dashTermRef{}
		for _, t := range ts {
			if t.Taxonomy == "category" {
				p.Categories = append(p.Categories, t)
			} else {
				p.Tags = append(p.Tags, t)
			}
		}
		edit := authz.EditOwn
		if p.Type == "page" {
			edit = authz.EditPages
		}
		p.CanEdit = authz.Can(role, edit) && (all || own)
		out = append(out, p)
	}
	return out, rows.Err()
}
