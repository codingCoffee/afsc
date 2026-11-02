package wpdb

import (
	"context"
	"os"
	"slices"
	"strings"
	"testing"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/extract"
)

// TestLoadFixture needs testdata/wp-fixture.sql loaded into the database AFSC_WP_TEST_MYSQL_DSN points at.
func TestLoadFixture(t *testing.T) {
	if os.Getenv("AFSC_WP_TEST_MYSQL_DSN") == "" {
		t.Skip("AFSC_WP_TEST_MYSQL_DSN not set")
	}
	cfg, err := config.Load("../../testdata/afsc-wp.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	ds, set, err := Load(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if p := ds.Post(19); p != nil {
		t.Errorf("revisions must not load")
	}
	if p := ds.Post(24); p != nil {
		t.Errorf("auto-drafts must not load")
	}
	if p := ds.Post(12); p == nil || p.Slug != "café-culture" || p.Name != "caf%c3%a9-culture" {
		t.Errorf("percent-encoded slug: %+v", p)
	}
	if p := ds.Post(15); p == nil || p.DateGMT != "0000-00-00 00:00:00" {
		t.Errorf("zero gmt draft: %+v", p)
	}
	if p := ds.Post(10); p == nil || p.Meta.First("_thumbnail_id") != "30" || len(p.TermTaxIDs) != 3 {
		t.Errorf("post 10 meta/terms: %+v", p)
	}
	titles := ds.OptionMap("wpseo_titles")
	if titles["company_name"] != "Example Org ₹" || titles["noindex-tax-post_tag"] != true {
		t.Errorf("serialized option with multibyte string: %v", titles)
	}
	fixed := false
	for _, c := range ds.Comments {
		if c.ID == 107 {
			fixed = c.Content == "It’s great"
		}
	}
	if !fixed || !slices.ContainsFunc(ds.Fixes, func(f extract.Fix) bool { return f.LegacyKey == "wpfx:comment:107" }) {
		t.Errorf("latin1 double-encoded comment not repaired: %+v", ds.Fixes)
	}
	for _, name := range []string{"wordpress_api_key", "smtp_password"} {
		if !slices.Contains(ds.Stats.SecretOptions, name) {
			t.Errorf("secret option %s not flagged: %v", name, ds.Stats.SecretOptions)
		}
		if _, loaded := ds.Options[name]; loaded {
			t.Errorf("secret option %s was loaded by value", name)
		}
	}
	if !slices.Contains(ds.Stats.SkippedUserMeta, "session_tokens") || !slices.Contains(ds.Stats.SkippedUserMeta, "community-events-location") {
		t.Errorf("usermeta that may hold secrets must be skipped: %v", ds.Stats.SkippedUserMeta)
	}
	for _, u := range ds.Users {
		if _, ok := u.Meta["session_tokens"]; ok {
			t.Errorf("session tokens loaded for user %d", u.ID)
		}
	}
	for _, id := range []string{"yoast", "yoast_premium", "yoast_video", "jetpack", "user_profile_picture", "guest_author"} {
		if !set.On(id) {
			t.Errorf("adapter %s not detected", id)
		}
	}
	latin := false
	for _, tb := range ds.Tables {
		latin = latin || (tb.Name == "wp_comments" && strings.HasPrefix(tb.Collation, "latin1"))
	}
	if !latin {
		t.Errorf("wp_comments should be latin1: %+v", ds.Tables)
	}
}
