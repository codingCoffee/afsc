package transform

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html"
	"path"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"afsc/internal/content"

	"afsc/migrators/afsc-wp/internal/convert"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/filter"
	"afsc/migrators/afsc-wp/internal/mediaidx"
	"afsc/migrators/afsc-wp/internal/permalink"
	"afsc/migrators/afsc-wp/internal/phpser"
	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/redirects"
	"afsc/migrators/afsc-wp/internal/shortcode"
	"afsc/migrators/afsc-wp/internal/wpseo"
)

func keyLess(a, b string) bool { return mediaidx.KeyLess(a, b) }

// planPosts assigns keys, slugs and primary categories before any path is computed.
func (s *state) planPosts() error {
	taken := map[string]bool{}
	for _, p := range s.ds.Posts {
		if !s.importable(p) {
			continue
		}
		s.postKey[p.ID] = s.key("post", p.ID)
	}
	for _, p := range s.ds.Posts {
		key, ok := s.postKey[p.ID]
		if !ok {
			continue
		}
		slug := p.Slug
		if !validSlug(slug) {
			fixed := Slugify(slug)
			if fixed == "" {
				fixed = Slugify(p.Title)
			}
			if fixed == "" {
				fixed = s.postType(p) + "-" + strconv.FormatInt(p.ID, 10)
			}
			if slug != "" {
				s.rep.note("%s: slug %q changed to %q", key, slug, fixed)
			}
			slug = fixed
		}
		scope := s.postType(p) + "\x00" + s.parentKey(p) + "\x00"
		if taken[scope+slug] {
			fixed := slug + "-" + strconv.FormatInt(p.ID, 10)
			s.rep.note("%s: slug %q is taken; using %q", key, slug, fixed)
			slug = fixed
		}
		taken[scope+slug] = true
		s.slugOf[-p.ID] = slug
		s.primary[p.ID] = s.primaryCategory(p)
	}
	return nil
}

func (s *state) postSlug(id int64) string { return s.slugOf[-id] }

func (s *state) parentKey(p *extract.Post) string {
	if s.postType(p) != "page" || p.Parent == 0 {
		return ""
	}
	if par := s.ds.Post(p.Parent); par != nil && s.postType(par) == "page" {
		return s.postKey[p.Parent]
	}
	return ""
}

// primaryCategory is Yoast's primary category, else the lowest-id category (WordPress's %category% choice).
func (s *state) primaryCategory(p *extract.Post) int64 {
	var cats []int64
	for _, tt := range p.TermTaxIDs {
		if t := s.termByID[tt]; t != nil && t.Taxonomy == "category" {
			cats = append(cats, tt)
		}
	}
	if s.set.On("yoast") {
		if id, err := strconv.ParseInt(p.Meta.First(s.set.Key("yoast", "primary_category")), 10, 64); err == nil {
			for _, tt := range cats {
				if s.termByID[tt].TermID == id {
					return tt
				}
			}
		}
	}
	best := int64(0)
	for _, tt := range cats {
		if best == 0 || s.termByID[tt].TermID < s.termByID[best].TermID {
			best = tt
		}
	}
	return best
}

func (s *state) published(p *extract.Post) bool { return mapStatus(p) == "published" }

func (s *state) date(p *extract.Post) time.Time {
	t, _ := extract.UTC(p.DateGMT, p.Date, s.loc)
	return t
}

func (s *state) pageChain(p *extract.Post, raw bool) []string {
	var segs []string
	seen := map[int64]bool{}
	for cur := p; cur != nil && !seen[cur.ID]; {
		seen[cur.ID] = true
		seg := s.postSlug(cur.ID)
		if raw {
			seg = cur.Name
		}
		segs = append([]string{seg}, segs...)
		if cur.Parent == 0 || s.parentKey(cur) == "" {
			break
		}
		cur = s.ds.Post(cur.Parent)
	}
	return segs
}

