package theme

// Candidates lists template names for d, most specific first. The first one the theme has is rendered.
func Candidates(d *PageData) []string {
	var c []string
	add := func(names ...string) {
		for _, n := range names {
			c = append(c, n+".html")
		}
	}
	page := func(p *Post) {
		if p.Template != "" {
			add("page-" + p.Template)
		}
		add("page-"+p.Slug, "page")
	}
	switch d.Kind {
	case KindHome:
		add("front-page", "home")
	case KindFrontPage:
		add("front-page")
		page(d.Post)
	case KindPostsPage:
		add("home")
	case KindSingle:
		add("single-"+d.Post.Slug, "single")
	case KindPage:
		page(d.Post)
	case KindCategory, KindTag:
		add(d.Kind+"-"+d.Term.Slug, d.Kind, "archive")
	case KindAuthor:
		add("author-"+d.Author.Slug, "author", "archive")
	case KindDate:
		add("date", "archive")
	case KindSearch:
		add("search")
	case Kind410:
		add("410", "404")
	case Kind404:
		add("404")
	}
	add("index")
	return c
}
