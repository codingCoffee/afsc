package taxonomy

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jackc/pgx/v5"

	"afsc/internal/site"
)

const (
	maxMenuDepth = 5
	maxMenuItems = 500
)

type MenuItemInput struct {
	Label      string          `json:"label"`
	Kind       string          `json:"kind"`
	TargetID   *int64          `json:"target_id"`
	URL        *string         `json:"url"`
	NewTab     bool            `json:"new_tab"`
	CSSClasses []string        `json:"css_classes"`
	Children   []MenuItemInput `json:"children"`
}

type MenuItem struct {
	ID          int64       `json:"id"`
	Label       string      `json:"label"`
	Kind        string      `json:"kind"`
	TargetID    *int64      `json:"target_id"`
	URL         *string     `json:"url"`
	NewTab      bool        `json:"new_tab"`
	CSSClasses  []string    `json:"css_classes"`
	ResolvedURL *string     `json:"resolved_url"`
	TargetTitle *string     `json:"target_title"`
	Children    []*MenuItem `json:"children"`
	parent      int64
}

type Menu struct {
	ID    int64       `json:"id"`
	Name  string      `json:"name"`
	Slug  string      `json:"slug"`
	Items []*MenuItem `json:"items"`
}

type MenuInput struct {
	Name  string          `json:"name"`
	Slug  string          `json:"slug"`
	Items []MenuItemInput `json:"items"`
}

type Location struct {
	Location string `json:"location"`
	Label    string `json:"label"`
	MenuID   *int64 `json:"menu_id"`
}

type Menus struct {
	Menus     []*Menu    `json:"menus"`
	Locations []Location `json:"locations"`
}

var classRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_-]{0,63}$`)

// purgeAll is the event for any menu change: menus show on every page.
var purgeAll = site.Event{Refresh: true, All: true}

// loadMenus reads menus (all when id is 0) with their item trees, resolving each target's title and public URL.
func (s *Service) loadMenus(ctx context.Context, id int64) ([]*Menu, error) {
	rows, err := s.DB.Query(ctx, `SELECT id, name, slug FROM menus WHERE $1::bigint = 0 OR id = $1 ORDER BY lower(name), id`, id)
	if err != nil {
		return nil, err
	}
	menus, err := pgx.CollectRows(rows, func(r pgx.CollectableRow) (*Menu, error) {
		m := &Menu{Items: []*MenuItem{}}
		return m, r.Scan(&m.ID, &m.Name, &m.Slug)
	})
	if err != nil || len(menus) == 0 {
		return menus, err
	}
	byMenu := map[int64]*Menu{}
	for _, m := range menus {
		byMenu[m.ID] = m
	}
	rows, err = s.DB.Query(ctx, `
