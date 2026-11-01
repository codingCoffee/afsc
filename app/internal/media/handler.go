package media

import (
	"context"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"afsc/internal/content"
	"afsc/internal/imgproxy"
	"afsc/internal/storage"
)

const (
	cacheImmutable = "public, max-age=31536000, immutable"
	cacheLegacy    = "public, max-age=86400"
	cacheMoved     = "public, max-age=3600"
)

// Imager fetches processed images; *imgproxy.Client implements it.
type Imager interface {
	Fetch(ctx context.Context, key string, o imgproxy.Options) (*imgproxy.Image, error)
}

type Config struct {
	Store    Store
	Storage  storage.Reader
	Imgproxy Imager
	Images   func() *content.ImageSizes // the current theme's sizes; the theme can hot-reload
	Logf     func(format string, args ...any)
}

type Server struct {
	cfg  Config
	now  func() time.Time
	mu   sync.Mutex
	hits map[string]Hit
}

func New(cfg Config) *Server {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	return &Server{cfg: cfg, now: time.Now, hits: map[string]Hit{}}
}

// ServeMedia answers a /m/ request. It returns false, having written nothing, when the URL names no media
// or a variant the theme doesn't allow; the caller renders its 404.
func (s *Server) ServeMedia(w http.ResponseWriter, r *http.Request) bool {
	p, ok := ParsePath(r.URL.EscapedPath())
	if !ok {
		return false
	}
	rec, err := s.cfg.Store.Media(r.Context(), p.ID)
	if err != nil {
		s.fail(w, r, err, false)
		return true
	}
	if rec == nil {
		return false
	}
	info := rec.Info()
	if !s.cfg.Images().Allowed(info, p.Variant) {
		return false
	}
	if p.V != rec.Version() || p.Filename != rec.Filename {
		w.Header().Set("Location", content.MediaURL(info, p.Variant))
		w.Header().Set("Cache-Control", cacheMoved)
		w.WriteHeader(http.StatusMovedPermanently)
		return true
	}

	if !content.Resizable(rec.Mime) {
		s.serveRaw(w, r, rec, rec.Mime, cacheImmutable)
		return true
	}
	o := imgproxy.Options{Format: Negotiate(r.Header.Get("Accept"), rec.Mime)}
	if vw, vh, crop, ok := content.ParseVariant(p.Variant); ok {
		o.Width, o.Height, o.Crop = vw, vh, crop
		if crop {
			o.Focal = rec.focal()
		}
	}
	w.Header().Set("Vary", "Accept")
	s.serveProcessed(w, r, rec, o, `"`+rec.SHA256[:16]+"-"+p.Variant+"-"+o.Format+`"`, cacheImmutable)
	return true
}

// ServeLegacy answers an exact media_legacy_paths match with a 200 and the file, never a redirect.
// It returns false, having written nothing, on a miss.
func (s *Server) ServeLegacy(w http.ResponseWriter, r *http.Request) bool {
	if !fileLike(r.URL.Path) {
		return false
	}
	paths := []string{r.URL.Path}
	if esc := r.URL.EscapedPath(); esc != r.URL.Path {
		paths = append(paths, esc)
	}
	l, err := s.cfg.Store.LegacyPath(r.Context(), paths)
	if err != nil {
		s.fail(w, r, err, false)
		return true
	}
	if l == nil {
		return false
	}
	rec := &l.Media
	var status int
	if l.Variant == "orig" {
		status = s.serveRaw(w, r, rec, typeForPath(l.Path, rec.Mime), cacheLegacy)
	} else {
		format := extFormat(l.Path)
		if format == "" || !content.Resizable(rec.Mime) {
			s.cfg.Logf("media: legacy path %s: can't resize %s to %q", l.Path, rec.Mime, format)
			return false
		}
		o := imgproxy.Options{Width: l.Width, Height: l.Height, Crop: l.Crop, Format: format}
		if l.Crop {
			o.Focal = rec.focal()
		}
		tag := `"` + rec.SHA256[:16] + "-" + strconv.Itoa(l.Width) + "x" + strconv.Itoa(l.Height)
		if l.Crop {
			tag += "c"
		}
		status = s.serveProcessed(w, r, rec, o, tag+`"`, cacheLegacy)
	}
	if status == http.StatusOK || status == http.StatusPartialContent || status == http.StatusNotModified {
		s.hit(l.Path)
	}
	return true
}

// fail writes a plain error; a missing stored file or unprocessable image is a 404, other upstream errors a 502.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error, upstream bool) int {
	s.cfg.Logf("media: %s %s: %v", r.Method, r.URL.Path, err)
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, imgproxy.ErrNotFound), errors.Is(err, storage.ErrNotFound):
		status = http.StatusNotFound
	case upstream:
		status = http.StatusBadGateway
	}
	for _, k := range []string{"ETag", "Last-Modified", "Content-Security-Policy", "Content-Disposition", "Vary", "Accept-Ranges"} {
		w.Header().Del(k)
	}
	w.Header().Set("Cache-Control", "no-store")
	http.Error(w, http.StatusText(status), status)
	return status
}

func setCommon(w http.ResponseWriter, ctype, etag, cache string, mod time.Time) {
	h := w.Header()
	h.Set("Content-Type", ctype)
	h.Set("ETag", etag)
	h.Set("Last-Modified", mod.UTC().Format(http.TimeFormat))
	h.Set("Cache-Control", cache)
	h.Set("X-Content-Type-Options", "nosniff")
	if ctype == "image/svg+xml" {
		// media shares the site's origin, so an SVG must never run as a document there (§7.4)
		h.Set("Content-Security-Policy", "sandbox; default-src 'none'")
		h.Set("Content-Disposition", "attachment")
	}
}

