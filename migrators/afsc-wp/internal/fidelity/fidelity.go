// Package fidelity compares a WordPress article body with AFSC's rendering of the same post (migration plan
// §8.3) and classifies it exact, minor, fallback or lossy.
package fidelity

import (
	"fmt"
	"net/url"
	"slices"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"afsc/migrators/afsc-wp/internal/convert"
)

const (
	Exact    = "exact"
	Minor    = "minor"
	Fallback = "fallback"
	Lossy    = "lossy"
)

// Side describes how to read one side's URLs.
type Side struct {
	// Media maps an img src or link href (with the element's classes) to a media legacy key.
	Media func(u string, classes []string) (string, bool)
	// Hosts are hosts treated as "this site" when normalizing links.
	Hosts map[string]bool
	// Link, when set, maps an href first (the WordPress side applies the transform's link rewrites).
	Link func(string) string
}

type Extract struct {
	Text     string   `json:"text"`
	Links    []string `json:"links"`
	Images   []string `json:"images"`
	Headings []string `json:"headings"`
	Cells    []string `json:"cells"`
	Embeds   []string `json:"embeds"`
	Markup   string   `json:"-"`
}

var skipText = map[string]bool{"script": true, "style": true, "template": true, "noscript": true}

// Analyze parses an article body and pulls out what the comparison looks at.
func Analyze(body string, side Side) Extract {
	nodes, _ := html.ParseFragment(strings.NewReader(body), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	a := &analyzer{side: side}
	for _, n := range nodes {
		a.walk(n)
	}
	return Extract{
		Text: collapse(a.text.String()), Links: a.links, Images: a.images, Headings: a.headings,
		Cells: a.cells, Embeds: a.embeds, Markup: collapse(a.markup.String()),
	}
}

type analyzer struct {
	side     Side
	text     strings.Builder
	markup   strings.Builder
	links    []string
	images   []string
	headings []string
	cells    []string
	embeds   []string
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func textOf(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		if x.Type == html.ElementNode && skipText[x.Data] {
			return
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return collapse(b.String())
}

func collapse(s string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(s, "\u00a0", " ")), " ")
}

var blockish = map[string]bool{"p": true, "div": true, "figure": true, "li": true, "blockquote": true}

// embedOf recognizes an embed: an iframe, a provider blockquote, or a block holding only a bare provider URL.
func embedOf(n *html.Node) (string, bool) {
	switch {
	case n.Data == "iframe":
		if e, ok := convert.EmbedFromURL(attr(n, "src")); ok {
			return embedKey(e.Attrs), true
		}
	case n.Data == "blockquote" && (strings.Contains(attr(n, "class"), "twitter-tweet") || strings.Contains(attr(n, "class"), "instagram-media") || attr(n, "data-instgrm-permalink") != ""):
		var found string
		var walk func(*html.Node)
		walk = func(x *html.Node) {
			if found != "" {
				return
			}
			if x.Type == html.ElementNode && x.Data == "a" {
				if e, ok := convert.EmbedFromURL(attr(x, "href")); ok {
					found = embedKey(e.Attrs)
				}
			}
			for c := x.FirstChild; c != nil; c = c.NextSibling {
				walk(c)
			}
		}
		if p := attr(n, "data-instgrm-permalink"); p != "" {
			if e, ok := convert.EmbedFromURL(p); ok {
				return embedKey(e.Attrs), true
			}
		}
		walk(n)
		return found, found != ""
	case blockish[n.Data]:
		t := textOf(n)
		if strings.HasPrefix(t, "http") && !strings.Contains(t, " ") {
			if e, ok := convert.EmbedFromURL(t); ok && !hasElement(n, "a") {
				return embedKey(e.Attrs), true
			}
		}
	}
	return "", false
}

func hasElement(n *html.Node, tag string) bool {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && (c.Data == tag || hasElement(c, tag)) {
			return true
		}
	}
	return false
}

func embedKey(a map[string]any) string {
	if id, ok := a["externalId"].(string); ok && id != "" {
		return fmt.Sprintf("%s:%s", a["provider"], id)
	}
	return fmt.Sprintf("%s:%s", a["provider"], a["url"])
}