SELECT id, menu_id, coalesce(parent_id, 0), label, kind, target_id, url, new_tab, css_classes FROM menu_items
WHERE $1::bigint = 0 OR menu_id = $1 ORDER BY menu_id, position, id`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var items []*MenuItem
	itemMenu := map[*MenuItem]int64{}
	for rows.Next() {
		it := &MenuItem{Children: []*MenuItem{}}
		var menuID int64
		if err := rows.Scan(&it.ID, &menuID, &it.parent, &it.Label, &it.Kind, &it.TargetID, &it.URL, &it.NewTab,
			&it.CSSClasses); err != nil {
			return nil, err
		}
		if it.CSSClasses == nil {
			it.CSSClasses = []string{}
		}
		items = append(items, it)
		itemMenu[it] = menuID
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if err := s.resolve(ctx, items); err != nil {
		return nil, err
	}
	byID := map[int64]*MenuItem{}
	for _, it := range items {
		byID[it.ID] = it
	}
	for _, it := range items {
		if p := byID[it.parent]; p != nil {
			p.Children = append(p.Children, it)
		} else if m := byMenu[itemMenu[it]]; m != nil {
			m.Items = append(m.Items, it)
		}
	}
	return menus, nil
}

// resolve sets target_title and resolved_url. Unpublished posts and missing targets resolve to null.
func (s *Service) resolve(ctx context.Context, items []*MenuItem) error {
	ids := map[string][]int64{}
	for _, it := range items {
		if it.Kind == "url" {
			it.ResolvedURL = it.URL
		} else if it.TargetID != nil {
			ids[it.Kind] = append(ids[it.Kind], *it.TargetID)
		}
	}
	type target struct {
		title string
		url   *string
	}
	found := map[string]target{}
	key := func(kind string, id int64) string { return fmt.Sprintf("%s:%d", kind, id) }
	perm, err := s.perm()
	if err != nil {
		return err
	}
	if len(ids["post"]) > 0 {
		rows, err := s.DB.Query(ctx, `SELECT id, title, status FROM posts WHERE id = ANY($1::bigint[])`, ids["post"])
		if err != nil {
			return err
		}
		var id int64
		var title, status string
		if _, err := pgx.ForEachRow(rows, []any{&id, &title, &status}, func() error {
			found[key("post", id)] = target{title: title}
			return nil
		}); err != nil {
			return err
		}
		for _, id := range ids["post"] {
			k := key("post", id)
			t, ok := found[k]
			if !ok {
				continue
			}
			urls, err := s.Site.PostURLs(ctx, id)
			if err != nil {
				return err
			}
			if p, ok := urls[k]; ok {
				t.url = s.abs(p)
				found[k] = t
			}
		}
	}
	if len(ids["term"]) > 0 {
		terms := []*Term{}
		rows, err := s.DB.Query(ctx, sqlTermsByID, ids["term"])
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			t, err := scanTerm(rows)
			if err != nil {
				return err
			}
			terms = append(terms, t)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if err := s.termURLs(ctx, terms); err != nil {
			return err
		}
		for _, t := range terms {
			found[key("term", t.ID)] = target{title: t.Name, url: t.URL}
		}
	}
	if len(ids["author"]) > 0 {
		rows, err := s.DB.Query(ctx, `SELECT id, display_name, slug FROM authors WHERE id = ANY($1::bigint[])`, ids["author"])
		if err != nil {
			return err
		}
		archives := s.Site.Settings().Permalinks.AuthorArchivesEnabled
		var id int64
		var name, slug string
		if _, err := pgx.ForEachRow(rows, []any{&id, &name, &slug}, func() error {
			t := target{title: name}
			if archives {
				t.url = s.abs(perm.Author(slug))
			}
			found[key("author", id)] = t
			return nil
		}); err != nil {
			return err
		}
	}
	for _, it := range items {
		if it.Kind == "url" || it.TargetID == nil {
			continue
		}
		if t, ok := found[key(it.Kind, *it.TargetID)]; ok {
			title := t.title
			it.TargetTitle, it.ResolvedURL = &title, t.url
		}
	}
	return nil
}

func (s *Service) locations(ctx context.Context) ([]Location, error) {
	m, err := s.manifest()
	if err != nil {
		return nil, err
	}
	rows, err := s.DB.Query(ctx, `SELECT location, menu_id FROM menu_locations`)
	if err != nil {
		return nil, err
	}
	assigned := map[string]int64{}
	var loc string
	var id int64
	if _, err := pgx.ForEachRow(rows, []any{&loc, &id}, func() error {
		assigned[loc] = id
		return nil
	}); err != nil {
		return nil, err
	}
	out := []Location{}
	for _, l := range m.MenuLocations {
		label := m.Strings[l+"_menu"]
		if label == "" {
			label = l
		}
		row := Location{Location: l, Label: label}
		if id, ok := assigned[l]; ok {
			row.MenuID = &id
		}
		out = append(out, row)
	}
	return out, nil
}

func (s *Service) ListMenus(ctx context.Context) (*Menus, error) {
	menus, err := s.loadMenus(ctx, 0)
	if err != nil {
		return nil, err
	}
	locs, err := s.locations(ctx)
	if err != nil {
		return nil, err
	}
	return &Menus{Menus: menus, Locations: locs}, nil
}

func (s *Service) GetMenu(ctx context.Context, id int64) (*Menu, error) {
	menus, err := s.loadMenus(ctx, id)
	if err != nil {
		return nil, err
	}
	if len(menus) == 0 {
		return nil, notFound("menu")
	}
	return menus[0], nil
}

func (s *Service) menuSlug(ctx context.Context, tx pgx.Tx, id int64, in *MenuInput, current string) error {
	in.Name = strings.TrimSpace(in.Name)
	if err := checkLen("name", in.Name, 1, 200); err != nil {
		return err
	}
	slug, err := pickSlug(in.Slug, current, in.Name, func(slug string) (bool, error) {
		var taken bool
		err := tx.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM menus WHERE slug = $1 AND id <> $2)`, slug, id).Scan(&taken)
		return taken, err
	})
	in.Slug = slug
	return err
}

