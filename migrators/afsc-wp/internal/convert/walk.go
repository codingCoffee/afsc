package convert

import (
	"slices"
	"strconv"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"afsc/internal/content"
	"afsc/internal/sanitize"

	"afsc/migrators/afsc-wp/internal/blocks"
	"afsc/migrators/afsc-wp/internal/shortcode"
)

type walker struct {
	ctx      *Context
	flags    []Flag
	pages    int
	readMore bool
	scs      []*shortcode.Shortcode
	blks     []*blocks.Block
	synth    map[*content.Node]bool
	err      error
}

// sink collects blocks. Inline content goes into the open text block; a block-level item closes it.
type sink struct {
	w     *walker
	out   []*content.Node
	cur   *content.Node
	tmpl  *content.Node
	fresh bool
}

func (s *sink) open() *content.Node {
	if s.cur != nil {
		return s.cur
	}
	if s.tmpl != nil {
		s.cur = &content.Node{Type: s.tmpl.Type, Attrs: cloneAttrs(s.tmpl.Attrs)}
	} else {
		s.cur = &content.Node{Type: "paragraph"}
	}
	if !s.fresh {
		if s.w.synth == nil {
			s.w.synth = map[*content.Node]bool{}
		}
		s.w.synth[s.cur] = true
	}
	s.fresh = false
	return s.cur
}

func (s *sink) text(t string, marks []*content.Mark) {
	if t == "" || (s.cur == nil && s.tmpl == nil && strings.TrimSpace(t) == "") {
		return
	}
	n := &content.Node{Type: "text", Text: t}
	if len(marks) > 0 {
		n.Marks = sortMarks(marks)
	}
	c := s.open()
	c.Content = append(c.Content, n)
}

func (s *sink) inline(n *content.Node) {
	c := s.open()
	c.Content = append(c.Content, n)
}

func (s *sink) block(ns ...*content.Node) {
	s.flush()
	s.out = append(s.out, ns...)
}

func (s *sink) flush() {
	if s.cur != nil {
		s.out = append(s.out, s.cur)
		s.cur = nil
	}
}

func (s *sink) done() []*content.Node {
	s.flush()
	return s.out
}

func cloneAttrs(a map[string]any) map[string]any {
	if a == nil {
		return nil
	}
	out := make(map[string]any, len(a))
	for k, v := range a {
		out[k] = v
	}
	return out
}

func sortMarks(marks []*content.Mark) []*content.Mark {
	out := append([]*content.Mark{}, marks...)
	slices.SortStableFunc(out, func(a, b *content.Mark) int {
		return slices.Index(content.MarkOrder, a.Type) - slices.Index(content.MarkOrder, b.Type)
	})
	return out
}

func withMark(marks []*content.Mark, m *content.Mark) []*content.Mark {
	out := make([]*content.Mark, 0, len(marks)+1)
	for _, x := range marks {
		if x.Type != m.Type {
			out = append(out, x)
		}
	}
	return append(out, m)
}

// html converts an HTML fragment that may contain shortcodes and block placeholders.
func (w *walker) html(s string) []*content.Node {
	s = w.shortcodes(s)
	nodes, err := html.ParseFragment(strings.NewReader(s), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		w.flag("raw_html", "unparseable html")
		return []*content.Node{w.rawNode(s)}
	}
	sk := &sink{w: w}
	for _, n := range nodes {
		w.node(n, sk, nil)
	}
	return sk.done()
}

// sub converts child nodes in a fresh block context.
func (w *walker) sub(n *html.Node) []*content.Node {
	sk := &sink{w: w}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c, sk, nil)
	}
	return sk.done()
}

