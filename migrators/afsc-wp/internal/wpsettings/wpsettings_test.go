package wpsettings

import (
	"strings"
	"testing"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/wpseo"
)

func TestDateLayout(t *testing.T) {
	ok := map[string]string{
		"F j, Y":    "January 2, 2006",
		"d/m/Y":     "02/01/2006",
		"Y-m-d H:i": "2006-01-02 15:04",
		"D, M j":    "Mon, Jan 2",
		"g:i a":     "3:04 pm",
		`\D\a\y j`:  "Day 2",
		"l jS F Y":  "",
		"Y \\1\\5":  "",
	}
	for in, want := range ok {
		got, err := DateLayout(in)
		if want == "" {
			if err == nil {
				t.Errorf("%q: want error, got %q", in, got)
			}
			continue
		}
		if err != nil || got != want {
			t.Errorf("%q: got %q %v want %q", in, got, err, want)
		}
	}
}

func input(opts map[string]any) Input {
	in := Input{
		DS: &extract.Dataset{Options: opts}, Timezone: "Asia/Kolkata", Yoast: true,
		Decide: func(_, _, def string) string { return def }, Option: func(_, _, _ string) string { return "" },
		Page: func(id int64) (string, bool) {
			return "wp:post:" + map[int64]string{2: "2", 4: "4"}[id], id == 2 || id == 4
		},
		Media: func(id, u string) *string {
			if id == "9" {
				return records.Ptr("wp:post:9")
			}
			return nil
		},
		Variable: func(string) wpseo.Variable { return wpseo.Variable{Decision: "remove"} },
	}
	in.Keys.Titles, in.Keys.Social = "wpseo_titles", "wpseo_social"
	return in
}

func TestBuild(t *testing.T) {
	out := Build(input(map[string]any{
		"blogname": "Example Blog", "blogdescription": "Tagline", "date_format": "F j, Y", "WPLANG": "en_IN",
		"permalink_structure": "/%year%/%monthnum%/%postname%/", "category_base": "/topics", "tag_base": "",
		"show_on_front": "page", "page_on_front": "2", "page_for_posts": "4", "posts_per_page": "500",
		"posts_per_rss": "15", "rss_use_excerpt": "1", "close_comments_for_old_posts": "1", "close_comments_days_old": "30",
		"thread_comments": "1", "thread_comments_depth": "12", "page_comments": "", "comments_per_page": "20",
		"wpseo_titles": map[string]any{"separator": "sc-pipe", "title-post": "%%title%% %%sep%% %%sitename%% %%cf_x%%",
			"noindex-tax-post_tag": true, "noindex-author-wpseo": true, "disable-date": "1", "disable-attachment": true,
			"company_or_person": "company", "company_name": "Example Org", "company_logo_id": int64(9)},
		"wpseo_social": map[string]any{"facebook_site": "https://facebook.com/example", "twitter_site": "example",
			"youtube_url": "http://youtube.com/insecure", "og_default_image_id": "9"},
	}))
	if len(out.Problems) > 0 {
		t.Fatalf("problems %v", out.Problems)
	}
	a := out.All
	checks := map[string]bool{
		"title":        a.Site.Title == "Example Blog",
		"language":     a.Site.Language == "en-IN",
		"pattern":      a.Permalinks.PostPattern == "/{year}/{month}/{slug}/",
		"cat base":     a.Permalinks.CategoryBase == "topics" && a.Permalinks.TagBase == "tag",
		"home":         a.Reading.Home != nil && a.Reading.Home.LegacyKey == "wp:post:2",
		"posts page":   a.Reading.PostsPage.LegacyKey == "wp:post:4",
		"clamped":      a.Reading.PostsPerPage == 100 && a.Reading.FeedItems == 15 && a.Reading.FeedExcerptOnly,
		"discussion":   a.Discussion.CloseAfterDays == 30 && a.Discussion.ThreadDepth == 10 && a.Discussion.CommentsPerPage == 0,
		"separator":    a.SEO.Separator == "|",
		"template":     a.SEO.TitleTemplates["post"] == "%%title%% %%sep%% %%sitename%% ",
		"tag noindex":  a.SEO.TagNoindex,
		"date off":     !a.Permalinks.DateArchivesEnabled && a.Permalinks.AuthorArchivesEnabled,
		"type noindex": out.TypeNoindex["author-wpseo"] && out.DisableAttachment,
		"kg":           a.SEO.KnowledgeGraph.Name == "Example Org" && a.SEO.KnowledgeGraph.Logo.LegacyKey == "wp:post:9",
		"social":       strings.Join(a.SEO.SocialProfiles, ",") == "https://facebook.com/example,https://x.com/example" && a.SEO.TwitterSite == "@example",
		"default img":  a.SEO.DefaultImage.LegacyKey == "wp:post:9",
	}
	for name, ok := range checks {
		if !ok {
			t.Errorf("%s failed: %+v", name, a)
		}
	}
	b, err := Encode(a)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(string(b), `"cache"`) || !strings.Contains(string(b), `"wp:post:2"`) {
		t.Fatalf("encoded %s", b)
	}
}

func TestUnsupportedPermalinkIsAProblem(t *testing.T) {
	out := Build(input(map[string]any{"permalink_structure": "/%author%/%postname%/"}))
	if len(out.Problems) == 0 || !strings.Contains(out.Problems[0], "%author%") {
		t.Fatalf("problems %v", out.Problems)
	}
	in := input(map[string]any{"permalink_structure": "/%author%/%postname%/"})
	in.Decide = func(kind, _, def string) string {
		if kind == "permalink_structure" {
			return "change"
		}
		return def
	}
	in.Option = func(_, _, _ string) string { return "/{slug}/" }
	if out := Build(in); len(out.Problems) > 0 || out.All.Permalinks.PostPattern != "/{slug}/" {
		t.Fatalf("change decision: %v %s", out.Problems, out.All.Permalinks.PostPattern)
	}
}
