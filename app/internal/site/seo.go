package site

import (
	"context"
	"errors"
	"strconv"

	"github.com/jackc/pgx/v5"

	"afsc/internal/content"
	"afsc/internal/seo"
	"afsc/internal/theme"
)

const (
	sqlSEORow = `
SELECT coalesce(title, ''), coalesce(description, ''), coalesce(canonical, ''), robots_noindex, robots_nofollow,
       coalesce(robots_advanced, '{}'), coalesce(og_title, ''), coalesce(og_description, ''), coalesce(og_image_media_id, 0),
       coalesce(twitter_title, ''), coalesce(twitter_description, ''), coalesce(twitter_image_media_id, 0),
       coalesce(breadcrumb_title, '')
FROM seo WHERE entity_type = $1 AND entity_id = $2`
	// One row per video URL; manual rows win over imported ones, which win over auto-detected ones.
	sqlPostVideos = `
SELECT title, description, url, provider, external_id, thumbnail_url, coalesce(thumbnail_media_id, 0),
       coalesce(duration_seconds, 0), upload_date, family_friendly
FROM (SELECT DISTINCT ON (url) * FROM post_videos WHERE post_id = $1
      ORDER BY url, CASE source WHEN 'manual' THEN 0 WHEN 'import' THEN 1 ELSE 2 END) v
ORDER BY id`
	sqlPostContent = `SELECT content FROM posts WHERE id = $1`
)

type seoRow struct {
	seo.Override
	ogImage, twitterImage int64
	breadcrumb            string
}

func (st *Site) seoRow(ctx context.Context, entity string, id int64) (*seoRow, error) {
	r := &seoRow{}
	o := &r.Override
	err := st.db.QueryRow(ctx, sqlSEORow, entity, id).Scan(&o.Title, &o.Description, &o.Canonical, &o.NoIndex, &o.NoFollow,
		&o.Advanced, &o.OGTitle, &o.OGDescription, &r.ogImage, &o.TwitterTitle, &o.TwitterDescription, &r.twitterImage,
		&r.breadcrumb)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return r, err
}

type videoRow struct {
	seo.Video
	url, provider, externalID, thumbURL string
	thumbMedia                          int64
}

func (st *Site) postVideos(ctx context.Context, postID int64) ([]*videoRow, error) {
	rows, err := st.db.Query(ctx, sqlPostVideos, postID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*videoRow
	for rows.Next() {
		v, err := scanVideo(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// scanVideo scans the columns of sqlPostVideos, after any leading columns in pre.
func scanVideo(rows pgx.Rows, pre ...any) (*videoRow, error) {
	v := &videoRow{}
	dest := append(pre, &v.Name, &v.Description, &v.url, &v.provider, &v.externalID, &v.thumbURL, &v.thumbMedia,
		&v.Duration, &v.UploadDate, &v.FamilyFriendly)
	if err := rows.Scan(dest...); err != nil {
		return nil, err
	}
	v.EmbedURL = seo.EmbedURL(v.provider, v.externalID)
	if v.EmbedURL == "" {
		v.ContentURL = v.url
	}
	return v, nil
}

// resolveThumb sets the thumbnail the JSON-LD and the video sitemap share: the thumbnail media, else the stored URL.
func (v *videoRow) resolveThumb(snap *snapshot, media map[int64]mediaRow) {
	v.ThumbnailURL = v.thumbURL
	if m, ok := media[v.thumbMedia]; ok && v.thumbMedia != 0 {
		v.ThumbnailURL = snap.mediaView(m).URL
	}
}

func (st *Site) firstImageID(ctx context.Context, postID int64) (int64, error) {
	var raw []byte
	if err := st.db.QueryRow(ctx, sqlPostContent, postID).Scan(&raw); err != nil {
		return 0, err
	}
	doc, err := content.Parse(raw)
	if err != nil {
		return 0, err
	}
	return content.FirstImageID(doc), nil
}

// buildHead resolves d.Head. It also sets the breadcrumb title of a single post, which the snapshot doesn't hold.
func (st *Site) buildHead(ctx context.Context, snap *snapshot, d *theme.PageData) error {
	s := &snap.settings
	var row *seoRow
	var err error
	switch {
	case d.Post != nil:
		row, err = st.seoRow(ctx, "post", d.Post.ID)
	case d.Term != nil:
		row, err = st.seoRow(ctx, "term", d.Term.ID)
	case d.Author != nil:
		row, err = st.seoRow(ctx, "author", d.Author.ID)
	}
	if err != nil {
		return err
	}

	var ids []int64
	if r := s.SEO.DefaultImage; r != nil {
		ids = append(ids, r.ID)
	}
	if r := s.SEO.KnowledgeGraph.Logo; r != nil {
		ids = append(ids, r.ID)
	}
	if row != nil {
		ids = append(ids, row.ogImage, row.twitterImage)
	}
	var first int64
	var videos []*videoRow
	single := d.Post != nil && d.Kind != theme.KindPostsPage
	if single {
		if d.Post.FeaturedMedia == nil {
			if first, err = st.firstImageID(ctx, d.Post.ID); err != nil {
				return err
			}
			ids = append(ids, first)
		}
		if videos, err = st.postVideos(ctx, d.Post.ID); err != nil {
			return err
		}
		for _, v := range videos {
			ids = append(ids, v.thumbMedia)
		}
	}
	media, err := st.loadMedia(ctx, ids)
	if err != nil {
		return err
	}
	view := func(id int64) *theme.Media {
		if m, ok := media[id]; ok && id != 0 {
			return snap.mediaView(m)
		}
		return nil
	}

	in := seo.Input{Page: d, Settings: s, Location: snap.loc, Now: st.now(), FirstImage: view(first)}
	if r := s.SEO.DefaultImage; r != nil {
		in.DefaultImage = view(r.ID)
	}
	if r := s.SEO.KnowledgeGraph.Logo; r != nil {
		in.Logo = view(r.ID)
	}
	if row != nil {
		row.OGImage, row.TwitterImage = view(row.ogImage), view(row.twitterImage)
		in.Override = &row.Override
		if d.Post != nil && row.breadcrumb != "" {
			d.Post.Breadcrumb = row.breadcrumb
		}
	}
	for _, v := range videos {
		v.resolveThumb(snap, media)
		in.Videos = append(in.Videos, v.Video)
	}
	in.Crumbs = st.theme.Breadcrumbs(d)
	d.Head, _ = seo.Build(in)
	return nil
}

func authorKey(id int64, legacyKey string) string {
	if legacyKey != "" {
		return legacyKey
	}
	return "afsc:author:" + strconv.FormatInt(id, 10)
}
