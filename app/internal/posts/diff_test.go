package posts

import (
	"reflect"
	"strings"
	"testing"

	"afsc/internal/content"
)

func rebuild(ops []DiffOp, skip string) string {
	var b strings.Builder
	for _, op := range ops {
		if op.Op != skip {
			b.WriteString(op.Text)
		}
	}
	return b.String()
}

func TestWordDiff(t *testing.T) {
	cases := []struct{ a, b string }{
		{"", ""},
		{"", "new text"},
		{"old text", ""},
		{"the quick brown fox", "the quick red fox"},
		{"a b c d e f", "x a b d e y f"},
		{"same\n\nparagraphs here", "same\n\nparagraphs  here too"},
		{strings.Repeat("w ", 3000), strings.Repeat("v ", 3000)},
	}
	for _, c := range cases {
		ops := WordDiff(c.a, c.b)
		if got := rebuild(ops, "insert"); got != c.a {
			t.Errorf("%q→%q: old side rebuilds as %q", c.a, c.b, got)
		}
		if got := rebuild(ops, "delete"); got != c.b {
			t.Errorf("%q→%q: new side rebuilds as %q", c.a, c.b, got)
		}
		for i := 1; i < len(ops); i++ {
			if ops[i].Op == ops[i-1].Op {
				t.Errorf("%q→%q: adjacent %s ops not merged", c.a, c.b, ops[i].Op)
			}
		}
		if again := WordDiff(c.a, c.b); !reflect.DeepEqual(ops, again) {
			t.Errorf("%q→%q: not deterministic", c.a, c.b)
		}
	}
	got := WordDiff("the quick brown fox", "the quick red fox")
	want := []DiffOp{{"equal", "the quick "}, {"delete", "brown"}, {"insert", "red"}, {"equal", " fox"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v", got)
	}
}

func doc(t *testing.T, s string) *content.Node {
	t.Helper()
	n, err := content.Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestBlockDiff(t *testing.T) {
	p := func(text string) string {
		return `{"type":"paragraph","content":[{"type":"text","text":"` + text + `"}]}`
	}
	a := doc(t, `{"type":"doc","content":[`+p("one")+`,`+p("two")+`,{"type":"horizontalRule"},`+p("four")+`]}`)
	b := doc(t, `{"type":"doc","content":[`+p("one")+`,`+p("TWO")+`,`+p("four")+`,`+p("five")+`]}`)
	got := BlockDiff(a, b)
	want := []BlockChange{{1, "changed", "paragraph"}, {2, "removed", "horizontalRule"}, {3, "added", "paragraph"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(BlockDiff(a, b), got) {
		t.Error("not deterministic")
	}
	if got := BlockDiff(a, a); len(got) != 0 {
		t.Errorf("identical docs: %+v", got)
	}
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":       "hello-world",
		"  Don't stop  ":      "dont-stop",
		"Ünïcode Straße 2024": "ünïcode-straße-2024",
		"---":                 "",
		"a/b\\c":              "a-b-c",
		"हमारी 10-वीं सालगिरह":   "हमारी-10-वीं-सालगिरह",
		strings.Repeat("x", 300): strings.Repeat("x", 197),
	} {
		if got := Slugify(in); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRawHTMLIssues(t *testing.T) {
	raw := func(h string) string { return `{"type":"rawHtml","attrs":{"html":"` + h + `"}}` }
	stored := []byte(`{"type":"doc","content":[` + raw("<b>a</b>") + `,` + raw("<i>b</i>") + `]}`)
	kept := doc(t, `{"type":"doc","content":[{"type":"horizontalRule"},`+raw("<i>b</i>")+`,`+raw("<b>a</b>")+`]}`)
	if issues := rawHTMLIssues(kept, stored); len(issues) != 0 {
		t.Errorf("unchanged blocks rejected: %+v", issues)
	}
	added := doc(t, `{"type":"doc","content":[`+raw("<b>a</b>")+`,`+raw("<b>a</b>")+`,{"type":"blockquote","content":[`+raw("<u>c</u>")+`]}]}`)
	issues := rawHTMLIssues(added, stored)
	paths := []string{}
	for _, i := range issues {
		paths = append(paths, i.Path)
	}
	if !reflect.DeepEqual(paths, []string{"content[1]", "content[2].content[0]"}) {
		t.Errorf("issues at %v", paths)
	}
}

func TestAutoVideos(t *testing.T) {
	d := doc(t, `{"type":"doc","content":[
{"type":"embed","attrs":{"provider":"youtube","url":"https://www.youtube.com/watch?v=abc","externalId":"abc"}},
{"type":"embed","attrs":{"provider":"x","url":"https://x.com/a/status/1"}},
{"type":"video","attrs":{"mediaId":7,"posterMediaId":8}},
{"type":"video","attrs":{"src":"https://cdn.example.com/v.mp4"}},
{"type":"embed","attrs":{"provider":"youtube","url":"https://www.youtube.com/watch?v=abc","externalId":"abc"}}]}`)
	got := autoVideos(d, func(id int64) string { return "https://blog.example.com/m/" + string(rune('0'+id)) })
	if len(got) != 3 {
		t.Fatalf("%d videos: %+v", len(got), got)
	}
	if got[0].provider != "youtube" || got[0].thumb != "https://i.ytimg.com/vi/abc/hqdefault.jpg" {
		t.Errorf("youtube: %+v", got[0])
	}
	if got[1].url != "https://blog.example.com/m/7" || got[1].thumbMedia == nil || *got[1].thumbMedia != 8 {
		t.Errorf("library video: %+v", got[1])
	}
	if got[2].url != "https://cdn.example.com/v.mp4" {
		t.Errorf("external video: %+v", got[2])
	}
}
