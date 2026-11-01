package site

import (
	"context"
	"fmt"
	"html"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/content"
	"afsc/internal/media"
	"afsc/internal/pagecache"
	"afsc/internal/settings"
	"afsc/internal/storage"
	"afsc/internal/theme"
)

type Config struct {
	Pool     *pgxpool.Pool
	BaseURL  string // absolute, no trailing slash; never derived from the Host header
	ThemeDir string
	Dev      bool // hot-reload the theme, refresh the snapshot often, render content per request, show errors
	Logf     func(format string, args ...any)
	// Storage and Imgproxy serve /m/ URLs and legacy upload paths; without them those requests 404.
	Storage  storage.Reader
	Imgproxy media.Imager
	// CacheBytes bounds the in-process page cache; 0 disables it.
	CacheBytes int64
}

// maxEntryBytes caps a single cached page.
const maxEntryBytes = 2 << 20

type Site struct {
	cfg       Config
	db        *pgxpool.Pool
	lk        Lookup
	theme     *theme.Theme
	media     *media.Server
	snap      atomic.Pointer[snapshot]
	refreshMu sync.Mutex // serialises snapshot loads so an older load never replaces a newer one
	cache     *pagecache.Cache
	host      string
	listening atomic.Bool
	nf        *notFoundLog
	rh        *redirectHits
	now       func() time.Time
}

func New(ctx context.Context, cfg Config) (*Site, error) {
	if cfg.Logf == nil {
		cfg.Logf = func(string, ...any) {}
	}
	st := &Site{cfg: cfg, db: cfg.Pool, lk: &pgLookup{db: cfg.Pool}, nf: newNotFoundLog(), rh: newRedirectHits(),
		now: time.Now}
	st.host = cfg.BaseURL[strings.Index(cfg.BaseURL, "://")+3:]
	if cfg.CacheBytes > 0 {
		st.cache = pagecache.New(cfg.CacheBytes, min(cfg.CacheBytes, maxEntryBytes))
	}
	snap, err := st.loadSnapshot(ctx)
	if err != nil {
		return nil, fmt.Errorf("load site data: %w", err)
	}
	st.snap.Store(snap)
	opts := theme.Options{Dev: cfg.Dev, Backend: st, OnReload: st.purgeAll}
	if st.theme, err = theme.Load(cfg.ThemeDir, opts); err != nil {
		return nil, fmt.Errorf("load theme %s: %w", cfg.ThemeDir, err)
	}
	if cfg.Storage != nil && cfg.Imgproxy != nil {
		st.media = media.New(media.Config{Store: &media.PG{DB: cfg.Pool}, Storage: cfg.Storage, Imgproxy: cfg.Imgproxy,
			Images: func() *content.ImageSizes { return st.theme.Manifest().Images() }, Logf: cfg.Logf})
	}
	return st, nil
}

// Run refreshes cached site data, listens for cache events, flushes the 404 log and hit counters and prunes the
// 404 log daily until ctx is done, then flushes once more.
func (st *Site) Run(ctx context.Context) {
	listenDone := make(chan struct{})
	go func() {
		st.listen(ctx)
		close(listenDone)
	}()
	defer func() { <-listenDone }()
	every := 30 * time.Second
	if st.cfg.Dev {
		every = 2 * time.Second
	}
	refresh, flush, hits := time.NewTicker(every), time.NewTicker(10*time.Second), time.NewTicker(60*time.Second)
	prune := time.NewTicker(24 * time.Hour)
	defer refresh.Stop()
	defer flush.Stop()
	defer hits.Stop()
	defer prune.Stop()
	flushHits := func(ctx context.Context) {
		st.flushRedirectHits(ctx)
		if st.media != nil {
			st.media.FlushHits(ctx)
		}
	}
	st.pruneNotFound(ctx)
	for {
		select {
		case <-ctx.Done():
			fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
			st.flushNotFound(fctx)
			flushHits(fctx)
			cancel()
			return
		case <-hits.C:
			flushHits(ctx)
		case <-prune.C:
			st.pruneNotFound(ctx)
		case <-refresh.C:
			_ = st.refresh(ctx)
		case <-flush.C:
			st.flushNotFound(ctx)
		case <-st.nf.full:
			st.flushNotFound(ctx)
		}
	}
}

// Settings, BaseURL and Menu implement theme.Backend.
func (st *Site) Settings() *settings.All {
	s := st.snap.Load()
	return &s.settings
}

func (st *Site) BaseURL() string { return st.cfg.BaseURL }

func (st *Site) Menu(loc string) ([]*theme.MenuItem, error) {
	return st.snap.Load().menus[loc], nil
}

func (st *Site) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if strings.HasPrefix(r.URL.Path, theme.AssetPrefix) {
		st.theme.AssetHandler().ServeHTTP(w, r)
		return
	}
	snap := st.snap.Load()
	if _, ok := snap.seoFile(r.URL.Path); ok {
		st.servePublic(w, r, snap)
		return
	}
	// §6.1 step 3: media is served before redirects and canonicalisation, because legacy paths are case-sensitive
	switch {
	case strings.HasPrefix(r.URL.Path, media.Prefix):
		if st.media != nil && st.media.ServeMedia(w, r) {
			return
		}
		resp, err := st.render(snap, r, notFound(), r.URL.RawQuery)
		st.finish(w, r, resp, err)
	case st.media != nil && st.media.ServeLegacy(w, r):
	default:
		st.servePublic(w, r, snap)
	}
}

// response is a rendered page, feed or redirect, not yet written.
type response struct {
	status int
	header http.Header
	body   []byte
}

func (st *Site) render(snap *snapshot, r *http.Request, res Result, query string) (*response, error) {
	switch {
	case res.Location != "":
		return &response{status: res.Status, header: http.Header{"Location": {res.Location}}}, nil
	case res.Kind == kindFeed:
		return st.renderFeed(r, snap, res)
	case res.Kind == kindSitemap || res.Kind == kindText:
		render := st.renderText
		if res.Kind == kindSitemap {
			render = st.renderSitemap
		}
		resp, err := render(r.Context(), snap, res)
		if resp != nil || err != nil {
			return resp, err
		}
		return st.renderPage(r, snap, notFound(), query)
	}
	return st.renderPage(r, snap, res, query)
}

// finish writes resp, or an error page when err is set.
func (st *Site) finish(w http.ResponseWriter, r *http.Request, resp *response, err error) {
	if err != nil {
		st.cfg.Logf("site: %s %s: %v", r.Method, r.URL.Path, err)
		msg := "Internal Server Error"
		if st.cfg.Dev {
			msg = err.Error()
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprintf(w, "<!doctype html><title>Error</title><pre>%s</pre>", html.EscapeString(msg))
		return
	}
	h := w.Header()
	for k, v := range resp.header {
		h[k] = v
	}
	w.WriteHeader(resp.status)
	if r.Method != http.MethodHead {
		_, _ = w.Write(resp.body)
	}
}
