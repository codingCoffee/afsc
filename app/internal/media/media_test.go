package media

import (
	"testing"
)

func TestParsePath(t *testing.T) {
	cases := []struct {
		in   string
		want Path
		ok   bool
	}{
		{"/m/7/ab12cd34/orig/chart.png", Path{7, "ab12cd34", "orig", "chart.png"}, true},
		{"/m/7/ab12cd34/w640/chart%20one.jpg", Path{7, "ab12cd34", "w640", "chart one.jpg"}, true},
		{"/m/7/ab12cd34/c150x150/a%2Fb.png", Path{7, "ab12cd34", "c150x150", "a/b.png"}, true},
		{"/m/7/AB12CD34/orig/chart.png", Path{}, false},
		{"/m/07/ab12cd34/orig/chart.png", Path{}, false},
		{"/m/7/ab12cd3/orig/chart.png", Path{}, false},
		{"/m/7/ab12cd34/w0640/chart.png", Path{}, false},
		{"/m/7/ab12cd34/large/chart.png", Path{}, false},
		{"/m/7/ab12cd34/orig/", Path{}, false},
		{"/m/7/ab12cd34/orig/a/b.png", Path{}, false},
		{"/m/7/ab12cd34/orig", Path{}, false},
		{"/m/7/ab12cd34/orig/%zz", Path{}, false},
		{"/media/7/ab12cd34/orig/chart.png", Path{}, false},
	}
	for _, c := range cases {
		got, ok := ParsePath(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("%s: got %+v %v, want %+v %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestFileLike(t *testing.T) {
	cases := map[string]bool{
		"/wp-content/uploads/2020/05/chart.png": true,
		"/files/signature-logo.PNG":             true,
		"/example-post/":                        false,
		"/example-post":                         false,
		"/robots.txt":                           true,
		"/a.verylongext":                        false,
		"/.":                                    false,
	}
	for in, want := range cases {
		if got := fileLike(in); got != want {
			t.Errorf("%s: got %v, want %v", in, got, want)
		}
	}
}

func TestNegotiate(t *testing.T) {
	const chrome = "image/avif,image/webp,image/apng,image/svg+xml,image/*,*/*;q=0.8"
	cases := []struct {
		accept, mime, want string
	}{
		{chrome, "image/jpeg", "avif"},
		{"image/webp,*/*", "image/png", "webp"},
		{"image/avif;q=0,image/webp", "image/jpeg", "webp"},
		{"image/avif; q=0.5", "image/jpeg", "avif"},
		{"*/*", "image/jpeg", "jpg"},
		{"image/*", "image/png", "png"},
		{"", "image/gif", "gif"},
		{chrome, "image/gif", "webp"},
		{"*/*", "image/webp", "png"},
		{"IMAGE/WEBP", "image/jpeg", "webp"},
		{"image/webp;q=bad", "image/jpeg", "jpg"},
	}
	for _, c := range cases {
		if got := Negotiate(c.accept, c.mime); got != c.want {
			t.Errorf("Accept %q, %s: got %s, want %s", c.accept, c.mime, got, c.want)
		}
	}
}

func TestTypeForPath(t *testing.T) {
	cases := []struct {
		path, mime, want, format string
	}{
		{"/wp-content/uploads/a.png", "image/png", "image/png", "png"},
		{"/wp-content/uploads/a.JPG", "image/jpeg", "image/jpeg", "jpg"},
		{"/wp-content/uploads/a.jpeg", "image/png", "image/jpeg", "jpg"},
		{"/wp-content/uploads/a.pdf", "application/pdf", "application/pdf", ""},
		{"/wp-content/uploads/a.svg", "image/svg+xml", "image/svg+xml", ""},
		{"/wp-content/uploads/a.bin", "application/octet-stream", "application/octet-stream", ""},
	}
	for _, c := range cases {
		if got := typeForPath(c.path, c.mime); got != c.want {
			t.Errorf("%s: type %s, want %s", c.path, got, c.want)
		}
		if got := extFormat(c.path); got != c.format {
			t.Errorf("%s: format %q, want %q", c.path, got, c.format)
		}
	}
}
