package convert

import (
	"html"
	"regexp"
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"afsc/internal/content"

	"afsc/migrators/afsc-wp/internal/shortcode"
)

// Handlers are the shortcodes afsc-wp can convert to nodes. Inventory only offers "convert" for these.
var Handlers = map[string]bool{
	"caption": true, "wp_caption": true, "gallery": true, "embed": true, "video": true, "audio": true,
	"youtube": true, "vimeo": true, "tweet": true, "instagram": true, "spotify": true, "soundcloud": true, "googlemaps": true,
}

var attachmentIDRe = regexp.MustCompile(`^attachment_(\d+)$`)

func (w *walker) shortcodeNodes(sc *shortcode.Shortcode) []*content.Node {
	switch w.ctx.Decide("shortcode", sc.Tag, "keep_text") {
	case "render_php":
		out, ok := w.ctx.RenderPHP(sc.Tag, sc.Raw)
		if !ok {
			w.flag("shortcode_unrendered", "%s: no PHP rendering; kept as text", sc.Tag)
			return textParagraph(sc.Raw)
		}
		w.flag("shortcode_rendered", "%s", sc.Tag)
		return w.rawHTML("["+sc.Tag+"]", out)
	case "convert":
	default:
		return textParagraph(sc.Raw)
	}
	var out []*content.Node
	switch sc.Tag {
	case "caption", "wp_caption":
		out = w.caption(sc)
	case "gallery":
		out = w.gallery(sc)
	case "embed":
		out = w.embedURL(strings.TrimSpace(html.UnescapeString(sc.Content)), sc)
	case "video":
		out = w.videoShortcode(sc)
	case "audio":
		out = w.audio(sc)
	case "youtube", "vimeo", "instagram", "spotify", "soundcloud", "googlemaps", "tweet":
		out = w.jetpack(sc)
	default:
		w.flag("shortcode_unhandled", "%s has no converter; kept as text", sc.Tag)
		return textParagraph(sc.Raw)
	}
	return out
}

func textParagraph(t string) []*content.Node {
	return []*content.Node{{Type: "paragraph", Content: []*content.Node{{Type: "text", Text: t}}}}
}

// caption: [caption id="attachment_N" align="alignX"]<a?><img></a?> Caption[/caption] → figure (§6.3).
func (w *walker) caption(sc *shortcode.Shortcode) []*content.Node {
	frag := w.shortcodes(sc.Content)
	nodes, _ := xhtml.ParseFragment(strings.NewReader(frag), &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body})
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	var img, link *xhtml.Node
	var find func(*xhtml.Node)
	find = func(x *xhtml.Node) {
		for c := x.FirstChild; c != nil && img == nil; c = c.NextSibling {
			if c.Type == xhtml.ElementNode && c.Data == "img" {
				img = c
				return
			}
			find(c)
		}
	}
	find(root)
	if img == nil {
		w.flag("caption_without_image", "caption kept as paragraphs")
		return w.sub(root)
	}
	top := img
	for top.Parent != root {
		top = top.Parent
	}
	if top.Data == "a" {
		link = top
	}
	var mark *content.Mark
	if link != nil {
		mark = w.linkMark(link)
	}
	imgNodes := w.image(img, mark)
	if len(imgNodes) == 0 || imgNodes[0].Type != "image" {
		top.Parent.RemoveChild(top)
		return append(imgNodes, w.sub(root)...)
	}
	im := imgNodes[0]
	if im.Attrs["mediaId"] == nil {
		if m := attachmentIDRe.FindStringSubmatch(sc.Attrs["id"]); m != nil {
			id, _ := strconv.ParseInt(m[1], 10, 64)
			if k, ok := w.ctx.Media.Attachment(id); ok {
				delete(im.Attrs, "src")
				delete(im.Attrs, "width")
				delete(im.Attrs, "height")
				im.Attrs["mediaId"] = k
			}
		}
	}
	align := alignWord(sc.Attrs["align"])
	if align != "" {
		im.Attrs["align"] = align
	} else if a, ok := im.Attrs["align"].(string); ok {
		align = a
	}
	root.RemoveChild(top)
	var capInline []*content.Node
	if c := sc.Attrs["caption"]; c != "" {
		capNodes, _ := xhtml.ParseFragment(strings.NewReader(c), &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body})
		holder := &xhtml.Node{Type: xhtml.ElementNode, Data: "span"}
		for _, n := range capNodes {
			holder.AppendChild(n)
		}
		capInline = w.inlineOnly(holder)
	} else {
		capInline = w.inlineOnly(root)
	}
	if len(capInline) == 0 {
		return []*content.Node{im}
	}
	fig := &content.Node{Type: "figure", Content: []*content.Node{im, {Type: "figcaption", Content: capInline}}}
	if align != "" {
		fig.Attrs = map[string]any{"align": align}
	}
	return []*content.Node{fig}
}

