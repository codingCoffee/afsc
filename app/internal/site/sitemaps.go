package site

import (
	"cmp"
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"afsc/internal/plugins"
	"afsc/internal/seo"
	"afsc/internal/theme"
)

const (
	kindSitemap      = "sitemap"
	sitemapIndexPath = "/sitemap_index.xml"
)

// sitemapTypes is the fixed Yoast layout, in index order.
var sitemapTypes = []string{"post", "page", "category", "post_tag", "author", "video"}

// sitemapRe matches page 1 without a number and pages 2 to 999999 with one, as Yoast names them.
var sitemapRe = regexp.MustCompile(`^/(post|page|category|post_tag|author|video)-sitemap([2-9]|[1-9][0-9]{1,5})?\.xml$`)

// sitemapGroup is the cache group shared by every page of a sitemap type.
func sitemapGroup(typ string) string { return "/" + typ + "-sitemap.xml" }

func sitemapPath(typ string, n int) string {
	if n <= 1 {
		return sitemapGroup(typ)
	}
	return "/" + typ + "-sitemap" + strconv.Itoa(n) + ".xml"
}

// seoFile matches sitemaps, robots.txt, llms.txt and the IndexNow key file. They are answered before redirects and
// content (§6.1 step 2). llms.txt and the key file only exist while switched on.
func (s *snapshot) seoFile(path string) (Result, bool) {
	text := Result{Status: http.StatusOK, Kind: kindText, File: path}
	seoSet := &s.settings.SEO
	key := seoSet.IndexNow.Key
	switch {
	case path == "/sitemap.xml":
		return Result{Status: http.StatusMovedPermanently, Location: sitemapIndexPath}, true
	case path == sitemapIndexPath:
		return Result{Status: http.StatusOK, Kind: kindSitemap, File: "index"}, true
	case path == robotsPath:
		return text, true
	case path == llmsPath:
		return text, seoSet.LLMsTxt.Mode != "off"
	case seoSet.IndexNow.Enabled && len(path) == len(key)+5 && path[0] == '/' && path[1:len(key)+1] == key &&
		strings.HasSuffix(path, ".txt"):
		return text, true
	}
	if !strings.Contains(path, "-sitemap") {
		return Result{}, false
	}
	if m := sitemapRe.FindStringSubmatch(path); m != nil {
		n, _ := strconv.Atoi(cmp.Or(m[2], "1"))
		return Result{Status: http.StatusOK, Kind: kindSitemap, File: m[1], Paged: n}, true
	}
	return Result{}, false
}

// smEntry is one sitemap URL. id is the post, term or author id; 0 for the home entry.
type smEntry struct {
	id        int64
	loc       string
	lastmod   time.Time
	published time.Time
	videos    []seo.Video
}

type seoFlags struct {
	noIndex   *bool
	canonical string
}

// excluded reports whether a URL is noindex or canonicalized to another URL.
func (s *snapshot) excluded(kind, loc string, f seoFlags) bool {
	if !seo.IndexableKind(kind, f.noIndex, &s.settings) {
		return true
	}
	c := seo.OverrideCanonical(s.base, f.canonical)
	return c != "" && c != loc
}

