package settings

import (
  "encoding/json"
  "strings"
  "testing"
)

func TestDefaultsAreValid(t *testing.T) {
  if err := Defaults().Validate(); err != nil {
    t.Fatal(err)
  }
}

func TestDecodeMergesOverDefaults(t *testing.T) {
  all, present, err := Decode([]byte(`{"site":{"title":"Example blog"},"reading":{"home":"wp:post:2","posts_per_page":5}}`))
  if err != nil {
    t.Fatal(err)
  }
  if all.Site.Title != "Example blog" || all.Site.Timezone != "UTC" {
    t.Errorf("site = %+v", all.Site)
  }
  if all.Reading.Home == nil || all.Reading.Home.LegacyKey != "wp:post:2" || all.Reading.PostsPerPage != 5 {
    t.Errorf("reading = %+v", all.Reading)
  }
  if !present["site"] || !present["reading"] || present["seo"] {
    t.Errorf("present = %v", present)
  }
}

func TestDecodeRejects(t *testing.T) {
  cases := map[string]string{
    `{"sight":{}}`:                                     "unknown settings key",
    `{"site":{"titel":"x"}}`:                           "unknown field",
    `{"site":{"timezone":"Mars/Base"}}`:                "site.timezone",
    `{"permalinks":{"post_pattern":"/{slug}/{foo}/"}}`: "unknown token",
    `{"permalinks":{"tag_base":"admin"}}`:              "permalinks.tag_base",
    `{"seo":{"title_templates":{"widget":"x"}}}`:       "unknown type",
    `{"seo":{"social_profiles":["http://x.com/a"]}}`:   "https",
    `{"cache":{"page_ttl_seconds":86401}}`:            "cache.page_ttl_seconds",
    `{"cache":{"page_ttl_seconds":-1}}`:               "cache.page_ttl_seconds",
  }
  for in, want := range cases {
    if _, _, err := Decode([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
      t.Errorf("Decode(%s) error = %v, want it to contain %q", in, err, want)
    }
  }
}

func TestRefJSON(t *testing.T) {
  for in, want := range map[string]Ref{`"wp:post:1"`: {LegacyKey: "wp:post:1"}, `12`: {ID: 12}, `null`: {}} {
    var r Ref
    if err := json.Unmarshal([]byte(in), &r); err != nil || r != want {
      t.Errorf("unmarshal %s = %+v, %v", in, r, err)
    }
  }
  b, _ := json.Marshal(Ref{ID: 3, LegacyKey: "wp:post:1"})
  if string(b) != "3" {
    t.Errorf("resolved ref must marshal as its id, got %s", b)
  }
}
