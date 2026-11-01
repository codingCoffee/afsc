package site

import (
  "context"
  "fmt"
  "html"
  "net/http"
  "strings"
  "sync/atomic"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"

  "afsc/internal/content"
  "afsc/internal/media"
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
}

type Site struct {
  cfg   Config
  db    *pgxpool.Pool
  lk    Lookup
  theme *theme.Theme
  media *media.Server
  snap  atomic.Pointer[snapshot]
  nf    *notFoundLog
  now   func() time.Time
}

func New(ctx context.Context, cfg Config) (*Site, error) {
  if cfg.Logf == nil {
    cfg.Logf = func(string, ...any) {}
  }
  st := &Site{cfg: cfg, db: cfg.Pool, lk: &pgLookup{db: cfg.Pool}, nf: newNotFoundLog(), now: time.Now}
  snap, err := st.loadSnapshot(ctx)
  if err != nil {
    return nil, fmt.Errorf("load site data: %w", err)
  }
  st.snap.Store(snap)
  if st.theme, err = theme.Load(cfg.ThemeDir, theme.Options{Dev: cfg.Dev, Backend: st}); err != nil {
    return nil, fmt.Errorf("load theme %s: %w", cfg.ThemeDir, err)
  }
  if cfg.Storage != nil && cfg.Imgproxy != nil {
    st.media = media.New(media.Config{Store: &media.PG{DB: cfg.Pool}, Storage: cfg.Storage, Imgproxy: cfg.Imgproxy,
      Images: func() *content.ImageSizes { return st.theme.Manifest().Images() }, Logf: cfg.Logf})
  }
  return st, nil
}

// Run refreshes cached site data and flushes the 404 log until ctx is done, then flushes once more.
func (st *Site) Run(ctx context.Context) {
  every := 30 * time.Second
  if st.cfg.Dev {
    every = 2 * time.Second
  }
  refresh, flush, hits := time.NewTicker(every), time.NewTicker(10*time.Second), time.NewTicker(60*time.Second)
  defer refresh.Stop()
  defer flush.Stop()
  defer hits.Stop()
  for {
    select {
    case <-ctx.Done():
      fctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
      st.flushNotFound(fctx)
      if st.media != nil {
        st.media.FlushHits(fctx)
      }
      cancel()
      return
    case <-hits.C:
      if st.media != nil {
        st.media.FlushHits(ctx)
      }
    case <-refresh.C:
      if snap, err := st.loadSnapshot(ctx); err != nil {
        st.cfg.Logf("site: refresh failed, keeping previous data: %v", err)
      } else {
        st.snap.Store(snap)
      }
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
  var res Result
  var err error
  // §6.1 step 3: media is served before redirects and canonicalisation, because legacy paths are case-sensitive
  switch {
  case strings.HasPrefix(r.URL.Path, media.Prefix):
    if st.media != nil && st.media.ServeMedia(w, r) {
      return
    }
    res = notFound()
  case st.media != nil && st.media.ServeLegacy(w, r):
    return
  default:
    res, err = st.resolve(r.Context(), snap, r)
  }
  if err == nil {
    switch {
    case res.Location != "":
      w.Header().Set("Location", res.Location)
      w.WriteHeader(res.Status)
    case res.Kind == kindFeed:
      err = st.serveFeed(w, r, snap, res)
    default:
      err = st.servePage(w, r, snap, res)
    }
  }
  if err != nil {
    st.cfg.Logf("site: %s %s: %v", r.Method, r.URL.Path, err)
    msg := "Internal Server Error"
    if st.cfg.Dev {
      msg = err.Error()
    }
    w.Header().Set("Content-Type", "text/html; charset=utf-8")
    w.WriteHeader(http.StatusInternalServerError)
    fmt.Fprintf(w, "<!doctype html><title>Error</title><pre>%s</pre>", html.EscapeString(msg))
  }
}
