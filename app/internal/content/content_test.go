package content

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite golden files")

var testMedia = map[int64]MediaInfo{
	7: {ID: 7, LegacyID: 123, Filename: "chart one.jpg", Version: "ab12cd34", Mime: "image/jpeg", Width: 1600, Height: 900},
	8: {ID: 8, Filename: "clip.mp4", Version: "0011aabb", Mime: "video/mp4"},
}

func testCtx() RenderContext {
	return RenderContext{PostID: 42, Media: func(id int64) (MediaInfo, bool) {
		m, ok := testMedia[id]
		return m, ok
	}}
}

func mustParse(t *testing.T, s string) *Node {
	t.Helper()
	n, err := Parse([]byte(s))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func render(t *testing.T, s string) (string, string) {
	t.Helper()
	doc := mustParse(t, s)
	if res := Validate(doc, Options{AllowRawHTML: true, AllowedClasses: map[string]bool{"lead": true, "callout": true}}); !res.OK() {
		t.Fatalf("invalid test doc: %+v", res.Errors)
	}
	h, txt, err := Render(doc, testCtx())
	if err != nil {
		t.Fatal(err)
	}
	return h, txt
}

func TestRenderWordPressMarkup(t *testing.T) {
	cases := []struct{ name, doc, want string }{
		{"marks merge", `{"type":"doc","content":[{"type":"paragraph","attrs":{"textAlign":"center"},"content":[
      {"type":"text","text":"Hello ","marks":[{"type":"bold"}]},
      {"type":"text","text":"world","marks":[{"type":"italic"},{"type":"bold"}]},
      {"type":"text","text":"!"}]}]}`,
			`<p style="text-align: center;"><strong>Hello <em>world</em></strong>!</p>`},
		{"linked media image", `{"type":"doc","content":[{"type":"image","attrs":{"mediaId":7,"alt":"A chart","align":"center","size":"medium","linkTarget":"media"}}]}`,
			`<a href="/m/7/ab12cd34/orig/chart%20one.jpg"><img src="/m/7/ab12cd34/orig/chart%20one.jpg" alt="A chart" width="1600" height="900" class="wp-image-123 size-medium aligncenter"></a>`},
		{"caption", `{"type":"doc","content":[{"type":"figure","content":[
      {"type":"image","attrs":{"mediaId":7,"alt":"","width":300,"height":169,"align":"right"}},
      {"type":"figcaption","content":[{"type":"text","text":"Caption"}]}]}]}`,
			`<figure id="attachment_123" aria-describedby="caption-attachment-123" style="width: 300px" class="wp-caption alignright"><img src="/m/7/ab12cd34/orig/chart%20one.jpg" alt="" width="300" height="169" class="wp-image-123 size-full"><figcaption id="caption-attachment-123" class="wp-caption-text">Caption</figcaption></figure>`},
		{"youtube", `{"type":"doc","content":[{"type":"embed","attrs":{"provider":"youtube","url":"https://www.youtube.com/watch?v=XXXXXXXXXXX","externalId":"XXXXXXXXXXX"}}]}`,
			`<figure class="wp-block-embed is-provider-youtube wp-block-embed-youtube"><div class="wp-block-embed__wrapper"><iframe src="https://www.youtube-nocookie.com/embed/XXXXXXXXXXX" title="YouTube embed" loading="lazy" allow="encrypted-media; picture-in-picture" allowfullscreen="true" sandbox="allow-scripts allow-same-origin allow-popups allow-presentation"></iframe></div></figure>`},
		{"link target blank", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"https://example.com","target":"_blank"}}]}]}]}`,
			`<p><a href="https://example.com" target="_blank" rel="noopener">x</a></p>`},
		{"read more", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]},{"type":"readMore"}]}`,
			`<p>a</p><span id="more-42"></span>`},
		{"raw html sanitized", `{"type":"doc","content":[{"type":"rawHtml","attrs":{"html":"<p onclick=\"x\">hi</p><script>bad()</script>"}}]}`,
			`<p>hi</p>`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got, _ := render(t, c.doc); got != c.want {
				t.Errorf("\n got %s\nwant %s", got, c.want)
			}
		})
	}
}

func TestRenderLazyAfterFirstImage(t *testing.T) {
	got, _ := render(t, `{"type":"doc","content":[
    {"type":"image","attrs":{"src":"https://example.com/a.png","alt":"a"}},
    {"type":"image","attrs":{"src":"https://example.com/b.png","alt":"b"}}]}`)
	first, _, _ := strings.Cut(got, `alt="b"`)
	if strings.Contains(first, "lazy") {
		t.Errorf("first image must be eager: %s", got)
	}
	if !strings.Contains(got, `alt="b" loading="lazy" decoding="async"`) {
		t.Errorf("second image must be lazy: %s", got)
	}
}

func TestRenderText(t *testing.T) {
	_, txt := render(t, `{"type":"doc","content":[
    {"type":"paragraph","content":[{"type":"text","text":"Hello"},{"type":"hardBreak"},{"type":"text","text":"there"}]},
    {"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"Title"}]},
    {"type":"codeBlock","content":[{"type":"text","text":"x := 1"}]}]}`)
	if want := "Hello\nthere\nTitle\nx := 1"; txt != want {
		t.Errorf("got %q want %q", txt, want)
	}
}

