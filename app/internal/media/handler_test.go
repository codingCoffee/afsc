package media

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"afsc/internal/content"
	"afsc/internal/imgproxy"
	"afsc/internal/storage"
)

var modTime = time.Date(2021, 3, 4, 5, 6, 7, 0, time.UTC)

const sha = "ab12cd34ef567890ab12cd34ef567890ab12cd34ef567890ab12cd34ef567890"

func rec(id int64, name, mime string, w, h int, body string) *Record {
	return &Record{ID: id, Key: "o/" + name, Filename: name, Mime: mime, Bytes: int64(len(body)), SHA256: sha,
		Width: w, Height: h, UpdatedAt: modTime}
}

type fakeStore struct {
	media  map[int64]*Record
	legacy map[string]*Legacy
	hits   map[string]Hit
}

func (f *fakeStore) Media(_ context.Context, id int64) (*Record, error) { return f.media[id], nil }

func (f *fakeStore) LegacyPath(_ context.Context, paths []string) (*Legacy, error) {
	for _, p := range paths {
		if l := f.legacy[p]; l != nil {
			return l, nil
		}
	}
	return nil, nil
}

func (f *fakeStore) AddHits(_ context.Context, hits map[string]Hit) error {
	for p, h := range hits {
		cur := f.hits[p]
		cur.N += h.N
		cur.Last = h.Last
		f.hits[p] = cur
	}
	return nil
}

type fakeStorage map[string]string