// shortcodes replaces handled shortcodes with placeholders; dropped enclosing ones keep their content.
func (w *walker) shortcodes(s string) string {
	if len(w.ctx.Shortcodes) == 0 || !strings.Contains(s, "[") {
		return s
	}
	var b strings.Builder
	for _, seg := range shortcode.Parse(s, w.ctx.Shortcodes) {
		if seg.SC == nil {
			b.WriteString(seg.Text)
			continue
		}
		switch w.ctx.Decide("shortcode", seg.SC.Tag, "keep_text") {
		case "keep_text":
			b.WriteString(seg.SC.Raw)
		case "drop":
			w.flag("shortcode_dropped", "%s", seg.SC.Tag)
			if seg.SC.Enclosing {
				b.WriteString(w.shortcodes(seg.SC.Content))
			}
		default:
			b.WriteString(`<afsc-sc data-i="` + strconv.Itoa(len(w.scs)) + `"></afsc-sc>`)
			w.scs = append(w.scs, seg.SC)
		}
	}
	return b.String()
}

func attr(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

func hasAttr(n *html.Node, key string) bool {
	return slices.ContainsFunc(n.Attr, func(a html.Attribute) bool { return a.Key == key })
}

func classes(n *html.Node) []string { return strings.Fields(attr(n, "class")) }

func placeholderIndex(n *html.Node) int {
	i, err := strconv.Atoi(attr(n, "data-i"))
	if err != nil {
		return -1
	}
	return i
}

var inlineMarks = map[string]string{
	"strong": "bold", "b": "bold", "em": "italic", "i": "italic", "u": "underline", "ins": "underline",
	"s": "strike", "del": "strike", "strike": "strike", "code": "code", "tt": "code", "kbd": "code",
	"samp": "code", "sub": "subscript", "sup": "superscript",
}

var unwrapBlocks = map[string]bool{
	"div": true, "section": true, "article": true, "aside": true, "header": true, "footer": true, "main": true,
	"nav": true, "address": true, "center": true, "hgroup": true, "dl": true, "dt": true, "dd": true, "li": true,
	"tr": true, "td": true, "th": true, "tbody": true, "thead": true, "tfoot": true, "caption": true,
	"figcaption": true, "picture": true, "body": true, "html": true,
}

var rawElements = map[string]bool{
	"script": true, "style": true, "form": true, "object": true, "embed": true, "noscript": true, "canvas": true,
	"svg": true, "math": true, "template": true, "input": true, "select": true, "textarea": true, "button": true,
	"audio": true, "map": true, "applet": true, "marquee": true, "frameset": true, "frame": true, "link": true, "meta": true,
}

func (w *walker) node(n *html.Node, s *sink, marks []*content.Mark) {
	switch n.Type {
	case html.TextNode:
		s.text(n.Data, marks)
		return
	case html.CommentNode:
		w.comment(n.Data, s)
		return
	case html.ElementNode:
	default:
		return
	}
	tag := n.Data
	if m, ok := inlineMarks[tag]; ok {
		next := withMark(marks, &content.Mark{Type: m})
		w.children(n, s, next)
		return
	}
	switch {
	case tag == "afsc-sc":
		if i := placeholderIndex(n); i >= 0 && i < len(w.scs) {
			s.block(w.shortcodeNodes(w.scs[i])...)
		}
	case tag == "afsc-blk":
		if i := placeholderIndex(n); i >= 0 && i < len(w.blks) {
			s.block(w.blockNodes(w.blks[i])...)
		}
	case tag == "p":
		w.textblock(n, s, "paragraph", marks)
	case len(tag) == 2 && tag[0] == 'h' && tag[1] >= '1' && tag[1] <= '6':
		w.textblock(n, s, "heading", marks)
	case tag == "br":
		s.inline(&content.Node{Type: "hardBreak"})
	case tag == "wbr":
	case tag == "a":
		w.link(n, s, marks)
	case tag == "span" || tag == "font":
		w.span(n, s, marks)
	case tag == "img":
		s.block(w.image(n, linkOf(marks))...)
	case tag == "iframe":
		s.block(w.iframe(n)...)
	case tag == "video":
		s.block(w.video(n)...)
	case rawElements[tag]:
		s.block(w.raw(n)...)
	case tag == "ul" || tag == "ol":
		s.block(w.list(n)...)
	case tag == "blockquote":
		s.block(w.blockquote(n))
	case tag == "pre":
		s.block(w.pre(n))
	case tag == "hr":
		s.block(&content.Node{Type: "horizontalRule"})
	case tag == "table":
		s.block(w.table(n)...)
	case tag == "details":
		s.block(w.details(n))
	case tag == "figure":
		s.block(w.figure(n)...)
	case unwrapBlocks[tag]:
		w.divLike(n, s, marks)
	default:
		w.flag("unwrap", "%s", tag)
		w.children(n, s, marks)
	}
}

func (w *walker) children(n *html.Node, s *sink, marks []*content.Mark) {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.node(c, s, marks)
	}
}