// gallery: explicit ids/include, else the post's attached images in menu_order (§6.3).
func (w *walker) gallery(sc *shortcode.Shortcode) []*content.Node {
	ids := GalleryIDs(sc, w.ctx.PostID, w.ctx.Media)
	if sc.Attrs["ids"] == "" && sc.Attrs["include"] == "" {
		w.flag("gallery_attached", "gallery without ids uses %d attached images", len(ids))
	}
	if strings.Contains(strings.ToLower(sc.Attrs["orderby"]), "rand") {
		w.flag("gallery_random", "orderby=rand kept in stored order")
	}
	size := sc.Attrs["size"]
	if size == "" {
		size = "thumbnail"
	}
	g := &content.Node{Type: "gallery", Attrs: map[string]any{}}
	for _, id := range ids {
		k, ok := w.ctx.Media.Attachment(id)
		if !ok {
			w.flag("missing_media", "gallery attachment %d", id)
			continue
		}
		im := &content.Node{Type: "image", Attrs: map[string]any{"mediaId": k, "alt": ""}}
		if sizeNameRe.MatchString(size) {
			im.Attrs["size"] = size
		}
		g.Content = append(g.Content, im)
	}
	if len(g.Content) == 0 {
		w.flag("gallery_empty", "gallery with no resolvable images")
		return nil
	}
	cols := 3
	if v, err := strconv.Atoi(sc.Attrs["columns"]); err == nil {
		cols = v
	}
	g.Attrs["columns"] = min(max(cols, 1), 9)
	if sizeNameRe.MatchString(size) {
		g.Attrs["size"] = size
	}
	switch sc.Attrs["link"] {
	case "none":
		g.Attrs["link"] = "none"
	case "file":
		g.Attrs["link"] = "media"
	default:
		w.flag("gallery_link", "attachment-page links become media links")
		g.Attrs["link"] = "media"
	}
	return []*content.Node{g}
}

// GalleryIDs lists a [gallery]'s attachment ids: ids/include, else the post's attached images (WordPress order).
func GalleryIDs(sc *shortcode.Shortcode, postID int64, media Media) []int64 {
	var ids []int64
	list := sc.Attrs["ids"]
	if list == "" {
		list = sc.Attrs["include"]
	}
	if list != "" {
		for _, s := range strings.Split(list, ",") {
			if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
				ids = append(ids, id)
			}
		}
		return ids
	}
	exclude := map[int64]bool{}
	for _, s := range strings.Split(sc.Attrs["exclude"], ",") {
		if id, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64); err == nil {
			exclude[id] = true
		}
	}
	parent := postID
	if v, err := strconv.ParseInt(sc.Attrs["id"], 10, 64); err == nil && v > 0 {
		parent = v
	}
	for _, id := range media.AttachedImages(parent) {
		if !exclude[id] {
			ids = append(ids, id)
		}
	}
	if strings.EqualFold(sc.Attrs["order"], "desc") {
		for i, j := 0, len(ids)-1; i < j; i, j = i+1, j-1 {
			ids[i], ids[j] = ids[j], ids[i]
		}
	}
	return ids
}

func (w *walker) embedURL(u string, sc *shortcode.Shortcode) []*content.Node {
	n, ok := EmbedFromURL(u)
	if !ok {
		w.flag("embed_failed", "%s", hostOf(u))
		return linkParagraph(u)
	}
	if sc != nil {
		if v, ok := dim(sc.Attrs["width"]); ok {
			n.Attrs["width"] = v
		}
		if v, ok := dim(sc.Attrs["height"]); ok {
			n.Attrs["height"] = v
		}
	}
	return []*content.Node{n}
}

func linkParagraph(u string) []*content.Node {
	t := &content.Node{Type: "text", Text: u}
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		t.Marks = []*content.Mark{{Type: "link", Attrs: map[string]any{"href": u}}}
	}
	return []*content.Node{{Type: "paragraph", Content: []*content.Node{t}}}
}

func truthy(v string) bool {
	switch strings.ToLower(v) {
	case "1", "on", "true", "yes", "autoplay", "loop", "muted":
		return true
	}
	return false
}

func (w *walker) videoShortcode(sc *shortcode.Shortcode) []*content.Node {
	src := sc.Attrs["src"]
	for _, k := range []string{"mp4", "m4v", "webm", "ogv", "wmv", "flv"} {
		if src == "" {
			src = sc.Attrs[k]
		}
	}
	return w.videoNode(src, sc.Attrs["poster"], map[string]bool{
		"controls": true, "autoplay": truthy(sc.Attrs["autoplay"]), "loop": truthy(sc.Attrs["loop"]), "muted": truthy(sc.Attrs["muted"]),
	})
}

func (w *walker) audio(sc *shortcode.Shortcode) []*content.Node {
	src := sc.Attrs["src"]
	for _, k := range []string{"mp3", "m4a", "ogg", "wav", "wma"} {
		if src == "" {
			src = sc.Attrs[k]
		}
	}
	if src == "" {
		w.flag("element_dropped", "audio without src")
		return nil
	}
	if rel, ok := w.relUpload(src); ok {
		src = rel
	}
	return w.rawHTML("[audio]", `<audio controls src="`+html.EscapeString(src)+`"></audio>`)
}

// relUpload turns an uploads URL into its root-relative legacy path, which AFSC serves directly.
func (w *walker) relUpload(u string) (string, bool) {
	if ref, _, ok := w.ctx.Media.Resolve(u); ok && ref.Key != "" {
		if i := strings.Index(u, "/wp-content/"); i >= 0 {
			return u[i:], true
		}
	}
	return "", false
}

// jetpack converts Jetpack's embed shortcodes; the URL is positional, "=URL" or in url/id attributes.
func (w *walker) jetpack(sc *shortcode.Shortcode) []*content.Node {
	u := sc.First()
	for _, k := range []string{"url", "src", "id"} {
		if u == "" {
			u = sc.Attrs[k]
		}
	}
	u = html.UnescapeString(strings.TrimSpace(u))
	switch sc.Tag {
	case "vimeo":
		if digitsRe.MatchString(u) {
			u = "https://vimeo.com/" + u
		}
	case "tweet":
		if digitsRe.MatchString(u) {
			u = "https://x.com/i/status/" + u
		}
	case "youtube":
		if ytIDRe.MatchString(u) && !strings.Contains(u, ".") {
			u = "https://www.youtube.com/watch?v=" + u
		}
	}
	return w.embedURL(u, sc)
}
