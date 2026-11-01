package site

import (
	"cmp"
	"context"
	"errors"
	"html"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"

	"github.com/jackc/pgx/v5"

	"afsc/internal/content"
	"afsc/internal/seo"
	"afsc/internal/theme"
)

// ErrSEONotFound means the entity to resolve doesn't exist.
var ErrSEONotFound = errors.New("not found")

// SEODraft is the editor's unsaved post: it replaces the stored fields it names when resolving.
type SEODraft struct {
	Type              string // new posts only; a saved post keeps its type
	Title             string
	Slug              string // empty derives one from the title
	Excerpt           string
	Content           *content.Node // nil keeps the stored content
	FeaturedMediaID   int64
	CategoryIDs       []int64
	PrimaryCategoryID int64
	PublishedAt       *time.Time
	ParentID          int64
}

// SEOResolved is what {{head}} would print for the entity, plus what the admin's checks need.
type SEOResolved struct {
	Head      *theme.Head
	Unknown   []string
	Meta      map[string]string // first content of each <meta name|property> in Head.Tags
	URL       string
	Crumbs    []theme.Crumb
	Doc       *content.Node // posts only
	Slug      string
	PostTitle string
}

var metaTagRe = regexp.MustCompile(`<meta (?:name|property)="([^"]*)" content="([^"]*)">`)

// ResolveSEO resolves the head of a post, term, author or the latest-posts home ("home", id 0) the way the public
// site would, using f instead of the stored seo row and, for posts, draft over the stored post (id 0 is a new post).
func (st *Site) ResolveSEO(ctx context.Context, entity string, id int64, draft *SEODraft, f *seo.Fields) (*SEOResolved, error) {
	snap := st.snap.Load()
	s := snap.settings.Site
	d := &theme.PageData{Site: theme.Site{Title: s.Title, Tagline: s.Tagline, URL: snap.base, Language: s.Language},
		Request: theme.Request{Paged: 1}}
	out := &SEOResolved{}
	breadcrumb := ""
	if f.BreadcrumbTitle != nil {
		breadcrumb = *f.BreadcrumbTitle
	}
	var path string
	switch entity {
	case "post":
		p, doc, err := st.draftPost(ctx, snap, id, draft)
		if err != nil {
			return nil, err
		}
		if p == nil {
			return nil, ErrSEONotFound
		}
		d.Post, d.Kind, path = p.view, postKind(snap, &p.ref), snap.refPath(&p.ref)
		d.Post.Breadcrumb = breadcrumb
		out.Doc, out.Slug, out.PostTitle = doc, p.ref.Slug, p.view.Title
	case "term":
		if d.Term = snap.termView(id); d.Term == nil {
			return nil, ErrSEONotFound
		}
		d.Kind, path = theme.KindCategory, snap.termArchive(id)
		if d.Term.Taxonomy == "tag" {
			d.Kind = theme.KindTag
		}
		d.Term.Breadcrumb, out.Slug = breadcrumb, d.Term.Slug
	case "author":
		if d.Author = snap.authorView(id); d.Author == nil {
			return nil, ErrSEONotFound
		}
		d.Kind, path = theme.KindAuthor, snap.authorArchive(id)
		d.Author.Breadcrumb, out.Slug = breadcrumb, d.Author.Slug
	case "home":
		d.Kind, path = theme.KindHome, "/"
	default:
		return nil, ErrSEONotFound
	}
	d.Request.Path, d.Request.URL = path, snap.abs(path)
	out.URL = d.Request.URL

	ov := f.Override()
	ids := []int64{}
	if r := snap.settings.SEO.DefaultImage; r != nil {
		ids = append(ids, r.ID)
	}
	if r := snap.settings.SEO.KnowledgeGraph.Logo; r != nil {
		ids = append(ids, r.ID)
	}
	ogID, twID := int64(0), int64(0)
	if f.OGImageMediaID != nil {
		ogID = *f.OGImageMediaID
	}
	if f.TwitterImageMediaID != nil {
		twID = *f.TwitterImageMediaID
	}
	ids = append(ids, ogID, twID)
	var first int64
	var videos []*videoRow
	if d.Post != nil && d.Kind != theme.KindPostsPage {
		if d.Post.FeaturedMedia == nil && out.Doc != nil {
			first = content.FirstImageID(out.Doc)
			ids = append(ids, first)
		}
		if id > 0 {
			var err error
			if videos, err = st.postVideos(ctx, id); err != nil {
				return nil, err
			}
		}
		for _, v := range videos {
			ids = append(ids, v.thumbMedia)
		}
	}
	media, err := st.loadMedia(ctx, ids)
	if err != nil {
		return nil, err
	}
	view := func(id int64) *theme.Media {
		if m, ok := media[id]; ok && id != 0 {
			return snap.mediaView(m)
		}
		return nil
	}
	ov.OGImage, ov.TwitterImage = view(ogID), view(twID)
	in := seo.Input{Page: d, Settings: &snap.settings, Override: ov, Location: snap.loc, Now: st.now(), FirstImage: view(first)}
	if r := snap.settings.SEO.DefaultImage; r != nil {
		in.DefaultImage = view(r.ID)
	}
	if r := snap.settings.SEO.KnowledgeGraph.Logo; r != nil {
		in.Logo = view(r.ID)
	}
	for _, v := range videos {
		v.resolveThumb(snap, media)
		in.Videos = append(in.Videos, v.Video)
	}
	in.Crumbs = st.theme.Breadcrumbs(d)
	out.Crumbs = in.Crumbs
	out.Head, out.Unknown = seo.Build(in)
	out.Meta = map[string]string{}
	for _, m := range metaTagRe.FindAllStringSubmatch(string(out.Head.Tags), -1) {
		k := html.UnescapeString(m[1])
		if _, ok := out.Meta[k]; !ok {
			out.Meta[k] = html.UnescapeString(m[2])
		}
	}
	return out, nil
}