// notModified applies If-None-Match, then If-Modified-Since, and writes a 304 when the client is current.
func notModified(w http.ResponseWriter, r *http.Request, etag string, mod time.Time) bool {
	if inm := r.Header.Get("If-None-Match"); inm != "" {
		if !etagMatch(inm, etag) {
			return false
		}
	} else if ims, err := http.ParseTime(r.Header.Get("If-Modified-Since")); err != nil || mod.Truncate(time.Second).After(ims) {
		return false
	}
	w.Header().Del("Content-Type")
	w.WriteHeader(http.StatusNotModified)
	return true
}

func etagMatch(header, etag string) bool {
	for _, t := range strings.Split(header, ",") {
		t = strings.TrimPrefix(strings.TrimSpace(t), "W/")
		if t == "*" || t == etag {
			return true
		}
	}
	return false
}

var rangeRe = regexp.MustCompile(`^bytes=([0-9]{0,18})-([0-9]{0,18})$`)

// singleRange returns the Range header when it is one satisfiable-looking range that applies to etag.
// Multi-range and malformed headers are ignored, so the client gets the whole file.
func singleRange(r *http.Request, etag string) string {
	rng := strings.ReplaceAll(r.Header.Get("Range"), " ", "")
	m := rangeRe.FindStringSubmatch(rng)
	if m == nil || (m[1] == "" && m[2] == "") {
		return ""
	}
	if m[1] != "" && m[2] != "" {
		a, _ := strconv.ParseInt(m[1], 10, 64)
		b, _ := strconv.ParseInt(m[2], 10, 64)
		if a > b {
			return ""
		}
	}
	if ir := r.Header.Get("If-Range"); ir != "" && ir != etag {
		return ""
	}
	return rng
}

// serveRaw streams the stored original, byte-identical, with Range support. It returns the status written.
func (s *Server) serveRaw(w http.ResponseWriter, r *http.Request, rec *Record, ctype, cache string) int {
	etag := `"` + rec.SHA256[:16] + `"`
	setCommon(w, ctype, etag, cache, rec.UpdatedAt)
	w.Header().Set("Accept-Ranges", "bytes")
	if notModified(w, r, etag, rec.UpdatedAt) {
		return http.StatusNotModified
	}
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(rec.Bytes, 10))
		w.WriteHeader(http.StatusOK)
		return http.StatusOK
	}
	obj, err := s.cfg.Storage.Get(r.Context(), rec.Key, singleRange(r, etag))
	if errors.Is(err, storage.ErrRange) {
		w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(rec.Bytes, 10))
		w.Header().Del("Content-Type")
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
		return http.StatusRequestedRangeNotSatisfiable
	}
	if err != nil {
		return s.fail(w, r, err, true)
	}
	defer obj.Body.Close()
	status := http.StatusOK
	if obj.ContentRange != "" {
		status = http.StatusPartialContent
		w.Header().Set("Content-Range", obj.ContentRange)
	}
	w.Header().Set("Content-Length", strconv.FormatInt(obj.Length, 10))
	w.WriteHeader(status)
	if _, err := io.Copy(w, obj.Body); err != nil && !errors.Is(err, context.Canceled) {
		s.cfg.Logf("media: %s: stream: %v", r.URL.Path, err)
	}
	return status
}

// serveProcessed streams an imgproxy result. The output format is decided before the call, so ETag and
// Content-Type are known without asking imgproxy.
func (s *Server) serveProcessed(w http.ResponseWriter, r *http.Request, rec *Record, o imgproxy.Options, etag, cache string) int {
	setCommon(w, formats[o.Format], etag, cache, rec.UpdatedAt)
	if notModified(w, r, etag, rec.UpdatedAt) {
		return http.StatusNotModified
	}
	img, err := s.cfg.Imgproxy.Fetch(r.Context(), rec.Key, o)
	if err != nil {
		return s.fail(w, r, err, true)
	}
	defer img.Body.Close()
	if img.ContentLength >= 0 {
		w.Header().Set("Content-Length", strconv.FormatInt(img.ContentLength, 10))
	}
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		if _, err := io.Copy(w, img.Body); err != nil && !errors.Is(err, context.Canceled) {
			s.cfg.Logf("media: %s: stream: %v", r.URL.Path, err)
		}
	}
	return http.StatusOK
}

func (s *Server) hit(path string) {
	now := s.now()
	s.mu.Lock()
	h := s.hits[path]
	h.N++
	h.Last = now
	s.hits[path] = h
	s.mu.Unlock()
}

// FlushHits writes the counted legacy-path hits in one statement. On failure they are kept for the next flush;
// the map stays bounded because only existing legacy paths are ever counted.
func (s *Server) FlushHits(ctx context.Context) {
	s.mu.Lock()
	batch := s.hits
	s.hits = map[string]Hit{}
	s.mu.Unlock()
	if len(batch) == 0 {
		return
	}
	if err := s.cfg.Store.AddHits(ctx, batch); err != nil {
		s.cfg.Logf("media: flushing %d legacy hit counters failed: %v", len(batch), err)
		s.mu.Lock()
		for p, h := range batch {
			cur := s.hits[p]
			cur.N += h.N
			if h.Last.After(cur.Last) {
				cur.Last = h.Last
			}
			s.hits[p] = cur
		}
		s.mu.Unlock()
	}
}