const (
	sqlSitemapPosts = `
SELECT p.id, coalesce(p.legacy_key, ''), p.type, p.slug, p.status, p.published_at, coalesce(p.parent_id, 0),
       CASE WHEN $2::bool THEN coalesce((SELECT pt.term_id FROM post_terms pt JOIN terms t ON t.id = pt.term_id
            WHERE pt.post_id = p.id AND t.taxonomy = 'category' ORDER BY pt.is_primary DESC, t.id LIMIT 1), 0) ELSE 0 END,
       p.updated_at, s.robots_noindex, coalesce(s.canonical, '')
FROM posts p LEFT JOIN seo s ON s.entity_type = 'post' AND s.entity_id = p.id
WHERE p.type = $1 AND p.status = 'published' AND p.published_at <= now()
ORDER BY p.published_at, p.id`
	sqlNewestPost = `
SELECT max(updated_at) FROM posts WHERE type = 'post' AND status = 'published' AND published_at <= now()`
	sqlSEOFlags     = `SELECT entity_id, robots_noindex, coalesce(canonical, '') FROM seo WHERE entity_type = $1`
	sqlTermsLastmod = `
SELECT pt.term_id, max(p.updated_at) FROM post_terms pt
JOIN terms t ON t.id = pt.term_id JOIN posts p ON p.id = pt.post_id
WHERE t.taxonomy = $1 AND p.type = 'post' AND p.status = 'published' AND p.published_at <= now()
GROUP BY pt.term_id`
	sqlAuthorsLastmod = `
SELECT pa.author_id, max(p.updated_at) FROM post_authors pa JOIN posts p ON p.id = pa.post_id
WHERE p.type = 'post' AND p.status = 'published' AND p.published_at <= now()
GROUP BY pa.author_id`
	sqlSitemapVideos = `
SELECT post_id, title, description, url, provider, external_id, thumbnail_url, coalesce(thumbnail_media_id, 0),
       coalesce(duration_seconds, 0), upload_date, family_friendly
FROM (SELECT DISTINCT ON (post_id, url) * FROM post_videos
      ORDER BY post_id, url, CASE source WHEN 'manual' THEN 0 WHEN 'import' THEN 1 ELSE 2 END) v
ORDER BY post_id, id`
	// Library images: the featured image, then image nodes in content order (gallery images are image nodes).
	sqlSitemapImages = `
SELECT p.id, coalesce(p.featured_media_id, 0),
       ARRAY(SELECT (m #>> '{}')::numeric::bigint FROM jsonb_path_query(p.content,
             'strict $.** ? (@.type == "image" && @.attrs.mediaId.type() == "number").attrs.mediaId', '{}', true) m)
FROM posts p WHERE p.id = ANY($1::bigint[])`
)

// postEntries lists indexable published posts or pages, oldest first. The page list starts with the home URL when
// the home page shows the latest posts.
func (st *Site) postEntries(ctx context.Context, snap *snapshot, typ string) ([]smEntry, error) {
	withCat := strings.Contains(snap.settings.Permalinks.PostPattern, "{category}")
	rows, err := st.db.Query(ctx, sqlSitemapPosts, typ, withCat)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	kind := theme.KindSingle
	if typ == "page" {
		kind = theme.KindPage
	}
	var out []smEntry
	for rows.Next() {
		var r PostRef
		var e smEntry
		var f seoFlags
		if err := rows.Scan(&r.ID, &r.LegacyKey, &r.Type, &r.Slug, &r.Status, &r.PublishedAt, &r.ParentID, &r.CategoryID,
			&e.lastmod, &f.noIndex, &f.canonical); err != nil {
			return nil, err
		}
		e.id, e.published, e.loc = r.ID, *r.PublishedAt, snap.abs(snap.postPath(&r))
		if !snap.excluded(kind, e.loc, f) {
			out = append(out, e)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if hp := snap.pages[snap.homeID]; typ == "page" && (hp == nil || !hp.Visible(st.now())) {
		var newest *time.Time
		if err := st.db.QueryRow(ctx, sqlNewestPost).Scan(&newest); err != nil {
			return nil, err
		}
		home := smEntry{loc: snap.abs("/")}
		if newest != nil {
			home.lastmod = *newest
		}
		out = slices.Insert(out, 0, home)
	}
	return out, nil
}

func (st *Site) seoFlags(ctx context.Context, entity string) (map[int64]seoFlags, error) {
	rows, err := st.db.Query(ctx, sqlSEOFlags, entity)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]seoFlags{}
	for rows.Next() {
		var id int64
		var f seoFlags
		if err := rows.Scan(&id, &f.noIndex, &f.canonical); err != nil {
			return nil, err
		}
		out[id] = f
	}
	return out, rows.Err()
}

func (st *Site) lastmods(ctx context.Context, sql string, args ...any) (map[int64]time.Time, error) {
	rows, err := st.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var t time.Time
		if err := rows.Scan(&id, &t); err != nil {
			return nil, err
		}
		out[id] = t
	}
	return out, rows.Err()
}

