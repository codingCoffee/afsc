package shortcode

import (
	"reflect"
	"testing"
)

var tags = map[string]bool{"caption": true, "gallery": true, "embed": true, "youtube": true, "box": true, "video": true}

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want []Segment
	}{
		{"self closing", `a [gallery ids="1,2" columns=3] b`, []Segment{
			{Text: "a "},
			{SC: &Shortcode{Tag: "gallery", Attrs: map[string]string{"ids": "1,2", "columns": "3"}, Raw: `[gallery ids="1,2" columns=3]`}},
			{Text: " b"},
		}},
		{"enclosing", `[caption id="attachment_5" align="alignleft"]<img src="x.jpg"> Cap[/caption]`, []Segment{
			{SC: &Shortcode{Tag: "caption", Attrs: map[string]string{"id": "attachment_5", "align": "alignleft"},
				Content: `<img src="x.jpg"> Cap`, Enclosing: true, Raw: `[caption id="attachment_5" align="alignleft"]<img src="x.jpg"> Cap[/caption]`}},
		}},
		{"escaped", `see [[gallery]] here`, []Segment{{Text: "see [gallery] here"}}},
		{"unregistered", `[sic] and [1]`, []Segment{{Text: "[sic] and [1]"}}},
		{"explicit self close", `[video src="a.mp4" /]x[/video]`, []Segment{
			{SC: &Shortcode{Tag: "video", Attrs: map[string]string{"src": "a.mp4"}, Raw: `[video src="a.mp4" /]`}},
			{Text: "x[/video]"},
		}},
		{"jetpack equals form", `[youtube=https://www.youtube.com/watch?v=abc]`, []Segment{
			{SC: &Shortcode{Tag: "youtube", Attrs: map[string]string{}, Pos: []string{"=https://www.youtube.com/watch?v=abc"}, Raw: `[youtube=https://www.youtube.com/watch?v=abc]`}},
		}},
		{"nested different", `[box][embed]https://vimeo.com/1[/embed][/box]`, []Segment{
			{SC: &Shortcode{Tag: "box", Attrs: map[string]string{}, Content: `[embed]https://vimeo.com/1[/embed]`, Enclosing: true, Raw: `[box][embed]https://vimeo.com/1[/embed][/box]`}},
		}},
		{"inside html attribute", `<a title="[gallery]">x</a>`, []Segment{{Text: `<a title="[gallery]">x</a>`}}},
		{"tag prefix is not a match", `[galleryx]`, []Segment{{Text: `[galleryx]`}}},
	}
	for _, c := range cases {
		got := Parse(c.in, tags)
		for _, s := range got {
			if s.SC != nil && s.SC.Attrs == nil {
				s.SC.Attrs = map[string]string{}
			}
		}
		for _, s := range c.want {
			if s.SC != nil && s.SC.Attrs == nil {
				s.SC.Attrs = map[string]string{}
			}
		}
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s:\n got %+v\nwant %+v", c.name, dump(got), dump(c.want))
		}
	}
}

func dump(segs []Segment) []any {
	var out []any
	for _, s := range segs {
		if s.SC != nil {
			out = append(out, *s.SC)
		} else {
			out = append(out, s.Text)
		}
	}
	return out
}

func TestParseAttrs(t *testing.T) {
	named, pos := ParseAttrs(` ID="5" align='alignright' width=300 "quoted pos" bare caption="It\'s"`)
	want := map[string]string{"id": "5", "align": "alignright", "width": "300", "caption": "It's"}
	if !reflect.DeepEqual(named, want) {
		t.Errorf("named %v", named)
	}
	if !reflect.DeepEqual(pos, []string{"quoted pos", "bare"}) {
		t.Errorf("pos %v", pos)
	}
	named, _ = ParseAttrs("ids=\"1,2\"\u00a0columns=\"2\"")
	if named["columns"] != "2" {
		t.Errorf("nbsp separator: %v", named)
	}
}

func TestScan(t *testing.T) {
	occ := Scan(`[caption id="x"]<img>[/caption] [[escaped]] [ad slot=1] <b title="[nope]">`)
	var tags []string
	for _, o := range occ {
		tags = append(tags, o.Tag)
	}
	if !reflect.DeepEqual(tags, []string{"caption", "ad"}) {
		t.Errorf("tags %v", tags)
	}
	if InTagCount(`<b title="[nope]"><i data-x="[a] [b]">`) != 3 {
		t.Errorf("in-tag count")
	}
}
