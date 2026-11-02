package convert

import (
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"afsc/internal/content"
	"afsc/internal/sanitize"
)

var wpImageRe = regexp.MustCompile(`^wp-image-(\d+)$`)

func dim(v string) (int, bool) {
	i, err := strconv.Atoi(strings.TrimSuffix(strings.TrimSpace(v), "px"))
	return i, err == nil && i >= 1 && i <= 100000
}

// absolute makes a root-relative URL absolute on the old site, for external-src fallbacks.
func (w *walker) absolute(u string) string {
	if strings.HasPrefix(u, "/") && !strings.HasPrefix(u, "//") && w.ctx.SiteURL != "" {
		return strings.TrimSuffix(w.ctx.SiteURL, "/") + u
	}
	if strings.HasPrefix(u, "//") {
		return "https:" + u
	}
	return u
}

// image maps <img> to an image node (§6.5): wp-image-N first, then the URL index, then the orphan file.
func (w *walker) image(n *html.Node, link *content.Mark) []*content.Node {
	src := strings.TrimSpace(attr(n, "src"))
	if src == "" {
		for _, k := range []string{"data-src", "data-lazy-src", "data-orig-file"} {
			if v := attr(n, k); v != "" {
				src = v
				w.flag("lazy_src", "%s used", k)
				break
			}
		}
	}
	if src == "" {
		w.flag("image_dropped", "img without src")
		return nil
	}
	if strings.HasPrefix(strings.ToLower(src), "data:") {
		return w.raw(n)
	}
	attrs := map[string]any{"alt": attr(n, "alt")}
	size := ""
	var mediaKey string
	for _, c := range classes(n) {
		if m := wpImageRe.FindStringSubmatch(c); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			if k, ok := w.ctx.Media.Attachment(id); ok {
				mediaKey = k
			}
		}
		if s, ok := strings.CutPrefix(c, "size-"); ok && sizeNameRe.MatchString(s) {
			size = s
		}
	}
	if ref, rel, ok := w.ctx.Media.Resolve(src); ok {
		if mediaKey == "" {
			mediaKey = ref.Key
		}
		if size == "" && ref.Size != "" && ref.Size != "full" && sizeNameRe.MatchString(ref.Size) {
			size = ref.Size
		}
		_ = rel
	}
	switch {
	case mediaKey != "":
		attrs["mediaId"] = mediaKey
		if size != "" {
			attrs["size"] = size
		}
	case w.ctx.Media.IsUpload(src):
		_, rel, _ := w.ctx.Media.Resolve(src)
		if w.ctx.Decide("missing_media", rel, "keep_src") == "remove" {
			w.flag("missing_media", "removed %s", rel)
			return nil
		}
		w.flag("missing_media", "%s", rel)
		attrs["src"] = w.absolute(src)
	default:
		abs := w.absolute(src)
		if !sanitize.SafeURL(abs) {
			w.flag("image_dropped", "unsafe src")
			return nil
		}
		w.flag("external_image", "%s", hostOf(abs))
		attrs["src"] = abs
	}
	if a := alignOf(n); a != "" {
		attrs["align"] = a
	}
	if t := attr(n, "title"); t != "" {
		attrs["title"] = t
	}
	if mediaKey == "" {
		if v, ok := dim(attr(n, "width")); ok {
			attrs["width"] = v
		}
		if v, ok := dim(attr(n, "height")); ok {
			attrs["height"] = v
		}
	}
	if c := w.keepClasses(n, nil); c != "" {
		attrs["classes"] = c
	}
	if link != nil {
		w.linkImage(attrs, mediaKey, link)
	}
	if style := attr(n, "style"); style != "" {
		w.flag("style", "img style")
	}
	return []*content.Node{{Type: "image", Attrs: attrs}}
}

func (w *walker) linkImage(attrs map[string]any, mediaKey string, link *content.Mark) {
	href, _ := link.Attrs["href"].(string)
	if href == "" {
		return
	}
	if ref, _, ok := w.ctx.Media.Resolve(href); ok && mediaKey != "" && ref.Key == mediaKey {
		attrs["linkTarget"] = "media"
	} else {
		attrs["href"], attrs["linkTarget"] = href, "custom"
	}
	if t, _ := link.Attrs["target"].(string); t == "_blank" {
		w.flag("image_link_target", "target=_blank on an image link is not kept")
	}
}

func hostOf(u string) string {
	u = strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
	h, _, _ := strings.Cut(u, "/")
	return h
}

// iframe becomes an embed when the provider is known; otherwise the iframe_host decision applies.
func (w *walker) iframe(n *html.Node) []*content.Node {
	src := attr(n, "src")
	host := IframeHost(src)
	node, ok := EmbedFromURL(src)
	def := "raw_html"
	if ok {
		def = "embed"
	}
	switch w.ctx.Decide("iframe_host", host, def) {
	case "embed":
		if !ok {
			w.flag("embed_failed", "%s", host)
			return w.raw(n)
		}
		if v, ok := dim(attr(n, "width")); ok {
			node.Attrs["width"] = v
		}
		if v, ok := dim(attr(n, "height")); ok {
			node.Attrs["height"] = v
		}
		return []*content.Node{node}
	case "drop":
		w.flag("element_dropped", "iframe %s", host)
		return nil
	}
	return w.raw(n)
}

func (w *walker) video(n *html.Node) []*content.Node {
	src := attr(n, "src")
	if src == "" {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "source" && attr(c, "src") != "" {
				src = attr(c, "src")
				break
			}
		}
	}
	bools := map[string]bool{}
	for _, k := range []string{"controls", "autoplay", "loop", "muted"} {
		bools[k] = hasAttr(n, k)
	}
	return w.videoNode(src, attr(n, "poster"), bools)
}

