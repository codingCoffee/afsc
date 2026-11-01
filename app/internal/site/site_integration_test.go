package site

import (
  "context"
  "encoding/xml"
  "io"
  "net/http"
  "net/http/httptest"
  "os"
  "regexp"
  "strings"
  "testing"
  "time"

  "github.com/jackc/pgx/v5/pgxpool"

  "afsc/internal/importer"
  "afsc/internal/settings"
  "afsc/internal/theme"
)

type memStorage struct{}

func (memStorage) Put(_ context.Context, _ string, body io.ReadSeeker, _ int64, _ string) error {
  _, err := io.Copy(io.Discard, body)
  return err
}

// TestPublicSite imports the sample bundle and requests every route type through the real handler.
// Run DB tests with -p 1: the importer package imports the same bundle.
func TestPublicSite(t *testing.T) {
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
  tm, err := theme.LoadManifest("../../themes/default")
  if err != nil {
    t.Fatal(err)
  }
  if rep, err := importer.Run(ctx, importer.Options{BundleDir: "../../testdata/bundle-sample", Theme: tm, Pool: pool,
    Storage: memStorage{}}); err != nil {
    t.Fatalf("import: %v %+v", err, rep.Errors)
  }

  st, err := New(ctx, Config{Pool: pool, BaseURL: "https://blog.example.com", ThemeDir: "../../themes/default", Logf: t.Logf})
  if err != nil {
    t.Fatal(err)
  }
  get := func(method, target string) *httptest.ResponseRecorder {
    rec := httptest.NewRecorder()
    st.ServeHTTP(rec, httptest.NewRequest(method, target, nil))
    return rec
  }
  card := func(title string) string { return `rel="bookmark">` + title + `</a>` }
  type tc struct {
    target   string
    status   int
    contains []string
    absent   []string
  }
  check := func(t *testing.T, cases []tc) {
    t.Helper()
    for _, c := range cases {
      rec := get("GET", c.target)
      body := rec.Body.String()
      if rec.Code != c.status {
        t.Errorf("%s: status %d, want %d (Location %q)", c.target, rec.Code, c.status, rec.Header().Get("Location"))
        continue
      }
      for _, s := range c.contains {
        if !strings.Contains(body, s) && rec.Header().Get("Location") != s {
          t.Errorf("%s: missing %q", c.target, s)
        }
      }
      for _, s := range c.absent {
        if strings.Contains(body, s) {
          t.Errorf("%s: unexpected %q", c.target, s)
        }
      }
    }
  }

  t.Run("routes", func(t *testing.T) {
    check(t, []tc{
      {"/", 200, []string{`class="home blog"`, card("Example post"), card("Market notes"), card("Bond basics"), card("Yearly review"),
        "Short teaser before the fold.", "A look back at the year.", " […]", `class="menu-item menu-item-type-term`},
        []string{"A draft", "Coming soon", "Legacy markup"}},
      {"/example-post/", 200, []string{"postid-123", "wp-image-456", "youtube-nocookie.com", "Author A", "Guest Author B",
        `href="https://blog.example.com/category/markets/stocks/"`, `aria-current="page">Example post</span>`}, nil},
      {"/about/", 200, []string{"page-id-2", "About this site."}, nil},
      {"/about/team/", 200, []string{"page-template-full-width", "parent-pageid-2", "The team."}, nil},
      {"/about/intro/", 200, []string{"About intro"}, []string{"Team intro"}},
      {"/about/team/intro/", 200, []string{"Team intro"}, nil},
      {"/blog/", 200, []string{"page-id-4", "Posts page."}, nil},
      {"/category/markets/", 200, []string{"category-markets", card("Example post"), card("Market notes"), "Market news"}, []string{card("Bond basics")}},
      {"/category/markets/stocks/", 200, []string{card("Example post")}, []string{card("Market notes"), card("Bond basics")}},
      {"/category/bonds/stocks/", 200, []string{card("Bond basics")}, []string{card("Example post")}},
      {"/category/bonds/", 200, []string{card("Bond basics"), card("Yearly review")}, nil},
      {"/tag/example/", 200, []string{"tag-example", card("Example post"), card("Market notes")}, []string{card("Bond basics")}},
      {"/author/author-a/", 200, []string{"author-author-a", "Writes example posts.", card("Bond basics"), card("Yearly review")}, []string{card("Market notes")}},
      {"/author/guest-author-b/", 200, []string{card("Market notes"), card("Bond basics")}, []string{card("Yearly review")}},
      {"/2021/", 200, []string{"archive date", card("Example post"), card("Bond basics")}, []string{card("Yearly review")}},
      {"/2021/03/", 200, []string{"March 2021", card("Example post"), card("Market notes")}, []string{card("Bond basics")}},
      {"/2020/", 404, []string{"error404"}, nil},
      {"/page/2/", 404, nil, nil},
      {"/search/?q=bonds", 200, []string{"search-results", card("Bond basics"), "<b>"}, nil},
      {"/search/?q=zzzunknownzzz", 200, []string{"search-no-results"}, nil},
      {"/search/", 200, []string{"search-no-results"}, nil},
      {"/draft-post/", 404, nil, nil},
      {"/legacy-markup/", 404, nil, nil},
      {"/scheduled-post/", 404, nil, nil},
      {"/nope/", 404, []string{"Page not found"}, nil},
      {"/deleted-post/", 410, []string{"error404"}, nil},
      {"/old-path/", 301, []string{"/example-post/"}, nil},
      {"/topics/markets/", 301, []string{"/category/markets/"}, nil},
      {"/archive/2015/x/", 302, []string{"/"}, nil},
      {"/Example-Post", 301, []string{"/example-post/"}, nil},
      {"/?p=123", 301, []string{"/example-post/"}, nil},
      {"/?page_id=3", 301, []string{"/about/team/"}, nil},
      {"/?cat=5", 301, []string{"/category/markets/"}, nil},
      {"/?author=2", 301, []string{"/author/author-a/"}, nil},
      {"/?p=124", 404, nil, nil},
      {"/?s=bonds", 301, []string{"/search/?q=bonds"}, nil},
      {"/feed/rss2/", 301, []string{"/feed/"}, nil},
      {"/example-post/feed/", 301, []string{"/example-post/"}, nil},
    })
  })

  t.Run("headers", func(t *testing.T) {
    rec := get("GET", "/search/?q=bonds")
    if rec.Header().Get("X-Robots-Tag") != "noindex" || rec.Header().Get("Cache-Control") != "private, no-store" ||
      !strings.Contains(rec.Body.String(), `<meta name="robots" content="noindex">`) {
      t.Errorf("search must be noindex and uncached: %v", rec.Header())
    }
    if rec := get("HEAD", "/"); rec.Code != 200 {
      t.Errorf("HEAD /: %d", rec.Code)
    }
    if rec := get("POST", "/"); rec.Code != http.StatusMethodNotAllowed {
      t.Errorf("POST /: %d", rec.Code)
    }
    home := get("GET", "/").Body.String()
    href := regexp.MustCompile(`href="(/_theme/[0-9a-f]+/style\.css)"`).FindStringSubmatch(home)
    if href == nil {
      t.Fatalf("no stylesheet link on the home page")
    }
    css := get("GET", href[1])
    if css.Code != 200 || !strings.Contains(css.Header().Get("Cache-Control"), "immutable") || !strings.Contains(css.Header().Get("Content-Type"), "text/css") {
      t.Errorf("asset: %d %v", css.Code, css.Header())
    }
  })

  t.Run("pagination", func(t *testing.T) {
    swapSettings(t, st, func(a *settings.All) { a.Reading.PostsPerPage = 2 })
    check(t, []tc{
      {"/", 200, []string{`class="page-numbers current">1</span>`, `href="https://blog.example.com/page/2/"`, card("Example post")}, nil},
      {"/page/2/", 200, []string{"paged-2", card("Bond basics"), card("Market notes")}, []string{card("Example post")}},
      {"/page/3/", 404, nil, nil},
      {"/page/1/", 301, []string{"/"}, nil},
    })
  })

  t.Run("static front page", func(t *testing.T) {
    snap := st.snap.Load()
    swapSettings(t, st, func(a *settings.All) {
      a.Reading.Home, a.Reading.PostsPage = &settings.Ref{ID: snap.pageByPath([]string{"about"}).ID}, &settings.Ref{ID: snap.pageByPath([]string{"blog"}).ID}
    })
    check(t, []tc{
      {"/", 200, []string{"About this site.", "page-template-default page page-id-2"}, nil},
      {"/about/", 301, []string{"/"}, nil},
      {"/blog/", 200, []string{`class="blog"`, card("Example post"), card("Bond basics")}, nil},
      {"/about/team/", 200, []string{"The team."}, nil},
    })
  })

  t.Run("feeds", func(t *testing.T) {
    type item struct {
      Title    string   `xml:"title"`
      Link     string   `xml:"link"`
      PubDate  string   `xml:"pubDate"`
      GUID     string   `xml:"guid"`
      Creators []string `xml:"http://purl.org/dc/elements/1.1/ creator"`
      Content  string   `xml:"http://purl.org/rss/1.0/modules/content/ encoded"`
      Desc     string   `xml:"description"`
    }
    type rss struct {
      Version string `xml:"version,attr"`
      Channel struct {
        Title string `xml:"title"`
        Link  string `xml:"link"`
        Desc  string `xml:"description"`
        Items []item `xml:"item"`
      } `xml:"channel"`
    }
    for path, want := range map[string]int{"/feed/": 4, "/category/markets/feed/": 2, "/tag/example/feed/": 2, "/author/author-a/feed/": 3} {
      rec := get("GET", path)
      if rec.Code != 200 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/rss+xml") {
        t.Errorf("%s: %d %s", path, rec.Code, rec.Header().Get("Content-Type"))
        continue
      }
      var f rss
      if err := xml.Unmarshal(rec.Body.Bytes(), &f); err != nil {
        t.Errorf("%s: invalid xml: %v", path, err)
        continue
      }
      if f.Version != "2.0" || f.Channel.Title == "" || f.Channel.Link == "" || f.Channel.Desc == "" || len(f.Channel.Items) != want {
        t.Errorf("%s: bad channel: %+v", path, f.Channel)
      }
      for _, it := range f.Channel.Items {
        if _, err := time.Parse(time.RFC1123Z, it.PubDate); err != nil || it.Title == "" || it.GUID == "" ||
          !strings.HasPrefix(it.Link, "https://blog.example.com/") || len(it.Creators) == 0 {
          t.Errorf("%s: bad item %+v", path, it)
        }
        if strings.Contains(it.Content, `loading="lazy"`) || strings.Contains(it.Content, `href="/`) || strings.Contains(it.Content, `src="/`) {
          t.Errorf("%s: %s content is not feed-safe", path, it.Title)
        }
      }
    }
    body := get("GET", "/feed/").Body.String()
    for _, s := range []string{`<guid isPermaLink="false">https://blog.example.com/?p=123</guid>`,
      `<atom:link href="https://blog.example.com/feed/" rel="self"`, `href="https://blog.example.com/about/"`} {
      if !strings.Contains(body, s) {
        t.Errorf("/feed/: missing %s", s)
      }
    }
  })

  t.Run("not found log", func(t *testing.T) {
    get("GET", "/integration-missing/")
    get("GET", "/integration-missing/")
    st.flushNotFound(ctx)
    var hits int64
    if err := pool.QueryRow(ctx, `SELECT hits FROM not_found_log WHERE path = '/integration-missing/'`).Scan(&hits); err != nil || hits < 2 {
      t.Errorf("not_found_log: hits %d, %v", hits, err)
    }
  })
}

// swapSettings changes settings in the in-memory snapshot only, so the DB other tests share is untouched.
func swapSettings(t *testing.T, st *Site, mod func(*settings.All)) {
  t.Helper()
  old := st.snap.Load()
  next := *old
  all := old.settings
  mod(&all)
  if err := next.setSettings(all); err != nil {
    t.Fatal(err)
  }
  st.snap.Store(&next)
  t.Cleanup(func() { st.snap.Store(old) })
}