func TestValidate(t *testing.T) {
	cases := []struct {
		name, doc, path, msg string
		opts                 Options
	}{
		{"heading level required", `{"type":"doc","content":[{"type":"heading","content":[{"type":"text","text":"x"}]}]}`,
			"content[0].attrs.level", "required", Options{}},
		{"heading level range", `{"type":"doc","content":[{"type":"heading","attrs":{"level":7}}]}`,
			"content[0].attrs.level", "between 1 and 6", Options{}},
		{"unknown node", `{"type":"doc","content":[{"type":"marquee"}]}`, "content[0]", "unknown node type", Options{}},
		{"unknown attr", `{"type":"doc","content":[{"type":"paragraph","attrs":{"onclick":"x"}}]}`,
			"content[0].attrs.onclick", "unknown attribute", Options{}},
		{"bad link scheme", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}]}]}]}`,
			"content[0].content[0].marks[0].attrs.href", "url", Options{}},
		{"image media and src", `{"type":"doc","content":[{"type":"image","attrs":{"mediaId":7,"src":"https://example.com/a.png","alt":""}}]}`,
			"content[0]", "exactly one of mediaId or src", Options{}},
		{"missing media", `{"type":"doc","content":[{"type":"image","attrs":{"mediaId":99,"alt":""}}]}`,
			"content[0].attrs.mediaId", "does not exist", Options{MediaExists: func(id int64) bool { return id == 7 }}},
		{"figure needs caption", `{"type":"doc","content":[{"type":"figure","content":[{"type":"image","attrs":{"src":"/a.png","alt":""}}]}]}`,
			"content[0]", "expected figcaption at content[1]", Options{}},
		{"raw html not allowed", `{"type":"doc","content":[{"type":"rawHtml","attrs":{"html":"<p>x</p>"}}]}`,
			"content[0]", "not allowed", Options{}},
		{"unknown class rejected", `{"type":"doc","content":[{"type":"paragraph","attrs":{"classes":"lead evil"}}]}`,
			"content[0].attrs.classes", `class "evil"`, Options{AllowedClasses: map[string]bool{"lead": true}}},
		{"embed provider url", `{"type":"doc","content":[{"type":"embed","attrs":{"provider":"youtube","url":"https://vimeo.com/1","externalId":"abc"}}]}`,
			"content[0].attrs.url", "does not belong", Options{}},
		{"bad color", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"textStyle","attrs":{"color":"red"}}]}]}]}`,
			"content[0].content[0].marks[0].attrs.color", "hex or rgb", Options{}},
		{"marks in code block", `{"type":"doc","content":[{"type":"codeBlock","content":[{"type":"text","text":"x","marks":[{"type":"bold"}]}]}]}`,
			"content[0].content[0].marks", "not allowed", Options{}},
		{"empty doc", `{"type":"doc"}`, "", "expected block", Options{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := Validate(mustParse(t, c.doc), c.opts)
			for _, e := range res.Errors {
				if e.Path == c.path && strings.Contains(e.Message, c.msg) {
					return
				}
			}
			t.Errorf("want error at %q containing %q, got %+v", c.path, c.msg, res.Errors)
		})
	}
}

func TestValidateDropsUnknownClassesOnImport(t *testing.T) {
	doc := mustParse(t, `{"type":"doc","content":[{"type":"paragraph","attrs":{"classes":"lead evil"}}]}`)
	res := Validate(doc, Options{AllowedClasses: map[string]bool{"lead": true}, DropUnknownClasses: true})
	if !res.OK() || len(res.Warnings) != 1 {
		t.Fatalf("want ok with one warning, got %+v", res)
	}
	if got := doc.Content[0].str("classes"); got != "lead" {
		t.Errorf("classes = %q, want lead", got)
	}
}

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run go test -update to create it)", err)
	}
	if string(want) != string(got) {
		t.Errorf("%s differs from golden file; run go test -update and review the diff", name)
	}
}

func TestRenderAllNodesGolden(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "all-nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	h, txt := render(t, string(src))
	golden(t, "all-nodes.html", []byte(h+"\n"))
	golden(t, "all-nodes.txt", []byte(txt+"\n"))
}

func TestSchemaJSONGolden(t *testing.T) {
	b, err := SchemaJSON()
	if err != nil {
		t.Fatal(err)
	}
	golden(t, "schema.json", append(b, '\n'))
}

func TestAllNodesFixtureCoversSchema(t *testing.T) {
	src, err := os.ReadFile(filepath.Join("testdata", "all-nodes.json"))
	if err != nil {
		t.Fatal(err)
	}
	seenNodes, seenMarks := map[string]bool{}, map[string]bool{}
	Walk(mustParse(t, string(src)), func(n *Node) {
		seenNodes[n.Type] = true
		for _, m := range n.Marks {
			seenMarks[m.Type] = true
		}
	})
	for name := range Nodes {
		if !seenNodes[name] {
			t.Errorf("all-nodes.json does not use node %q", name)
		}
	}
	for name := range Marks {
		if !seenMarks[name] {
			t.Errorf("all-nodes.json does not use mark %q", name)
		}
	}
}