func linkOf(marks []*content.Mark) *content.Mark {
	for _, m := range marks {
		if m.Type == "link" {
			return m
		}
	}
	return nil
}

func (w *walker) comment(data string, s *sink) {
	d := strings.TrimSpace(data)
	switch {
	case strings.HasPrefix(d, "more"):
		if w.readMore {
			w.flag("readmore_extra", "later <!--more--> dropped")
			return
		}
		w.readMore = true
		s.block(&content.Node{Type: "readMore"})
	case d == "nextpage":
		w.pages++
		w.flag("nextpage", "%s", w.ctx.Decide("marker", "nextpage", "merge"))
	case d == "noteaser":
	default:
		w.flag("comment", "html comment dropped")
	}
}

// textblock converts <p> and <h1>–<h6>. An element that produces nothing still yields one empty block.
func (w *walker) textblock(n *html.Node, s *sink, typ string, marks []*content.Mark) {
	s.flush()
	attrs := map[string]any{}
	if typ == "heading" {
		attrs["level"] = int(n.Data[1] - '0')
		if id := attr(n, "id"); id != "" {
			if headingIDRe.MatchString(id) {
				attrs["id"] = id
			} else {
				w.flag("attr_dropped", "heading id %q", id)
			}
		}
	}
	if a := w.textAlign(n); a != "" {
		attrs["textAlign"] = a
	}
	if c := w.keepClasses(n, nil); c != "" {
		attrs["classes"] = c
	}
	prevT, prevF := s.tmpl, s.fresh
	s.tmpl, s.fresh = &content.Node{Type: typ, Attrs: attrs}, true
	start := len(s.out)
	w.children(n, s, marks)
	s.flush()
	if len(s.out) == start {
		s.out = append(s.out, &content.Node{Type: typ, Attrs: cloneAttrs(attrs)})
	}
	s.tmpl, s.fresh = prevT, prevF
}

func (w *walker) divLike(n *html.Node, s *sink, marks []*content.Mark) {
	if len(marks) > 0 {
		w.children(n, s, marks)
		return
	}
	if n.Data == "div" || n.Data == "section" || n.Data == "aside" {
		if c := w.keepClasses(n, nil); c != "" {
			kids := w.sub(n)
			if len(kids) > 0 {
				s.block(&content.Node{Type: "container", Attrs: map[string]any{"classes": c}, Content: kids})
			}
			return
		}
	}
	if n.Data != "body" {
		w.flag("unwrap", "%s", n.Data)
	}
	s.flush()
	w.children(n, s, nil)
	s.flush()
}

func (w *walker) link(n *html.Node, s *sink, marks []*content.Mark) {
	if m := w.linkMark(n); m != nil {
		marks = withMark(marks, m)
	}
	w.children(n, s, marks)
}

