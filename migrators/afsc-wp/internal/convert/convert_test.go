package convert

import (
	"encoding/json"
	"strings"
	"testing"

	"afsc/internal/content"

	"afsc/migrators/afsc-wp/internal/mediaidx"
)

type fakeMedia struct{}

var mediaURLs = map[string]mediaidx.Ref{
	"a.jpg":         {Key: "wp:post:5"},
	"a-300x200.jpg": {Key: "wp:post:5", Size: "medium"},
	"clip.mp4":      {Key: "wp:post:9"},
}

func uploadRel(u string) (string, bool) {
	u = strings.TrimPrefix(u, "https://blog.example.com")
	return strings.CutPrefix(u, "/wp-content/uploads/")
}

func (fakeMedia) Resolve(u string) (mediaidx.Ref, string, bool) {
	rel, _ := uploadRel(u)
	r, ok := mediaURLs[rel]
	return r, rel, ok
}
func (fakeMedia) IsUpload(u string) bool {
	_, ok := uploadRel(u)
	return ok
}
func (fakeMedia) Attachment(id int64) (string, bool) {
	switch id {
	case 5, 6:
		return "wp:post:" + string(rune('0'+id)), true
	}
	return "", false
}
func (fakeMedia) AttachedImages(parent int64) []int64 {
	if parent == 7 {
		return []int64{5, 6}
	}
	return nil
}

type fakeLinks struct{}

func (fakeLinks) Rewrite(h string) (string, bool) {
	if strings.HasPrefix(h, "javascript:") {
		return "", false
	}
	return strings.TrimPrefix(h, "https://blog.example.com"), true
}

func ctx(decisions map[string]string) *Context {
	return &Context{
		PostID: 7, Media: fakeMedia{}, Links: fakeLinks{}, SiteURL: "https://blog.example.com",
		AllowedClasses: map[string]bool{"callout": true, "lead": true},
		Shortcodes:     map[string]bool{"caption": true, "gallery": true, "embed": true, "ad": true, "pull": true},
		Decide: func(kind, name, def string) string {
			if v, ok := decisions[kind+":"+name]; ok {
				return v
			}
			switch kind + ":" + name {
			case "shortcode:caption", "shortcode:gallery", "shortcode:embed":
				return "convert"
			case "shortcode:ad":
				return "drop"
			case "shortcode:pull":
				return "render_php"
			}
			return def
		},
		RenderPHP: func(tag, raw string) (string, bool) {
			return `<blockquote class="pull">Quoted <script>x()</script></blockquote>`, tag == "pull"
		},
	}
}

func run(t *testing.T, in string, decisions map[string]string) (string, *Result) {
	t.Helper()
	res, err := Convert(in, ctx(decisions))
	if err != nil {
		t.Fatal(err)
	}
	vr := content.Validate(mediaToInts(res.Doc), content.Options{AllowedClasses: map[string]bool{"callout": true, "lead": true}, AllowRawHTML: true})
	if !vr.OK() {
		t.Fatalf("%q: invalid document %+v", in, vr.Errors)
	}
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.Encode(res.Doc.Content)
	return strings.TrimSpace(b.String()), res
}

// mediaToInts mimics the importer: media legacy keys become ids before validation. It works on a copy.
func mediaToInts(doc *content.Node) *content.Node {
	b, _ := json.Marshal(doc)
	cp, _ := content.Parse(b)
	content.Walk(cp, func(n *content.Node) {
		for _, k := range []string{"mediaId", "posterMediaId"} {
			if _, ok := n.Attrs[k].(string); ok {
				n.Attrs[k] = 1
			}
		}
	})
	return cp
}

func hasFlag(r *Result, kind string) bool {
	for _, f := range r.Flags {
		if f.Kind == kind {
			return true
		}
	}
	return false
}