func (s *state) wpPostPath(p *extract.Post, slug string, local time.Time) string {
	if s.postType(p) == "page" {
		chain := s.pageChain(p, true)
		chain[len(chain)-1] = slug
		return s.wp.Page(p.ID, chain)
	}
	nicename := ""
	for _, u := range s.ds.Users {
		if u.ID == p.Author {
			nicename = u.Nicename
		}
	}
	return s.wp.Post(permalink.WPPost{ID: p.ID, Slug: slug, Local: local, Category: s.termRawPath(s.primary[p.ID]), Author: nicename})
}

func decodePath(p string) string {
	if strings.HasPrefix(p, "/?") {
		return p
	}
	return redirects.Canonical(p, "keep")
}

// paths computes AFSC and WordPress URLs of everything public, the path map for links and the live set.
func (s *state) paths() error {
	perm := s.settings.All.Permalinks
	a, err := permalink.NewAFSC(perm, s.loc)
	if err != nil {
		return err
	}
	s.afsc = a
	frontPage := ""
	if h := s.settings.All.Reading.Home; h != nil {
		frontPage = h.LegacyKey
	}
	s.live["/"] = true
	months := map[[2]int]bool{}
	termUsed := map[int64]bool{}
	authorUsed := map[int64]bool{}
	for _, p := range s.ds.Posts {
		key, ok := s.postKey[p.ID]
		if !ok || !s.published(p) {
			continue
		}
		d := s.date(p)
		var ap string
		if s.postType(p) == "page" {
			ap = a.Page(s.pageChain(p, false))
		} else {
			ap = a.Post(p.ID, s.postSlug(p.ID), d, s.termSlugPath(s.primary[p.ID]))
			l := d.In(s.loc)
			months[[2]int{l.Year(), int(l.Month())}] = true
			for _, tt := range p.TermTaxIDs {
				termUsed[tt] = true
			}
			for _, k := range s.postAuthors(p) {
				for id, ak := range s.authorKey {
					if ak == k {
						authorUsed[id] = true
					}
				}
			}
		}
		if key == frontPage {
			ap = "/"
		}
		s.postPath[p.ID] = ap
		s.live[redirects.Canonical(ap, perm.TrailingSlash)] = true
		wpp := s.wpPostPath(p, p.Name, d.In(s.loc))
		s.wpPath[p.ID] = wpp
		if key == frontPage {
			s.wpPath[p.ID] = "/"
		}
		if dp := decodePath(s.wpPath[p.ID]); dp != redirects.Canonical(ap, "keep") && !strings.HasPrefix(dp, "/?") {
			s.pathMap[dp] = ap
		}
	}
	for tt := range s.termKey {
		t := s.termByID[tt]
		ap := a.Term(s.termTax[tt], s.termSlugPath(tt))
		s.termPath[tt] = ap
		if termUsed[tt] {
			s.live[redirects.Canonical(ap, perm.TrailingSlash)] = true
		}
		if wpp := decodePath(s.wp.Term(t.Taxonomy, s.termRawPath(tt))); wpp != redirects.Canonical(ap, "keep") {
			s.pathMap[wpp] = ap
		}
	}
	for id := range s.authorKey {
		for _, au := range s.ds.Users {
			if au.ID == id {
				slug := extract.DecodeSlug(au.Nicename)
				if !validSlug(slug) {
					slug = Slugify(slug)
				}
				s.authorPath[id] = a.Author(slug)
			}
		}
		if authorUsed[id] && perm.AuthorArchivesEnabled {
			s.live[redirects.Canonical(s.authorPath[id], perm.TrailingSlash)] = true
		}
	}
	if perm.DateArchivesEnabled {
		for ym := range months {
			s.live[redirects.Canonical(a.Year(ym[0]), perm.TrailingSlash)] = true
			s.live[redirects.Canonical(a.Month(ym[0], ym[1]), perm.TrailingSlash)] = true
		}
	}
	return nil
}

