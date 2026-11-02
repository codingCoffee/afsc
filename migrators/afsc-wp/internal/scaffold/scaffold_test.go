package scaffold

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"afsc/migrators/afsc-wp/internal/extract"
)

func TestScaffold(t *testing.T) {
	code := t.TempDir()
	files := map[string]string{
		"functions.php": `<?php
register_nav_menus( array( 'primary' => __( 'Primary' ), 'social' => 'Social' ) );
add_image_size( 'hero', 1200, 600, true );
wp_enqueue_style( 'fonts', 'https://fonts.googleapis.com/css?family=Example' );
add_action( 'wp_head', function () { echo '<script src="https://www.googletagmanager.com/gtag/js"></script>'; } );`,
		"page-wide.php": "<?php\n/* Template Name: Wide */\nget_header();",
		"single.php":    "<?php get_header();",
		"style.css":     ".entry-content .callout { color: red } .site-header {}",
	}
	for p, b := range files {
		os.WriteFile(filepath.Join(code, p), []byte(b), 0o644)
	}
	ds := &extract.Dataset{Options: map[string]any{
		"stylesheet": "Acme Child", "thumbnail_size_w": int64(150), "thumbnail_size_h": int64(150), "thumbnail_crop": "1",
		"medium_size_w": "300", "medium_size_h": "300",
		"theme_mods_Acme Child": map[string]any{"nav_menu_locations": map[string]any{"footer": int64(3)}, "header_color": "#AABBCC",
			"show_tagline": true, "custom_css_post_id": int64(9), "0": false},
		"widget_text":      map[string]any{"2": map[string]any{"title": "About", "text": "<p>About us</p>"}, "_multiwidget": int64(1)},
		"sidebars_widgets": map[string]any{"sidebar-1": []any{"text-2"}, "wp_inactive_widgets": []any{}, "array_version": int64(3)},
	}}
	out := filepath.Join(t.TempDir(), "theme")
	res, err := Run(Input{DS: ds, Classes: map[string]int{"callout": 3, "lead": 1, "alignleft": 5, "wp-image-5": 1},
		CodeDirs: []string{code}, Selector: ".entry-content", Out: out})
	if err != nil {
		t.Fatal(err)
	}
	m := res.Manifest
	if m.Name != "acme-child" || !reflect.DeepEqual(m.Content.AllowedClasses, []string{"callout"}) {
		t.Errorf("name/classes: %s %v", m.Name, m.Content.AllowedClasses)
	}
	if !reflect.DeepEqual(m.MenuLocations, []string{"footer", "primary", "social"}) {
		t.Errorf("menus %v", m.MenuLocations)
	}
	if m.ImageSizes["hero"] != (ImageSize{1200, 600, true}) || !m.ImageSizes["thumbnail"].Crop || m.ImageSizes["medium"].Width != 300 {
		t.Errorf("sizes %v", m.ImageSizes)
	}
	if !reflect.DeepEqual(m.CSP["style"], []string{"https://fonts.googleapis.com"}) || !reflect.DeepEqual(res.Tracking, []string{"www.googletagmanager.com"}) {
		t.Errorf("csp %v tracking %v", m.CSP, res.Tracking)
	}
	if m.Settings["header_color"].Type != "color" || m.Settings["show_tagline"].Default != true || len(m.Settings) != 2 {
		t.Errorf("settings %v", m.Settings)
	}
	if !strings.HasPrefix(res.Templates["page-wide.php"], "page-wide.html") || res.Templates["single.php"] != "single.html" {
		t.Errorf("templates %v", res.Templates)
	}
	w, err := os.ReadFile(filepath.Join(out, "templates", "parts", "widget-sidebar-1-1.html"))
	if err != nil || !strings.Contains(string(w), "About us") {
		t.Errorf("widget partial: %v %s", err, w)
	}
	readme, _ := os.ReadFile(filepath.Join(out, "README.md"))
	if !strings.Contains(string(readme), "dynamic_sidebar") || !strings.Contains(string(readme), "www.googletagmanager.com") {
		t.Errorf("readme:\n%s", readme)
	}
	os.WriteFile(filepath.Join(out, "templates", "index.html"), []byte("ported"), 0o644)
	if _, err := Run(Input{DS: ds, CodeDirs: []string{code}, Out: out}); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(out, "templates", "index.html")); string(b) != "ported" {
		t.Error("a re-run must not overwrite ported templates")
	}
}
