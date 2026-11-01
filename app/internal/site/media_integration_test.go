package site

import (
  "bufio"
  "bytes"
  "context"
  "encoding/json"
  "fmt"
  "io"
  "net/http/httptest"
  "os"
  "path/filepath"
  "regexp"
  "sync"
  "testing"

  "github.com/jackc/pgx/v5/pgxpool"

  "afsc/internal/imgproxy"
  "afsc/internal/importer"
  "afsc/internal/storage"
  "afsc/internal/theme"
)

// blobs keeps imported files in memory so served originals can be compared byte for byte.
type blobs struct {
  mu sync.Mutex
  m  map[string][]byte
}

func (b *blobs) Put(_ context.Context, key string, body io.ReadSeeker, _ int64, _ string) error {
  data, err := io.ReadAll(body)
  b.mu.Lock()
  b.m[key] = data
  b.mu.Unlock()
  return err
}

func (b *blobs) Get(_ context.Context, key, rng string) (*storage.Object, error) {
  b.mu.Lock()
  data, ok := b.m[key]
  b.mu.Unlock()
  if !ok {
    return nil, storage.ErrNotFound
  }
  if rng != "" {
    return nil, fmt.Errorf("range not supported in this test")
  }
  n := int64(len(data))
  return &storage.Object{Body: io.NopCloser(bytes.NewReader(data)), Size: n, Length: n}, nil
}

func (b *blobs) Head(ctx context.Context, key string) (*storage.Object, error) {
  o, err := b.Get(ctx, key, "")
  if err == nil {
    o.Body = nil
  }
  return o, err
}

type echoImager struct{}

func (echoImager) Fetch(_ context.Context, key string, o imgproxy.Options) (*imgproxy.Image, error) {
  body := fmt.Sprintf("%dx%d crop=%v %s", o.Width, o.Height, o.Crop, o.Format)
  return &imgproxy.Image{Body: io.NopCloser(bytes.NewReader([]byte(body))), ContentLength: int64(len(body))}, nil
}

type bundleMedia struct {
  LegacyKey   string `json:"legacy_key"`
  File        string `json:"file"`
  Mime        string `json:"mime"`
  LegacyPaths []struct {
    Path    string `json:"path"`
    Variant string `json:"variant"`
    Width   int    `json:"width"`
    Height  int    `json:"height"`
    Crop    bool   `json:"crop"`
  } `json:"legacy_paths"`
}

