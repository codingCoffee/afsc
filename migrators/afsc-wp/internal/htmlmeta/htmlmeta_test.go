package htmlmeta

import (
	"reflect"
	"testing"
)

const page = `<!doctype html><html><head><title> Hello  &amp; world | Site </title>
<meta name="description" content="Desc">
<meta name="robots" content="index, follow, max-snippet:-1">
<link rel="canonical" href="https://blog.example.com/hello/">
<link rel="next" href="https://blog.example.com/page/2/">
<meta property="og:title" content="OG"><meta name="twitter:card" content="summary_large_image">
<script type="application/ld+json">{"@context":"https://schema.org","@graph":[{"@type":"WebPage"},{"@type":"Article","headline":"Hello"},{"@type":["Person","Thing"]}]}</script>
</head><body><h1>Hello</h1><div id="main"><article class="post"><div class="entry-content wide"><p>One two <b>three</b></p><img src="/a.jpg"></div></article></div></body></html>`

func TestParse(t *testing.T) {
	m, err := Parse([]byte(page), "#main .entry-content")
	if err != nil {
		t.Fatal(err)
	}
	if m.Title != "Hello & world | Site" || m.Description != "Desc" || m.Canonical != "https://blog.example.com/hello/" ||
		m.OG["og:title"] != "OG" || m.Twitter["twitter:card"] == "" || m.Next == "" || m.H1[0] != "Hello" {
		t.Fatalf("meta %+v", m)
	}
	if m.Body != `<p>One two <b>three</b></p><img src="/a.jpg"/>` || m.WordCount != 3 {
		t.Fatalf("body %q %d", m.Body, m.WordCount)
	}
	if got := m.Types(); !reflect.DeepEqual(got, []string{"Article", "Person", "Thing", "WebPage"}) {
		t.Fatalf("types %v", got)
	}
	if m.Headline() != "Hello" {
		t.Fatalf("headline %q", m.Headline())
	}
	if got := RobotsSet(m.Robots); !reflect.DeepEqual(got, []string{"follow", "index", "max-snippet:-1"}) {
		t.Fatalf("robots %v", got)
	}
}

func TestSelectorForms(t *testing.T) {
	for sel, ok := range map[string]bool{
		".entry-content":           true,
		"article.post div.wide":    true,
		"div[class]":               true,
		"#nope, .entry-content":    true,
		"section .entry-content":   false,
		"article > .entry-content": true,
	} {
		m, _ := Parse([]byte(page), sel)
		if (m.Body != "") != ok {
			t.Errorf("%q: body %q", sel, m.Body)
		}
	}
}