func TestConvert(t *testing.T) {
	cases := []struct {
		name, in, want string
		dec            map[string]string
		flag           string
	}{
		{"marks", `<p>Hello <strong>bold <em>both</em></strong> <a href="https://blog.example.com/x/" target="_blank" rel="noopener">link</a></p>`,
			`[{"type":"paragraph","content":[{"type":"text","text":"Hello "},{"type":"text","marks":[{"type":"bold"}],"text":"bold "},{"type":"text","marks":[{"type":"bold"},{"type":"italic"}],"text":"both"},{"type":"text","text":" "},{"type":"text","marks":[{"type":"link","attrs":{"href":"/x/","rel":"noopener","target":"_blank"}}],"text":"link"}]}]`, nil, ""},
		{"align and class", `<p style="text-align: center; font-size: 20px" class="lead foo">x</p>`,
			`[{"type":"paragraph","attrs":{"classes":"lead","textAlign":"center"},"content":[{"type":"text","text":"x"}]}]`, nil, "style"},
		{"image splits paragraph", `<p>Before <a href="https://blog.example.com/wp-content/uploads/a.jpg"><img class="alignleft size-medium wp-image-5" src="https://blog.example.com/wp-content/uploads/a-300x200.jpg" alt="A" width="300" height="200"></a> after</p>`,
			`[{"type":"paragraph","content":[{"type":"text","text":"Before"}]},{"type":"image","attrs":{"align":"left","alt":"A","linkTarget":"media","mediaId":"wp:post:5","size":"medium"}},{"type":"paragraph","content":[{"type":"text","text":"after"}]}]`, nil, ""},
		{"read more once", "<p>Teaser</p>\n<p><!--more--></p>\n<p>Rest<!--more--></p>",
			`[{"type":"paragraph","content":[{"type":"text","text":"Teaser"}]},{"type":"readMore"},{"type":"paragraph","content":[{"type":"text","text":"Rest"}]}]`, nil, "readmore_extra"},
		{"caption", `[caption id="attachment_5" align="alignright" width="300"]<img class="size-medium wp-image-5" src="https://blog.example.com/wp-content/uploads/a-300x200.jpg" alt="" /> Caption <em>text</em>[/caption]`,
			`[{"type":"figure","attrs":{"align":"right"},"content":[{"type":"image","attrs":{"align":"right","alt":"","mediaId":"wp:post:5","size":"medium"}},{"type":"figcaption","content":[{"type":"text","text":"Caption "},{"type":"text","marks":[{"type":"italic"}],"text":"text"}]}]}]`, nil, ""},
		{"gallery without ids", `[gallery columns="2" link="file"]`,
			`[{"type":"gallery","attrs":{"columns":2,"link":"media","size":"thumbnail"},"content":[{"type":"image","attrs":{"alt":"","mediaId":"wp:post:5","size":"thumbnail"}},{"type":"image","attrs":{"alt":"","mediaId":"wp:post:6","size":"thumbnail"}}]}]`, nil, "gallery_attached"},
		{"dropped enclosing shortcode keeps text", `<p>[ad slot="1"]Sponsored text[/ad]</p>`,
			`[{"type":"paragraph","content":[{"type":"text","text":"Sponsored text"}]}]`, nil, "shortcode_dropped"},
		{"unregistered brackets stay text", `<p>Quote [sic] and [1]</p>`,
			`[{"type":"paragraph","content":[{"type":"text","text":"Quote [sic] and [1]"}]}]`, nil, ""},
		{"render via php", "[pull]Quoted[/pull]",
			`[{"type":"rawHtml","attrs":{"html":"<blockquote class=\"pull\">Quoted </blockquote>"}}]`, nil, "shortcode_rendered"},
		{"script removed", `<p>Text</p><script>alert(1)</script>`,
			`[{"type":"paragraph","content":[{"type":"text","text":"Text"}]}]`, nil, "element_dropped"},
		{"youtube iframe", `<iframe src="https://www.youtube.com/embed/abcdefghijk" width="560" height="315"></iframe>`,
			`[{"type":"embed","attrs":{"externalId":"abcdefghijk","height":315,"provider":"youtube","url":"https://www.youtube.com/watch?v=abcdefghijk","width":560}}]`, nil, ""},
		{"other iframe stays raw", `<iframe src="https://forms.example.com/f"></iframe>`, `[]`, nil, "element_dropped"},
		{"bare url oembed", `<p>https://vimeo.com/12345</p>`,
			`[{"type":"embed","attrs":{"externalId":"12345","provider":"vimeo","url":"https://vimeo.com/12345"}}]`, nil, "oembed"},
		{"embed shortcode", `[embed]https://youtu.be/abcdefghijk[/embed]`,
			`[{"type":"embed","attrs":{"externalId":"abcdefghijk","provider":"youtube","url":"https://www.youtube.com/watch?v=abcdefghijk"}}]`, nil, ""},
		{"lists", `<ol start="3"><li>One</li><li>Two<ul><li>Nested</li></ul></li></ol>`,
			`[{"type":"orderedList","attrs":{"start":3},"content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"One"}]}]},{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Two"}]},{"type":"bulletList","content":[{"type":"listItem","content":[{"type":"paragraph","content":[{"type":"text","text":"Nested"}]}]}]}]}]}]`, nil, ""},
		{"table", `<table><thead><tr><th>H</th></tr></thead><tbody><tr><td colspan="2">C</td></tr></tbody></table>`,
			`[{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","content":[{"type":"paragraph","content":[{"type":"text","text":"H"}]}]}]},{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colspan":2},"content":[{"type":"paragraph","content":[{"type":"text","text":"C"}]}]}]}]}]`, nil, ""},
		{"container and unwrap", `<div class="callout"><p>In</p></div><div class="wrapper"><p>Out</p></div>`,
			`[{"type":"container","attrs":{"classes":"callout"},"content":[{"type":"paragraph","content":[{"type":"text","text":"In"}]}]},{"type":"paragraph","content":[{"type":"text","text":"Out"}]}]`, nil, "unwrap"},
		{"empty paragraphs dropped", "<p>&nbsp;</p>\n<p>x</p>", `[{"type":"paragraph","content":[{"type":"text","text":"x"}]}]`, nil, "empty_block"},
		{"empty paragraphs kept", "<p>&nbsp;</p>\n<p>x</p>",
			strings.Replace(`[{"type":"paragraph","content":[{"type":"text","text":"NBSP"}]},{"type":"paragraph","content":[{"type":"text","text":"x"}]}]`, "NBSP", "\u00a0", 1),
			map[string]string{"empty_paragraphs:empty_paragraphs": "keep"}, ""},
		{"missing upload keeps src", `<p><img src="https://blog.example.com/wp-content/uploads/gone.png" alt="g"></p>`,
			`[{"type":"image","attrs":{"alt":"g","src":"https://blog.example.com/wp-content/uploads/gone.png"}}]`, nil, "missing_media"},
		{"external image", `<img src="//cdn.other.com/x.png" alt="">`,
			`[{"type":"image","attrs":{"alt":"","src":"https://cdn.other.com/x.png"}}]`, nil, "external_image"},
		{"pre code", "<pre class=\"language-go\"><code>a := 1\n\tb()</code></pre>",
			`[{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"a := 1\n\tb()"}]}]`, nil, ""},
		{"colors", `<p><span style="color: red; background-color:#fff">c</span></p>`,
			`[{"type":"paragraph","content":[{"type":"text","marks":[{"type":"textStyle","attrs":{"backgroundColor":"#fff","color":"#ff0000"}}],"text":"c"}]}]`, nil, ""},
		{"javascript link dropped", `<p><a href="javascript:alert(1)">t</a></p>`,
			`[{"type":"paragraph","content":[{"type":"text","text":"t"}]}]`, nil, "link_dropped"},
		{"hard breaks", "<p>a<br />\nb<br /></p>",
			`[{"type":"paragraph","content":[{"type":"text","text":"a"},{"type":"hardBreak"},{"type":"text","text":"b"}]}]`, nil, ""},
		{"video", `<video controls src="https://blog.example.com/wp-content/uploads/clip.mp4"></video>`,
			`[{"type":"video","attrs":{"autoplay":false,"controls":true,"loop":false,"mediaId":"wp:post:9","muted":false}}]`, nil, ""},
		{"details", `<details><summary>S</summary><p>Body</p></details>`,
			`[{"type":"details","content":[{"type":"detailsSummary","content":[{"type":"text","text":"S"}]},{"type":"detailsContent","content":[{"type":"paragraph","content":[{"type":"text","text":"Body"}]}]}]}]`, nil, ""},
		{"blocks", `<!-- wp:heading {"level":3} --><h3 id="intro">Intro</h3><!-- /wp:heading --><!-- wp:image {"id":5,"sizeSlug":"large"} --><figure class="wp-block-image size-large"><img src="https://blog.example.com/wp-content/uploads/a.jpg" alt=""/><figcaption>Cap</figcaption></figure><!-- /wp:image --><!-- wp:acme/widget {"x":1} /-->`,
			`[{"type":"heading","attrs":{"id":"intro","level":3},"content":[{"type":"text","text":"Intro"}]},{"type":"figure","content":[{"type":"image","attrs":{"alt":"","mediaId":"wp:post:5","size":"large"}},{"type":"figcaption","content":[{"type":"text","text":"Cap"}]}]}]`, nil, "dynamic_block"},
		{"embed block", `<!-- wp:embed {"url":"https://www.youtube.com/watch?v=abcdefghijk","providerNameSlug":"youtube"} --><figure class="wp-block-embed"><div class="wp-block-embed__wrapper">https://www.youtube.com/watch?v=abcdefghijk</div><figcaption>Watch</figcaption></figure><!-- /wp:embed -->`,
			`[{"type":"embed","attrs":{"caption":"Watch","externalId":"abcdefghijk","provider":"youtube","url":"https://www.youtube.com/watch?v=abcdefghijk"}}]`, nil, ""},
		{"html block", `<!-- wp:html --><div onclick="x()">Hi</div><!-- /wp:html -->`,
			`[{"type":"rawHtml","attrs":{"html":"<div>Hi</div>"}}]`, nil, "raw_html"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, res := run(t, c.in, c.dec)
			if c.want == "[]" {
				c.want = `[{"type":"paragraph"}]`
			}
			if got != c.want {
				t.Errorf("\n got %s\nwant %s\nflags %+v", got, c.want, res.Flags)
			}
			if c.flag != "" && !hasFlag(res, c.flag) {
				t.Errorf("want flag %s, got %+v", c.flag, res.Flags)
			}
		})
	}
}