func postKind(snap *snapshot, r *PostRef) string {
	switch {
	case r.Type == "post":
		return theme.KindSingle
	case r.ID != 0 && r.ID == snap.homeID:
		return theme.KindFrontPage
	case r.ID != 0 && r.ID == snap.postsPageID:
		return theme.KindPostsPage
	}
	return theme.KindPage
}

// draftPost loads and hydrates post id with draft applied, and returns its document. nil means no such post.
func (st *Site) draftPost(ctx context.Context, snap *snapshot, id int64, dr *SEODraft) (*postRow, *content.Node, error) {
	var p *postRow
	if id > 0 {
		rows, err := st.postsByID(ctx, []int64{id})
		if err != nil || len(rows) == 0 {
			return nil, nil, err
		}
		p = rows[0]
	} else {
		if dr == nil {
			return nil, nil, nil
		}
		p = &postRow{view: &theme.Post{UpdatedAt: st.now(), CommentStatus: "open"}, ref: PostRef{Type: dr.Type}}
		if p.ref.Type != "page" {
			p.ref.Type = "post"
		}
	}
	if dr != nil {
		r := &p.ref
		r.Slug = cmp.Or(dr.Slug, draftSlug(dr.Title))
		r.PublishedAt, r.ParentID, r.CategoryID = dr.PublishedAt, dr.ParentID, draftPrimary(snap, dr)
		p.mediaID = dr.FeaturedMediaID
		v := p.view
		v.ID, v.Type, v.Slug, v.Title, v.Excerpt = r.ID, r.Type, r.Slug, dr.Title, dr.Excerpt
		v.PublishedAt = time.Time{}
		if r.PublishedAt != nil {
			v.PublishedAt = *r.PublishedAt
		}
	}
	if err := st.hydrate(ctx, snap, []*postRow{p}, hydrateOpts{}); err != nil {
		return nil, nil, err
	}
	doc := &content.Node{Type: "doc"}
	if dr != nil && dr.Content != nil {
		doc = dr.Content
		_, text, err := st.RenderContent(ctx, p.ref.ID, p.ref.LegacyKey, doc)
		if err != nil {
			text = seo.DocText(doc)
		}
		p.view.ContentText = text
	} else if len(p.doc) > 0 {
		parsed, err := content.Parse(p.doc)
		if err != nil {
			return nil, nil, err
		}
		doc = parsed
	}
	if dr == nil {
		return p, doc, nil
	}
	v := p.view
	v.Categories = nil
	for _, cid := range dr.CategoryIDs {
		if t := snap.termView(cid); t != nil && t.Taxonomy == "category" {
			v.Categories = append(v.Categories, t)
		}
	}
	slices.SortFunc(v.Categories, func(a, b *theme.Term) int { return cmp.Compare(a.Name, b.Name) })
	v.Primary = nil
	if p.ref.CategoryID != 0 {
		v.Primary = snap.termView(p.ref.CategoryID)
	}
	v.URL = snap.abs(snap.refPath(&p.ref))
	if p.ref.Type == "page" {
		v.Ancestors = nil
		for _, a := range snap.pageChain(p.ref.ParentID) {
			v.Ancestors = append(v.Ancestors, &theme.Post{ID: a.ID, WPID: wpID(a.ID, a.LegacyKey, "post"), Type: "page",
				Slug: a.Slug, Title: a.Title, URL: snap.abs(snap.pagePath(a.ID)), Breadcrumb: a.Breadcrumb})
		}
	}
	return p, doc, nil
}

// draftPrimary is the category permalinks and breadcrumbs use: the primary one, else the lowest id, as in refCols.
func draftPrimary(snap *snapshot, dr *SEODraft) int64 {
	var lowest int64
	for _, id := range dr.CategoryIDs {
		if t := snap.terms[id]; t == nil || t.Taxonomy != "category" {
			continue
		}
		if id == dr.PrimaryCategoryID {
			return id
		}
		if lowest == 0 || id < lowest {
			lowest = id
		}
	}
	return lowest
}

