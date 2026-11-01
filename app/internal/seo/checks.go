package seo

import (
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"afsc/internal/content"
)

// Check statuses, shown as green, amber and red in the editor's SEO panel (§8.6).
const (
	OK      = "ok"
	Warn    = "warn"
	Problem = "problem"
)

// Length targets in characters. The admin also estimates pixel width (title 580 px at 20px Arial, description
// 920 px at 14px Arial), since Google truncates by width.
const (
	TitleMin       = 30
	TitleMax       = 60
	DescriptionMin = 120
	DescriptionMax = 156
)

type Check struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Message string `json:"message"`
}

// CheckInput is what the deterministic checks look at. Doc is nil for terms, authors and home, which only get
// the length checks.
type CheckInput struct {
	Title       string // resolved
	Description string // resolved
	Doc         *content.Node
	Slug        string
	Keyphrase   string
	BaseURL     string // links to this host, or relative links, are internal
}

// Checks runs the §8.6 checks that need no database. Duplicate titles and keyphrase reuse are the caller's.
func Checks(in CheckInput) []Check {
	var out []Check
	add := func(id, status, msg string, args ...any) {
		out = append(out, Check{ID: id, Status: status, Message: fmt.Sprintf(msg, args...)})
	}
	kp := normText(in.Keyphrase)
	switch {
	case kp == "":
		add("keyphrase", Warn, "Set a focus keyphrase to check how the content uses it.")
	default:
		has := func(id, where, s string) {
			if strings.Contains(normText(s), kp) {
				add(id, OK, "The keyphrase appears in the %s.", where)
			} else {
				add(id, Problem, "The keyphrase doesn't appear in the %s.", where)
			}
		}
		has("keyphrase_title", "SEO title", in.Title)
		has("keyphrase_description", "meta description", in.Description)
		if in.Doc != nil {
			if strings.Contains(in.Slug, slugify(in.Keyphrase)) {
				add("keyphrase_slug", OK, "The keyphrase appears in the slug.")
			} else {
				add("keyphrase_slug", Warn, "The slug doesn't contain the keyphrase.")
			}
			has("keyphrase_intro", "first paragraph", firstParagraph(in.Doc))
			found := false
			for _, h := range headings(in.Doc, 2) {
				found = found || strings.Contains(normText(h), kp)
			}
			if found {
				add("keyphrase_subheading", OK, "The keyphrase appears in an H2 subheading.")
			} else {
				add("keyphrase_subheading", Warn, "No H2 subheading contains the keyphrase.")
			}
		}
	}

	lengthCheck := func(id, what, s string, lo, hi int) {
		n := utf8.RuneCountInString(s)
		switch {
		case n == 0:
			add(id, Problem, "The %s is empty.", what)
		case n < lo:
			add(id, Warn, "The %s is short (%d characters); aim for %d–%d.", what, n, lo, hi)
		case n > hi:
			add(id, Warn, "The %s is long (%d characters); search engines cut it after about %d.", what, n, hi)
		default:
			add(id, OK, "The %s length is good (%d characters).", what, n)
		}
	}
	lengthCheck("title_length", "SEO title", in.Title, TitleMin, TitleMax)
	lengthCheck("description_length", "meta description", in.Description, DescriptionMin, DescriptionMax)

	if in.Doc == nil {
		return out
	}
	images, missing := 0, 0
	content.Walk(in.Doc, func(n *content.Node) {
		if n.Type == "image" {
			images++
			if alt, _ := n.Attrs["alt"].(string); strings.TrimSpace(alt) == "" {
				missing++
			}
		}
	})
	switch {
	case missing > 0:
		add("image_alt", Problem, "%d of %d images have no alt text.", missing, images)
	case images > 0:
		add("image_alt", OK, "Every image has alt text.")
	default:
		add("image_alt", OK, "No images to check.")
	}

	internal, outbound := countLinks(in.Doc, in.BaseURL)
	if internal == 0 {
		add("internal_links", Warn, "No internal links. Link to related posts on this site.")
	} else {
		add("internal_links", OK, "%d internal links.", internal)
	}
	if outbound == 0 {
		add("outbound_links", Warn, "No outbound links. Cite your sources where it helps the reader.")
	} else {
		add("outbound_links", OK, "%d outbound links.", outbound)
	}
	return out
}

// normText lowercases s and collapses whitespace, so matching ignores case and spacing.
func normText(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(s)), " ")
}

// slugify turns a keyphrase into the form it takes in a slug: "Index Funds" → "index-funds".
func slugify(s string) string {
	words := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsNumber(r) })
	return strings.Join(words, "-")
}

// DocText is the plain text of a document: text nodes joined, blocks separated by newlines.
func DocText(doc *content.Node) string {
	var b strings.Builder
	var walk func(n *content.Node)
	walk = func(n *content.Node) {
		switch n.Type {
		case "text":
			b.WriteString(n.Text)
			return
		case "hardBreak":
			b.WriteString("\n")
			return
		}
		for _, c := range n.Content {
			walk(c)
		}
		if n.Type != "doc" && len(n.Content) > 0 && !strings.HasSuffix(b.String(), "\n") {
			b.WriteString("\n")
		}
	}
	if doc != nil {
		walk(doc)
	}
	return strings.TrimSpace(b.String())
}

func firstParagraph(doc *content.Node) string {
	for _, n := range doc.Content {
		if n.Type == "paragraph" {
			if t := DocText(n); t != "" {
				return t
			}
		}
	}
	return ""
}

func headings(doc *content.Node, level int) []string {
	var out []string
	content.Walk(doc, func(n *content.Node) {
		if n.Type != "heading" {
			return
		}
		var l int
		switch v := n.Attrs["level"].(type) {
		case float64:
			l = int(v)
		case int:
			l = v
		}
		if l == level {
			out = append(out, DocText(n))
		}
	})
	return out
}

// countLinks counts link marks: relative links and links to the base URL's host are internal, other http(s)
// links outbound. Fragments, mailto: and the like count as neither.
func countLinks(doc *content.Node, baseURL string) (internal, outbound int) {
	host := ""
	if u, err := url.Parse(baseURL); err == nil {
		host = strings.ToLower(u.Host)
	}
	content.Walk(doc, func(n *content.Node) {
		for _, m := range n.Marks {
			if m.Type != "link" {
				continue
			}
			href, _ := m.Attrs["href"].(string)
			href = strings.TrimSpace(href)
			u, err := url.Parse(href)
			switch {
			case err != nil || href == "" || strings.HasPrefix(href, "#"):
			case u.Host == "" && u.Scheme == "":
				internal++
			case u.Scheme != "" && u.Scheme != "http" && u.Scheme != "https":
			case strings.ToLower(u.Host) == host:
				internal++
			default:
				outbound++
			}
		}
	})
	return internal, outbound
}
