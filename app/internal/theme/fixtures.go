package theme

import (
	"html/template"
	"strconv"
	"time"

	"afsc/internal/settings"
)

const fixtureBase = "https://blog.example.com"

// FixtureBackend serves default settings and a nested menu for every location.
func FixtureBackend(m *Manifest) *StaticBackend {
	s := settings.Defaults()
	s.Site.Title, s.Site.Tagline = "Example Blog", "Just another example site"
	menus := map[string][]*MenuItem{}
	for _, loc := range m.MenuLocations {
		menus[loc] = []*MenuItem{
			{ID: 1, Label: "Markets", URL: fixtureBase + "/category/markets/", Kind: "term", Children: []*MenuItem{
				{ID: 2, Label: "Example post", URL: fixtureBase + "/example-post/", Kind: "post", Classes: []string{"highlight"}},
			}},
			{ID: 3, Label: "Example.com", URL: "https://example.com/", Kind: "url", NewTab: true},
		}
	}
	return &StaticBackend{S: &s, Base: fixtureBase, Menus: menus}
}

func fixtureMedia() *Media {
	return &Media{ID: 7, WPID: 456, Filename: "chart.png", Version: "ab12cd34", Mime: "image/png", Width: 1600, Height: 900,
		Alt: "Bar chart", Caption: "Example chart", URL: fixtureBase + "/m/7/ab12cd34/orig/chart.png"}
}

func fixtureTerms(full bool) (*Term, *Term, *Term) {
	parent := &Term{ID: 1, WPID: 5, Taxonomy: "category", Slug: "markets", Name: "Markets", Description: "Market news",
		Path: "markets", URL: fixtureBase + "/category/markets/"}
	cat := &Term{ID: 2, WPID: 6, Taxonomy: "category", Slug: "stocks", Name: "Stocks", Path: "markets/stocks",
		Ancestors: []*Term{parent}, URL: fixtureBase + "/category/markets/stocks/"}
	tag := &Term{ID: 3, WPID: 8, Taxonomy: "tag", Slug: "example", Name: "Example", Path: "example", URL: fixtureBase + "/tag/example/"}
	if !full {
		cat.Ancestors, cat.Description = nil, ""
	}
	return parent, cat, tag
}

func fixtureAuthor(full bool) *Author {
	a := &Author{ID: 1, WPID: 2, Slug: "author-a", Name: "Author A", URL: fixtureBase + "/author/author-a/", Social: map[string]string{},
		Key: "wp:user:2"}
	if full {
		a.Bio, a.Website, a.Avatar = "Writes example posts.", "https://example.com", fixtureMedia()
		a.Social["x"] = "https://x.com/example"
	}
	return a
}

func fixturePost(n int, typ string, full bool) *Post {
	slug := "example-post-" + strconv.Itoa(n)
	at := time.Date(2021, 3, n, 5, 6, 7, 0, time.UTC)
	p := &Post{ID: int64(n), WPID: int64(100 + n), Type: typ, Slug: slug, Title: "Example post " + strconv.Itoa(n),
		ContentHTML: "<p>Body <strong>text</strong>.</p>", ContentText: "Body text.", CommentStatus: "open",
		PublishedAt: at, UpdatedAt: at, URL: fixtureBase + "/" + slug + "/"}
	if !full {
		return p
	}
	parent, cat, tag := fixtureTerms(true)
	p.Excerpt, p.Teaser, p.TeaserText, p.HasMore = "An explicit excerpt.", "<p>Body</p>", "Body", true
	p.Template, p.Sticky, p.FeedGUID, p.Breadcrumb = "full-width", true, "https://blog.example.com/?p=123", "Example"
	p.Authors = []*Author{fixtureAuthor(true), {ID: 2, WPID: 0, Slug: "guest-author-b", Name: "Guest Author B", URL: fixtureBase + "/author/guest-author-b/", Key: "wp:guest:9f3c"}}
	p.FeaturedMedia, p.Snippet = fixtureMedia(), template.HTML("Body <b>text</b>")
	if typ == "post" {
		p.Categories, p.Tags, p.Primary = []*Term{parent, cat}, []*Term{tag}, cat
	} else {
		p.Ancestors = []*Post{{ID: 50, WPID: 2, Type: "page", Slug: "about", Title: "About", URL: fixtureBase + "/about/"}}
	}
	return p
}

// fixtureComments is a closed, empty section when sparse; full has a three-level thread, a second page, a reply
// form and a pending notice.
func fixtureComments(p *Post, full bool) *Comments {
	if !full {
		return &Comments{}
	}
	at := time.Date(2021, 3, 6, 10, 0, 0, 0, time.UTC)
	c := func(id int64, depth int, name, url, body string) *Comment {
		return &Comment{ID: id, Anchor: "comment-" + strconv.FormatInt(id, 10), AuthorName: name, AuthorURL: url,
			BodyHTML: template.HTML(body), Date: at, Depth: depth,
			ReplyURL: p.URL + "?replytocom=" + strconv.FormatInt(id, 10) + "#respond"}
	}
	leaf := c(3, 3, "Commenter A", "", "Third level.")
	leaf.ReplyURL = ""
	mid := c(2, 2, "Author A", "", "Thanks!")
	mid.Children = []*Comment{leaf}
	root := c(1, 1, "Commenter A", "https://example.org", `Nice post, see <a href="https://example.org/x" rel="nofollow ugc noopener">https://example.org/x</a>`)
	root.Children = []*Comment{mid}
	notice := c(9, 1, "New Visitor", "", "Awaiting moderation.")
	notice.Pending, notice.ReplyURL = true, ""
	return &Comments{
		Open: true, Total: 4, Threads: []*Comment{root, c(4, 1, "Visitor C", "", "Second thread.")},
		Pagination: NewPagination(1, 2, func(n int) string {
			if n == 1 {
				return p.URL + "#comments"
			}
			return p.URL + "?cpage=" + strconv.Itoa(n) + "#comments"
		}),
		Form:   &CommentForm{Action: fixtureBase + "/comments", PostID: p.ID, Token: "fixture-token", Honeypot: "website_url", ReplyTo: root, CancelURL: p.URL + "#respond"},
		Notice: notice,
	}
}

