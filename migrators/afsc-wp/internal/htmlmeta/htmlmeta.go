// Package htmlmeta extracts the SEO-relevant parts of a page (migration plan §4) and the article body by a
// configurable CSS selector.
package htmlmeta

import (
	"encoding/json"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

type Meta struct {
	Title       string            `json:"title"`
	Description string            `json:"description"`
	Canonical   string            `json:"canonical"`
	Robots      string            `json:"robots"`
	OG          map[string]string `json:"og,omitempty"`
	Twitter     map[string]string `json:"twitter,omitempty"`
	JSONLD      []string          `json:"jsonld,omitempty"`
	H1          []string          `json:"h1,omitempty"`
	Prev        string            `json:"prev,omitempty"`
	Next        string            `json:"next,omitempty"`
	Body        string            `json:"body,omitempty"`
	WordCount   int               `json:"word_count"`
	Images      []string          `json:"images,omitempty"`
}

// Parse reads a full HTML page. selector picks the article body ("" skips it).
func Parse(page []byte, selector string) (*Meta, error) {
	doc, err := html.Parse(strings.NewReader(string(page)))
	if err != nil {
		return nil, err
	}
	m := &Meta{OG: map[string]string{}, Twitter: map[string]string{}}
	var walk func(*html.Node, bool)
	walk = func(n *html.Node, inHead bool) {
		if n.Type == html.ElementNode {
			switch n.Data {
			case "head":
				inHead = true
			case "title":
				if inHead && m.Title == "" {
					m.Title = collapse(text(n))
				}
			case "meta":
				name, prop, val := strings.ToLower(attr(n, "name")), strings.ToLower(attr(n, "property")), attr(n, "content")
				switch {
				case name == "description":
					m.Description = collapse(val)
				case name == "robots":
					m.Robots = strings.ToLower(collapse(val))
				case strings.HasPrefix(prop, "og:"):
					m.OG[prop] = val
				case strings.HasPrefix(name, "twitter:"):
					m.Twitter[name] = val
				}
			case "link":
				rel := strings.ToLower(attr(n, "rel"))
				switch rel {
				case "canonical":
					m.Canonical = attr(n, "href")
				case "prev":
					m.Prev = attr(n, "href")
				case "next":
					m.Next = attr(n, "href")
				}
			case "script":
				if strings.Contains(strings.ToLower(attr(n, "type")), "ld+json") {
					m.JSONLD = append(m.JSONLD, text(n))
				}
			case "h1":
				m.H1 = append(m.H1, collapse(text(n)))
			case "img":
				if src := attr(n, "src"); src != "" {
					m.Images = append(m.Images, src)
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c, inHead)
		}
	}
	walk(doc, false)
	if selector != "" {
		if body := Select(doc, selector); body != nil {
			var b strings.Builder
			for c := body.FirstChild; c != nil; c = c.NextSibling {
				html.Render(&b, c)
			}
			m.Body = b.String()
			m.WordCount = len(strings.Fields(text(body)))
		}
	}
	return m, nil
}

func attr(n *html.Node, k string) string {
	for _, a := range n.Attr {
		if a.Key == k {
			return a.Val
		}
	}
	return ""
}

func text(n *html.Node) string {
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return b.String()
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

// Types returns the sorted set of JSON-LD @types, walking @graph arrays.
func (m *Meta) Types() []string {
	set := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			switch t := x["@type"].(type) {
			case string:
				set[t] = true
			case []any:
				for _, e := range t {
					if s, ok := e.(string); ok {
						set[s] = true
					}
				}
			}
			for k, e := range x {
				if k == "@graph" || k == "itemListElement" || k == "mainEntity" {
					walk(e)
				}
			}
		case []any:
			for _, e := range x {
				walk(e)
			}
		}
	}
	for _, raw := range m.JSONLD {
		var v any
		if json.Unmarshal([]byte(raw), &v) == nil {
			walk(v)
		}
	}
	out := make([]string, 0, len(set))
	for t := range set {
		out = append(out, t)
	}
	slices.Sort(out)
	return out
}

// Headline returns the headline or name of the first Article-like JSON-LD node.
func (m *Meta) Headline() string {
	for _, raw := range m.JSONLD {
		var v any
		if json.Unmarshal([]byte(raw), &v) != nil {
			continue
		}
		var nodes []any
		switch x := v.(type) {
		case map[string]any:
			if g, ok := x["@graph"].([]any); ok {
				nodes = g
			} else {
				nodes = []any{x}
			}
		case []any:
			nodes = x
		}
		for _, n := range nodes {
			if o, ok := n.(map[string]any); ok {
				t, _ := o["@type"].(string)
				if strings.Contains(t, "Article") || t == "BlogPosting" || t == "NewsArticle" {
					if h, ok := o["headline"].(string); ok {
						return h
					}
					if h, ok := o["name"].(string); ok {
						return h
					}
				}
			}
		}
	}
	return ""
}

// RobotsSet splits a robots directive into a sorted set.
func RobotsSet(s string) []string {
	var out []string
	for _, p := range strings.Split(strings.ToLower(s), ",") {
		if p = strings.TrimSpace(p); p != "" && !slices.Contains(out, p) {
			out = append(out, p)
		}
	}
	slices.Sort(out)
	return out
}
