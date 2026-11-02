package convert

import (
	"strconv"
	"strings"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"afsc/internal/content"

	"afsc/migrators/afsc-wp/internal/blocks"
)

// BlockHandlers are block names with dedicated handling; other core/* blocks convert by walking their HTML.
var BlockHandlers = map[string]bool{
	"core/more": true, "core/nextpage": true, "core/html": true, "core/embed": true, "core/gallery": true,
	"core/image": true, "core/spacer": true, "core/shortcode": true, "core/freeform": true,
}

// blockParts joins parts into one HTML string with blocks as placeholders, so wrapper markup stays balanced.
func (w *walker) blockParts(parts []blocks.Part) []*content.Node {
	var b strings.Builder
	for _, p := range parts {
		if p.Block == nil {
			b.WriteString(p.HTML)
			continue
		}
		b.WriteString(`<afsc-blk data-i="` + strconv.Itoa(len(w.blks)) + `"></afsc-blk>`)
		w.blks = append(w.blks, p.Block)
	}
	return w.html(b.String())
}

func fullHTML(b *blocks.Block) string {
	var s strings.Builder
	for _, p := range b.Parts {
		if p.Block != nil {
			s.WriteString(fullHTML(p.Block))
		} else {
			s.WriteString(p.HTML)
		}
	}
	return s.String()
}

func defaultBlockDecision(name string) string {
	if strings.HasPrefix(name, "core/") || strings.HasPrefix(name, "core-embed/") {
		return "convert"
	}
	return "raw_html"
}

func (w *walker) blockNodes(b *blocks.Block) []*content.Node {
	name := b.Name
	if strings.HasPrefix(name, "core-embed/") {
		name = "core/embed"
	}
	switch name {
	case "core/more":
		if w.readMore {
			w.flag("readmore_extra", "later more block dropped")
			return nil
		}
		w.readMore = true
		return []*content.Node{{Type: "readMore"}}
	case "core/nextpage":
		w.pages++
		w.flag("nextpage", "%s", w.ctx.Decide("marker", "nextpage", "merge"))
		return nil
	case "core/spacer":
		w.flag("block_dropped", "core/spacer")
		return nil
	}
	if w.ctx.Decide("block", b.Name, defaultBlockDecision(b.Name)) == "raw_html" {
		h := fullHTML(b)
		if strings.TrimSpace(h) == "" {
			w.flag("dynamic_block", "%s has no saved HTML (rendered by PHP at request time)", b.Name)
			return nil
		}
		return w.rawHTML(b.Name, h)
	}
	switch name {
	case "core/html":
		return w.rawHTML(b.Name, fullHTML(b))
	case "core/embed":
		return w.embedBlock(b)
	case "core/gallery":
		return w.galleryBlock(b)
	case "core/image":
		out := w.blockParts(b.Parts)
		w.patchImage(out, b)
		return out
	}
	out := w.blockParts(b.Parts)
	if len(out) == 0 && len(b.Parts) == 0 {
		w.flag("dynamic_block", "%s has no saved HTML (rendered by PHP at request time)", b.Name)
	}
	return out
}

func num(v any) int64 {
	switch x := v.(type) {
	case float64:
		return int64(x)
	case string:
		n, _ := strconv.ParseInt(x, 10, 64)
		return n
	}
	return 0
}

// patchImage fills mediaId and size from the image block's attributes when the markup lacked them.
func (w *walker) patchImage(nodes []*content.Node, b *blocks.Block) {
	for _, n := range nodes {
		content.Walk(n, func(x *content.Node) {
			if x.Type != "image" {
				return
			}
			if x.Attrs["mediaId"] == nil {
				if k, ok := w.ctx.Media.Attachment(num(b.Attrs["id"])); ok {
					delete(x.Attrs, "src")
					delete(x.Attrs, "width")
					delete(x.Attrs, "height")
					x.Attrs["mediaId"] = k
				}
			}
			if s, ok := b.Attrs["sizeSlug"].(string); ok && x.Attrs["size"] == nil && x.Attrs["mediaId"] != nil && sizeNameRe.MatchString(s) {
				x.Attrs["size"] = s
			}
		})
	}
}

