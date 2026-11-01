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
		`{"cache":{"page_ttl_seconds":86401}}`:             "cache.page_ttl_seconds",
		`{"cache":{"page_ttl_seconds":-1}}`:                "cache.page_ttl_seconds",
		`{"seo":{"llms_txt":{"mode":"on"}}}`:               "seo.llms_txt.mode",
		`{"seo":{"llms_txt":{"mode":"custom"}}}`:           "seo.llms_txt.body",
		`{"seo":{"sitemaps":{"page_size":0}}}`:             "seo.sitemaps.page_size",
		`{"seo":{"sitemaps":{"page_size":50001}}}`:         "seo.sitemaps.page_size",
		`{"seo":{"indexnow":{"enabled":true}}}`:            "seo.indexnow.key",
		`{"seo":{"indexnow":{"key":"short"}}}`:             "seo.indexnow.key",
		`{"seo":{"indexnow":{"key":"../../etc/passwd"}}}`:  "seo.indexnow.key",
		`{"seo":{"sitemaps":{"layout":"x"}}}`:              "unknown field",
	}
	for _, in := range []string{`{"retention_days":0}`, `{"retention_days":3651}`} {
		cases[`{"seo":{"not_found":`+in+`}}`] = "seo.not_found.retention_days"
	}
	for _, in := range []string{`["x.php"]`, `[""]`, `["/` + strings.Repeat("a", 256) + `"]`} {
		cases[`{"seo":{"not_found":{"noise_patterns":`+in+`}}}`] = "seo.not_found.noise_patterns"
	}
	for in, want := range cases {
		if _, _, err := Decode([]byte(in)); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Decode(%s) error = %v, want it to contain %q", in, err, want)
		}
	}
}

func TestSEOFileSettings(t *testing.T) {
	all, _, err := Decode([]byte(`{"seo":{"separator":"|"}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s := all.SEO; s.Sitemaps.PageSize != 1000 || s.LLMsTxt.Mode != "off" || s.IndexNow.Enabled || s.RobotsTxt != "" {
		t.Errorf("an seo group without the new keys must keep their defaults: %+v", s)
	}
	all, _, err = Decode([]byte(`{"seo":{"sitemaps":{"page_size":2},"llms_txt":{"mode":"custom","body":"# X"},` +
		`"indexnow":{"enabled":true,"key":"0123abcd-ef"}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if s := all.SEO; s.Sitemaps.PageSize != 2 || s.LLMsTxt.Body != "# X" || s.IndexNow.Key != "0123abcd-ef" {
		t.Errorf("seo = %+v", s)
	}
}

func TestNotFoundSettings(t *testing.T) {
	all, _, err := Decode([]byte(`{"seo":{"not_found":{"retention_days":30}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if nf := all.SEO.NotFound; nf.RetentionDays != 30 || len(nf.NoisePatterns) != len(DefaultNoisePatterns()) {
		t.Errorf("not_found = %+v", nf)
	}
	all, _, err = Decode([]byte(`{"seo":{"not_found":{"noise_patterns":[]}}}`))
	if err != nil {
		t.Fatal(err)
	}
	if nf := all.SEO.NotFound; len(nf.NoisePatterns) != 0 || nf.RetentionDays != 90 {
		t.Errorf("an empty list must turn filtering off: %+v", nf)
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