// termEntries lists the non-empty, indexable archives of a taxonomy by term id. A category's lastmod is the newest
// post in it or its descendants, as its archive lists them.
func (st *Site) termEntries(ctx context.Context, snap *snapshot, taxonomy string) ([]smEntry, error) {
	last, err := st.lastmods(ctx, sqlTermsLastmod, taxonomy)
	if err != nil {
		return nil, err
	}
	flags, err := st.seoFlags(ctx, "term")
	if err != nil {
		return nil, err
	}
	var ids []int64
	for id, t := range snap.terms {
		if t.Taxonomy == taxonomy {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	var out []smEntry
	for _, id := range ids {
		e := smEntry{id: id, loc: snap.abs(snap.termArchive(id))}
		members := []int64{id}
		if taxonomy == "category" {
			members = snap.termDescendants(id)
		}
		for _, m := range members {
			if t := last[m]; t.After(e.lastmod) {
				e.lastmod = t
			}
		}
		if !e.lastmod.IsZero() && !snap.excluded(taxonomy, e.loc, flags[id]) {
			out = append(out, e)
		}
	}
	return out, nil
}

// authorEntries lists the non-empty, indexable author archives by author id; none when author archives are off.
func (st *Site) authorEntries(ctx context.Context, snap *snapshot) ([]smEntry, error) {
	if !snap.settings.Permalinks.AuthorArchivesEnabled {
		return nil, nil
	}
	last, err := st.lastmods(ctx, sqlAuthorsLastmod)
	if err != nil {
		return nil, err
	}
	flags, err := st.seoFlags(ctx, "author")
	if err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(last))
	for id := range last {
		if snap.authors[id] != nil {
			ids = append(ids, id)
		}
	}
	slices.Sort(ids)
	var out []smEntry
	for _, id := range ids {
		e := smEntry{id: id, loc: snap.abs(snap.authorArchive(id)), lastmod: last[id]}
		if !snap.excluded(theme.KindAuthor, e.loc, flags[id]) {
			out = append(out, e)
		}
	}
	return out, nil
}

// videoEntries are the posts and pages from the given lists that have at least one video fit for the sitemap,
// oldest first.
func (st *Site) videoEntries(ctx context.Context, snap *snapshot, lists ...[]smEntry) ([]smEntry, error) {
	rows, err := st.db.Query(ctx, sqlSitemapVideos)
	if err != nil {
		return nil, err
	}
	byPost := map[int64][]*videoRow{}
	var thumbs []int64
	for rows.Next() {
		var postID int64
		v, err := scanVideo(rows, &postID)
		if err != nil {
			rows.Close()
			return nil, err
		}
		byPost[postID] = append(byPost[postID], v)
		thumbs = append(thumbs, v.thumbMedia)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	media, err := st.loadMedia(ctx, thumbs)
	if err != nil {
		return nil, err
	}
	var out []smEntry
	for _, list := range lists {
		for _, e := range list {
			for _, v := range byPost[e.id] {
				v.resolveThumb(snap, media)
				if seo.SitemapVideo(v.Video) {
					e.videos = append(e.videos, v.Video)
				}
			}
			if len(e.videos) > 0 {
				out = append(out, e)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b smEntry) int {
		return cmp.Or(a.published.Compare(b.published), cmp.Compare(a.id, b.id))
	})
	return out, nil
}

// images returns the library images of each post as absolute /m/ orig URLs, featured image first.
func (st *Site) images(ctx context.Context, snap *snapshot, ids []int64) (map[int64][]string, error) {
	rows, err := st.db.Query(ctx, sqlSitemapImages, ids)
	if err != nil {
		return nil, err
	}
	perPost := map[int64][]int64{}
	var all []int64
	for rows.Next() {
		var id, featured int64
		var content []int64
		if err := rows.Scan(&id, &featured, &content); err != nil {
			rows.Close()
			return nil, err
		}
		list := append([]int64{featured}, content...)
		perPost[id] = list
		all = append(all, list...)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	media, err := st.loadMedia(ctx, all)
	if err != nil {
		return nil, err
	}
	out := map[int64][]string{}
	for id, list := range perPost {
		seen := map[int64]bool{}
		for _, mid := range list {
			m, ok := media[mid]
			if !ok || seen[mid] || !strings.HasPrefix(m.Mime, "image/") {
				continue
			}
			seen[mid] = true
			out[id] = append(out[id], snap.mediaView(m).URL)
		}
	}
	return out, nil
}

// sitemapEntries lists every URL of one sitemap type in sitemap order.
func (st *Site) sitemapEntries(ctx context.Context, snap *snapshot, typ string) ([]smEntry, error) {
	switch typ {
	case "post", "page":
		return st.postEntries(ctx, snap, typ)
	case "category":
		return st.termEntries(ctx, snap, "category")
	case "post_tag":
		return st.termEntries(ctx, snap, "tag")
	case "author":
		return st.authorEntries(ctx, snap)
	}
	posts, err := st.postEntries(ctx, snap, "post")
	if err != nil {
		return nil, err
	}
	pages, err := st.postEntries(ctx, snap, "page")
	if err != nil {
		return nil, err
	}
	return st.videoEntries(ctx, snap, posts, pages)
}

// renderSitemap renders the index or one sitemap page. It returns nil for an empty type or a page past the end.
func (st *Site) renderSitemap(ctx context.Context, snap *snapshot, res Result) (*response, error) {
	size := snap.settings.SEO.Sitemaps.PageSize
	var body []byte
	if res.File == "index" {
		refs, err := st.sitemapIndex(ctx, snap, size)
		if err != nil {
			return nil, err
		}
		body = seo.Index(refs)
	} else {
		entries, err := st.sitemapEntries(ctx, snap, res.File)
		if err != nil {
			return nil, err
		}
		start := (res.Paged - 1) * size
		if res.Paged < 1 || start >= len(entries) {
			return nil, nil
		}
		entries = entries[start:min(len(entries), start+size)]
		urls := make([]seo.SitemapURL, len(entries))
		var ids []int64
		for i, e := range entries {
			urls[i] = seo.SitemapURL{Loc: e.loc, LastMod: e.lastmod, Videos: e.videos}
			if e.id != 0 && (res.File == "post" || res.File == "page") {
				ids = append(ids, e.id)
			}
		}
		if len(ids) > 0 {
			imgs, err := st.images(ctx, snap, ids)
			if err != nil {
				return nil, err
			}
			for i, e := range entries {
				urls[i].Images = imgs[e.id]
			}
		}
		urls = plugins.ApplyFilter(ctx, st.cfg.Plugins, plugins.SitemapEntries, urls,
			plugins.SitemapArgs{Type: res.File, Page: res.Paged})
		body = seo.URLSet(urls)
	}
	return &response{status: http.StatusOK, header: http.Header{"Content-Type": {"application/xml; charset=utf-8"}},
		body: body}, nil
}

// sitemapIndex lists every non-empty sitemap page with the newest lastmod on it.
func (st *Site) sitemapIndex(ctx context.Context, snap *snapshot, size int) ([]seo.SitemapRef, error) {
	posts, err := st.postEntries(ctx, snap, "post")
	if err != nil {
		return nil, err
	}
	pages, err := st.postEntries(ctx, snap, "page")
	if err != nil {
		return nil, err
	}
	lists := map[string][]smEntry{"post": posts, "page": pages}
	for _, typ := range []string{"category", "post_tag", "author"} {
		if lists[typ], err = st.sitemapEntries(ctx, snap, typ); err != nil {
			return nil, err
		}
	}
	if lists["video"], err = st.videoEntries(ctx, snap, posts, pages); err != nil {
		return nil, err
	}
	var refs []seo.SitemapRef
	for _, typ := range sitemapTypes {
		for n, chunk := range slices.Collect(slices.Chunk(lists[typ], size)) {
			ref := seo.SitemapRef{Loc: snap.abs(sitemapPath(typ, n+1))}
			for _, e := range chunk {
				if e.lastmod.After(ref.LastMod) {
					ref.LastMod = e.lastmod
				}
			}
			refs = append(refs, ref)
		}
	}
	return refs, nil
}
