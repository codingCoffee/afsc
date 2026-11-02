package plugins

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/extract"
)

func cfg(t *testing.T, body string) *config.Config {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(p)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestResolve(t *testing.T) {
	c := cfg(t, `{"site":{"base_url":"https://blog.example.com"},
		"plugins":{"jetpack":{"enabled":false},"guest_author":{"mode":"replace","keys":{"name":"guest_name","email":"guest_email"}},
		"yoast":{"keys":{"title":"_custom_title"}}}}`)
	active := []string{"wordpress-seo-premium/wp-seo-premium.php", "jetpack/jetpack.php"}
	meta := []extract.Count{{Key: "metronet_image_id", Count: 2}, {Key: "guest_name", Count: 3}, {Key: "_yoast_wpseo_video_meta", Count: 1}}
	s := Resolve(c, active, meta)

	want := map[string]bool{"core": true, "yoast": true, "yoast_premium": true, "yoast_video": true, "jetpack": false,
		"user_profile_picture": true, "guest_author": true}
	for id, on := range want {
		if s.On(id) != on {
			t.Errorf("%s: on=%v want %v", id, s.On(id), on)
		}
	}
	if s.Key("yoast", "title") != "_custom_title" || s.Key("yoast", "description") != "_yoast_wpseo_metadesc" {
		t.Errorf("key override failed")
	}
	if s.GuestMode != "replace" {
		t.Errorf("guest mode %q", s.GuestMode)
	}
	if !slices.Contains(OptionNames(s), "wpseo-premium-redirects-base") {
		t.Errorf("options: %v", OptionNames(s))
	}
	if keys := UserMetaKeys(s, "wp_"); !slices.Contains(keys, "wp_capabilities") || !slices.Contains(keys, "metronet_image_id") {
		t.Errorf("usermeta: %v", keys)
	}
}

func TestGuestAuthorNeedsConfig(t *testing.T) {
	s := Resolve(cfg(t, `{"site":{"base_url":"https://blog.example.com"}}`), nil, []extract.Count{{Key: "guest_name"}})
	if s.On("guest_author") || s.On("yoast") {
		t.Fatal("nothing should be detected")
	}
}
