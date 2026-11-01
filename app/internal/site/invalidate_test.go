package site

import (
  "context"
  "encoding/json"
  "fmt"
  "slices"
  "strings"
  "testing"

  "github.com/jackc/pgx/v5/pgconn"

  "afsc/internal/theme"
)

func TestGroup(t *testing.T) {
  s := testSite(t, nil).snap.Load()
  cases := []struct {
    res  Result
    path string
    want string
  }{
    {Result{Kind: theme.KindHome, Paged: 3}, "/page/3/", "/"},
    {Result{Kind: theme.KindFrontPage, ID: 2}, "/", "/"},
    {Result{Kind: theme.KindPostsPage, ID: 4, Paged: 2}, "/blog/page/2/", "/blog/"},
    {Result{Kind: theme.KindPage, ID: 6}, "/about/team/intro/", "/about/team/intro/"},
    {Result{Kind: theme.KindSingle, ID: 123}, "/example-post/", "/example-post/"},
    {Result{Kind: theme.KindCategory, ID: 6, Paged: 2}, "/category/markets/stocks/page/2/", "/category/markets/stocks/"},
    {Result{Kind: theme.KindTag, ID: 8}, "/tag/example/", "/tag/example/"},
    {Result{Kind: theme.KindAuthor, ID: 2}, "/author/author-a/", "/author/author-a/"},
    {Result{Kind: theme.KindDate, Year: 2021}, "/2021/", "/2021/"},
    {Result{Kind: theme.KindDate, Year: 2021, Month: 3, Paged: 2}, "/2021/03/page/2/", "/2021/03/"},
    {Result{Kind: kindFeed, FeedOf: "home"}, "/feed/", "/"},
    {Result{Kind: kindFeed, FeedOf: "category", ID: 5}, "/category/markets/feed/", "/category/markets/"},
    {Result{Kind: kindFeed, FeedOf: "author", ID: 2}, "/author/author-a/feed/", "/author/author-a/"},
  }
  for _, c := range cases {
    if got := s.group(c.res, c.path); got != c.want {
      t.Errorf("group(%s %s) = %q, want %q", c.res.Kind, c.path, got, c.want)
    }
  }
}

func TestPostChange(t *testing.T) {
  post := func(slug, status string, terms ...int64) *PostState {
    return &PostState{Ref: PostRef{ID: 200, Type: "post", Slug: slug, Status: status, PublishedAt: &testPub, CategoryID: 6},
      TermIDs: terms, AuthorIDs: []int64{2}}
  }
  page := func(id, parent int64, slug string) *PostState {
    return &PostState{Ref: PostRef{ID: id, Type: "page", Slug: slug, Status: "published", PublishedAt: &testPub, ParentID: parent}}
  }
  postArchives := []string{"/", "/2021/", "/2021/03/", "/author/author-a/", "/category/markets/", "/category/markets/stocks/", "/tag/example/"}
  with := func(extra ...string) []string {
    out := slices.Concat(postArchives, extra)
    slices.Sort(out)
    return out
  }
  all := "ALL"
  cases := []struct {
    name          string
    before, after *PostState
    mod           func(s *snapshot)
    want          []string // or {"ALL"}
    refresh       bool
  }{
    {"new post", nil, post("new-post", "published", 6, 8), nil, with("/new-post/"), false},
    {"slug change", post("old-post", "published", 6, 8), post("new-post", "published", 6, 8), nil, with("/new-post/", "/old-post/"), false},
    {"term removed", post("p", "published", 6, 8), post("p", "published", 6), nil, with("/p/"), false},
    {"unpublished", post("p", "published", 6, 8), post("p", "draft", 6, 8), nil, with("/p/"), false},
    {"deleted", post("p", "published", 6, 8), nil, nil, with("/p/"), false},
    {"draft edit", post("p", "draft"), post("p", "draft"), nil, []string{}, false},
    {"posts page", nil, post("p", "published", 6, 8), func(s *snapshot) { s.postsPageID, s.homeID = 4, 2 },
      with("/blog/", "/p/"), false},
    {"leaf page", page(5, 2, "intro"), page(5, 2, "intro"), nil, []string{"/about/intro/"}, true},
    {"page with children", page(3, 2, "team"), page(3, 2, "team"), nil, []string{"/about/team/", "/about/team/intro/"}, true},
    {"page with children moved", page(3, 2, "team"), page(3, 2, "people"), nil, []string{all}, true},
    {"leaf page moved", page(5, 2, "intro"), page(5, 2, "welcome"), nil, []string{"/about/intro/", "/about/welcome/"}, true},
    {"front page", page(2, 0, "about"), page(2, 0, "about"), func(s *snapshot) { s.homeID = 2 },
      []string{"/", "/about/intro/", "/about/team/", "/about/team/intro/"}, true},
    {"menu-linked post moved", post("old-post", "published", 6), post("new-post", "published", 6), func(s *snapshot) {
      s.menus["primary"] = []*theme.MenuItem{{URL: "https://blog.example.com/x/", Children: []*theme.MenuItem{
        {URL: "https://blog.example.com/old-post/"}}}}
    }, []string{all}, false},
    {"menu-linked post edited in place", post("p", "published", 6), post("p", "published", 6), func(s *snapshot) {
      s.menus["primary"] = []*theme.MenuItem{{URL: "https://blog.example.com/p/"}}
    }, []string{"/", "/2021/", "/2021/03/", "/author/author-a/", "/category/markets/", "/category/markets/stocks/", "/p/"}, false},
  }
  for _, c := range cases {
    s := testSite(t, nil).snap.Load()
    if c.mod != nil {
      c.mod(s)
    }
    ev := s.postChange(c.before, c.after)
    got := ev.Paths
    if ev.All {
      got = []string{all}
    }
    if got == nil {
      got = []string{}
    }
    if !slices.Equal(got, c.want) || ev.Refresh != c.refresh {
      t.Errorf("%s:\n got %v refresh=%v\nwant %v refresh=%v", c.name, got, ev.Refresh, c.want, c.refresh)
    }
  }
}

