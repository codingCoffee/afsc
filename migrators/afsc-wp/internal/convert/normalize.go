package convert

import (
	"reflect"
	"regexp"
	"strings"

	"afsc/internal/content"
)

var (
	wsRe      = regexp.MustCompile(`[ \t\n\r\f]+`)
	bareURLRe = regexp.MustCompile(`^https?://[^\s<>"]+$`)
)

// normalize collapses whitespace the way a browser renders it, removes empty blocks and turns bare provider
// URLs into embeds (§6.4, §6.7).
func normalize(nodes []*content.Node, w *walker) []*content.Node {
	var out []*content.Node
	for _, n := range nodes {
		switch n.Type {
		case "paragraph", "heading":
			n.Content = normInline(n.Content)
			if blank(n.Content) {
				switch {
				case w.synth[n]:
					continue
				case n.Type == "heading":
					w.flag("empty_block", "empty heading removed")
					continue
				case w.ctx.Decide("empty_paragraphs", "empty_paragraphs", "drop") == "drop":
					w.flag("empty_block", "empty paragraph removed")
					continue
				}
			}
			if n.Type == "paragraph" {
				if e, ok := w.oembed(n); ok {
					out = append(out, e...)
					continue
				}
			}
		case "figure":
			if len(n.Content) == 2 {
				n.Content[1].Content = normInline(n.Content[1].Content)
				if blank(n.Content[1].Content) {
					out = append(out, n.Content[0])
					continue
				}
			}
		case "details":
			n.Content[0].Content = normInline(n.Content[0].Content)
			n.Content[1].Content = orEmpty(normalize(n.Content[1].Content, w))
		case "blockquote", "listItem", "tableCell", "tableHeader", "detailsContent":
			n.Content = orEmpty(normalize(n.Content, w))
		case "bulletList", "orderedList", "table", "tableRow":
			n.Content = normalize(n.Content, w)
		case "container":
			n.Content = normalize(n.Content, w)
			if len(n.Content) == 0 {
				continue
			}
		}
		out = append(out, n)
	}
	return out
}

func blank(ns []*content.Node) bool {
	for _, n := range ns {
		if n.Type == "text" && strings.TrimSpace(strings.ReplaceAll(n.Text, "\u00a0", " ")) != "" {
			return false
		}
		if n.Type != "text" && n.Type != "hardBreak" {
			return false
		}
	}
	return true
}

// normInline merges same-marked text, collapses whitespace and trims it at line edges.
func normInline(ns []*content.Node) []*content.Node {
	var merged []*content.Node
	for _, n := range ns {
		if n.Type == "text" {
			n.Text = wsRe.ReplaceAllString(n.Text, " ")
			if last := len(merged) - 1; last >= 0 && merged[last].Type == "text" && reflect.DeepEqual(merged[last].Marks, n.Marks) {
				merged[last] = &content.Node{Type: "text", Text: merged[last].Text + n.Text, Marks: n.Marks}
				merged[last].Text = wsRe.ReplaceAllString(merged[last].Text, " ")
				continue
			}
		}
		merged = append(merged, n)
	}
	lineStart := true
	for i, n := range merged {
		switch n.Type {
		case "hardBreak":
			if i > 0 && merged[i-1].Type == "text" {
				merged[i-1].Text = strings.TrimRight(merged[i-1].Text, " ")
			}
			lineStart = true
		case "text":
			if lineStart {
				n.Text = strings.TrimLeft(n.Text, " ")
			}
			if i > 0 && merged[i-1].Type == "text" && strings.HasSuffix(merged[i-1].Text, " ") {
				n.Text = strings.TrimLeft(n.Text, " ")
			}
			lineStart = n.Text == "" && lineStart
		default:
			lineStart = false
		}
	}
	if last := len(merged) - 1; last >= 0 && merged[last].Type == "text" {
		merged[last].Text = strings.TrimRight(merged[last].Text, " ")
	}
	var out []*content.Node
	for _, n := range merged {
		if n.Type == "text" && n.Text == "" {
			continue
		}
		out = append(out, n)
	}
	for len(out) > 0 && out[len(out)-1].Type == "hardBreak" {
		out = out[:len(out)-1]
	}
	for i := len(out) - 1; i >= 0; i-- {
		if out[i].Type == "text" {
			out[i].Text = strings.TrimRight(out[i].Text, " ")
			if out[i].Text == "" {
				out = append(out[:i], out[i+1:]...)
			}
			break
		}
	}
	return out
}

// oembed turns a paragraph holding only a bare URL into an embed, as WordPress's autoembed did.
func (w *walker) oembed(p *content.Node) ([]*content.Node, bool) {
	if len(p.Content) != 1 || p.Content[0].Type != "text" || len(p.Content[0].Marks) > 0 {
		return nil, false
	}
	u := strings.TrimSpace(p.Content[0].Text)
	if !bareURLRe.MatchString(u) {
		return nil, false
	}
	status := ""
	if w.ctx.OEmbed != nil {
		status, _ = w.ctx.OEmbed(u)
	}
	if status == "unknown" {
		return nil, false
	}
	if e, ok := EmbedFromURL(u); ok {
		w.flag("oembed", "%s", e.Attrs["provider"])
		return []*content.Node{e}, true
	}
	if status == "embedded" {
		return w.oembedFallback(u, ""), true
	}
	return nil, false
}
