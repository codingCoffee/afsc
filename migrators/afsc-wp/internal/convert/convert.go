// Package convert turns WordPress-filtered post HTML into an AFSC ProseMirror document (migration plan §6).
// Every step is a pure function of its inputs; the Context carries lookups, never I/O.
package convert

import (
	"fmt"
	"sort"
	"strings"

	"afsc/internal/content"

	"afsc/migrators/afsc-wp/internal/blocks"
	"afsc/migrators/afsc-wp/internal/mediaidx"
)

type Media interface {
	Resolve(url string) (mediaidx.Ref, string, bool)
	IsUpload(url string) bool
	Attachment(id int64) (string, bool)
	AttachedImages(parent int64) []int64
}

type Links interface {
	// Rewrite returns the href to store; ok is false when the link must be dropped (unsafe scheme).
	Rewrite(href string) (string, bool)
}

type Context struct {
	PostID         int64
	Media          Media
	Links          Links
	AllowedClasses map[string]bool
	// Decide returns the confirmed decision for kind/name, or def when the decision table has no item.
	Decide func(kind, name, def string) string
	// Shortcodes are the tags parsed as shortcodes (every decision except keep_text).
	Shortcodes map[string]bool
	// RenderPHP returns wp-filter.php's rendering of one shortcode (looked up, not executed here).
	RenderPHP func(tag, raw string) (string, bool)
	// OEmbed reports what WordPress did with a bare URL ("embedded", "unknown" or "" when uncached) and
	// the cached embed HTML.
	OEmbed func(url string) (status, html string)
	// SiteURL makes root-relative URLs absolute for external-src fallbacks.
	SiteURL string
}

type Flag struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail,omitempty"`
}

type Result struct {
	Doc   *content.Node
	Flags []Flag
	// Pages counts <!--nextpage--> sections (1 when there is none).
	Pages int
}

// Kind classifies content: classic, blocks or mixed (§6.1).
func Kind(raw string) string {
	if !blocks.HasBlocks(raw) {
		return "classic"
	}
	for _, p := range blocks.Parse(raw) {
		if p.Block == nil && strings.TrimSpace(stripComments(p.HTML)) != "" {
			return "mixed"
		}
	}
	return "blocks"
}

func stripComments(s string) string {
	for {
		i := strings.Index(s, "<!--")
		if i < 0 {
			return s
		}
		j := strings.Index(s[i:], "-->")
		if j < 0 {
			return s[:i]
		}
		s = s[:i] + s[i+j+3:]
	}
}

// Convert turns filtered HTML into a document. Text is never dropped: anything unmappable becomes a flagged
// rawHtml node.
func Convert(filtered string, ctx *Context) (*Result, error) {
	if ctx.Decide == nil {
		ctx.Decide = func(_, _, def string) string { return def }
	}
	w := &walker{ctx: ctx, pages: 1}
	var nodes []*content.Node
	if blocks.HasBlocks(filtered) {
		nodes = w.blockParts(blocks.Parse(filtered))
	} else {
		nodes = w.html(filtered)
	}
	nodes = normalize(nodes, w)
	if len(nodes) == 0 {
		nodes = []*content.Node{{Type: "paragraph"}}
	}
	doc := &content.Node{Type: "doc", Content: nodes}
	if w.err != nil {
		return nil, w.err
	}
	sortFlags(w.flags)
	return &Result{Doc: doc, Flags: w.flags, Pages: w.pages}, nil
}

func sortFlags(f []Flag) {
	sort.SliceStable(f, func(i, j int) bool {
		if f[i].Kind != f[j].Kind {
			return f[i].Kind < f[j].Kind
		}
		return f[i].Detail < f[j].Detail
	})
}

func (w *walker) flag(kind, format string, args ...any) {
	w.flags = append(w.flags, Flag{Kind: kind, Detail: fmt.Sprintf(format, args...)})
}