func (s *Service) CreateMenu(ctx context.Context, in MenuInput, act Actor) (*Menu, error) {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err := s.menuSlug(ctx, tx, 0, &in, ""); err != nil {
		return nil, err
	}
	var id int64
	err = tx.QueryRow(ctx, `INSERT INTO menus (name, slug) VALUES ($1, $2) RETURNING id`, in.Name, in.Slug).Scan(&id)
	if isUnique(err) {
		return nil, conflict("slug", "the slug %q is already used", in.Slug)
	}
	if err != nil {
		return nil, err
	}
	if err := act.audit(ctx, tx, "menu.create", "menu", id, map[string]string{"name": in.Name, "slug": in.Slug}); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.GetMenu(ctx, id)
}

// checkItems validates a whole tree and returns the targets it names, by kind.
func checkItems(items []MenuItemInput) (map[string][]int64, error) {
	targets := map[string][]int64{}
	count := 0
	var walk func(items []MenuItemInput, depth int, path string) error
	walk = func(items []MenuItemInput, depth int, path string) error {
		if depth > maxMenuDepth {
			return badField(path, "menus nest at most %d levels deep", maxMenuDepth)
		}
		for i := range items {
			it := &items[i]
			p := fmt.Sprintf("%s.%d", path, i)
			if count++; count > maxMenuItems {
				return badField("items", "a menu holds at most %d items", maxMenuItems)
			}
			it.Label = strings.TrimSpace(it.Label)
			if err := checkLen(p+".label", it.Label, 1, 200); err != nil {
				return err
			}
			switch it.Kind {
			case "post", "term", "author":
				if it.TargetID == nil {
					return badField(p+".target_id", "pick what the item links to")
				}
				it.URL = nil
				targets[it.Kind] = append(targets[it.Kind], *it.TargetID)
			case "url":
				it.TargetID = nil
				if it.URL == nil || !menuURL(strings.TrimSpace(*it.URL)) {
					return badField(p+".url", "use an http(s) URL, or a path starting with /")
				}
				u := strings.TrimSpace(*it.URL)
				it.URL = &u
			default:
				return badField(p+".kind", "must be post, term, author or url")
			}
			if len(it.CSSClasses) > 10 {
				return badField(p+".css_classes", "at most 10 classes")
			}
			for _, c := range it.CSSClasses {
				if !classRe.MatchString(c) {
					return badField(p+".css_classes", "%q isn't a valid class name", c)
				}
			}
			if it.CSSClasses == nil {
				it.CSSClasses = []string{}
			}
			if err := walk(it.Children, depth+1, p+".children"); err != nil {
				return err
			}
		}
		return nil
	}
	return targets, walk(items, 1, "items")
}

func menuURL(u string) bool {
	switch {
	case u == "" || len(u) > 2000:
		return false
	case strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//"), strings.HasPrefix(u, "#"):
		return true
	}
	return httpURL(u)
}

var targetTables = map[string]string{
	"post":   `SELECT count(*) FROM posts WHERE id = ANY($1::bigint[]) AND status <> 'trash'`,
	"term":   `SELECT count(*) FROM terms WHERE id = ANY($1::bigint[])`,
	"author": `SELECT count(*) FROM authors WHERE id = ANY($1::bigint[])`,
}

func checkTargets(ctx context.Context, tx pgx.Tx, targets map[string][]int64) error {
	for kind, ids := range targets {
		uniq := map[int64]bool{}
		for _, id := range ids {
			uniq[id] = true
		}
		var n int
		if err := tx.QueryRow(ctx, targetTables[kind], ids).Scan(&n); err != nil {
			return err
		}
		if n != len(uniq) {
			return badField("items", "a %s an item links to doesn't exist", kind)
		}
	}
	return nil
}

