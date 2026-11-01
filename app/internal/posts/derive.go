package posts

import (
	"context"
	"fmt"
	"math"
	"slices"

	"afsc/internal/content"
)

func walkPaths(n *content.Node, path string, fn func(n *content.Node, path string)) {
	if n == nil {
		return
	}
	fn(n, path)
	for i, c := range n.Content {
		p := fmt.Sprintf("content[%d]", i)
		if path != "" {
			p = path + "." + p
		}
		walkPaths(c, p, fn)
	}
}

func rawHTML(n *content.Node) string {
	s, _ := n.Attrs["html"].(string)
	return s
}

// rawHTMLIssues applies Q9 for users without content.raw_html: every rawHtml node in doc must match one in the
// stored document (as a multiset of html strings). Run it before Validate, which rewrites html.
func rawHTMLIssues(doc *content.Node, stored []byte) []content.Issue {
	have := map[string]int{}
	if old, err := content.Parse(stored); err == nil && len(stored) > 0 {
		content.Walk(old, func(n *content.Node) {
			if n.Type == "rawHtml" {
				have[rawHTML(n)]++
			}
		})
	}
	var out []content.Issue
	walkPaths(doc, "", func(n *content.Node, path string) {
		if n.Type != "rawHtml" {
			return
		}
		if h := rawHTML(n); have[h] > 0 {
			have[h]--
			return
		}
		out = append(out, content.Issue{Path: path, Message: "only an admin can add or change raw HTML blocks"})
	})
	return out
}

func attrInt(n *content.Node, k string) (int64, bool) {
	f, ok := n.Attrs[k].(float64)
	if !ok || f != math.Trunc(f) {
		return 0, false
	}
	return int64(f), true
}

func attrStr(n *content.Node, k string) string {
	s, _ := n.Attrs[k].(string)
	return s
}

// mediaIDs is what post_media holds for a document: its mediaId and posterMediaId references plus the featured
// image, deduplicated and sorted.
func mediaIDs(doc *content.Node, featured *int64) []int64 {
	ids := content.MediaIDs(doc)
	if featured != nil {
		ids = append(ids, *featured)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

type autoVideo struct {
	url, provider, externalID, thumb string
	thumbMedia                       *int64
}

// autoVideos lists the videos a document shows (§5.3): YouTube and Vimeo embeds and video blocks. mediaURL turns
// a library video into its absolute URL ("" when unknown).
func autoVideos(doc *content.Node, mediaURL func(id int64) string) []autoVideo {
	var out []autoVideo
	seen := map[string]bool{}
	content.Walk(doc, func(n *content.Node) {
		var v autoVideo
		switch n.Type {
		case "embed":
			v.provider, v.externalID, v.url = attrStr(n, "provider"), attrStr(n, "externalId"), attrStr(n, "url")
			switch v.provider {
			case "youtube":
				v.thumb = "https://i.ytimg.com/vi/" + v.externalID + "/hqdefault.jpg"
			case "vimeo":
			default:
				return
			}
		case "video":
			v.url = attrStr(n, "src")
			if id, ok := attrInt(n, "mediaId"); ok {
				v.url = mediaURL(id)
			}
			if id, ok := attrInt(n, "posterMediaId"); ok {
				v.thumbMedia = &id
			}
		default:
			return
		}
		if v.url != "" && !seen[v.url] {
			seen[v.url] = true
			out = append(out, v)
		}
	})
	return out
}

const sqlSyncAutoVideos = `
WITH gone AS (
  DELETE FROM post_videos a
  WHERE a.post_id = $1 AND a.source = 'auto'
    AND (NOT (a.url = ANY($2::text[]))
         OR EXISTS (SELECT 1 FROM post_videos m WHERE m.post_id = $1 AND m.url = a.url AND m.source <> 'auto'))
)
INSERT INTO post_videos (post_id, provider, external_id, url, title, thumbnail_url, thumbnail_media_id, source)
SELECT $1, u.provider, u.external_id, u.url, $7, u.thumb, m.id, 'auto'
FROM unnest($2::text[], $3::text[], $4::text[], $5::text[], $6::bigint[]) AS u(url, provider, external_id, thumb, thumb_media)
LEFT JOIN media m ON m.id = u.thumb_media
WHERE NOT EXISTS (SELECT 1 FROM post_videos x WHERE x.post_id = $1 AND x.url = u.url AND x.source <> 'auto')
ON CONFLICT (post_id, source, url) DO UPDATE SET
  provider = EXCLUDED.provider, external_id = EXCLUDED.external_id, title = EXCLUDED.title,
  thumbnail_url = EXCLUDED.thumbnail_url, thumbnail_media_id = EXCLUDED.thumbnail_media_id, updated_at = now()`

// syncDerived rewrites a post's auto post_videos rows (manual and imported rows with the same URL win) and its
// post_media rows.
func syncDerived(ctx context.Context, db dbtx, postID int64, title string, videos []autoVideo, media []int64) error {
	var urls, providers, ids, thumbs []string
	var thumbMedia []*int64
	for _, v := range videos {
		urls, providers, ids, thumbs = append(urls, v.url), append(providers, v.provider), append(ids, v.externalID), append(thumbs, v.thumb)
		thumbMedia = append(thumbMedia, v.thumbMedia)
	}
	if urls == nil {
		urls, providers, ids, thumbs, thumbMedia = []string{}, []string{}, []string{}, []string{}, []*int64{}
	}
	if _, err := db.Exec(ctx, sqlSyncAutoVideos, postID, urls, providers, ids, thumbs, thumbMedia, title); err != nil {
		return err
	}
	if media == nil {
		media = []int64{}
	}
	if _, err := db.Exec(ctx, `DELETE FROM post_media WHERE post_id = $1 AND NOT (media_id = ANY($2::bigint[]))`, postID, media); err != nil {
		return err
	}
	_, err := db.Exec(ctx, `
INSERT INTO post_media (post_id, media_id) SELECT $1, id FROM media WHERE id = ANY($2::bigint[])
ON CONFLICT DO NOTHING`, postID, media)
	return err
}