func fixturePosts(full bool) []*Post {
	if !full {
		return nil
	}
	return []*Post{fixturePost(1, "post", true), fixturePost(2, "post", false), fixturePost(3, "post", true)}
}

// Fixture returns sample page data of a kind. full sets every optional field; sparse leaves them empty.
func Fixture(kind string, full bool) *PageData {
	d := &PageData{Kind: kind, Site: Site{Title: "Example Blog", Tagline: "Just another example site", URL: fixtureBase, Language: "en"}}
	d.Request = Request{Path: "/", URL: fixtureBase + "/", Paged: 1}
	listing := func(path string, feed bool) {
		d.Request.Path, d.Request.URL = path, fixtureBase+path
		d.Posts = fixturePosts(full)
		cur, total := 1, 1
		if full {
			cur, total = 2, 7
			d.Request.Paged = 2
		}
		d.Pagination = NewPagination(cur, total, func(n int) string {
			if n == 1 {
				return fixtureBase + path
			}
			return fixtureBase + path + "page/" + strconv.Itoa(n) + "/"
		})
		if feed {
			d.FeedURL = fixtureBase + path + "feed/"
		}
	}
	switch kind {
	case KindHome:
		listing("/", true)
	case KindPostsPage:
		listing("/blog/", true)
		d.Post = fixturePost(4, "page", full)
	case KindFrontPage, KindPage:
		d.Post = fixturePost(5, "page", full)
		d.Request.Path, d.Request.URL = "/"+d.Post.Slug+"/", d.Post.URL
		d.Comments = fixtureComments(d.Post, full)
	case KindSingle:
		d.Post = fixturePost(1, "post", full)
		d.Request.Path, d.Request.URL = "/"+d.Post.Slug+"/", d.Post.URL
		d.Comments = fixtureComments(d.Post, full)
	case KindCategory:
		_, d.Term, _ = fixtureTerms(full)
		listing("/category/markets/stocks/", true)
	case KindTag:
		_, _, d.Term = fixtureTerms(full)
		listing("/tag/example/", true)
	case KindAuthor:
		d.Author = fixtureAuthor(full)
		listing("/author/author-a/", true)
	case KindDate:
		d.Date = &DateArchive{Year: 2021, Start: time.Date(2021, 1, 1, 0, 0, 0, 0, time.UTC), YearURL: fixtureBase + "/2021/"}
		if full {
			d.Date.Month, d.Date.Start = 3, time.Date(2021, 3, 1, 0, 0, 0, 0, time.UTC)
		}
		listing("/2021/03/", false)
	case KindSearch:
		d.Search = &Search{}
		if full {
			d.Search = &Search{Query: "example <b>", Total: 3}
		}
		listing("/search/", false)
		d.Request.NoIndex = true
	case Kind404, Kind410:
		d.Request.Path, d.Request.URL = "/missing/", fixtureBase+"/missing/"
	}
	return d
}

// FixtureKinds maps a template file to the page kinds the hierarchy can render it for. nil means unknown.
func FixtureKinds(name string) []string {
	base := name[:len(name)-len(".html")]
	prefixed := func(p string) bool { return base == p || len(base) > len(p) && base[:len(p)+1] == p+"-" }
	switch {
	case base == "index":
		return []string{KindHome, KindFrontPage, KindPostsPage, KindSingle, KindPage, KindCategory, KindTag, KindAuthor, KindDate, KindSearch, Kind404, Kind410}
	case base == "front-page":
		return []string{KindHome, KindFrontPage}
	case base == "home":
		return []string{KindHome, KindPostsPage}
	case prefixed("single"):
		return []string{KindSingle}
	case prefixed("page"):
		return []string{KindPage, KindFrontPage}
	case base == "archive":
		return []string{KindCategory, KindTag, KindAuthor, KindDate}
	case prefixed("category"):
		return []string{KindCategory}
	case prefixed("tag"):
		return []string{KindTag}
	case prefixed("author"):
		return []string{KindAuthor}
	case base == "date":
		return []string{KindDate}
	case base == "search":
		return []string{KindSearch}
	case base == "404":
		return []string{Kind404, Kind410}
	case base == "410":
		return []string{Kind410}
	}
	return nil
}

// FixtureRelated is sample data for a related-posts func in `afsc theme check`: up to n fixture posts other than p.
func FixtureRelated(p *Post, n int) []*Post {
	var out []*Post
	for _, f := range fixturePosts(true) {
		if len(out) < n && (p == nil || f.ID != p.ID) {
			out = append(out, f)
		}
	}
	return out
}