// linkMark builds the link mark for an <a>, or nil (flagged) when it has no usable href.
func (w *walker) linkMark(n *html.Node) *content.Mark {
	href := strings.TrimSpace(attr(n, "href"))
	if href == "" {
		if hasAttr(n, "name") || hasAttr(n, "id") {
			w.flag("anchor", "a name/id without href dropped")
		}
		return nil
	}
	out, ok := w.ctx.Links.Rewrite(href)
	if !ok || !sanitize.SafeURL(out) {
		w.flag("link_dropped", "%s", truncate(href, 80))
		return nil
	}
	attrs := map[string]any{"href": out}
	switch t := attr(n, "target"); t {
	case "":
	case "_blank", "_self":
		attrs["target"] = t
	default:
		w.flag("attr_dropped", "link target %q", t)
	}
	if rel := strings.ToLower(strings.Join(strings.Fields(attr(n, "rel")), " ")); rel != "" {
		if relRe.MatchString(rel) {
			attrs["rel"] = rel
		} else {
			w.flag("attr_dropped", "link rel %q", rel)
		}
	}
	if t := attr(n, "title"); t != "" {
		attrs["title"] = t
	}
	return &content.Mark{Type: "link", Attrs: attrs}
}

func (w *walker) span(n *html.Node, s *sink, marks []*content.Mark) {
	style := map[string]any{}
	if n.Data == "font" {
		if c, ok := cssColor(attr(n, "color")); ok {
			style["color"] = c
		}
		w.flag("unwrap", "font")
	}
	for _, d := range parseStyle(attr(n, "style")) {
		switch d.prop {
		case "color", "background-color":
			if c, ok := cssColor(d.val); ok {
				style[map[string]string{"color": "color", "background-color": "backgroundColor"}[d.prop]] = c
			} else {
				w.flag("style", "%s: %s", d.prop, d.val)
			}
		case "background":
			if c, ok := cssColor(d.val); ok {
				style["backgroundColor"] = c
			} else {
				w.flag("style", "background")
			}
		default:
			w.flag("style", "%s", d.prop)
		}
	}
	for _, c := range classes(n) {
		w.flag("class", "%s", c)
	}
	if len(style) > 0 {
		marks = withMark(marks, &content.Mark{Type: "textStyle", Attrs: style})
	}
	w.children(n, s, marks)
}

func (w *walker) list(n *html.Node) []*content.Node {
	typ := "bulletList"
	attrs := map[string]any{}
	if n.Data == "ol" {
		typ = "orderedList"
		if v := attr(n, "start"); v != "" {
			if i, err := strconv.Atoi(v); err == nil && i >= 0 && i <= 1000000 {
				attrs["start"] = i
			}
		}
		if hasAttr(n, "reversed") || attr(n, "type") != "" {
			w.flag("attr_dropped", "ol reversed/type")
		}
	}
	list := &content.Node{Type: typ}
	if len(attrs) > 0 {
		list.Attrs = attrs
	}
	var stray *sink
	flushStray := func() {
		if stray != nil {
			if kids := stray.done(); len(kids) > 0 {
				list.Content = append(list.Content, &content.Node{Type: "listItem", Content: kids})
			}
			stray = nil
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "li" {
			flushStray()
			if hasAttr(c, "value") {
				w.flag("attr_dropped", "li value")
			}
			list.Content = append(list.Content, &content.Node{Type: "listItem", Content: orEmpty(w.sub(c))})
			continue
		}
		if stray == nil {
			stray = &sink{w: w}
		}
		w.node(c, stray, nil)
	}
	flushStray()
	if len(list.Content) == 0 {
		return nil
	}
	return []*content.Node{list}
}

func orEmpty(n []*content.Node) []*content.Node {
	if len(n) == 0 {
		return []*content.Node{{Type: "paragraph"}}
	}
	return n
}

func (w *walker) blockquote(n *html.Node) *content.Node {
	bq := &content.Node{Type: "blockquote", Content: orEmpty(w.sub(n))}
	if c := w.keepClasses(n, nil); c != "" {
		bq.Attrs = map[string]any{"classes": c}
	}
	return bq
}

func (w *walker) pre(n *html.Node) *content.Node {
	var b strings.Builder
	lang := langOf(n)
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			switch {
			case c.Type == html.TextNode:
				b.WriteString(c.Data)
			case c.Type == html.ElementNode && c.Data == "br":
				b.WriteString("\n")
			case c.Type == html.ElementNode:
				if c.Data == "code" && lang == "" {
					lang = langOf(c)
				}
				walk(c)
			}
		}
	}
	walk(n)
	cb := &content.Node{Type: "codeBlock"}
	if lang != "" {
		cb.Attrs = map[string]any{"language": lang}
	}
	if t := b.String(); t != "" {
		cb.Content = []*content.Node{{Type: "text", Text: t}}
	}
	return cb
}