func (w *walker) videoNode(src, poster string, bools map[string]bool) []*content.Node {
	if src == "" {
		w.flag("element_dropped", "video without src")
		return nil
	}
	attrs := map[string]any{}
	for k, v := range bools {
		attrs[k] = v
	}
	if ref, _, ok := w.ctx.Media.Resolve(src); ok {
		attrs["mediaId"] = ref.Key
	} else {
		abs := w.absolute(src)
		if !sanitize.SafeURL(abs) {
			w.flag("element_dropped", "video with unsafe src")
			return nil
		}
		if w.ctx.Media.IsUpload(src) {
			w.flag("missing_media", "video %s", src)
		}
		attrs["src"] = abs
	}
	if poster != "" {
		if ref, _, ok := w.ctx.Media.Resolve(poster); ok {
			attrs["posterMediaId"] = ref.Key
		} else {
			w.flag("attr_dropped", "video poster not in the media library")
		}
	}
	return []*content.Node{{Type: "video", Attrs: attrs}}
}

func outerHTML(n *html.Node) string {
	var b strings.Builder
	html.Render(&b, n)
	return b.String()
}

// raw keeps an element as sanitized rawHtml, or drops it per the element decision.
func (w *walker) raw(n *html.Node) []*content.Node {
	if w.ctx.Decide("element", n.Data, "raw_html") == "drop" {
		w.flag("element_dropped", "%s", n.Data)
		return nil
	}
	return w.rawHTML(n.Data, outerHTML(n))
}

func (w *walker) rawHTML(what, src string) []*content.Node {
	clean, removed := sanitize.Trusted(src)
	for _, r := range removed {
		w.flag("sanitized", "%s %s: %s", r.Element, r.Attribute, r.Reason)
	}
	if visible(src) != visible(clean) {
		w.flag("text_loss", "%s: sanitizer removed visible text", what)
	}
	if strings.TrimSpace(clean) == "" {
		w.flag("element_dropped", "%s (nothing left after sanitizing)", what)
		return nil
	}
	w.flag("raw_html", "%s", what)
	return []*content.Node{{Type: "rawHtml", Attrs: map[string]any{"html": clean}}}
}

func (w *walker) rawNode(src string) *content.Node {
	clean, _ := sanitize.Trusted(src)
	return &content.Node{Type: "rawHtml", Attrs: map[string]any{"html": clean}}
}

// visible is the text a reader sees: script, style and template bodies excluded, whitespace collapsed.
func visible(src string) string {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return ""
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.TextNode {
			b.WriteString(n.Data)
			b.WriteByte(' ')
		}
		if n.Type == html.ElementNode && (n.Data == "script" || n.Data == "style" || n.Data == "template" || n.Data == "noscript") {
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// figure handles <figure>: one image with an optional caption becomes figure/image; anything else unwraps.
func (w *walker) figure(n *html.Node) []*content.Node {
	var imgs, iframes []*html.Node
	var caption *html.Node
	var find func(*html.Node, bool)
	find = func(x *html.Node, inCaption bool) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "figcaption":
				if caption == nil {
					caption = c
				}
				find(c, true)
			case "img":
				if !inCaption {
					imgs = append(imgs, c)
				}
			case "iframe":
				iframes = append(iframes, c)
			default:
				find(c, inCaption)
			}
		}
	}
	find(n, false)
	align := alignOf(n)
	if len(imgs) == 1 && len(iframes) == 0 {
		var link *content.Mark
		for p := imgs[0].Parent; p != nil && p != n; p = p.Parent {
			if p.Type == html.ElementNode && p.Data == "a" {
				link = w.linkMark(p)
				break
			}
		}
		img := w.image(imgs[0], link)
		if len(img) == 0 || img[0].Type != "image" {
			return append(img, w.captionAsParagraph(caption)...)
		}
		if align != "" && img[0].Attrs["align"] == nil {
			img[0].Attrs["align"] = align
		}
		if caption == nil {
			return img
		}
		inl := w.inlineOnly(caption)
		if len(inl) == 0 {
			return img
		}
		fig := &content.Node{Type: "figure", Content: []*content.Node{img[0], {Type: "figcaption", Content: inl}}}
		fa := map[string]any{}
		if a, ok := img[0].Attrs["align"].(string); ok {
			fa["align"] = a
		}
		if c := w.keepClasses(n, nil); c != "" {
			fa["classes"] = c
		}
		if len(fa) > 0 {
			fig.Attrs = fa
		}
		return []*content.Node{fig}
	}
	if len(iframes) == 1 && len(imgs) == 0 {
		out := w.iframe(iframes[0])
		if len(out) == 1 && out[0].Type == "embed" && caption != nil {
			if t := strings.TrimSpace(visible(outerHTML(caption))); t != "" {
				out[0].Attrs["caption"] = t
			}
			return out
		}
		return append(out, w.captionAsParagraph(caption)...)
	}
	w.flag("unwrap", "figure")
	return w.sub(n)
}

func (w *walker) captionAsParagraph(c *html.Node) []*content.Node {
	if c == nil {
		return nil
	}
	inl := w.inlineOnly(c)
	if len(inl) == 0 {
		return nil
	}
	w.flag("caption_unwrapped", "figcaption kept as a paragraph")
	return []*content.Node{{Type: "paragraph", Content: inl}}
}