func (w *walker) embedBlock(b *blocks.Block) []*content.Node {
	u, _ := b.Attrs["url"].(string)
	if u == "" {
		u = strings.TrimSpace(visible(fullHTML(b)))
	}
	n, ok := EmbedFromURL(u)
	if !ok {
		return w.oembedFallback(u, fullHTML(b))
	}
	if c := blockCaption(fullHTML(b)); c != "" {
		n.Attrs["caption"] = c
	}
	return []*content.Node{n}
}

// oembedFallback handles an embed AFSC has no provider for, per the oembed decision for its host.
func (w *walker) oembedFallback(u, fallbackHTML string) []*content.Node {
	host := hostOf(u)
	if w.ctx.Decide("oembed", host, "link") == "raw_html" {
		if w.ctx.OEmbed != nil {
			if status, h := w.ctx.OEmbed(u); status == "embedded" && h != "" {
				return w.rawHTML("oembed "+host, h)
			}
		}
		if strings.TrimSpace(fallbackHTML) != "" {
			return w.rawHTML("oembed "+host, fallbackHTML)
		}
	}
	w.flag("embed_failed", "%s kept as a link", host)
	return linkParagraph(u)
}

func blockCaption(h string) string {
	nodes, err := xhtml.ParseFragment(strings.NewReader(h), &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return ""
	}
	var out string
	var walk func(*xhtml.Node)
	walk = func(n *xhtml.Node) {
		if n.Type == xhtml.ElementNode && n.Data == "figcaption" && out == "" {
			out = visible(outerHTML(n))
			return
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return out
}

// galleryBlock reads nested image blocks (WP 5.9+) or the older ul.blocks-gallery-grid markup.
func (w *walker) galleryBlock(b *blocks.Block) []*content.Node {
	g := &content.Node{Type: "gallery", Attrs: map[string]any{}}
	var after []*content.Node
	collect := func(nodes []*content.Node) {
		for _, n := range nodes {
			switch n.Type {
			case "image":
				g.Content = append(g.Content, n)
			case "figure":
				g.Content = append(g.Content, n.Content[0])
				w.flag("gallery_caption", "per-image caption dropped from gallery; text kept after it")
				after = append(after, &content.Node{Type: "paragraph", Content: n.Content[1].Content})
			default:
				after = append(after, n)
			}
		}
	}
	inner := b.Inner()
	if len(inner) > 0 {
		for _, ib := range inner {
			collect(w.blockNodes(ib))
		}
		if c := blockCaption(b.InnerHTML()); c != "" {
			after = append(after, textParagraph(c)...)
			w.flag("gallery_caption", "gallery caption kept as a paragraph")
		}
	} else {
		var nodes []*content.Node
		for _, n := range w.html(fullHTML(b)) {
			content.Walk(n, func(x *content.Node) {
				if x.Type == "image" || x.Type == "figure" {
					nodes = append(nodes, x)
				}
			})
		}
		collect(dedupeFigures(nodes))
	}
	if len(g.Content) == 0 {
		w.flag("gallery_empty", "gallery block with no images")
		return after
	}
	cols := int(num(b.Attrs["columns"]))
	if cols == 0 {
		cols = min(3, len(g.Content))
	}
	g.Attrs["columns"] = min(max(cols, 1), 9)
	switch b.Attrs["linkTo"] {
	case "media", "file":
		g.Attrs["link"] = "media"
	case "attachment", "post":
		g.Attrs["link"] = "media"
		w.flag("gallery_link", "attachment-page links become media links")
	default:
		g.Attrs["link"] = "none"
	}
	if s, ok := b.Attrs["sizeSlug"].(string); ok && sizeNameRe.MatchString(s) {
		g.Attrs["size"] = s
	}
	for _, im := range g.Content {
		delete(im.Attrs, "href")
		delete(im.Attrs, "linkTarget")
	}
	return append([]*content.Node{g}, after...)
}

// dedupeFigures drops images already counted inside a collected figure.
func dedupeFigures(nodes []*content.Node) []*content.Node {
	inFig := map[*content.Node]bool{}
	for _, n := range nodes {
		if n.Type == "figure" {
			inFig[n.Content[0]] = true
		}
	}
	var out []*content.Node
	for _, n := range nodes {
		if !inFig[n] {
			out = append(out, n)
		}
	}
	return out
}