func (s *state) resolveQuery(param string, id int64) (string, bool) {
	switch param {
	case "p", "page_id":
		p, ok := s.postPath[id]
		return p, ok
	case "attachment_id":
		return s.attachmentTarget(id)
	case "cat", "tag_id":
		for tt, t := range s.termByID {
			if t.TermID == id {
				if p, ok := s.termPath[tt]; ok {
					return p, true
				}
			}
		}
	case "author":
		p, ok := s.authorPath[id]
		return p, ok
	}
	return "", false
}

// attachmentTarget is where an attachment page goes: the file (Yoast disable-attachment, or unattached),
// otherwise the parent post.
func (s *state) attachmentTarget(id int64) (string, bool) {
	key, ok := s.media.Attachment(id)
	if !ok {
		return "", false
	}
	file := s.media.OrigPath(key)
	if p := s.ds.Post(id); p != nil && !s.settings.DisableAttachment {
		if pp, ok := s.postPath[p.Parent]; ok {
			return pp, true
		}
	}
	return file, file != ""
}

// shortcodeTags are the tags parsed as shortcodes: every confirmed shortcode decision except keep_text.
func (s *state) shortcodeTags() []string {
	var tags []string
	for _, it := range s.dec.Items {
		if it.Kind == "shortcode" && !it.Stale && it.Decision != "keep_text" {
			tags = append(tags, it.Name)
		}
	}
	slices.Sort(tags)
	return tags
}

func (s *state) filter(ctx context.Context) (map[string]string, error) {
	req := filter.Request{Shortcodes: s.shortcodeTags()}
	for _, p := range s.ds.Posts {
		if _, ok := s.postKey[p.ID]; ok && strings.TrimSpace(p.Content) != "" {
			req.Items = append(req.Items, filter.Item{ID: strconv.FormatInt(p.ID, 10), Content: p.Content})
		}
	}
	if len(req.Items) == 0 {
		return map[string]string{}, nil
	}
	return s.o.Filter.Run(ctx, req)
}

func phpID(postID string, raw string) string {
	h := sha1.Sum([]byte(raw))
	return postID + "#" + hex.EncodeToString(h[:])[:12]
}

// renderPHP collects every render_php shortcode and renders each tag in one batch through wp-filter.php.
func (s *state) renderPHP(ctx context.Context, filtered map[string]string) (map[string]string, error) {
	tags := map[string]bool{}
	for _, t := range s.shortcodeTags() {
		tags[t] = true
	}
	byTag := map[string][]filter.Item{}
	seen := map[string]bool{}
	var walk func(id, src string)
	walk = func(id, src string) {
		for _, seg := range shortcode.Parse(src, tags) {
			if seg.SC == nil {
				continue
			}
			if s.decide("shortcode", seg.SC.Tag, "") == "render_php" {
				iid := phpID(id, seg.SC.Raw)
				if !seen[seg.SC.Tag+iid] {
					seen[seg.SC.Tag+iid] = true
					byTag[seg.SC.Tag] = append(byTag[seg.SC.Tag], filter.Item{ID: iid, Content: seg.SC.Raw})
				}
			} else if seg.SC.Enclosing {
				walk(id, seg.SC.Content)
			}
		}
	}
	for _, id := range sortedKeys(filtered) {
		walk(id, filtered[id])
	}
	out := map[string]string{}
	for _, tag := range sortedKeys(byTag) {
		it := s.dec.Get("shortcode", tag)
		got, err := s.o.Filter.Run(ctx, filter.Request{Tag: tag, Includes: it.OptionList("include"), Items: byTag[tag]})
		if err != nil {
			return nil, fmt.Errorf("render [%s] with PHP: %w", tag, err)
		}
		for _, item := range byTag[tag] {
			out[tag+"|"+item.ID] = got[item.ID]
		}
	}
	return out, nil
}

