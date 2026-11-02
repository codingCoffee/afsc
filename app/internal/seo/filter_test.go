package seo

import (
	"slices"
	"strings"
	"testing"
	"time"

	"afsc/internal/settings"
	"afsc/internal/theme"
)

func filterInput() Input {
	s := settings.Defaults()
	s.Site.Title = "Example Blog"
	d := &theme.PageData{Kind: theme.KindHome, Site: theme.Site{Title: "Example Blog", URL: "https://blog.example.com", Language: "en"},
		Request: theme.Request{Path: "/", URL: "https://blog.example.com/", Paged: 1}}
	return Input{Page: d, Settings: &s, Now: time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)}
}

func TestBuildFilters(t *testing.T) {
	plain, _ := Build(filterInput())

	in := filterInput()
	in.MetaFilter = func(m *Meta) *Meta { return m }
	in.GraphFilter = func(g []map[string]any) []map[string]any { return g }
	same, _ := Build(in)
	if *same != *plain {
		t.Fatalf("identity filters changed the head:\n%s\n%s", plain.Tags, same.Tags)
	}

	in = filterInput()
	in.MetaFilter = func(m *Meta) *Meta {
		m.Title = "Filtered"
		m.Tags = slices.DeleteFunc(m.Tags, func(t Tag) bool { return t.Key == "twitter:card" })
		m.Tags = append(m.Tags, Tag{Kind: "name", Key: "x-plugin", Value: `a"<b`}, Tag{Kind: "bogus", Key: "k", Value: "v"})
		return m
	}
	in.GraphFilter = func(g []map[string]any) []map[string]any {
		return append(g, map[string]any{"@type": "Thing", "name": "</script><script>alert(1)</script>"})
	}
	h, _ := Build(in)
	tags := string(h.Tags)
	if h.Title != "Filtered" {
		t.Errorf("title %q", h.Title)
	}
	if strings.Contains(tags, "twitter:card") || strings.Contains(tags, "bogus") {
		t.Errorf("removed or unknown tags printed: %s", tags)
	}
	if !strings.Contains(tags, `<meta name="x-plugin" content="a&#34;&lt;b">`) {
		t.Errorf("added tag missing or unescaped: %s", tags)
	}
	if strings.Count(tags, "<script") != 1 || !strings.Contains(tags, `</script>`) || !strings.Contains(tags, `"@type":"Thing"`) {
		t.Errorf("graph node missing or unescaped: %s", tags)
	}

	in = filterInput()
	in.GraphFilter = func([]map[string]any) []map[string]any { return nil }
	if h, _ := Build(in); strings.Contains(string(h.Tags), "ld+json") {
		t.Error("an empty graph printed a script")
	}
}