func TestNextpageCountsPages(t *testing.T) {
	_, res := run(t, "<p>One</p>\n<!--nextpage-->\n<p>Two</p><!--nextpage--><p>Three</p>", nil)
	if res.Pages != 3 || !hasFlag(res, "nextpage") {
		t.Fatalf("pages %d flags %+v", res.Pages, res.Flags)
	}
}

func TestKind(t *testing.T) {
	cases := map[string]string{
		"<p>x</p>": "classic",
		"<!-- wp:paragraph --><p>x</p><!-- /wp:paragraph -->":             "blocks",
		"intro text\n<!-- wp:paragraph --><p>x</p><!-- /wp:paragraph -->": "mixed",
	}
	for in, want := range cases {
		if got := Kind(in); got != want {
			t.Errorf("%q: %s want %s", in, got, want)
		}
	}
}

func TestEmbedFromURL(t *testing.T) {
	cases := map[string]string{
		"https://www.youtube.com/watch?v=abcdefghijk&t=10":                          "youtube https://www.youtube.com/watch?v=abcdefghijk",
		"https://youtube.com/shorts/abcdefghijk":                                    "youtube https://www.youtube.com/watch?v=abcdefghijk",
		"https://player.vimeo.com/video/76979871":                                   "vimeo https://vimeo.com/76979871",
		"https://twitter.com/example/status/123":                                    "x https://x.com/example/status/123",
		"spotify:track:4uLU6hMCjMI75M1A2tKUQC":                                      "spotify https://open.spotify.com/track/4uLU6hMCjMI75M1A2tKUQC",
		"https://open.spotify.com/embed/album/1A2B3C":                               "spotify https://open.spotify.com/album/1A2B3C",
		"https://w.soundcloud.com/player/?url=https%3A%2F%2Fsoundcloud.com%2Fa%2Fb": "soundcloud https://soundcloud.com/a/b",
		"https://www.google.com/maps/embed?pb=!1m18":                                "googlemaps https://www.google.com/maps/embed?pb=!1m18",
		"https://www.instagram.com/p/AbC123/":                                       "instagram https://www.instagram.com/p/AbC123/",
		"https://example.com/video":                                                 "",
		"https://api.soundcloud.com/tracks/1":                                       "",
	}
	for in, want := range cases {
		n, ok := EmbedFromURL(in)
		got := ""
		if ok {
			got = n.Attrs["provider"].(string) + " " + n.Attrs["url"].(string)
		}
		if got != want {
			t.Errorf("%s: got %q want %q", in, got, want)
		}
	}
}

func TestMarkerlessListWrapper(t *testing.T) {
	got, _ := run(t, `<ol><li style="list-style-type: none;"><ol><li>a</li><li>b</li></ol></li><li>c</li></ol>`, nil)
	if strings.Count(got, `"type":"listItem"`) != 3 || strings.Count(got, `"type":"orderedList"`) != 1 {
		t.Errorf("want one ordered list with 3 items, got %s", got)
	}
}
