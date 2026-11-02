package mediaidx

import (
	"strings"
	"testing"

	"afsc/migrators/afsc-wp/internal/extract"
)

func testOptions() Options {
	o := Options{Source: "wp", UploadsPath: "/wp-content/uploads", SiteHosts: []string{"blog.example.com", "www.blog.example.com"},
		CDNHosts: []string{"i0.wp.com", "i2.wp.com"}}
	o.Keys.AttachedFile, o.Keys.Metadata, o.Keys.Alt = "_wp_attached_file", "_wp_attachment_metadata", "_wp_attachment_image_alt"
	return o
}

func fixture() *extract.Dataset {
	meta := `a:5:{s:5:"width";i:2560;s:6:"height";i:1440;s:4:"file";s:22:"2021/03/big-scaled.jpg";s:5:"sizes";a:2:{s:6:"medium";a:3:{s:4:"file";s:15:"big-300x169.jpg";s:5:"width";i:300;s:6:"height";i:169;}s:9:"thumbnail";a:3:{s:4:"file";s:15:"big-150x150.jpg";s:5:"width";i:150;s:6:"height";i:150;}}s:14:"original_image";s:7:"big.jpg";}`
	return &extract.Dataset{
		Posts: []*extract.Post{
			{ID: 10, Type: "attachment", Parent: 5, MimeType: "image/jpeg", Title: "Big", DateGMT: "2021-03-01 00:00:00",
				Meta: extract.Meta{"_wp_attached_file": {"2021/03/big-scaled.jpg"}, "_wp_attachment_metadata": {meta}, "_wp_attachment_image_alt": {"Alt A"}}},
			{ID: 11, Type: "attachment", Parent: 5, MimeType: "image/jpeg", MenuOrder: -1,
				Meta: extract.Meta{"_wp_attached_file": {"2021/03/copy.jpg"}}},
			{ID: 12, Type: "attachment", Meta: extract.Meta{"_wp_attached_file": {"2021/03/missing.png"}}},
		},
		Files: []*extract.File{
			{Path: "2021/03/big.jpg", Media: true, SHA256: strings.Repeat("a", 64), Mime: "image/jpeg", Width: 4000, Height: 2250},
			{Path: "2021/03/big-scaled.jpg", Media: true, SHA256: strings.Repeat("b", 64), Mime: "image/jpeg", Width: 2560, Height: 1440},
			{Path: "2021/03/big-300x169.jpg", Media: true, SHA256: strings.Repeat("c", 64), Mime: "image/jpeg"},
			{Path: "2021/03/copy.jpg", Media: true, SHA256: strings.Repeat("a", 64), Mime: "image/jpeg"},
			{Path: "2019/01/signature logo.png", Media: true, SHA256: strings.Repeat("d", 64), Mime: "image/png"},
			{Path: "2019/01/signature.png", Media: true, SHA256: strings.Repeat("e", 64), Mime: "image/png", Width: 200, Height: 60},
			{Path: "backup.sql", Reason: "not a media file"},
		},
	}
}

func TestBuild(t *testing.T) {
	ix, media, rep := Build(fixture(), testOptions())
	if len(media) != 2 {
		t.Fatalf("want attachment + orphan, got %d: %+v", len(media), media)
	}
	if media[0].LegacyKey != "wp:file:eeeeeeeeeeeeeeee" || media[1].LegacyKey != "wp:post:10" {
		t.Fatalf("order: %s %s", media[0].LegacyKey, media[1].LegacyKey)
	}
	big := ix.Media("wp:post:10")
	if big == nil || big.SourcePath != "2021/03/big.jpg" || big.Filename != "big.jpg" || big.Alt != "Alt A" || *big.Width != 4000 {
		t.Fatalf("big: %+v", big)
	}
	paths := map[string]string{}
	for _, lp := range big.LegacyPaths {
		paths[lp.Path] = lp.Variant
		if lp.Path == "/wp-content/uploads/2021/03/big-150x150.jpg" && !lp.Crop {
			t.Errorf("thumbnail should be a crop")
		}
		if lp.Path == "/wp-content/uploads/2021/03/big-300x169.jpg" && lp.Crop {
			t.Errorf("medium keeps the aspect ratio")
		}
	}
	for p, v := range map[string]string{
		"/wp-content/uploads/2021/03/big.jpg": "orig", "/wp-content/uploads/2021/03/big-scaled.jpg": "resize",
		"/wp-content/uploads/2021/03/copy.jpg": "orig", "/wp-content/uploads/2021/03/big-300x169.jpg": "resize",
	} {
		if paths[p] != v {
			t.Errorf("%s: got %q want %q (all: %v)", p, paths[p], v, paths)
		}
	}
	if rep.Aliases["wp:post:11"] != "wp:post:10" {
		t.Errorf("aliases %v", rep.Aliases)
	}
	if len(rep.Missing) != 1 || rep.Missing[0].Key != "wp:post:12" {
		t.Errorf("missing %v", rep.Missing)
	}
	if len(rep.Unservable) != 1 || !strings.Contains(rep.Unservable[0], "signature logo.png") {
		t.Errorf("unservable %v", rep.Unservable)
	}
	if len(rep.NotImported) != 1 {
		t.Errorf("not imported %v", rep.NotImported)
	}
	if k, _ := ix.Attachment(11); k != "wp:post:10" {
		t.Errorf("alias attachment %s", k)
	}
	if got := ix.AttachedImages(5); len(got) != 2 || got[0] != 11 {
		t.Errorf("attached order %v", got)
	}
}

func TestResolve(t *testing.T) {
	ix, _, _ := Build(fixture(), testOptions())
	cases := []struct {
		url, key, size string
		ok             bool
	}{
		{"https://blog.example.com/wp-content/uploads/2021/03/big-300x169.jpg", "wp:post:10", "medium", true},
		{"http://www.blog.example.com/wp-content/uploads/2021/03/big-scaled.jpg", "wp:post:10", "full", true},
		{"/wp-content/uploads/2021/03/big.jpg", "wp:post:10", "", true},
		{"wp-content/uploads/2021/03/copy.jpg", "wp:post:10", "", true},
		{"https://i2.wp.com/blog.example.com/wp-content/uploads/2021/03/big.jpg?resize=300%2C200&ssl=1", "wp:post:10", "", true},
		{"https://blog.example.com/wp-content/uploads/2021/03/big-640x360.jpg", "wp:post:10", "", true},
		{"https://blog.example.com/wp-content/uploads/2019/01/signature.png", "wp:file:eeeeeeeeeeeeeeee", "", true},
		{"https://blog.example.com/wp-content/uploads/2021/03/missing.png", "", "", false},
		{"https://cdn.other.com/wp-content/uploads/2021/03/big.jpg", "", "", false},
		{"https://i0.wp.com/other.com/wp-content/uploads/2021/03/big.jpg", "", "", false},
	}
	for _, c := range cases {
		ref, _, ok := ix.Resolve(c.url)
		if ok != c.ok || ref.Key != c.key || ref.Size != c.size {
			t.Errorf("%s: got %+v %v", c.url, ref, ok)
		}
	}
	if !ix.IsUpload("https://blog.example.com/wp-content/uploads/2021/03/missing.png") {
		t.Error("missing file is still an upload URL")
	}
}