// TestMediaServing imports the sample bundle and checks every legacy upload path and the /m/ rules.
// Run DB tests with -p 1: other packages import the same bundle.
func TestMediaServing(t *testing.T) {
  dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
  if dbURL == "" {
    t.Skip("AFSC_TEST_DATABASE_URL not set")
  }
  ctx := context.Background()
  pool, err := pgxpool.New(ctx, dbURL)
  if err != nil {
    t.Fatal(err)
  }
  defer pool.Close()
  const bundle = "../../testdata/bundle-sample"
  tm, err := theme.LoadManifest("../../themes/default")
  if err != nil {
    t.Fatal(err)
  }
  store := &blobs{m: map[string][]byte{}}
  if rep, err := importer.Run(ctx, importer.Options{BundleDir: bundle, Theme: tm, Pool: pool, Storage: store}); err != nil {
    t.Fatalf("import: %v %+v", err, rep.Errors)
  }
  // an earlier import may have stored the files under keys this run didn't upload again
  rows, err := pool.Query(ctx, `SELECT storage_key, encode(sha256, 'hex') FROM media`)
  if err != nil {
    t.Fatal(err)
  }
  keys := map[string]string{}
  for rows.Next() {
    var key, sum string
    if err := rows.Scan(&key, &sum); err != nil {
      t.Fatal(err)
    }
    keys[sum] = key
  }
  rows.Close()

  f, err := os.Open(filepath.Join(bundle, "media.ndjson"))
  if err != nil {
    t.Fatal(err)
  }
  defer f.Close()
  var recs []bundleMedia
  sc := bufio.NewScanner(f)
  for sc.Scan() {
    var m bundleMedia
    if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
      t.Fatal(err)
    }
    recs = append(recs, m)
    data, err := os.ReadFile(filepath.Join(bundle, m.File))
    if err != nil {
      t.Fatal(err)
    }
    sum := filepath.Base(m.File)
    sum = sum[:len(sum)-len(filepath.Ext(sum))]
    store.m[keys[sum]] = data
  }

  st, err := New(ctx, Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "../../themes/default", Logf: t.Logf,
    Storage: store, Imgproxy: echoImager{}, CacheBytes: 1 << 20})
  if err != nil {
    t.Fatal(err)
  }
  get := func(target string, hdr ...string) *httptest.ResponseRecorder {
    r := httptest.NewRequest("GET", target, nil)
    for i := 0; i+1 < len(hdr); i += 2 {
      r.Header.Set(hdr[i], hdr[i+1])
    }
    w := httptest.NewRecorder()
    st.ServeHTTP(w, r)
    return w
  }

  hits := map[string]int64{}
  t.Run("legacy paths", func(t *testing.T) {
    exts := map[string]string{".png": "image/png", ".gif": "image/gif", ".mp4": "video/mp4", ".pdf": "application/pdf",
      ".jpg": "image/jpeg"}
    n := 0
    for _, m := range recs {
      orig, err := os.ReadFile(filepath.Join(bundle, m.File))
      if err != nil {
        t.Fatal(err)
      }
      for _, lp := range m.LegacyPaths {
        n++
        w := get(lp.Path, "Accept", "image/avif,image/webp,*/*")
        hits[lp.Path]++
        h := w.Header()
        if w.Code != 200 || h.Get("Location") != "" {
          t.Errorf("%s: status %d, location %q", lp.Path, w.Code, h.Get("Location"))
          continue
        }
        if want := exts[filepath.Ext(lp.Path)]; h.Get("Content-Type") != want {
          t.Errorf("%s: Content-Type %q, want %q", lp.Path, h.Get("Content-Type"), want)
        }
        if h.Get("Cache-Control") != "public, max-age=86400" || h.Get("ETag") == "" || h.Get("Last-Modified") == "" ||
          h.Get("Vary") != "" || h.Get("X-Cache") != "" {
          t.Errorf("%s: headers %v", lp.Path, h)
        }
        if lp.Variant == "orig" && !bytes.Equal(w.Body.Bytes(), orig) {
          t.Errorf("%s: body is not byte-identical to %s", lp.Path, m.File)
        }
        if want := fmt.Sprintf("%dx%d crop=%v png", lp.Width, lp.Height, lp.Crop); lp.Variant == "resize" && w.Body.String() != want {
          t.Errorf("%s: imgproxy got %q, want %q", lp.Path, w.Body.String(), want)
        }
      }
    }
    if n == 0 {
      t.Fatal("the sample bundle has no legacy paths")
    }
  })

  t.Run("legacy misses fall through", func(t *testing.T) {
    if w := get("/wp-content/uploads/2020/05/Chart.png"); w.Code != 404 {
      t.Errorf("case-changed legacy path: %d", w.Code)
    }
    if w := get("/wp-content/uploads/2020/05/nope.png"); w.Code != 404 {
      t.Errorf("unknown legacy path: %d", w.Code)
    }
  })

  t.Run("hits flushed", func(t *testing.T) {
    before := map[string]int64{}
    for p := range hits {
      var n int64
      if err := pool.QueryRow(ctx, `SELECT hits FROM media_legacy_paths WHERE path = $1`, p).Scan(&n); err != nil {
        t.Fatal(err)
      }
      before[p] = n
    }
    st.media.FlushHits(ctx)
    for p, want := range hits {
      var n int64
      var seen bool
      if err := pool.QueryRow(ctx, `SELECT hits, last_hit_at IS NOT NULL FROM media_legacy_paths WHERE path = $1`, p).Scan(&n, &seen); err != nil {
        t.Fatal(err)
      }
      if n-before[p] != want || !seen {
        t.Errorf("%s: %d new hits (last_hit_at set: %v), want %d", p, n-before[p], seen, want)
      }
    }
  })

  t.Run("versioned media", func(t *testing.T) {
    var id int64
    var v string
    if err := pool.QueryRow(ctx, `SELECT id, substr(encode(sha256, 'hex'), 1, 8) FROM media WHERE legacy_key = 'wp:post:456'`).Scan(&id, &v); err != nil {
      t.Fatal(err)
    }
    base := fmt.Sprintf("/m/%d/%s/", id, v)
    w := get(base+"w320/chart.png", "Accept", "image/webp,*/*")
    if w.Code != 200 || w.Header().Get("Content-Type") != "image/webp" || w.Header().Get("Vary") != "Accept" ||
      w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || w.Header().Get("X-Cache") != "" {
      t.Errorf("w320: %d %v", w.Code, w.Header())
    }
    if w := get(base + "c150x150/chart.png"); w.Code != 200 || w.Body.String() != "150x150 crop=true png" {
      t.Errorf("thumbnail crop: %d %q", w.Code, w.Body.String())
    }
    if w := get(base + "w500/chart.png"); w.Code != 404 {
      t.Errorf("w500 is not in the theme's widths: %d", w.Code)
    }
    if w := get(fmt.Sprintf("/m/%d/00000000/w320/chart.png", id)); w.Code != 301 || w.Header().Get("Location") != base+"w320/chart.png" {
      t.Errorf("stale version: %d %q", w.Code, w.Header().Get("Location"))
    }
    if w := get("/?attachment_id=456"); w.Code != 301 {
      t.Errorf("attachment_id: %d", w.Code)
    } else if w2 := get(w.Header().Get("Location")); w2.Code != 200 {
      t.Errorf("attachment_id target %s: %d", w.Header().Get("Location"), w2.Code)
    }
  })

  t.Run("every media URL on a page resolves", func(t *testing.T) {
    body := get("/example-post/").Body.String()
    urls := regexp.MustCompile(`(/m/[0-9]+/[0-9a-f]{8}/[a-z0-9]+/[^"\s,]+)`).FindAllString(body, -1)
    if len(urls) == 0 {
      t.Fatal("no /m/ URLs on /example-post/")
    }
    for _, u := range urls {
      if w := get(u); w.Code != 200 {
        t.Errorf("%s: %d", u, w.Code)
      }
    }
  })

  t.Run("media is never page-cached", func(t *testing.T) {
    st.purgeAll()
    for _, m := range recs {
      for _, lp := range m.LegacyPaths {
        get(lp.Path)
      }
    }
    get("/wp-content/uploads/2020/05/nope.png")
    get("/m/1/00000000/orig/missing.png")
    if n, _ := st.cache.Stats(); n != 0 {
      t.Errorf("media requests left %d page cache entries", n)
    }
  })
}