// oembedLookup reads the post's _oembed_* cache: which provider URLs WordPress embedded or marked unknown.
func oembedLookup(p *extract.Post) func(string) (string, string) {
	embedded := map[string]string{}
	unknown := false
	for k, vals := range p.Meta {
		if !strings.HasPrefix(k, "_oembed_") || strings.HasPrefix(k, "_oembed_time_") {
			continue
		}
		for _, v := range vals {
			if v == "{{unknown}}" {
				unknown = true
				continue
			}
			for _, m := range urlInHTML.FindAllString(v, -1) {
				if e, ok := convert.EmbedFromURL(html.UnescapeString(m)); ok {
					embedded[e.Attrs["url"].(string)] = v
				}
			}
		}
	}
	return func(u string) (string, string) {
		if e, ok := convert.EmbedFromURL(u); ok {
			if h, ok := embedded[e.Attrs["url"].(string)]; ok {
				return "embedded", h
			}
		}
		if unknown {
			return "unknown", ""
		}
		return "", ""
	}
}

var urlInHTML = regexp.MustCompile(`https?://[^\s"'<>]+`)

func docJSON(doc *content.Node) (json.RawMessage, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(doc); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimSpace(b.Bytes())), nil
}

var secretMetaRe = regexp.MustCompile(`(?i)(pass|secret|token|api[_-]?key|private|credential|license)`)

