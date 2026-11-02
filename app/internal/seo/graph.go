package seo

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"afsc/internal/theme"
)

type node = map[string]any

func ref(id string) node { return node{"@id": id} }

// PersonID is the stable @id of an author: a hash of its legacy key, so it survives re-imports.
func PersonID(site string, a *theme.Author) string {
	sum := sha256.Sum256([]byte(a.Key))
	return site + "/#/schema/person/" + hex.EncodeToString(sum[:16])
}

// graph builds the §8.3 JSON-LD @graph as a <script> tag, or "" for pages without one.
func (in *Input) graph(title, desc, canonical string) []node {
	d, s := in.Page, in.Settings
	site := d.Site.URL
	lang := strings.ReplaceAll(d.Site.Language, "_", "-")
	var g []node

	publisherID := site + "/#organization"
	publisher := node{"@type": "Organization", "@id": publisherID, "name": firstNonEmpty(s.SEO.KnowledgeGraph.Name, d.Site.Title),
		"url": site + "/"}
	if s.SEO.KnowledgeGraph.Type == "person" {
		publisherID = site + "/#/schema/person/site"
		publisher["@type"], publisher["@id"] = "Person", publisherID
	}
	if l := in.Logo; l != nil {
		logo := imageNode(site+"/#/schema/logo/image/", l, lang)
		key := "logo"
		if s.SEO.KnowledgeGraph.Type == "person" {
			key = "image"
		}
		publisher[key] = logo
	}
	if len(s.SEO.SocialProfiles) > 0 {
		publisher["sameAs"] = s.SEO.SocialProfiles
	}
	website := node{"@type": "WebSite", "@id": site + "/#website", "url": site + "/", "name": d.Site.Title,
		"publisher": ref(publisherID), "inLanguage": lang,
		"potentialAction": []node{{
			"@type":       "SearchAction",
			"target":      node{"@type": "EntryPoint", "urlTemplate": site + "/search/?q={search_term_string}"},
			"query-input": "required name=search_term_string",
		}}}
	if d.Site.Tagline != "" {
		website["description"] = d.Site.Tagline
	}

	if d.Kind == theme.Kind404 || d.Kind == theme.Kind410 {
		return append(g, website, publisher)
	}

	pageURL := canonical
	if pageURL == "" {
		pageURL = d.Request.URL
		if d.Search != nil && d.Search.Query != "" {
			pageURL += "?q=" + url.QueryEscape(d.Search.Query)
		}
	}
	page := node{"@type": pageType(d.Kind), "@id": pageURL, "url": pageURL, "name": title, "isPartOf": ref(site + "/#website"),
		"inLanguage": lang}
	if desc != "" {
		page["description"] = desc
	}
	if len(in.Crumbs) > 1 {
		page["breadcrumb"] = ref(pageURL + "#breadcrumb")
	}
	if d.Kind == theme.KindFrontPage || d.Kind == theme.KindHome {
		page["about"] = ref(publisherID)
	}

	var article node
	if p := d.Post; p != nil && d.Kind != theme.KindPostsPage {
		page["datePublished"], page["dateModified"] = isoTime(p.PublishedAt), isoTime(p.UpdatedAt)
		if p.Type == "post" {
			article = in.article(p, pageURL, publisherID, lang)
		}
	}
	if img := in.image(nil); img != nil && d.Post != nil && d.Kind != theme.KindPostsPage {
		id := pageURL + "#primaryimage"
		page["primaryImageOfPage"], page["image"], page["thumbnailUrl"] = ref(id), ref(id), img.URL
		g = append(g, imageNode(id, img, lang))
		if article != nil {
			article["image"], article["thumbnailUrl"] = ref(id), img.URL
		}
	}
	if d.Kind == theme.KindAuthor && d.Author != nil {
		page["mainEntity"] = ref(PersonID(site, d.Author))
	}

	var people []node
	seen := map[string]bool{}
	addPerson := func(a *theme.Author) {
		id := PersonID(site, a)
		if seen[id] {
			return
		}
		seen[id] = true
		people = append(people, in.person(a, id, lang))
	}
	lead, primary := []node{page}, page
	if article != nil {
		lead, primary = []node{article, page}, article
		for _, a := range d.Post.Authors {
			addPerson(a)
		}
	}
	if d.Author != nil {
		addPerson(d.Author)
	}
	var videos []node
	if d.Post != nil && d.Kind != theme.KindPostsPage {
		var ids []node
		for i, v := range in.Videos {
			id := pageURL + "#video-" + strconv.Itoa(i+1)
			if vn := videoNode(v, id, lang); vn != nil {
				videos, ids = append(videos, vn), append(ids, ref(id))
			}
		}
		if len(ids) > 0 {
			primary["video"] = ids
		}
	}
	g = append(lead, g...)
	g = append(g, crumbs(in.Crumbs, pageURL)...)
	g = append(g, website, publisher)
	g = append(g, people...)
	return append(g, videos...)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}

