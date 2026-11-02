package share

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"afsc/internal/plugins/plugintest"
	"afsc/internal/settings"
	"afsc/internal/theme"
)

func TestLinks(t *testing.T) {
	p := &theme.Post{Title: `Q&A: "Taxes" — naïve 100% guide`, URL: "https://blog.example.com/q-a/?x=1&y=2"}
	links, err := Links(p)
	if err != nil {
		t.Fatal(err)
	}
	u, title := "https%3A%2F%2Fblog.example.com%2Fq-a%2F%3Fx%3D1%26y%3D2", "Q%26A%3A%20%22Taxes%22%20%E2%80%94%20na%C3%AFve%20100%25%20guide"
	want := []Link{
		{"x", "X", "https://x.com/intent/post?url=" + u + "&text=" + title},
		{"linkedin", "LinkedIn", "https://www.linkedin.com/sharing/share-offsite/?url=" + u},
		{"facebook", "Facebook", "https://www.facebook.com/sharer/sharer.php?u=" + u},
		{"whatsapp", "WhatsApp", "https://wa.me/?text=" + title + "%20" + u},
		{"telegram", "Telegram", "https://t.me/share/url?url=" + u + "&text=" + title},
		{"email", "Email", "mailto:?subject=" + title + "&body=" + u},
	}
	if len(links) != len(want) {
		t.Fatalf("got %d links", len(links))
	}
	for i := range want {
		if links[i] != want[i] {
			t.Errorf("%s:\n got %s\nwant %s", want[i].Network, links[i].URL, want[i].URL)
		}
	}
}

func TestFilter(t *testing.T) {
	p := &theme.Post{Title: "T", URL: "https://blog.example.com/t/"}
	links, err := Links(p, "email", "x")
	if err != nil || len(links) != 2 || links[0].Network != "x" || links[1].Network != "email" {
		t.Errorf("filter keeps the fixed order: %+v %v", links, err)
	}
	if _, err := Links(p, "myspace"); err == nil {
		t.Error("an unknown network must be an error, for theme check")
	}
	if links, err := Links(nil); links != nil || err != nil {
		t.Errorf("nil post: %v %v", links, err)
	}
}

func TestRenderedOutputIsEscaped(t *testing.T) {
	s := settings.Defaults()
	rt := plugintest.Runtime(t, &s, New())
	tpl := template.Must(template.New("t").Funcs(rt.Funcs()).Parse(
		`{{range plg_share_links .}}<a href="{{.URL}}">{{.Label}}</a>{{end}}`))
	var b bytes.Buffer
	p := &theme.Post{Title: `"><script>alert(1)</script>`, URL: "https://blog.example.com/x/"}
	if err := tpl.Execute(&b, p); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	if strings.Contains(out, "<script>") || !strings.Contains(out, `href="mailto:?subject=%22%3E%3Cscript%3E`) {
		t.Errorf("output %s", out)
	}
	s.Plugins.Enabled = map[string]bool{"share": false}
	b.Reset()
	if err := tpl.Execute(&b, p); err != nil || b.Len() != 0 {
		t.Errorf("disabled: %q %v", b.String(), err)
	}
}