func plainExcerpt(s string) string {
	return strings.Join(strings.Fields(html.UnescapeString(tagRe.ReplaceAllString(s, " "))), " ")
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func (s *state) templateName(p *extract.Post) *string {
	t := strings.TrimSpace(p.Meta.First(s.set.Key("core", "page_template")))
	if t == "" || t == "default" {
		return nil
	}
	if v, ok := s.cfg.Site.PageTemplates[t]; ok {
		if v == "" {
			return nil
		}
		return records.Ptr(v)
	}
	name := strings.TrimSuffix(path.Base(t), ".php")
	name = strings.Trim(nonTemplate.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if len(name) > 64 {
		name = name[:64]
	}
	if name == "" {
		return nil
	}
	return records.Ptr(name)
}

var nonTemplate = regexp.MustCompile(`[^a-z0-9_-]+`)

func (s *state) stickies() map[int64]bool {
	out := map[int64]bool{}
	if l, ok := s.ds.Option("sticky_posts").([]any); ok {
		for _, v := range l {
			if n, ok := v.(int64); ok {
				out[n] = true
			} else if str, ok := v.(string); ok {
				if n, err := strconv.ParseInt(str, 10, 64); err == nil {
					out[n] = true
				}
			}
		}
	}
	return out
}

func (s *state) posts(filtered, rendered map[string]string, media []*records.Media) ([]*records.Post, []*records.Legacy, map[int64]int, error) {
	pages := map[int64]int{}
	sticky := s.stickies()
	chk := newChecker(s, media)
	var out []*records.Post
	var legacy []*records.Legacy
	var invalid []string
	tags := map[string]bool{}
	for _, t := range s.shortcodeTags() {
		tags[t] = true
	}
	for _, p := range s.ds.Posts {
		key, ok := s.postKey[p.ID]
		if !ok {
			if p.Type != "attachment" && p.Type != "nav_menu_item" {
				reason := "status " + p.Status
				if s.postType(p) == "" {
					reason = "type " + p.Type
				}
				s.rep.skip("posts", reason, s.key("post", p.ID))
			}
			continue
		}
		id := strconv.FormatInt(p.ID, 10)
		ctx := &convert.Context{
			PostID: p.ID, Media: s.media, Links: s.rw, AllowedClasses: s.allowed, Decide: s.decide, Shortcodes: tags,
			RenderPHP: func(tag, raw string) (string, bool) {
				h, ok := rendered[tag+"|"+phpID(id, raw)]
				return h, ok
			},
			OEmbed: oembedLookup(p), SiteURL: s.cfg.Site.BaseURL,
		}
		res, err := convert.Convert(filtered[id], ctx)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("%s: %w", key, err)
		}
		if errs := chk.validate(res.Doc); len(errs) > 0 {
			for _, e := range errs {
				invalid = append(invalid, key+": "+e)
			}
			continue
		}
		for _, f := range res.Flags {
			s.rep.flag(key, f.Kind, f.Detail)
		}
		if res.Pages > 1 {
			pages[p.ID] = res.Pages
		}
		raw, err := docJSON(res.Doc)
		if err != nil {
			return nil, nil, nil, err
		}
		rec := s.postRecord(p, key, raw, sticky)
		out = append(out, rec)
		usesRaw := false
		for _, f := range res.Flags {
			usesRaw = usesRaw || f.Kind == "raw_html" || f.Kind == "shortcode_rendered"
		}
		s.pathRecs = append(s.pathRecs, PathRec{Key: key, Type: rec.Type, Status: rec.Status, WPPath: s.wpPath[p.ID],
			AFSCPath: s.postPath[p.ID], RawHTML: usesRaw})
		if s.published(p) {
			s.reference[id] = chk.ExpandShortcodes(filtered[id], p.ID, rendered)
			s.rep.Fidelity = append(s.rep.Fidelity, chk.fidelity(key, p.ID, filtered[id], res, rendered))
		}
		legacy = append(legacy, s.legacyRecord(p, key))
	}
	if len(invalid) > 0 {
		if len(invalid) > 50 {
			invalid = append(invalid[:50], fmt.Sprintf("… %d more", len(invalid)-50))
		}
		return nil, nil, nil, fmt.Errorf("converted content fails AFSC's validator (a converter bug; please report):\n  %s", strings.Join(invalid, "\n  "))
	}
	return out, legacy, pages, nil
}

func (s *state) postRecord(p *extract.Post, key string, content json.RawMessage, sticky map[int64]bool) *records.Post {
	created := s.date(p)
	updated, _ := extract.UTC(p.ModifiedGMT, p.Modified, s.loc)
	if created.IsZero() {
		created = updated
	}
	if created.IsZero() {
		created = s.generatedAt()
		s.rep.note("%s has no usable date; created_at set to generated_at", key)
	}
	rec := &records.Post{
		LegacyKey: key, Type: s.postType(p), Status: mapStatus(p), Slug: s.postSlug(p.ID), Title: p.Title,
		Excerpt: plainExcerpt(p.Excerpt), Content: content, CreatedAt: &created, MenuOrder: p.MenuOrder,
		CommentStatus: "open", Sticky: sticky[p.ID], Authors: s.postAuthors(p), Template: s.templateName(p),
	}
	if !updated.IsZero() && !updated.Equal(created) {
		rec.UpdatedAt = &updated
	}
	if p.CommentStatus == "closed" {
		rec.CommentStatus = "closed"
	}
	switch rec.Status {
	case "published", "scheduled", "private":
		rec.PublishedAt = &created
	}
	if p.Password != "" {
		s.rep.flag(key, "password_protected", "imported as private")
	}
	if pk := s.parentKey(p); pk != "" {
		rec.Parent = records.Ptr(pk)
	}
	if p.GUID != "" {
		rec.FeedGUID = records.Ptr(p.GUID)
	}
	if id := p.Meta.First(s.set.Key("core", "thumbnail")); id != "" && id != "0" && id != "-1" {
		if k := s.resolveImage(id, ""); k != nil {
			rec.FeaturedMedia = k
		} else {
			s.rep.flag(key, "missing_media", "featured image attachment "+id)
		}
	}
	for _, tt := range p.TermTaxIDs {
		tk, ok := s.termKey[tt]
		if !ok {
			continue
		}
		pt := records.PostTerm{Term: tk}
		if tt == s.primary[p.ID] && s.termTax[tt] == "category" {
			pt.Primary = true
		}
		rec.Terms = append(rec.Terms, pt)
	}
	if s.set.On("yoast") {
		rec.SEO = s.postSEO(p, key)
	}
	if s.settings.TypeNoindex[rec.Type] {
		if rec.SEO == nil {
			rec.SEO = &records.SEO{}
		}
		if rec.SEO.RobotsNoindex == nil {
			rec.SEO.RobotsNoindex = records.Ptr(true)
		}
	}
	if s.set.On("yoast_video") {
		rec.Videos = s.videos(p, key)
	}
	return rec
}

func (s *state) postSEO(p *extract.Post, key string) *records.SEO {
	get := func(f string) string { return strings.TrimSpace(p.Meta.First(s.set.Key("yoast", f))) }
	seo := wpseo.Post(get, s.resolveImage)
	values := map[string]string{"focuskw": get("focus_keyphrase"), "id": strconv.FormatInt(p.ID, 10)}
	if par := s.ds.Post(p.Parent); par != nil {
		values["parent_title"] = par.Title
	}
	if d := s.date(p).In(s.loc); !d.IsZero() {
		values["post_year"], values["post_month"], values["post_day"] = d.Format("2006"), d.Format("01"), d.Format("02")
	}
	for _, f := range []**string{&seo.Title, &seo.Description, &seo.OGTitle, &seo.OGDescription, &seo.TwitterTitle, &seo.TwitterDescription} {
		if *f != nil {
			v := wpseo.Apply(**f, s.variable, values)
			*f = &v
		}
	}
	if seo.Canonical != nil {
		c, ok := s.rw.Rewrite(*seo.Canonical)
		own := s.postPath[p.ID]
		if !ok || (own != "" && redirects.Canonical(c, "keep") == redirects.Canonical(own, "keep")) {
			seo.Canonical = nil
		} else {
			seo.Canonical = &c
		}
	}
	if (get("og_image_id") != "" || get("og_image") != "") && seo.OGImageMedia == nil {
		s.rep.flag(key, "missing_media", "og:image not in the media library")
	}
	if seo.Empty() {
		return nil
	}
	return seo
}

func (s *state) videos(p *extract.Post, key string) []records.Video {
	if v := p.Meta.First(s.set.Key("yoast_video", "disable")); v == "on" || v == "1" {
		return nil
	}
	raw := p.Meta.First(s.set.Key("yoast_video", "video_meta"))
	if raw == "" {
		return nil
	}
	v, err := phpser.Decode(raw)
	if err != nil {
		s.rep.flag(key, "video_meta", "unreadable: "+err.Error())
		return nil
	}
	var list []map[string]any
	switch x := v.(type) {
	case map[string]any:
		list = append(list, x)
	case []any:
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				list = append(list, m)
			}
		}
	}
	embed := func(u string) (string, string, string, bool) {
		e, ok := convert.EmbedFromURL(u)
		if !ok {
			return "", "", "", false
		}
		id, _ := e.Attrs["externalId"].(string)
		return e.Attrs["provider"].(string), e.Attrs["url"].(string), id, true
	}
	var out []records.Video
	seen := map[string]bool{}
	nf := p.Meta.First(s.set.Key("yoast_video", "not_family_friendly"))
	for _, m := range list {
		vid, ok := wpseo.Video(m, embed, s.resolveImage)
		if !ok || seen[vid.URL] {
			continue
		}
		seen[vid.URL] = true
		if nf == "on" || nf == "1" || nf == "true" {
			vid.FamilyFriendly = records.Ptr(false)
		}
		out = append(out, vid)
	}
	return out
}

func (s *state) legacyRecord(p *extract.Post, key string) *records.Legacy {
	meta := map[string]any{}
	for k, v := range p.Meta {
		if secretMetaRe.MatchString(k) {
			continue
		}
		meta[k] = v
	}
	return &records.Legacy{LegacyKey: key, Kind: "post", Source: map[string]any{
		"post_content": p.Content, "post_excerpt": p.Excerpt, "post_title": p.Title, "post_status": p.Status,
		"post_name": p.Name, "post_type": p.Type, "guid": p.GUID, "post_parent": p.Parent, "menu_order": p.MenuOrder,
		"post_date": p.Date, "post_date_gmt": p.DateGMT, "postmeta": meta,
	}}
}