func pageType(kind string) string {
	switch kind {
	case theme.KindHome, theme.KindPostsPage, theme.KindCategory, theme.KindTag, theme.KindDate:
		return "CollectionPage"
	case theme.KindAuthor:
		return "ProfilePage"
	case theme.KindSearch:
		return "SearchResultsPage"
	}
	return "WebPage"
}

func (in *Input) article(p *theme.Post, pageURL, publisherID, lang string) node {
	site := in.Page.Site.URL
	a := node{"@type": "BlogPosting", "@id": pageURL + "#article", "isPartOf": ref(pageURL), "mainEntityOfPage": ref(pageURL),
		"headline": truncate(p.Title, 110), "datePublished": isoTime(p.PublishedAt), "dateModified": isoTime(p.UpdatedAt),
		"publisher": ref(publisherID), "wordCount": len(strings.Fields(p.ContentText)), "inLanguage": lang}
	a["author"] = ref(publisherID)
	if len(p.Authors) > 0 {
		authors := make([]node, len(p.Authors))
		for i, au := range p.Authors {
			authors[i] = node{"@id": PersonID(site, au), "name": au.Name}
		}
		a["author"] = authors
	}
	if len(p.Categories) > 0 {
		a["articleSection"] = names(p.Categories)
	}
	if len(p.Tags) > 0 {
		a["keywords"] = names(p.Tags)
	}
	return a
}

func names(ts []*theme.Term) []string {
	out := make([]string, len(ts))
	for i, t := range ts {
		out[i] = t.Name
	}
	return out
}

func (in *Input) person(a *theme.Author, id, lang string) node {
	p := node{"@type": "Person", "@id": id, "name": a.Name, "url": a.URL}
	if a.Bio != "" {
		p["description"] = a.Bio
	}
	if a.Avatar != nil {
		p["image"] = imageNode(id+"/image/", a.Avatar, lang)
	}
	var same []string
	if a.Website != "" {
		same = append(same, a.Website)
	}
	for _, k := range sortedKeys(a.Social) {
		if v := a.Social[k]; strings.HasPrefix(v, "https://") || strings.HasPrefix(v, "http://") {
			same = append(same, v)
		}
	}
	if len(same) > 0 {
		p["sameAs"] = same
	}
	return p
}

func imageNode(id string, m *theme.Media, lang string) node {
	n := node{"@type": "ImageObject", "@id": id, "url": m.URL, "contentUrl": m.URL, "inLanguage": lang}
	if m.Width > 0 && m.Height > 0 {
		n["width"], n["height"] = m.Width, m.Height
	}
	if c := firstNonEmpty(m.Caption, m.Alt); c != "" {
		n["caption"] = c
	}
	return n
}

// videoNode returns nil when a field Google requires is missing.
func videoNode(v Video, id, lang string) node {
	if v.Name == "" || v.ThumbnailURL == "" || v.UploadDate == nil || (v.EmbedURL == "" && v.ContentURL == "") {
		return nil
	}
	n := node{"@type": "VideoObject", "@id": id, "name": v.Name, "thumbnailUrl": v.ThumbnailURL,
		"uploadDate": isoTime(*v.UploadDate), "inLanguage": lang}
	if v.Description != "" {
		n["description"] = v.Description
	}
	if v.Duration > 0 {
		n["duration"] = ISODuration(v.Duration)
	}
	if v.EmbedURL != "" {
		n["embedUrl"] = v.EmbedURL
	}
	if v.ContentURL != "" {
		n["contentUrl"] = v.ContentURL
	}
	return n
}

// ISODuration formats seconds as an ISO 8601 duration, for example PT1H2M3S.
func ISODuration(sec int) string {
	h, m, s := sec/3600, sec%3600/60, sec%60
	out := "PT"
	if h > 0 {
		out += strconv.Itoa(h) + "H"
	}
	if m > 0 {
		out += strconv.Itoa(m) + "M"
	}
	if s > 0 || out == "PT" {
		out += strconv.Itoa(s) + "S"
	}
	return out
}

func crumbs(cs []theme.Crumb, pageURL string) []node {
	if len(cs) < 2 {
		return nil
	}
	items := make([]node, len(cs))
	for i, c := range cs {
		item := node{"@type": "ListItem", "position": i + 1, "name": c.Name}
		if i < len(cs)-1 && c.URL != "" {
			item["item"] = c.URL
		}
		items[i] = item
	}
	return []node{{"@type": "BreadcrumbList", "@id": pageURL + "#breadcrumb", "itemListElement": items}}
}

func script(g []node) string {
	if len(g) == 0 {
		return ""
	}
	b, err := json.Marshal(node{"@context": "https://schema.org", "@graph": g})
	if err != nil {
		return ""
	}
	return `<script type="application/ld+json" class="afsc-schema-graph">` + string(b) + "</script>\n"
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// EmbedURL is the player URL of a video on a known provider, "" for others.
func EmbedURL(provider, id string) string {
	if id == "" {
		return ""
	}
	switch strings.ToLower(provider) {
	case "youtube":
		return "https://www.youtube.com/embed/" + url.PathEscape(id)
	case "vimeo":
		return "https://player.vimeo.com/video/" + url.PathEscape(id)
	}
	return ""
}
