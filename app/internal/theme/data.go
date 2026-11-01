package theme

import (
  "html/template"
  "time"
)

// Page kinds. Each maps to a template hierarchy in hierarchy.go.
const (
  KindHome      = "home"       // latest posts on /
  KindFrontPage = "front_page" // static page on /
  KindPostsPage = "posts_page"
  KindSingle    = "single"
  KindPage      = "page"
  KindCategory  = "category"
  KindTag       = "tag"
  KindAuthor    = "author"
  KindDate      = "date"
  KindSearch    = "search"
  Kind404       = "404"
  Kind410       = "410"
)

// PageData is the root value every page template receives.
type PageData struct {
  Kind       string
  Site       Site
  Request    Request
  Post       *Post   // single, page, front_page, posts_page
  Posts      []*Post // home, posts_page, archives, search
  Term       *Term
  Author     *Author
  Date       *DateArchive
  Search     *Search
  Pagination *Pagination
  FeedURL    string // feed of this archive, empty when it has none
}

type Site struct {
  Title    string
  Tagline  string
  URL      string // absolute base URL, no trailing slash
  Language string
}

type Request struct {
  Path    string // canonical path
  URL     string // absolute canonical URL, without query
  Query   string // raw query string
  Paged   int    // current page number, 1 when not paginated
  NoIndex bool
}

type Post struct {
  ID            int64
  WPID          int64 // WordPress id for WordPress-compatible classes; AFSC id when there is none
  Type          string
  Slug          string
  Title         string
  Excerpt       string // explicit excerpt only; use the excerpt func for the fallback chain
  ContentHTML   template.HTML
  ContentText   string
  Teaser        template.HTML // content before readMore; set on listings
  TeaserText    string
  HasMore       bool
  Template      string
  Sticky        bool
  CommentStatus string
  PublishedAt   time.Time
  UpdatedAt     time.Time
  URL           string
  FeedGUID      string
  Authors       []*Author
  Categories    []*Term
  Tags          []*Term
  Primary       *Term   // primary category
  Ancestors     []*Post // pages: root first
  FeaturedMedia *Media
  Snippet       template.HTML // search headline with <b> highlights
}

type Term struct {
  ID          int64
  WPID        int64
  Taxonomy    string
  Slug        string
  Name        string
  Description string
  Path        string  // slash-joined slugs from the root category
  Ancestors   []*Term // root first
  URL         string
}

type Author struct {
  ID      int64
  WPID    int64
  Slug    string
  Name    string
  Bio     string
  Website string
  Social  map[string]string
  Avatar  *Media
  URL     string
}

type Media struct {
  ID       int64
  WPID     int64
  Filename string
  Version  string // first 8 hex chars of the sha256, part of every /m/ URL
  Mime     string
  Width    int
  Height   int
  Alt      string
  Caption  string
  URL      string
}

type DateArchive struct {
  Year    int
  Month   int // 0 for a year archive
  Start   time.Time
  YearURL string
}

type Search struct {
  Query string
  Total int
}

type Pagination struct {
  Current int
  Total   int
  Prev    string
  Next    string
  Links   []PageLink
}

type PageLink struct {
  N       int
  URL     string
  Current bool
  Dots    bool
}

// MenuItem is one entry of a menu location; Current flags are set per request by the menu func.
type MenuItem struct {
  ID              int64
  Label           string
  URL             string
  NewTab          bool
  Classes         []string
  Kind            string
  Children        []*MenuItem
  Current         bool
  CurrentAncestor bool
}

type Crumb struct {
  Name string
  URL  string // empty for the current page
}

// NewPagination builds numbered links like WordPress paginate_links (end_size 1, mid_size 2).
func NewPagination(current, total int, urlFor func(n int) string) *Pagination {
  if total < 1 {
    total = 1
  }
  p := &Pagination{Current: current, Total: total}
  if current > 1 {
    p.Prev = urlFor(current - 1)
  }
  if current < total {
    p.Next = urlFor(current + 1)
  }
  dots := false
  for n := 1; n <= total; n++ {
    if n == 1 || n == total || (n >= current-2 && n <= current+2) {
      p.Links = append(p.Links, PageLink{N: n, URL: urlFor(n), Current: n == current})
      dots = false
    } else if !dots {
      p.Links = append(p.Links, PageLink{Dots: true})
      dots = true
    }
  }
  return p
}