func (f fakeStorage) Get(_ context.Context, key, rng string) (*storage.Object, error) {
	b, ok := f[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	o := &storage.Object{Size: int64(len(b)), LastModified: modTime}
	if rng != "" {
		var a, z int
		if _, err := fmt.Sscanf(rng, "bytes=%d-%d", &a, &z); err != nil || a >= len(b) {
			return nil, storage.ErrRange
		}
		z = min(z, len(b)-1)
		o.ContentRange = fmt.Sprintf("bytes %d-%d/%d", a, z, len(b))
		b = b[a : z+1]
	}
	o.Body, o.Length = io.NopCloser(strings.NewReader(b)), int64(len(b))
	return o, nil
}

func (f fakeStorage) Head(_ context.Context, key string) (*storage.Object, error) {
	b, ok := f[key]
	if !ok {
		return nil, storage.ErrNotFound
	}
	return &storage.Object{Size: int64(len(b)), Length: int64(len(b)), LastModified: modTime}, nil
}

// fakeImager records requests and answers with a description of the options.
type fakeImager struct{ calls []imgproxy.Options }

func (f *fakeImager) Fetch(_ context.Context, key string, o imgproxy.Options) (*imgproxy.Image, error) {
	f.calls = append(f.calls, o)
	if strings.HasSuffix(key, "missing.png") {
		return nil, imgproxy.ErrNotFound
	}
	body := fmt.Sprintf("%s %dx%d crop=%v %s", key, o.Width, o.Height, o.Crop, o.Format)
	return &imgproxy.Image{Body: io.NopCloser(strings.NewReader(body)), ContentType: formats[o.Format], ContentLength: int64(len(body))}, nil
}

var testSizes = &content.ImageSizes{
	Sizes:  map[string]content.ImageSize{"thumbnail": {Width: 150, Height: 150, Crop: true}, "medium": {Width: 300, Height: 300}},
	Widths: []int{320, 640},
}

func newTestServer() (*Server, *fakeStore, *fakeImager) {
	chart := rec(7, "chart.png", "image/png", 1600, 900, "PNGDATA")
	pdf := rec(8, "paper.pdf", "application/pdf", 0, 0, "%PDF-1.4 data")
	svg := rec(9, "logo.svg", "image/svg+xml", 0, 0, "<svg/>")
	gone := rec(10, "missing.png", "image/png", 10, 10, "")
	st := &fakeStore{media: map[int64]*Record{7: chart, 8: pdf, 9: svg, 10: gone}, legacy: map[string]*Legacy{}, hits: map[string]Hit{}}
	add := func(l Legacy) { st.legacy[l.Path] = &l }
	add(Legacy{Path: "/wp-content/uploads/2020/05/chart.png", Variant: "orig", Media: *chart})
	add(Legacy{Path: "/wp-content/uploads/2020/05/chart-300x169.png", Variant: "resize", Width: 300, Height: 169, Media: *chart})
	add(Legacy{Path: "/wp-content/uploads/2020/05/chart-150x150.png", Variant: "resize", Width: 150, Height: 150, Crop: true, Media: *chart})
	add(Legacy{Path: "/wp-content/uploads/2021/02/paper.pdf", Variant: "orig", Media: *pdf})
	add(Legacy{Path: "/wp-content/uploads/2021/02/caf%C3%A9.png", Variant: "orig", Media: *chart})
	store := fakeStorage{"o/chart.png": "PNGDATA", "o/paper.pdf": "%PDF-1.4 data", "o/logo.svg": "<svg/>"}
	im := &fakeImager{}
	s := New(Config{Store: st, Storage: store, Imgproxy: im, Images: func() *content.ImageSizes { return testSizes }})
	return s, st, im
}

func do(h func(http.ResponseWriter, *http.Request) bool, method, target string, hdr ...string) (*httptest.ResponseRecorder, bool) {
	r := httptest.NewRequest(method, target, nil)
	for i := 0; i+1 < len(hdr); i += 2 {
		r.Header.Set(hdr[i], hdr[i+1])
	}
	w := httptest.NewRecorder()
	return w, h(w, r)
}

func TestServeMedia(t *testing.T) {
	s, _, im := newTestServer()
	const webp = "image/webp,*/*"
	cases := []struct {
		name, target, accept string
		handled              bool
		status               int
		ctype, body, vary    string
	}{
		{"orig png via imgproxy", "/m/7/ab12cd34/orig/chart.png", "*/*", true, 200, "image/png", "o/chart.png 0x0 crop=false png", "Accept"},
		{"orig negotiated", "/m/7/ab12cd34/orig/chart.png", webp, true, 200, "image/webp", "o/chart.png 0x0 crop=false webp", "Accept"},
		{"srcset width", "/m/7/ab12cd34/w640/chart.png", "image/avif,image/webp", true, 200, "image/avif", "o/chart.png 640x0 crop=false avif", "Accept"},
		{"size width", "/m/7/ab12cd34/w300/chart.png", "", true, 200, "image/png", "o/chart.png 300x0 crop=false png", "Accept"},
		{"crop", "/m/7/ab12cd34/c150x150/chart.png", "", true, 200, "image/png", "o/chart.png 150x150 crop=true png", "Accept"},
		{"width not allowed", "/m/7/ab12cd34/w500/chart.png", "", false, 0, "", "", ""},
		{"crop not allowed", "/m/7/ab12cd34/c300x300/chart.png", "", false, 0, "", "", ""},
		{"unknown id", "/m/99/ab12cd34/orig/chart.png", "", false, 0, "", "", ""},
		{"malformed", "/m/7/ab12cd34/orig", "", false, 0, "", "", ""},
		{"pdf raw", "/m/8/ab12cd34/orig/paper.pdf", webp, true, 200, "application/pdf", "%PDF-1.4 data", ""},
		{"pdf resize refused", "/m/8/ab12cd34/w320/paper.pdf", "", false, 0, "", "", ""},
		{"svg raw", "/m/9/ab12cd34/orig/logo.svg", webp, true, 200, "image/svg+xml", "<svg/>", ""},
		{"imgproxy 404", "/m/10/ab12cd34/orig/missing.png", "", true, 404, "", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			w, handled := do(s.ServeMedia, "GET", c.target, "Accept", c.accept)
			if handled != c.handled {
				t.Fatalf("handled %v, want %v", handled, c.handled)
			}
			if !handled {
				if w.Body.Len() > 0 || len(w.Header()) > 0 {
					t.Errorf("an unhandled request must write nothing")
				}
				return
			}
			if w.Code != c.status {
				t.Fatalf("status %d, want %d", w.Code, c.status)
			}
			if c.status != 200 {
				return
			}
			h := w.Header()
			if h.Get("Content-Type") != c.ctype || w.Body.String() != c.body || h.Get("Vary") != c.vary {
				t.Errorf("got %q %q vary %q", h.Get("Content-Type"), w.Body.String(), h.Get("Vary"))
			}
			if h.Get("Cache-Control") != "public, max-age=31536000, immutable" || h.Get("ETag") == "" {
				t.Errorf("cache headers: %v", h)
			}
		})
	}
	if len(im.calls) == 0 {
		t.Fatal("imgproxy was never called")
	}

	t.Run("stale version and filename redirect", func(t *testing.T) {
		for _, target := range []string{"/m/7/00000000/w640/chart.png", "/m/7/ab12cd34/w640/old-name.png"} {
			w, _ := do(s.ServeMedia, "GET", target)
			if w.Code != 301 || w.Header().Get("Location") != "/m/7/ab12cd34/w640/chart.png" {
				t.Errorf("%s: %d %q", target, w.Code, w.Header().Get("Location"))
			}
		}
	})

	t.Run("svg is sandboxed", func(t *testing.T) {
		w, _ := do(s.ServeMedia, "GET", "/m/9/ab12cd34/orig/logo.svg")
		h := w.Header()
		if h.Get("Content-Security-Policy") != "sandbox; default-src 'none'" || h.Get("Content-Disposition") != "attachment" ||
			h.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("svg headers: %v", h)
		}
	})

	t.Run("etag 304 differs by format", func(t *testing.T) {
		w, _ := do(s.ServeMedia, "GET", "/m/7/ab12cd34/w320/chart.png", "Accept", webp)
		tag := w.Header().Get("ETag")
		n := len(im.calls)
		w, _ = do(s.ServeMedia, "GET", "/m/7/ab12cd34/w320/chart.png", "Accept", webp, "If-None-Match", tag)
		if w.Code != 304 || len(im.calls) != n {
			t.Errorf("want a 304 without calling imgproxy, got %d", w.Code)
		}
		w, _ = do(s.ServeMedia, "GET", "/m/7/ab12cd34/w320/chart.png", "If-None-Match", tag)
		if w.Code != 200 {
			t.Errorf("a different format must not match the webp etag, got %d", w.Code)
		}
	})

	t.Run("range on raw files", func(t *testing.T) {
		w, _ := do(s.ServeMedia, "GET", "/m/8/ab12cd34/orig/paper.pdf", "Range", "bytes=0-3")
		if w.Code != 206 || w.Body.String() != "%PDF" || w.Header().Get("Content-Range") != "bytes 0-3/13" {
			t.Errorf("range: %d %q %v", w.Code, w.Body.String(), w.Header())
		}
		w, _ = do(s.ServeMedia, "GET", "/m/8/ab12cd34/orig/paper.pdf", "Range", "bytes=50-60")
		if w.Code != 416 || w.Header().Get("Content-Range") != "bytes */13" {
			t.Errorf("unsatisfiable range: %d %v", w.Code, w.Header())
		}
		w, _ = do(s.ServeMedia, "GET", "/m/8/ab12cd34/orig/paper.pdf", "Range", "bytes=0-1,4-5")
		if w.Code != 200 || w.Body.Len() != 13 {
			t.Errorf("multi-range should get the whole file: %d", w.Code)
		}
		w, _ = do(s.ServeMedia, "GET", "/m/8/ab12cd34/orig/paper.pdf", "Range", "bytes=0-3", "If-Range", `"other"`)
		if w.Code != 200 {
			t.Errorf("a stale If-Range should get the whole file: %d", w.Code)
		}
	})

	t.Run("head", func(t *testing.T) {
		w, _ := do(s.ServeMedia, "HEAD", "/m/8/ab12cd34/orig/paper.pdf")
		if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != "13" {
			t.Errorf("head: %d %d %v", w.Code, w.Body.Len(), w.Header())
		}
	})
}

