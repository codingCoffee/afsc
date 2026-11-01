package seo

import (
	"strings"
	"testing"

	"afsc/internal/content"
)

func ckText(s string, marks ...*content.Mark) *content.Node {
	return &content.Node{Type: "text", Text: s, Marks: marks}
}

func ckLink(href string) *content.Mark {
	return &content.Mark{Type: "link", Attrs: map[string]any{"href": href}}
}

func ckPara(c ...*content.Node) *content.Node { return &content.Node{Type: "paragraph", Content: c} }

func ckH(level int, s string) *content.Node {
	return &content.Node{Type: "heading", Attrs: map[string]any{"level": float64(level)}, Content: []*content.Node{ckText(s)}}
}

func ckImg(alt string) *content.Node {
	return &content.Node{Type: "image", Attrs: map[string]any{"src": "https://cdn.example.com/a.png", "alt": alt}}
}

func ckDoc(c ...*content.Node) *content.Node { return &content.Node{Type: "doc", Content: c} }

func TestChecks(t *testing.T) {
	good := CheckInput{
		Title:       "Index funds explained for first-time investors - Blog",
		Description: strings.Repeat("Index funds track a market index at low cost. ", 3)[:130],
		Doc: ckDoc(
			ckPara(ckText("Index funds are the simplest way to start.")),
			ckH(2, "Why index funds work"),
			ckPara(ckText("See "), ckText("our guide", ckLink("/guides/start/")), ckText(" and "), ckText("the regulator", ckLink("https://sebi.example.org/"))),
			ckImg("A chart"),
		),
		Slug:      "index-funds-explained",
		Keyphrase: "Index  Funds",
		BaseURL:   "https://blog.example.com",
	}
	short := good
	short.Title, short.Description = "Index funds", "Index funds."
	long := good
	long.Title = strings.Repeat("index funds ", 7)
	long.Description = strings.Repeat("index funds ", 15)
	missing := good
	missing.Keyphrase = "bond ladders"
	empty := good
	empty.Title, empty.Description, empty.Keyphrase = "", "", ""
	empty.Doc = ckDoc(ckPara(ckText("Text with "), ckText("a fragment", ckLink("#top")), ckText(" and "), ckText("mail", ckLink("mailto:a@example.com"))),
		ckImg(""), ckImg(" "), ckImg("ok"))
	absolute := good
	absolute.Doc = ckDoc(ckPara(ckText("x", ckLink("https://blog.example.com/a/")), ckText("y", ckLink("//blog.example.com/b/"))))
	term := CheckInput{Title: "Markets archive for long-term investors - Blog", Description: "Markets.", Keyphrase: "markets"}

	cases := []struct {
		name string
		in   CheckInput
		want map[string]string
	}{
		{"all good", good, map[string]string{
			"keyphrase_title": OK, "keyphrase_description": OK, "keyphrase_slug": OK, "keyphrase_intro": OK,
			"keyphrase_subheading": OK, "title_length": OK, "description_length": OK, "image_alt": OK,
			"internal_links": OK, "outbound_links": OK,
		}},
		{"short", short, map[string]string{"title_length": Warn, "description_length": Warn}},
		{"long", long, map[string]string{"title_length": Warn, "description_length": Warn}},
		{"keyphrase missing everywhere", missing, map[string]string{
			"keyphrase_title": Problem, "keyphrase_description": Problem, "keyphrase_slug": Warn,
			"keyphrase_intro": Problem, "keyphrase_subheading": Warn,
		}},
		{"empty", empty, map[string]string{
			"keyphrase": Warn, "title_length": Problem, "description_length": Problem, "image_alt": Problem,
			"internal_links": Warn, "outbound_links": Warn,
		}},
		{"same-host absolute links are internal", absolute, map[string]string{"internal_links": OK, "outbound_links": Warn,
			"image_alt": OK}},
		{"term: no content checks", term, map[string]string{"keyphrase_title": OK, "keyphrase_description": OK,
			"title_length": OK, "description_length": Warn}},
	}
	for _, c := range cases {
		got := map[string]Check{}
		for _, ch := range Checks(c.in) {
			if _, dup := got[ch.ID]; dup {
				t.Errorf("%s: check %s twice", c.name, ch.ID)
			}
			if ch.Message == "" {
				t.Errorf("%s: check %s has no message", c.name, ch.ID)
			}
			got[ch.ID] = ch
		}
		for id, status := range c.want {
			if got[id].Status != status {
				t.Errorf("%s: %s = %q (%s), want %q", c.name, id, got[id].Status, got[id].Message, status)
			}
		}
		if c.in.Doc == nil {
			for _, id := range []string{"keyphrase_slug", "keyphrase_intro", "image_alt", "internal_links"} {
				if _, ok := got[id]; ok {
					t.Errorf("%s: unexpected %s", c.name, id)
				}
			}
		}
		if c.in.Keyphrase == "" {
			if _, ok := got["keyphrase_title"]; ok {
				t.Errorf("%s: keyphrase checks without a keyphrase", c.name)
			}
		}
	}
	for _, ch := range Checks(empty) {
		if ch.ID == "image_alt" && !strings.HasPrefix(ch.Message, "2 of 3") {
			t.Errorf("image_alt message: %q", ch.Message)
		}
	}
}

func TestDocText(t *testing.T) {
	d := ckDoc(ckH(2, "Title"), ckPara(ckText("One "), ckText("two"), &content.Node{Type: "hardBreak"}, ckText("three")), ckPara(ckText("Four")))
	if got, want := DocText(d), "Title\nOne two\nthree\nFour"; got != want {
		t.Errorf("DocText = %q, want %q", got, want)
	}
}
