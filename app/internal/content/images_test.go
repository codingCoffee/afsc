package content

import (
	"strings"
	"testing"
)

var testSizes = &ImageSizes{
	Sizes: map[string]ImageSize{
		"thumbnail":    {Width: 150, Height: 150, Crop: true},
		"medium":       {Width: 300, Height: 300},
		"medium_large": {Width: 768},
		"large":        {Width: 1024, Height: 1024},
	},
	Widths: []int{320, 640, 960, 1280, 1920},
}

var (
	chart = testMedia[7]
	small = MediaInfo{ID: 9, Filename: "small.png", Version: "00112233", Mime: "image/png", Width: 100, Height: 50}
	pdf   = MediaInfo{ID: 10, Filename: "doc.pdf", Version: "44556677", Mime: "application/pdf"}
)

func TestImageVariant(t *testing.T) {
	cases := []struct {
		m       MediaInfo
		size    string
		variant string
		w, h    int
		ok      bool
	}{
		{chart, "", "orig", 1600, 900, true},
		{chart, "full", "orig", 1600, 900, true},
		{chart, "medium", "w300", 300, 169, true},
		{chart, "medium_large", "w768", 768, 432, true},
		{chart, "large", "w1024", 1024, 576, true},
		{chart, "thumbnail", "c150x150", 150, 150, true},
		{chart, "huge", "orig", 1600, 900, false},
		{small, "medium", "orig", 100, 50, true},
		{small, "thumbnail", "c150x150", 50, 50, true},
		{pdf, "medium", "orig", 0, 0, true},
	}
	for _, c := range cases {
		v, w, h, ok := testSizes.Variant(c.m, c.size)
		if v != c.variant || w != c.w || h != c.h || ok != c.ok {
			t.Errorf("%s/%s: got %s %dx%d %v, want %s %dx%d %v", c.m.Filename, c.size, v, w, h, ok, c.variant, c.w, c.h, c.ok)
		}
	}
}

func TestImageAllowed(t *testing.T) {
	cases := []struct {
		m       MediaInfo
		variant string
		want    bool
	}{
		{chart, "orig", true},
		{chart, "w320", true},   // srcset width
		{chart, "w1920", true},  // srcset width above the original: imgproxy won't enlarge
		{chart, "w300", true},   // medium, fitted to this image
		{chart, "w1024", true},  // large
		{chart, "w768", true},   // medium_large
		{chart, "w500", false},  // arbitrary
		{chart, "w0320", false}, // second spelling
		{chart, "c150x150", true},
		{chart, "c300x300", false},
		{chart, "c150", false},
		{chart, "x", false},
		{pdf, "orig", true},
		{pdf, "w320", false},
		{MediaInfo{Mime: "image/svg+xml", Width: 10, Height: 10}, "w320", false},
	}
	for _, c := range cases {
		if got := testSizes.Allowed(c.m, c.variant); got != c.want {
			t.Errorf("%s %s: got %v, want %v", c.m.Mime, c.variant, got, c.want)
		}
	}
}

func TestParseVariant(t *testing.T) {
	cases := []struct {
		in       string
		w, h     int
		crop, ok bool
	}{
		{"w640", 640, 0, false, true},
		{"c150x100", 150, 100, true, true},
		{"w0", 0, 0, false, false},
		{"w01", 0, 0, false, false},
		{"w123456", 0, 0, false, false},
		{"c150", 0, 0, true, false},
		{"cx150", 0, 0, true, false},
		{"orig", 0, 0, false, false},
	}
	for _, c := range cases {
		w, h, crop, ok := ParseVariant(c.in)
		if ok != c.ok || (ok && (w != c.w || h != c.h || crop != c.crop)) {
			t.Errorf("%s: got %d %d %v %v", c.in, w, h, crop, ok)
		}
	}
}

func TestImageSrcset(t *testing.T) {
	u := func(v string, w string) string { return "/m/7/ab12cd34/" + v + "/chart%20one.jpg " + w + "w" }
	want := strings.Join([]string{u("w320", "320"), u("w640", "640"), u("w960", "960"), u("w1024", "1024"),
		u("w1280", "1280"), u("orig", "1600")}, ", ")
	if got := testSizes.Srcset(chart, "w1024"); got != want {
		t.Errorf("srcset:\n got %s\nwant %s", got, want)
	}
	for _, c := range []struct {
		m       MediaInfo
		variant string
	}{{chart, "c150x150"}, {pdf, "orig"}, {small, "orig"}} {
		if got := testSizes.Srcset(c.m, c.variant); got != "" {
			t.Errorf("%s %s: want no srcset, got %s", c.m.Filename, c.variant, got)
		}
	}
}

func TestRenderImageSizes(t *testing.T) {
	doc := `{"type":"doc","content":[{"type":"image","attrs":{"mediaId":7,"alt":"a","size":"large"}},
    {"type":"gallery","attrs":{"columns":2,"size":"thumbnail"},"content":[{"type":"image","attrs":{"mediaId":7,"alt":"b"}}]}]}`
	ctx := testCtx()
	ctx.Images = testSizes
	h, _, err := Render(mustParse(t, doc), ctx)
	if err != nil {
		t.Fatal(err)
	}
	srcset := testSizes.Srcset(chart, "w1024")
	for _, want := range []string{
		`<img src="/m/7/ab12cd34/w1024/chart%20one.jpg" alt="a" width="1024" height="576" class="wp-image-123 size-large" srcset="` +
			srcset + `" sizes="(max-width: 1024px) 100vw, 1024px">`,
		`<img src="/m/7/ab12cd34/c150x150/chart%20one.jpg" alt="b" width="150" height="150" class="wp-image-123 size-thumbnail" loading="lazy"`,
	} {
		if !strings.Contains(h, want) {
			t.Errorf("missing\n%s\nin\n%s", want, h)
		}
	}

	ctx.Feed, ctx.BaseURL = true, "https://blog.example.com"
	h, _, err = Render(mustParse(t, doc), ctx)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(h, "srcset") || !strings.Contains(h, `src="https://blog.example.com/m/7/ab12cd34/w1024/chart%20one.jpg"`) {
		t.Errorf("feed images need an absolute sized src and no srcset: %s", h)
	}
}
