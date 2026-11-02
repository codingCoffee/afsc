package wpseo

import (
	"reflect"
	"testing"

	"afsc/migrators/afsc-wp/internal/records"
)

func TestUnsupportedAndApply(t *testing.T) {
	s := "%%title%% %%sep%% %%focuskw%% %%cf_foo%% %%parent_title%%"
	if got := Unsupported(s); !reflect.DeepEqual(got, []string{"cf_foo", "focuskw", "parent_title"}) {
		t.Fatalf("unsupported %v", got)
	}
	decide := func(name string) Variable {
		switch name {
		case "focuskw":
			return Variable{Decision: "expand"}
		case "cf_foo":
			return Variable{Decision: "remove"}
		}
		return Variable{Decision: "replace", With: "Parent"}
	}
	got := Apply(s, decide, map[string]string{"focuskw": "money"})
	if got != "%%title%% %%sep%% money  Parent" {
		t.Fatalf("apply %q", got)
	}
}

func TestPost(t *testing.T) {
	meta := map[string]string{"title": "%%title%% %%sep%% Site", "noindex": "2", "nofollow": "1",
		"robots_advanced": "noarchive,nosnippet", "cornerstone": "1", "og_image_id": "5", "focus_keyphrase": "kw"}
	s := Post(func(f string) string { return meta[f] }, func(id, u string) *string {
		if id == "5" {
			return records.Ptr("wp:post:5")
		}
		return nil
	})
	if *s.Title != "%%title%% %%sep%% Site" || *s.RobotsNoindex != false || !*s.RobotsNofollow || !s.IsCornerstone ||
		*s.OGImageMedia != "wp:post:5" || !reflect.DeepEqual(s.RobotsAdvanced, []string{"noarchive", "nosnippet"}) {
		t.Fatalf("seo %+v", s)
	}
	if empty := Post(func(string) string { return "" }, nil); !empty.Empty() {
		t.Fatalf("empty meta should give an empty record: %+v", empty)
	}
	if Tri("0") != nil || *Tri("1") != true {
		t.Fatal("tri")
	}
}

func TestTermAndTemplates(t *testing.T) {
	s := Term(map[string]any{"wpseo_title": "T", "wpseo_noindex": "noindex", "wpseo_desc": ""}, nil)
	if *s.Title != "T" || !*s.RobotsNoindex || s.Description != nil {
		t.Fatalf("term %+v", s)
	}
	title, desc := Templates(map[string]any{"title-post": "%%title%% %%sep%% %%sitename%%", "title-tax-post_tag": "Tag %%term_title%%",
		"metadesc-home-wpseo": "Home desc", "title-search-wpseo": ""})
	if title["post"] == "" || title["tag"] != "Tag %%term_title%%" || desc["home"] != "Home desc" || title["search"] != "" {
		t.Fatalf("templates %v %v", title, desc)
	}
}

func TestVideo(t *testing.T) {
	m := map[string]any{"player_loc": "https://www.youtube-nocookie.com/embed/abcdefghijk", "title": "V", "duration": int64(312),
		"thumbnail_loc": "https://i.ytimg.com/vi/abcdefghijk/hqdefault.jpg", "publication_date": "2021-03-01T00:00:00+00:00", "type": "youtube"}
	embed := func(u string) (string, string, string, bool) {
		return "youtube", "https://www.youtube.com/watch?v=abcdefghijk", "abcdefghijk", true
	}
	v, ok := Video(m, embed, func(string, string) *string { return nil })
	if !ok || v.Provider != "youtube" || v.ExternalID != "abcdefghijk" || *v.DurationSeconds != 312 || v.UploadDate == nil || v.ThumbnailURL == "" {
		t.Fatalf("video %+v", v)
	}
	if _, ok := Video(map[string]any{"title": "no url"}, embed, nil); ok {
		t.Fatal("video without url must be skipped")
	}
}