func insertItems(ctx context.Context, tx pgx.Tx, menuID int64, parent *int64, items []MenuItemInput) error {
	for i, it := range items {
		var id int64
		if err := tx.QueryRow(ctx, `
INSERT INTO menu_items (menu_id, parent_id, position, label, kind, target_id, url, new_tab, css_classes)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9) RETURNING id`,
			menuID, parent, i, it.Label, it.Kind, it.TargetID, it.URL, it.NewTab, it.CSSClasses).Scan(&id); err != nil {
			return err
		}
		if err := insertItems(ctx, tx, menuID, &id, it.Children); err != nil {
			return err
		}
	}
	return nil
}

// SaveMenu replaces a menu's name, slug and whole item tree. A nested tree can't hold a cycle.
func (s *Service) SaveMenu(ctx context.Context, id int64, in MenuInput, act Actor) (*Menu, error) {
	targets, err := checkItems(in.Items)
	if err != nil {
		return nil, err
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var cur Menu
	err = tx.QueryRow(ctx, `SELECT id, name, slug FROM menus WHERE id = $1 FOR UPDATE`, id).Scan(&cur.ID, &cur.Name, &cur.Slug)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, notFound("menu")
	}
	if err != nil {
		return nil, err
	}
	if err := s.menuSlug(ctx, tx, id, &in, cur.Slug); err != nil {
		return nil, err
	}
	if err := checkTargets(ctx, tx, targets); err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE menus SET name = $2, slug = $3, updated_at = now() WHERE id = $1`, id, in.Name, in.Slug)
	if isUnique(err) {
		return nil, conflict("slug", "the slug %q is already used", in.Slug)
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM menu_items WHERE menu_id = $1`, id); err != nil {
		return nil, err
	}
	if err := insertItems(ctx, tx, id, nil, in.Items); err != nil {
		return nil, err
	}
	if err := act.audit(ctx, tx, "menu.update", "menu", id, in); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if err := s.Site.Invalidate(ctx, purgeAll); err != nil {
		return nil, err
	}
	return s.GetMenu(ctx, id)
}

func (s *Service) DeleteMenu(ctx context.Context, id int64, act Actor) error {
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var name string
	err = tx.QueryRow(ctx, `DELETE FROM menus WHERE id = $1 RETURNING name`, id).Scan(&name)
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound("menu")
	}
	if err != nil {
		return err
	}
	if err := act.audit(ctx, tx, "menu.delete", "menu", id, map[string]string{"name": name}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return err
	}
	return s.Site.Invalidate(ctx, purgeAll)
}

// SetLocations assigns menus to the active theme's locations; a null id clears one. Unlisted locations stay as they are.
func (s *Service) SetLocations(ctx context.Context, locs map[string]*int64, act Actor) (*Menus, error) {
	m, err := s.manifest()
	if err != nil {
		return nil, err
	}
	known := map[string]bool{}
	for _, l := range m.MenuLocations {
		known[l] = true
	}
	tx, err := s.DB.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	for loc, id := range locs {
		if !known[loc] {
			return nil, badField("locations."+loc, "the theme has no %q menu location", loc)
		}
		if id == nil {
			_, err = tx.Exec(ctx, `DELETE FROM menu_locations WHERE location = $1`, loc)
		} else {
			tag, e := tx.Exec(ctx, `
INSERT INTO menu_locations (location, menu_id) SELECT $1, id FROM menus WHERE id = $2
ON CONFLICT (location) DO UPDATE SET menu_id = excluded.menu_id, updated_at = now()`, loc, *id)
			err = e
			if err == nil && tag.RowsAffected() == 0 {
				return nil, badField("locations."+loc, "the menu doesn't exist")
			}
		}
		if err != nil {
			return nil, err
		}
	}
	if err := act.audit(ctx, tx, "menu.locations", "menu_location", 0, locs); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	if err := s.Site.Invalidate(ctx, purgeAll); err != nil {
		return nil, err
	}
	return s.ListMenus(ctx)
}
