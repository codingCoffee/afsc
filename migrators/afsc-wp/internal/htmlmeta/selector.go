package htmlmeta

import (
	"strings"

	"golang.org/x/net/html"
)

// compound is one selector step: tag, #id, .classes and [attr] / [attr=value].
type compound struct {
	tag     string
	id      string
	classes []string
	attrs   [][2]string
}

// parseSelector supports descendant combinators and comma groups, enough for content-area selectors such as
// ".entry-content", "article .post-body", "#content div.entry".
func parseSelector(sel string) [][]compound {
	var groups [][]compound
	for _, g := range strings.Split(sel, ",") {
		var steps []compound
		for _, part := range strings.Fields(g) {
			if part == ">" {
				continue
			}
			steps = append(steps, parseCompound(part))
		}
		if len(steps) > 0 {
			groups = append(groups, steps)
		}
	}
	return groups
}

func parseCompound(s string) compound {
	var c compound
	i := 0
	read := func() string {
		start := i
		for i < len(s) && !strings.ContainsRune(".#[", rune(s[i])) {
			i++
		}
		return s[start:i]
	}
	c.tag = strings.ToLower(read())
	for i < len(s) {
		switch s[i] {
		case '.':
			i++
			c.classes = append(c.classes, read())
		case '#':
			i++
			c.id = read()
		case '[':
			end := strings.IndexByte(s[i:], ']')
			if end < 0 {
				return c
			}
			k, v, _ := strings.Cut(s[i+1:i+end], "=")
			c.attrs = append(c.attrs, [2]string{k, strings.Trim(v, `"'`)})
			i += end + 1
		default:
			i++
		}
	}
	return c
}

func (c compound) match(n *html.Node) bool {
	if n.Type != html.ElementNode || (c.tag != "" && c.tag != "*" && n.Data != c.tag) {
		return false
	}
	if c.id != "" && attr(n, "id") != c.id {
		return false
	}
	cls := strings.Fields(attr(n, "class"))
	for _, want := range c.classes {
		found := false
		for _, have := range cls {
			found = found || have == want
		}
		if !found {
			return false
		}
	}
	for _, a := range c.attrs {
		has := false
		for _, at := range n.Attr {
			if at.Key == a[0] && (a[1] == "" || at.Val == a[1]) {
				has = true
			}
		}
		if !has {
			return false
		}
	}
	return true
}

// matches checks steps right to left against n and its ancestors.
func matches(n *html.Node, steps []compound) bool {
	if !steps[len(steps)-1].match(n) {
		return false
	}
	i := len(steps) - 2
	for p := n.Parent; p != nil && i >= 0; p = p.Parent {
		if steps[i].match(p) {
			i--
		}
	}
	return i < 0
}

// Select returns the first node in document order matching sel.
func Select(root *html.Node, sel string) *html.Node {
	groups := parseSelector(sel)
	var found *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if found != nil {
			return
		}
		for _, g := range groups {
			if matches(n, g) {
				found = n
				return
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(root)
	return found
}