func TestEventNormalize(t *testing.T) {
  m := Event{Paths: []string{"/b/", "/a/"}}.Merge(Event{Refresh: true, Paths: []string{"/a/", "/c/"}})
  if !m.Refresh || m.All || !slices.Equal(m.Paths, []string{"/a/", "/b/", "/c/"}) {
    t.Errorf("merge = %+v", m)
  }
  if m := (Event{Paths: []string{"/a/"}}).Merge(Event{All: true}); !m.All || m.Paths != nil {
    t.Errorf("merge with all = %+v", m)
  }
  many := pathSet{}
  for i := range maxPaths + 1 {
    many.add(fmt.Sprintf("/p%d/", i))
  }
  if ev := many.event(true); !ev.All || !ev.Refresh || ev.Paths != nil {
    t.Errorf("over %d paths must purge all: %+v", maxPaths, ev)
  }
}

type fakeExec struct{ payloads []string }

func (f *fakeExec) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
  if args[0] != Channel {
    return pgconn.CommandTag{}, fmt.Errorf("channel %v", args[0])
  }
  f.payloads = append(f.payloads, args[1].(string))
  return pgconn.CommandTag{}, nil
}

func TestNotifyPayload(t *testing.T) {
  f := &fakeExec{}
  ctx := context.Background()
  long := make([]string, 150)
  for i := range long {
    long[i] = "/" + strings.Repeat("x", 60) + fmt.Sprint(i) + "/"
  }
  for _, e := range []Event{{}, {Paths: []string{"/b/", "/a/", "/a/"}}, {Refresh: true, All: true, Paths: []string{"/x/"}}, {Paths: long}} {
    if err := Notify(ctx, f, e); err != nil {
      t.Fatal(err)
    }
  }
  want := []Event{{Paths: []string{"/a/", "/b/"}}, {Refresh: true, All: true}, {All: true}}
  if len(f.payloads) != len(want) {
    t.Fatalf("payloads = %v; an empty event must not notify", f.payloads)
  }
  for i, p := range f.payloads {
    var got Event
    if err := json.Unmarshal([]byte(p), &got); err != nil || len(p) > maxPayload {
      t.Fatalf("payload %q: %v", p, err)
    }
    if got.Refresh != want[i].Refresh || got.All != want[i].All || !slices.Equal(got.Paths, want[i].Paths) {
      t.Errorf("payload %d = %+v, want %+v", i, got, want[i])
    }
  }
}
