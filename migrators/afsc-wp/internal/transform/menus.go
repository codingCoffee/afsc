package transform

import (
	"regexp"
	"sort"
	"strconv"
	"strings"

	"afsc/internal/sanitize"

	"afsc/migrators/afsc-wp/internal/phpser"
	"afsc/migrators/afsc-wp/internal/records"
)

var (
	menuSlugRe = regexp.MustCompile(`^[a-z0-9_-]{1,64}$`)
	cssClassRe = regexp.MustCompile(`^[A-Za-z_-][A-Za-z0-9_-]{0,63}$`)
	asciiSlug  = regexp.MustCompile(`[^a-z0-9_-]+`)
)

// MenuLocations reads theme_mods_{stylesheet}.nav_menu_locations: location → menu term_id.
func (s *state) menuLocations() map[string]int64 {
	mods := s.ds.OptionMap("theme_mods_" + s.ds.OptionString("stylesheet"))
	out := map[string]int64{}
	locs, _ := mods["nav_menu_locations"].(map[string]any)
	for loc, v := range locs {
		switch n := v.(type) {
		case int64:
			out[loc] = n
		case string:
			if id, err := strconv.ParseInt(n, 10, 64); err == nil {
				out[loc] = id
			}
		}
	}
	return out
}

func (s *state) menus() []*records.Menu {
	locs := s.menuLocations()
	byMenu := map[int64][]string{}
	for _, loc := range sortedKeys(locs) {
		if locs[loc] > 0 {
			byMenu[locs[loc]] = append(byMenu[locs[loc]], loc)
		}
	}
	slugs := map[string]bool{}
	var out []*records.Menu
	for _, t := range s.ds.Terms {
		if t.Taxonomy != "nav_menu" {
			continue
		}
		key := s.key("menu", t.TermID)
		slug := strings.Trim(asciiSlug.ReplaceAllString(strings.ToLower(t.Slug), "-"), "-")
		if len(slug) > 64 {
			slug = strings.TrimRight(slug[:64], "-")
		}
		if !menuSlugRe.MatchString(slug) {
			slug = "menu-" + strconv.FormatInt(t.TermID, 10)
		}
		slug = uniq(slug, slugs)
		if len(slug) > 64 {
			slug = "menu-" + strconv.FormatInt(t.TermID, 10)
		}
		m := &records.Menu{LegacyKey: key, Slug: slug, Name: strings.TrimSpace(t.Name), Items: []records.MenuItem{}}
		if m.Name == "" {
			m.Name = slug
		}
		if l := byMenu[t.TermID]; len(l) > 0 {
			m.Location = records.Ptr(l[0])
			if len(l) > 1 {
				s.rep.issue("menu " + key + " is assigned to several locations (" + strings.Join(l, ", ") + "); AFSC allows one per menu, kept " + l[0])
			}
		}
		m.Items = s.menuItems(t.TermTaxID)
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i].LegacyKey, out[j].LegacyKey) })
	return out
}

func (s *state) menuItems(tt int64) []records.MenuItem {
	type item struct {
		rec    records.MenuItem
		parent int64
		order  int
		id     int64
	}
	var items []*item
	for _, p := range s.ds.Posts {
		if p.Type != "nav_menu_item" || p.Status != "publish" || !containsID(p.TermTaxIDs, tt) {
			continue
		}
		key := s.key("menu_item", p.ID)
		m := p.Meta
		rec := records.MenuItem{LegacyKey: key, Label: strings.TrimSpace(p.Title), CSSClasses: []string{},
			NewTab: m.First("_menu_item_target") == "_blank"}
		objID, _ := strconv.ParseInt(m.First("_menu_item_object_id"), 10, 64)
		obj := m.First("_menu_item_object")
		switch m.First("_menu_item_type") {
		case "post_type":
			pk, ok := s.postKey[objID]
			if !ok {
				s.rep.skip("menu_items", "target post not imported", key)
				continue
			}
			rec.Kind, rec.Target = "post", records.Ptr(pk)
			if rec.Label == "" {
				rec.Label = s.ds.Post(objID).Title
			}
		case "taxonomy":
			var tk string
			for ttid, t := range s.termByID {
				if t.TermID == objID && t.Taxonomy == obj {
					tk = s.termKey[ttid]
					if rec.Label == "" {
						rec.Label = t.Name
					}
				}
			}
			if tk == "" {
				s.rep.skip("menu_items", "target term not imported", key)
				continue
			}
			rec.Kind, rec.Target = "term", records.Ptr(tk)
		case "custom":
			u, ok := s.rw.Rewrite(m.First("_menu_item_url"))
			if !ok || u == "" || !sanitize.SafeURL(u) {
				s.rep.skip("menu_items", "unusable custom url", key)
				continue
			}
			rec.Kind, rec.URL = "url", records.Ptr(u)
		default:
			s.rep.skip("menu_items", "item type "+m.First("_menu_item_type"), key)
			continue
		}
		if rec.Label == "" {
			rec.Label = "(untitled)"
		}
		if v, err := phpser.Decode(m.First("_menu_item_classes")); err == nil {
			if l, ok := v.([]any); ok {
				for _, c := range l {
					cs, _ := c.(string)
					for _, one := range strings.Fields(cs) {
						if cssClassRe.MatchString(one) {
							rec.CSSClasses = append(rec.CSSClasses, one)
						} else {
							s.rep.note("%s: css class %q dropped", key, one)
						}
					}
				}
			}
		}
		parent, _ := strconv.ParseInt(m.First("_menu_item_menu_item_parent"), 10, 64)
		items = append(items, &item{rec: rec, parent: parent, order: p.MenuOrder, id: p.ID})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].order != items[j].order {
			return items[i].order < items[j].order
		}
		return items[i].id < items[j].id
	})
	present := map[int64]bool{}
	for _, it := range items {
		present[it.id] = true
	}
	out := make([]records.MenuItem, 0, len(items))
	for i, it := range items {
		it.rec.Position = i
		if it.parent > 0 {
			if present[it.parent] {
				it.rec.Parent = records.Ptr(s.key("menu_item", it.parent))
			} else {
				s.rep.note("%s: parent item %d was skipped; item moved to the top level", it.rec.LegacyKey, it.parent)
			}
		}
		out = append(out, it.rec)
	}
	return out
}

func containsID(l []int64, id int64) bool {
	for _, x := range l {
		if x == id {
			return true
		}
	}
	return false
}
