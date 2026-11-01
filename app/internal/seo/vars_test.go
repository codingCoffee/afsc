package seo

import (
	"slices"
	"testing"
	"time"
)

func TestExpand(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	full := Vars{Title: "Example post", SiteName: "Example Blog", SiteDesc: "Just another site", Sep: "|", Page: 1, PageTotal: 1,
		PrimaryCategory: "Stocks", Category: "Markets, Stocks", Tag: "Example", TermTitle: "Markets", TermDescription: "Market news",
		Name: "Author A", AuthorFirst: "Author", AuthorLast: "A", Excerpt: "An excerpt.", ExcerptOnly: "An excerpt.",
		Date: "March 4, 2021", Modified: "March 5, 2021", SearchPhrase: "bonds", PTSingle: "Post", PTPlural: "Posts", Now: now}
	paged := full
	paged.Page, paged.PageTotal = 3, 7
	noSep := full
	noSep.Sep = ""
	dash := full
	dash.Sep = "-"

	cases := []struct {
		name    string
		tpl     string
		v       Vars
		want    string
		unknown []string
	}{
		{"title", "%%title%% %%sep%% %%sitename%%", full, "Example post | Example Blog", nil},
		{"sitedesc", "%%sitename%% %%sep%% %%sitedesc%%", full, "Example Blog | Just another site", nil},
		{"page hidden on page 1", "%%title%% %%page%% %%sep%% %%sitename%%", full, "Example post | Example Blog", nil},
		{"page on page 3", "%%title%% %%page%% %%sep%% %%sitename%%", paged, "Example post | Page 3 of 7 | Example Blog", nil},
		{"pagenumber and pagetotal", "%%pagenumber%%/%%pagetotal%%", paged, "3/7", nil},
		{"pagenumber unpaged", "%%pagenumber%%/%%pagetotal%%", full, "1/1", nil},
		{"primary category", "%%primary_category%% %%sep%% %%category%%", full, "Stocks | Markets, Stocks", nil},
		{"tag", "%%tag%%", full, "Example", nil},
		{"term", "%%term_title%% %%sep%% %%term_description%%", full, "Markets | Market news", nil},
		{"term description aliases", "%%category_description%% %%tag_description%%", full, "Market news Market news", nil},
		{"author", "%%name%% (%%author_first_name%% %%author_last_name%%)", full, "Author A (Author A)", nil},
		{"excerpt", "%%excerpt%% %%excerpt_only%%", full, "An excerpt. An excerpt.", nil},
		{"dates", "%%date%% %%sep%% %%modified%%", full, "March 4, 2021 | March 5, 2021", nil},
		{"current date", "%%currentyear%% %%currentmonth%%", full, "2026 November", nil},
		{"search", "You searched for %%searchphrase%% %%page%% %%sep%% %%sitename%%", full, "You searched for bonds | Example Blog", nil},
		{"post type", "%%pt_single%% %%pt_plural%%", full, "Post Posts", nil},
		{"unknown var", "%%title%% %%focuskw%% %%sep%% %%sitename%%", full, "Example post | Example Blog", []string{"focuskw"}},
		{"custom field", "%%cf_price%% %%title%%", full, "Example post", []string{"cf_price"}},
		{"empty value leaves no double separator", "%%title%% %%sep%% %%tag%% %%sep%% %%sitename%%", Vars{Title: "T", SiteName: "S", Sep: "|"}, "T | S", nil},
		{"leading separator dropped", "%%sep%% %%sitename%%", full, "Example Blog", nil},
		{"trailing separator dropped", "%%title%% %%sep%%", full, "Example post", nil},
		{"whitespace collapsed", "  %%title%%\n\t %%sep%%   %%sitename%% ", full, "Example post | Example Blog", nil},
		{"dash separator", "%%title%% %%page%% %%sep%% %%sitename%%", dash, "Example post - Example Blog", nil},
		{"no separator", "%%title%% %%sep%% %%sitename%%", noSep, "Example post Example Blog", nil},
		{"literal text kept", "Read: %%title%%", full, "Read: Example post", nil},
		{"unterminated var kept", "%%title%% 100%% sure", full, "Example post 100%% sure", nil},
		{"empty template", "", full, "", nil},
		{"all empty", "%%title%% %%sep%% %%sitename%%", Vars{Sep: "|"}, "", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, unknown := Expand(c.tpl, c.v)
			if got != c.want {
				t.Errorf("Expand(%q) = %q, want %q", c.tpl, got, c.want)
			}
			if !slices.Equal(unknown, c.unknown) {
				t.Errorf("unknown = %v, want %v", unknown, c.unknown)
			}
		})
	}
}

func TestTruncate(t *testing.T) {
	cases := []struct{ in, want string }{
		{"short", "short"},
		{"one two three four five six", "one two three…"},
		{"  spaced   out  ", "spaced out"},
	}
	for _, c := range cases {
		if got := truncate(c.in, 16); got != c.want {
			t.Errorf("truncate(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestISODuration(t *testing.T) {
	for sec, want := range map[int]string{0: "PT0S", 45: "PT45S", 312: "PT5M12S", 3600: "PT1H", 3723: "PT1H2M3S"} {
		if got := ISODuration(sec); got != want {
			t.Errorf("ISODuration(%d) = %q, want %q", sec, got, want)
		}
	}
}

func TestLocale(t *testing.T) {
	for in, want := range map[string]string{"en": "en_US", "en-IN": "en_IN", "en_gb": "en_GB", "": "en_US", "de": "de_DE"} {
		if got := locale(in); got != want {
			t.Errorf("locale(%q) = %q, want %q", in, got, want)
		}
	}
}
