package seo

import (
	"bytes"
	"encoding/xml"
	"strconv"
	"time"
)

// Sitemap namespaces and the limits of the sitemaps.org and Google image/video schemas (§8.4).
const (
	NSSitemap = "http://www.sitemaps.org/schemas/sitemap/0.9"
	NSImage   = "http://www.google.com/schemas/sitemap-image/1.1"
	NSVideo   = "http://www.google.com/schemas/sitemap-video/1.1"

	MaxSitemapURLs   = 50000
	maxSitemapImages = 1000
	maxVideoTitle    = 100
	maxVideoDesc     = 2048
	maxVideoDuration = 28800
)

// SitemapURL is one <url>: a page with its images (absolute URLs) and videos.
type SitemapURL struct {
	Loc     string
	LastMod time.Time
	Images  []string
	Videos  []Video
}

// SitemapRef is one <sitemap> in the index.
type SitemapRef struct {
	Loc     string
	LastMod time.Time
}

// SitemapVideo reports whether v has what a video sitemap entry needs: a thumbnail, a title, a description and a
// player or content URL.
func SitemapVideo(v Video) bool {
	return v.ThumbnailURL != "" && truncate(v.Name, maxVideoTitle) != "" && truncate(v.Description, maxVideoDesc) != "" &&
		(v.EmbedURL != "" || v.ContentURL != "")
}

// URLSet renders a <urlset>. Videos SitemapVideo rejects are left out, and images past 1,000 per URL are dropped.
func URLSet(urls []SitemapURL) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<urlset xmlns="` + NSSitemap + `" xmlns:image="` + NSImage + `" xmlns:video="` + NSVideo + `">` + "\n")
	for _, u := range urls {
		b.WriteString("<url>\n")
		elem(&b, "loc", u.Loc)
		if !u.LastMod.IsZero() {
			elem(&b, "lastmod", isoTime(u.LastMod))
		}
		for i, img := range u.Images {
			if i == maxSitemapImages {
				break
			}
			b.WriteString("<image:image>")
			elem(&b, "image:loc", img)
			b.WriteString("</image:image>\n")
		}
		for _, v := range u.Videos {
			if SitemapVideo(v) {
				video(&b, v)
			}
		}
		b.WriteString("</url>\n")
	}
	b.WriteString("</urlset>\n")
	return b.Bytes()
}

// video writes the children in the order the video schema's sequence requires.
func video(b *bytes.Buffer, v Video) {
	b.WriteString("<video:video>\n")
	elem(b, "video:thumbnail_loc", v.ThumbnailURL)
	elem(b, "video:title", truncate(v.Name, maxVideoTitle))
	elem(b, "video:description", truncate(v.Description, maxVideoDesc))
	if v.ContentURL != "" {
		elem(b, "video:content_loc", v.ContentURL)
	}
	if v.EmbedURL != "" {
		elem(b, "video:player_loc", v.EmbedURL)
	}
	if v.Duration > 0 && v.Duration <= maxVideoDuration {
		elem(b, "video:duration", strconv.Itoa(v.Duration))
	}
	if v.UploadDate != nil {
		elem(b, "video:publication_date", isoTime(*v.UploadDate))
	}
	if f := v.FamilyFriendly; f != nil {
		elem(b, "video:family_friendly", map[bool]string{true: "yes", false: "no"}[*f])
	}
	b.WriteString("</video:video>\n")
}

// Index renders a <sitemapindex>.
func Index(refs []SitemapRef) []byte {
	var b bytes.Buffer
	b.WriteString(xml.Header)
	b.WriteString(`<sitemapindex xmlns="` + NSSitemap + `">` + "\n")
	for _, r := range refs {
		b.WriteString("<sitemap>\n")
		elem(&b, "loc", r.Loc)
		if !r.LastMod.IsZero() {
			elem(&b, "lastmod", isoTime(r.LastMod))
		}
		b.WriteString("</sitemap>\n")
	}
	b.WriteString("</sitemapindex>\n")
	return b.Bytes()
}

func elem(b *bytes.Buffer, name, text string) {
	b.WriteString("<" + name + ">")
	_ = xml.EscapeText(b, []byte(text))
	b.WriteString("</" + name + ">\n")
}
