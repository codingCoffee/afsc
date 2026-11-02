package main

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPublicURLs(t *testing.T) {
	cases := []struct{ in, origin, path string }{
		{"https://blog.example.com", "https://blog.example.com", ""},
		{"https://blog.example.com/", "https://blog.example.com", ""},
		{"https://example.com/varsity", "https://example.com", "/varsity"},
		{"https://example.com/varsity/", "https://example.com", "/varsity"},
	}
	for _, c := range cases {
		t.Setenv("AFSC_PUBLIC_BASE_URL", c.in)
		o, p, err := publicURLs()
		if err != nil || o != c.origin || p != c.path {
			t.Errorf("%s: got %q %q %v", c.in, o, p, err)
		}
	}
	for _, bad := range []string{"", "ftp://x.com", "https://x.com/admin", "https://x.com/Blog", "https://x.com/a?b=1"} {
		t.Setenv("AFSC_PUBLIC_BASE_URL", bad)
		if _, _, err := publicURLs(); err == nil {
			t.Errorf("%q: want error", bad)
		}
	}
}

func TestPrefixHTML(t *testing.T) {
	in := `<!doctype html><html><head><link rel="stylesheet" href="/_assets/abc/style.css">` +
		`<link rel="canonical" href="https://example.com/varsity/x/"></head><body>` +
		`<a href="/chapter/x/">x</a> <a href="//cdn.example.com/a.js">cdn</a> <a href="#top">t</a> ` +
		`<a href="/varsity/already/">a</a> <img src="/m/1/ab/w300/a.jpg" srcset="/m/1/ab/w320/a.jpg 320w, /m/1/ab/w640/a.jpg 640w" alt="">` +
		`<form action="/comments" method="post"></form><script>var s = "<a href=\"/x\">";</script>` +
		`<p>Text with &amp; entity and href="/not-a-tag"</p></body></html>`
	got := string(prefixHTML([]byte(in), "/varsity"))
	for _, want := range []string{
		`href="/varsity/_assets/abc/style.css"`,
		`href="https://example.com/varsity/x/"`,
		`href="/varsity/chapter/x/"`,
		`href="//cdn.example.com/a.js"`,
		`href="#top"`,
		`href="/varsity/already/"`,
		`src="/varsity/m/1/ab/w300/a.jpg"`,
		`srcset="/varsity/m/1/ab/w320/a.jpg 320w, /varsity/m/1/ab/w640/a.jpg 640w"`,
		`action="/varsity/comments"`,
		`var s = "<a href=\"/x\">";`,
		`<p>Text with &amp; entity and href="/not-a-tag"</p>`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in\n%s", want, got)
		}
	}
	if strings.Contains(got, "/varsity/varsity/") {
		t.Errorf("double prefix:\n%s", got)
	}
}

func TestWithBasePath(t *testing.T) {
	var seen string
	inner := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		switch r.URL.Path {
		case "/old/":
			http.Redirect(w, r, "/new/", http.StatusMovedPermanently)
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("PNG"))
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", "22")
			_, _ = io.WriteString(w, `<a href="/next/">n</a>`)
		}
	})
	h := withBasePath(inner, "/varsity")
	do := func(path string) *httptest.ResponseRecorder {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		return rec
	}

	rec := do("/varsity/page/")
	if seen != "/page/" || !strings.Contains(rec.Body.String(), `href="/varsity/next/"`) {
		t.Errorf("page: seen %q body %q", seen, rec.Body.String())
	}
	if rec.Header().Get("Content-Length") != "30" {
		t.Errorf("content-length %q", rec.Header().Get("Content-Length"))
	}
	if rec = do("/varsity/old/"); rec.Code != 301 || rec.Header().Get("Location") != "/varsity/new/" {
		t.Errorf("redirect: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec = do("/varsity/img"); rec.Body.String() != "PNG" {
		t.Errorf("binary body changed: %q", rec.Body.String())
	}
	if rec = do("/varsity"); rec.Code != 301 || rec.Header().Get("Location") != "/varsity/" {
		t.Errorf("bare prefix: %d %q", rec.Code, rec.Header().Get("Location"))
	}
	for path, want := range map[string]string{"/varsity/admin": "/admin", "/varsity/admin/dashboard?x=1": "/admin/dashboard?x=1"} {
		if rec = do(path); rec.Code != http.StatusFound || rec.Header().Get("Location") != want {
			t.Errorf("%s: %d %q, want redirect to %q", path, rec.Code, rec.Header().Get("Location"), want)
		}
	}
	if rec = do("/varsity/administrator/"); seen != "/administrator/" {
		t.Errorf("only /admin itself redirects: seen %q", seen)
	}
	if rec = do("/admin/x"); seen != "/admin/x" || !strings.Contains(rec.Body.String(), `href="/next/"`) {
		t.Errorf("outside prefix must pass through: seen %q body %q", seen, rec.Body.String())
	}
}
