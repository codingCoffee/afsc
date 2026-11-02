package transform

import (
	"net/netip"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/records"
)

var commentStatus = map[string]string{"1": "approved", "0": "pending", "spam": "spam", "trash": "trash", "post-trashed": "trash"}

func (s *state) comments() []*records.Comment {
	var out []*records.Comment
	imported := map[int64]string{}
	for _, c := range s.ds.Comments {
		key := s.key("comment", c.ID)
		if c.Type != "" && c.Type != "comment" {
			s.rep.skip("comments", "type "+c.Type, key)
			continue
		}
		pk, ok := s.postKey[c.PostID]
		if !ok {
			s.rep.skip("comments", "post not imported", key)
			continue
		}
		st := commentStatus[c.Approved]
		if st == "" {
			s.rep.skip("comments", "status "+c.Approved, key)
			continue
		}
		body := CommentText(c.Content)
		if body == "" {
			s.rep.skip("comments", "empty body", key)
			continue
		}
		created, _ := extract.UTC(c.DateGMT, c.Date, s.loc)
		if created.IsZero() {
			s.rep.skip("comments", "no date", key)
			continue
		}
		rec := &records.Comment{LegacyKey: key, Post: pk, AuthorName: strings.TrimSpace(c.Author),
			AuthorEmail: strings.TrimSpace(c.AuthorEmail), UserAgent: c.Agent, Body: body, Status: st, CreatedAt: &created}
		if u := strings.TrimSpace(c.AuthorURL); strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
			rec.AuthorURL = u
		}
		if ip := strings.TrimSpace(c.AuthorIP); ip != "" {
			if _, err := netip.ParseAddr(ip); err == nil {
				rec.IP = ip
			}
		}
		if c.Parent > 0 {
			if p, ok := imported[c.Parent]; ok {
				rec.Parent = records.Ptr(p)
			} else {
				s.rep.note("%s: parent comment %d was not imported; imported as top-level", key, c.Parent)
			}
		}
		if uk, ok := s.userKey[c.UserID]; ok && c.UserID > 0 {
			rec.User = records.Ptr(uk)
		}
		imported[c.ID] = key
		out = append(out, rec)
	}
	return out
}

// CommentText turns comment HTML into plain text: links become "text (url)", block breaks become newlines,
// everything else is stripped (AFSC's comment policy autolinks URLs when rendering).
func CommentText(src string) string {
	nodes, err := html.ParseFragment(strings.NewReader(src), &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body})
	if err != nil {
		return strings.TrimSpace(src)
	}
	var b strings.Builder
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		switch {
		case n.Type == html.TextNode:
			b.WriteString(n.Data)
			return
		case n.Type != html.ElementNode:
			return
		case n.Data == "script" || n.Data == "style":
			return
		case n.Data == "br":
			b.WriteString("\n")
			return
		case n.Data == "a":
			var t strings.Builder
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				t.WriteString(textContent(c))
			}
			href := ""
			for _, a := range n.Attr {
				if a.Key == "href" {
					href = strings.TrimSpace(a.Val)
				}
			}
			text := strings.TrimSpace(t.String())
			switch {
			case href == "" || strings.HasPrefix(strings.ToLower(href), "javascript:"):
				b.WriteString(text)
			case text == "" || text == href:
				b.WriteString(href)
			default:
				b.WriteString(text + " (" + href + ")")
			}
			return
		}
		block := n.Data == "p" || n.Data == "div" || n.Data == "blockquote" || n.Data == "li" || n.Data == "pre"
		if block {
			b.WriteString("\n\n")
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
		if block {
			b.WriteString("\n\n")
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	lines := strings.Split(strings.ReplaceAll(b.String(), "\r\n", "\n"), "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	out := strings.Join(lines, "\n")
	for strings.Contains(out, "\n\n\n") {
		out = strings.ReplaceAll(out, "\n\n\n", "\n\n")
	}
	return strings.TrimSpace(out)
}

func textContent(n *html.Node) string {
	if n.Type == html.TextNode {
		return n.Data
	}
	var b strings.Builder
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		b.WriteString(textContent(c))
	}
	return b.String()
}