func langOf(n *html.Node) string {
	for _, c := range classes(n) {
		for _, p := range []string{"language-", "lang-"} {
			if l, ok := strings.CutPrefix(c, p); ok && codeLangRe.MatchString(l) {
				return l
			}
		}
	}
	return ""
}

func (w *walker) table(n *html.Node) []*content.Node {
	t := &content.Node{Type: "table"}
	var after []*content.Node
	var rows func(*html.Node)
	rows = func(x *html.Node) {
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if c.Type != html.ElementNode {
				continue
			}
			switch c.Data {
			case "thead", "tbody", "tfoot":
				rows(c)
			case "tr":
				if r := w.row(c); r != nil {
					t.Content = append(t.Content, r)
				}
			case "caption":
				w.flag("table_caption", "moved after the table")
				after = append(after, w.sub(c)...)
			case "colgroup", "col":
			default:
				w.flag("unwrap", "table > %s", c.Data)
				after = append(after, w.sub(c)...)
			}
		}
	}
	rows(n)
	if len(t.Content) == 0 {
		w.flag("table_empty", "table without rows")
		return after
	}
	return append([]*content.Node{t}, after...)
}

func (w *walker) row(tr *html.Node) *content.Node {
	r := &content.Node{Type: "tableRow"}
	for c := tr.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode || (c.Data != "td" && c.Data != "th") {
			continue
		}
		typ := "tableCell"
		if c.Data == "th" {
			typ = "tableHeader"
		}
		cell := &content.Node{Type: typ, Content: orEmpty(w.sub(c))}
		for _, k := range []string{"colspan", "rowspan"} {
			if v, err := strconv.Atoi(attr(c, k)); err == nil && v > 1 && v <= 1000 {
				if cell.Attrs == nil {
					cell.Attrs = map[string]any{}
				}
				cell.Attrs[k] = v
			}
		}
		r.Content = append(r.Content, cell)
	}
	if len(r.Content) == 0 {
		return nil
	}
	return r
}

func (w *walker) details(n *html.Node) *content.Node {
	summary := &content.Node{Type: "detailsSummary"}
	body := &sink{w: w}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "summary" && summary.Content == nil {
			summary.Content = w.inlineOnly(c)
			continue
		}
		w.node(c, body, nil)
	}
	return &content.Node{Type: "details", Content: []*content.Node{summary, {Type: "detailsContent", Content: orEmpty(body.done())}}}
}

// inlineOnly converts children into inline content; block items are flattened to their text so nothing is lost.
func (w *walker) inlineOnly(n *html.Node) []*content.Node {
	sk := &sink{w: w, tmpl: &content.Node{Type: "paragraph"}, fresh: true}
	w.children(n, sk, nil)
	var out []*content.Node
	for i, b := range sk.done() {
		if i > 0 && len(out) > 0 {
			out = append(out, &content.Node{Type: "hardBreak"})
		}
		if b.Type == "paragraph" || b.Type == "heading" {
			out = append(out, b.Content...)
			continue
		}
		w.flag("block_in_inline", "%s flattened to text", b.Type)
		if t := nodeText(b); t != "" {
			out = append(out, &content.Node{Type: "text", Text: t})
		}
	}
	return out
}

func nodeText(n *content.Node) string {
	var b strings.Builder
	content.Walk(n, func(x *content.Node) {
		if x.Type == "text" {
			b.WriteString(x.Text)
		}
	})
	return b.String()
}

func truncate(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