func TestServeLegacy(t *testing.T) {
	s, st, im := newTestServer()
	cases := []struct {
		name, target string
		handled      bool
		ctype, body  string
	}{
		{"orig is byte-identical", "/wp-content/uploads/2020/05/chart.png", true, "image/png", "PNGDATA"},
		{"resize", "/wp-content/uploads/2020/05/chart-300x169.png", true, "image/png", "o/chart.png 300x169 crop=false png"},
		{"crop", "/wp-content/uploads/2020/05/chart-150x150.png", true, "image/png", "o/chart.png 150x150 crop=true png"},
		{"pdf", "/wp-content/uploads/2021/02/paper.pdf", true, "application/pdf", "%PDF-1.4 data"},
		{"escaped path", "/wp-content/uploads/2021/02/caf%C3%A9.png", true, "image/png", "PNGDATA"},
		{"case-sensitive miss", "/wp-content/uploads/2020/05/Chart.png", false, "", ""},
		{"unknown path", "/wp-content/uploads/2020/05/other.png", false, "", ""},
		{"page path skips lookup", "/example-post/", false, "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			// browsers asking for AVIF/WebP must still get the extension's format
			w, handled := do(s.ServeLegacy, "GET", c.target, "Accept", "image/avif,image/webp,*/*")
			if handled != c.handled {
				t.Fatalf("handled %v, want %v", handled, c.handled)
			}
			if !handled {
				return
			}
			h := w.Header()
			if w.Code != 200 || h.Get("Location") != "" {
				t.Fatalf("status %d, location %q", w.Code, h.Get("Location"))
			}
			if h.Get("Content-Type") != c.ctype || w.Body.String() != c.body {
				t.Errorf("got %q %q", h.Get("Content-Type"), w.Body.String())
			}
			if h.Get("Vary") != "" || h.Get("Cache-Control") != "public, max-age=86400" || h.Get("ETag") == "" ||
				h.Get("Last-Modified") != modTime.Format(http.TimeFormat) {
				t.Errorf("headers: %v", h)
			}
		})
	}
	for _, o := range im.calls {
		if o.Format != "png" {
			t.Errorf("legacy resize negotiated %s", o.Format)
		}
	}

	t.Run("not modified", func(t *testing.T) {
		w, _ := do(s.ServeLegacy, "GET", "/wp-content/uploads/2020/05/chart.png", "If-Modified-Since", modTime.Format(http.TimeFormat))
		if w.Code != 304 {
			t.Errorf("If-Modified-Since: %d", w.Code)
		}
	})

	t.Run("hits are counted in memory and flushed", func(t *testing.T) {
		if len(st.hits) != 0 {
			t.Fatal("hits were written before a flush")
		}
		s.FlushHits(context.Background())
		got := st.hits["/wp-content/uploads/2020/05/chart.png"]
		if got.N != 2 || got.Last.IsZero() {
			t.Errorf("chart.png hits %+v, want 2 (one 200, one 304)", got)
		}
		if st.hits["/wp-content/uploads/2020/05/Chart.png"].N != 0 {
			t.Error("a miss was counted")
		}
		s.FlushHits(context.Background())
		if st.hits["/wp-content/uploads/2020/05/chart.png"].N != 2 {
			t.Error("a second flush re-sent the same hits")
		}
	})
}

func TestHitsKeptWhenFlushFails(t *testing.T) {
	s, _, _ := newTestServer()
	s.cfg.Store = failingStore{}
	s.hit("/a.png")
	s.FlushHits(context.Background())
	s.hit("/a.png")
	if s.hits["/a.png"].N != 2 {
		t.Errorf("hits after a failed flush: %+v", s.hits["/a.png"])
	}
}

type failingStore struct{ *fakeStore }

func (failingStore) AddHits(context.Context, map[string]Hit) error { return io.ErrUnexpectedEOF }
