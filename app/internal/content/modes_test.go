package content

import (
	"html/template"
	"strings"
	"testing"
)

type testPartials map[string]*template.Template

func (p testPartials) Has(node string) bool { return p[node] != nil }

func (p testPartials) Render(node string, d NodeData) (string, error) {
	var b strings.Builder
	err := p[node].Execute(&b, d)
	return b.String(), err
}

func newTestPartials() testPartials {
	return testPartials{
		"paragraph": template.Must(template.New("p").Parse(`<div class="p">{{.Children}}</div>`)),
		"image":     template.Must(template.New("i").Parse(`<x-img data-src="{{.Src}}">{{.Default}}</x-img>`)),
	}
}

const partialsDoc = `{"type":"doc","content":[
  {"type":"paragraph","content":[{"type":"text","text":"hi <b>","marks":[{"type":"bold"}]}]},
  {"type":"image","attrs":{"mediaId":7,"alt":"a"}},
  {"type":"blockquote","content":[{"type":"paragraph","content":[{"type":"text","text":"q"}]}]}]}`

func TestRenderPartials(t *testing.T) {
	ctx := testCtx()
	ctx.Partials = newTestPartials()
	h, txt, err := Render(mustParse(t, partialsDoc), ctx)
	if err != nil {
		t.Fatal(err)
	}
	want := `<div class="p"><strong>hi &lt;b&gt;</strong></div>` +
		`<x-img data-src="/m/7/ab12cd34/orig/chart%20one.jpg"><img src="/m/7/ab12cd34/orig/chart%20one.jpg" alt="a" width="1600" height="900" class="wp-image-123 size-full"></x-img>` +
		`<blockquote><div class="p">q</div></blockquote>`
	if h != want {
		t.Errorf("\n got %s\nwant %s", h, want)
	}
	_, plain, _ := Render(mustParse(t, partialsDoc), testCtx())
	if txt != plain {
		t.Errorf("partials changed the text: %q vs %q", txt, plain)
	}
}

func TestRenderTeaser(t *testing.T) {
	cases := []struct {
		name, doc, want string
		more            bool
	}{
		{"top level", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]},{"type":"readMore"},
      {"type":"paragraph","content":[{"type":"text","text":"b"}]}]}`, `<p>a</p>`, true},
		{"nested", `{"type":"doc","content":[{"type":"container","attrs":{"classes":"lead"},"content":[
      {"type":"paragraph","content":[{"type":"text","text":"a"}]},{"type":"readMore"},
      {"type":"paragraph","content":[{"type":"text","text":"b"}]}]},{"type":"paragraph","content":[{"type":"text","text":"c"}]}]}`,
			`<div class="lead"><p>a</p></div>`, true},
		{"none", `{"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"a"}]}]}`, `<p>a</p>`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h, _, more, err := RenderTeaser(mustParse(t, c.doc), testCtx())
			if err != nil {
				t.Fatal(err)
			}
			if h != c.want || more != c.more {
				t.Errorf("got %s (more=%v), want %s (more=%v)", h, more, c.want, c.more)
			}
		})
	}
}

func TestRenderFeed(t *testing.T) {
	ctx := testCtx()
	ctx.Feed, ctx.BaseURL, ctx.Partials = true, "https://blog.example.com", newTestPartials()
	doc := `{"type":"doc","content":[{"type":"image","attrs":{"mediaId":7,"alt":"a"}},{"type":"image","attrs":{"mediaId":7,"alt":"b"}},
    {"type":"paragraph","content":[{"type":"text","text":"x","marks":[{"type":"link","attrs":{"href":"/about/"}}]}]}]}`
	h, _, err := Render(mustParse(t, doc), ctx)
	if err != nil {
		t.Fatal(err)
	}
	img := `<img src="https://blog.example.com/m/7/ab12cd34/orig/chart%20one.jpg" alt="%s" width="1600" height="900" class="wp-image-123 size-full">`
	want := strings.Replace(img, "%s", "a", 1) + strings.Replace(img, "%s", "b", 1) + `<p><a href="https://blog.example.com/about/">x</a></p>`
	if h != want {
		t.Errorf("\n got %s\nwant %s", h, want)
	}
}