// draftSlug approximates the slug a save would derive from the title, for previewing an unsaved post's URL.
func draftSlug(title string) string {
	words := strings.FieldsFunc(strings.ToLower(title), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	return strings.Join(words, "-")
}

// LinkSuggestion is a published post the editor may want to link to.
type LinkSuggestion struct {
	ID    int64  `json:"id"`
	Title string `json:"title"`
	URL   string `json:"url"`
}

const sqlLinkSuggestions = `
WITH q AS (SELECT websearch_to_tsquery('english', $1) AS q)
SELECT p.id, p.title FROM posts p, q
WHERE p.status = 'published' AND p.published_at <= now() AND p.id <> $2 AND p.search @@ q.q
ORDER BY ts_rank_cd(p.search, q.q) DESC, p.published_at DESC, p.id DESC
LIMIT $3`

// LinkSuggestions ranks published posts by full-text match against any word of text, leaving out exclude.
func (st *Site) LinkSuggestions(ctx context.Context, text string, exclude int64, limit int) ([]LinkSuggestion, error) {
	words := strings.FieldsFunc(text, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	if len(words) > 32 {
		words = words[:32]
	}
	out := []LinkSuggestion{}
	if len(words) == 0 {
		return out, nil
	}
	rows, err := st.db.Query(ctx, sqlLinkSuggestions, strings.Join(words, " or "), exclude, limit)
	if err != nil {
		return nil, err
	}
	var found []LinkSuggestion
	var sug LinkSuggestion
	if _, err := pgx.ForEachRow(rows, []any{&sug.ID, &sug.Title}, func() error {
		found = append(found, sug)
		return nil
	}); err != nil {
		return nil, err
	}
	ids := make([]int64, len(found))
	for i, f := range found {
		ids[i] = f.ID
	}
	urls, err := st.VisibleURLs(ctx, ids)
	if err != nil {
		return nil, err
	}
	for _, f := range found {
		if u := urls[f.ID]; u != "" {
			f.URL = u
			out = append(out, f)
		}
	}
	return out, nil
}

// SEOSummary is a post's resolved SEO title and description, and its public URL ("" unless visible).
type SEOSummary struct {
	Title, Description, URL string
}

const sqlSEOTitles = `
SELECT entity_id, coalesce(title, ''), coalesce(description, '') FROM seo
WHERE entity_type = 'post' AND entity_id = ANY($1::bigint[])`

// SEOSummaries resolves the title and description of many posts at once, for the bulk editor.
func (st *Site) SEOSummaries(ctx context.Context, ids []int64) (map[int64]SEOSummary, error) {
	snap := st.snap.Load()
	out := map[int64]SEOSummary{}
	rows, err := st.postsByID(ctx, ids)
	if err != nil || len(rows) == 0 {
		return out, err
	}
	if err := st.hydrate(ctx, snap, rows, hydrateOpts{}); err != nil {
		return nil, err
	}
	ovs := map[int64]*seo.Override{}
	sr, err := st.db.Query(ctx, sqlSEOTitles, ids)
	if err != nil {
		return nil, err
	}
	var id int64
	var title, desc string
	if _, err := pgx.ForEachRow(sr, []any{&id, &title, &desc}, func() error {
		ovs[id] = &seo.Override{Title: title, Description: desc}
		return nil
	}); err != nil {
		return nil, err
	}
	s, now := snap.settings.Site, st.now()
	for _, p := range rows {
		path := snap.postPath(&p.ref)
		d := &theme.PageData{Kind: postKind(snap, &p.ref), Post: p.view,
			Site:    theme.Site{Title: s.Title, Tagline: s.Tagline, URL: snap.base, Language: s.Language},
			Request: theme.Request{Path: path, URL: snap.abs(path), Paged: 1}}
		h, _ := seo.Build(seo.Input{Page: d, Settings: &snap.settings, Override: ovs[p.ref.ID], Location: snap.loc, Now: now})
		sum := SEOSummary{Title: h.Title, Description: h.Description}
		if p.ref.Visible(now) {
			sum.URL = d.Request.URL
		}
		out[p.ref.ID] = sum
	}
	return out, nil
}

// SEOChange is the purge event after the seo rows or videos of posts changed. A changed video list also moves
// the video sitemap.
func (st *Site) SEOChange(ctx context.Context, ids []int64, videos bool) (Event, error) {
	states, err := st.postStates(ctx, ids)
	if err != nil {
		return Event{}, err
	}
	s := st.snap.Load()
	ev := Event{}
	for _, p := range states {
		p.Videos = videos
		ev = ev.Merge(s.postChange(nil, p))
	}
	return ev, nil
}