func (a *analyzer) walk(n *html.Node) {
	switch n.Type {
	case html.TextNode:
		a.text.WriteString(n.Data)
		a.markup.WriteString(" " + collapse(n.Data) + " ")
		return
	case html.ElementNode:
	default:
		return
	}
	if skipText[n.Data] {
		return
	}
	if e, ok := embedOf(n); ok {
		a.embeds = append(a.embeds, e)
		a.markup.WriteString("<embed " + e + ">")
		a.text.WriteString(" ")
		return
	}
	cls := strings.Fields(attr(n, "class"))
	switch n.Data {
	case "a":
		if h := attr(n, "href"); h != "" {
			a.links = append(a.links, a.normLink(h, cls))
		}
	case "img":
		a.images = append(a.images, a.imageKey(attr(n, "src"), cls))
	case "h1", "h2", "h3", "h4", "h5", "h6":
		a.headings = append(a.headings, n.Data+":"+textOf(n))
	case "td", "th":
		a.cells = append(a.cells, textOf(n))
	}
	a.markup.WriteString("<" + n.Data + a.markupAttrs(n, cls) + ">")
	if blockish[n.Data] || strings.HasPrefix(n.Data, "h") || n.Data == "br" || n.Data == "td" || n.Data == "th" {
		a.text.WriteString(" ")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		a.walk(c)
	}
	if blockish[n.Data] {
		a.text.WriteString(" ")
	}
	a.markup.WriteString("</" + n.Data + ">")
}

var ignoredAttrs = map[string]bool{"srcset": true, "sizes": true, "loading": true, "decoding": true, "width": true,
	"height": true, "id": true, "style": true, "rel": true, "sandbox": true, "allow": true, "allowfullscreen": true,
	"frameborder": true, "title": true, "data-id": true}

func (a *analyzer) markupAttrs(n *html.Node, cls []string) string {
	var parts []string
	for _, at := range n.Attr {
		switch {
		case ignoredAttrs[at.Key] || strings.HasPrefix(at.Key, "data-"):
		case at.Key == "class":
			c := append([]string{}, cls...)
			sort.Strings(c)
			parts = append(parts, "class="+strings.Join(c, "."))
		case at.Key == "src" && n.Data == "img":
			parts = append(parts, "src="+a.imageKey(at.Val, cls))
		case at.Key == "href":
			parts = append(parts, "href="+a.normLink(at.Val, cls))
		default:
			parts = append(parts, at.Key+"="+at.Val)
		}
	}
	sort.Strings(parts)
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func (a *analyzer) imageKey(src string, cls []string) string {
	if a.side.Media != nil {
		if k, ok := a.side.Media(src, cls); ok {
			return "media:" + k
		}
	}
	return "ext:" + src
}

// normLink makes links comparable: media files become media keys, site hosts are dropped, paths decoded.
func (a *analyzer) normLink(h string, cls []string) string {
	if a.side.Link != nil {
		h = a.side.Link(h)
	}
	if a.side.Media != nil {
		if k, ok := a.side.Media(h, nil); ok {
			return "media:" + k
		}
	}
	u, err := url.Parse(strings.TrimSpace(h))
	if err != nil {
		return h
	}
	if u.Host != "" && !a.side.Hosts[strings.ToLower(u.Hostname())] {
		return strings.TrimSuffix(u.String(), "/")
	}
	p := u.Path
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	p = strings.TrimSuffix(p, "/")
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	if u.Fragment != "" {
		p += "#" + u.Fragment
	}
	if p == "" {
		p = "/"
	}
	return p
}

// Compare classifies a post. rawHTML says AFSC used a rawHtml fallback for it.
func Compare(wp, af Extract, rawHTML bool) (string, []string) {
	var diffs []string
	if wp.Text != af.Text {
		diffs = append(diffs, "text: "+firstDiff(wp.Text, af.Text))
	}
	for _, c := range []struct {
		name   string
		wp, af []string
	}{{"links", wp.Links, af.Links}, {"images", wp.Images, af.Images}, {"headings", wp.Headings, af.Headings},
		{"cells", wp.Cells, af.Cells}, {"embeds", wp.Embeds, af.Embeds}} {
		if !slices.Equal(c.wp, c.af) {
			diffs = append(diffs, fmt.Sprintf("%s: wp=%v afsc=%v", c.name, trim(c.wp), trim(c.af)))
		}
	}
	switch {
	case len(diffs) > 0:
		return Lossy, diffs
	case rawHTML:
		return Fallback, nil
	case wp.Markup == af.Markup:
		return Exact, nil
	}
	return Minor, nil
}

func trim(s []string) []string {
	if len(s) > 8 {
		return append(s[:8:8], fmt.Sprintf("…(%d more)", len(s)-8))
	}
	return s
}

func firstDiff(a, b string) string {
	ra, rb := []rune(a), []rune(b)
	i := 0
	for i < len(ra) && i < len(rb) && ra[i] == rb[i] {
		i++
	}
	from := max(i-30, 0)
	return fmt.Sprintf("at %d: wp %q vs afsc %q", i, string(ra[from:min(i+40, len(ra))]), string(rb[from:min(i+40, len(rb))]))
}
