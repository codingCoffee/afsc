package transform

import (
	"sort"
	"strconv"
	"strings"

	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/wpseo"
)

func (s *state) taxonomyOf(tax string) string {
	switch tax {
	case "category":
		return "category"
	case "post_tag":
		return "tag"
	case "nav_menu":
		return ""
	}
	return map[string]string{"category": "category", "tag": "tag"}[s.decide("taxonomy", tax, "drop")]
}

func (s *state) terms() []*records.Term {
	var out []*records.Term
	usedKey := map[string]bool{}
	taxMeta := s.ds.OptionMap(s.set.Key("yoast", "opt_taxonomy_meta"))
	byTermID := map[int64][]int64{}
	for _, t := range s.ds.Terms {
		s.termByID[t.TermTaxID] = t
		byTermID[t.TermID] = append(byTermID[t.TermID], t.TermTaxID)
	}
	for _, t := range s.ds.Terms {
		tax := s.taxonomyOf(t.Taxonomy)
		if tax == "" {
			if t.Taxonomy != "nav_menu" {
				s.rep.skip("terms", "taxonomy "+t.Taxonomy+" dropped", s.key("term", t.TermID))
			}
			continue
		}
		key := s.key("term", t.TermID)
		if usedKey[key] {
			key += "-" + t.Taxonomy
			s.rep.note("term %d is shared by several taxonomies; %s keyed %s", t.TermID, t.Taxonomy, key)
		}
		usedKey[key] = true
		s.termKey[t.TermTaxID] = key
		s.termTax[t.TermTaxID] = tax
		slug := t.Slug
		if !validSlug(slug) {
			fixed := Slugify(slug)
			if fixed == "" {
				fixed = "term-" + strconv.FormatInt(t.TermID, 10)
			}
			s.rep.note("%s: slug %q changed to %q", key, slug, fixed)
			slug = fixed
		}
		s.slugOf[t.TermTaxID] = slug
		rec := &records.Term{LegacyKey: key, Taxonomy: tax, Slug: slug, Name: strings.TrimSpace(t.Name), Description: t.Description}
		if rec.Name == "" {
			rec.Name = slug
		}
		if s.set.On("yoast") && taxMeta != nil {
			if byTax, ok := taxMeta[t.Taxonomy].(map[string]any); ok {
				if m, ok := byTax[strconv.FormatInt(t.TermID, 10)].(map[string]any); ok {
					seo := wpseo.Term(m, s.resolveImage)
					if seo.Title != nil {
						seo.Title = records.Ptr(wpseo.Apply(*seo.Title, s.variable, nil))
					}
					if seo.Description != nil {
						seo.Description = records.Ptr(wpseo.Apply(*seo.Description, s.variable, nil))
					}
					if !seo.Empty() {
						rec.SEO = seo
					}
				}
			}
		}
		out = append(out, rec)
	}
	// Parents point at term_ids; resolve them to the parent's key in the same taxonomy.
	byKey := map[string]*records.Term{}
	for _, r := range out {
		byKey[r.LegacyKey] = r
	}
	for _, t := range s.ds.Terms {
		key, ok := s.termKey[t.TermTaxID]
		if !ok || t.Parent == 0 || s.termTax[t.TermTaxID] != "category" {
			continue
		}
		for _, ptt := range byTermID[t.Parent] {
			if pk, ok := s.termKey[ptt]; ok && s.termTax[ptt] == "category" {
				byKey[key].Parent = records.Ptr(pk)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return keyLess(out[i].LegacyKey, out[j].LegacyKey) })
	return out
}

// termRawPath and termSlugPath walk category parents: raw slugs for WordPress URLs, decoded for AFSC.
func (s *state) termChain(tt int64) []int64 {
	var chain []int64
	seen := map[int64]bool{}
	for cur := tt; cur != 0 && !seen[cur]; {
		seen[cur] = true
		chain = append([]int64{cur}, chain...)
		t := s.termByID[cur]
		if t == nil || t.Parent == 0 {
			break
		}
		next := int64(0)
		for _, cand := range s.ds.Terms {
			if cand.TermID == t.Parent && cand.Taxonomy == t.Taxonomy {
				next = cand.TermTaxID
			}
		}
		cur = next
	}
	return chain
}

func (s *state) termRawPath(tt int64) string {
	var parts []string
	for _, id := range s.termChain(tt) {
		parts = append(parts, s.termByID[id].RawSlug)
	}
	return strings.Join(parts, "/")
}

func (s *state) termSlugPath(tt int64) string {
	var parts []string
	for _, id := range s.termChain(tt) {
		parts = append(parts, s.slugOf[id])
	}
	return strings.Join(parts, "/")
}

// applyTypeDefaults copies Yoast's per-type noindex onto entities AFSC has no type setting for.
func (s *state) applyTypeDefaults(terms []*records.Term, authors []*records.Author) {
	if s.settings.TypeNoindex["tax-category"] {
		for _, t := range terms {
			if t.Taxonomy != "category" {
				continue
			}
			if t.SEO == nil {
				t.SEO = &records.SEO{}
			}
			if t.SEO.RobotsNoindex == nil {
				t.SEO.RobotsNoindex = records.Ptr(true)
			}
		}
	}
	if s.settings.TypeNoindex["author-wpseo"] {
		for _, a := range authors {
			if a.SEO == nil {
				a.SEO = &records.SEO{}
			}
			if a.SEO.RobotsNoindex == nil {
				a.SEO.RobotsNoindex = records.Ptr(true)
			}
		}
	}
	if s.settings.TypeNoindex["archive-wpseo"] {
		s.rep.issue("Yoast noindexes date archives; AFSC has no date-archive noindex setting")
	}
}
