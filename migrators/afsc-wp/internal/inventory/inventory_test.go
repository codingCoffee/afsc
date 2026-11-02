package inventory

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/decisions"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/plugins"
)

func TestPropose(t *testing.T) {
	dir := t.TempDir()
	code := filepath.Join(dir, "theme")
	os.MkdirAll(code, 0o755)
	os.WriteFile(filepath.Join(code, "functions.php"), []byte(`<?php
add_shortcode( 'pullquote', 'acme_pull' );
add_filter( 'the_content', 'acme_toc' );`), 0o644)
	cfgPath := filepath.Join(dir, "c.json")
	os.WriteFile(cfgPath, []byte(`{"site":{"base_url":"https://blog.example.com"}}`), 0o600)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	ds := &extract.Dataset{Options: map[string]any{
		"permalink_structure": "/%author%/%postname%/", "category_base": ".",
		"active_plugins": []any{"jetpack/jetpack.php", "redirection/redirection.php"}, "jetpack_active_modules": []any{"photon", "custom-thing"},
	}}
	ds.Posts = []*extract.Post{
		{ID: 1, Type: "post", Status: "publish", Password: "x", Content: "[caption]<img src=\"/wp-content/uploads/gone.png\">[/caption] [pullquote]Q[/pullquote] [sic] [vimeo 123]\n<p>&nbsp;</p><!--nextpage-->\n<script>x()</script><iframe src=\"https://www.youtube.com/embed/abcdefghijk\"></iframe><iframe src=\"https://forms.example.org/f\"></iframe>\nhttps://www.flickr.com/photos/x/1\n<!-- wp:acme/card /--><!-- wp:paragraph --><p>x</p><!-- /wp:paragraph -->"},
		{ID: 2, Type: "event", Status: "publish", Content: "event"},
	}
	ds.Stats.Taxonomies = []extract.Count{{Key: "category"}, {Key: "genre", Count: 3}}
	ds.Stats.PostTypeStatus = []extract.Count{{Key: "post", Sub: "publish", Count: 1}, {Key: "event", Sub: "publish", Count: 1}}
	set := plugins.Resolve(cfg, []string{"jetpack/jetpack.php"}, nil)
	inv, err := Run(cfg, ds, set, []string{code})
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"shortcode:caption": "convert", "shortcode:pullquote": "render_php", "shortcode:sic": "keep_text", "shortcode:vimeo": "convert",
		"block:acme/card": "raw_html", "block:core/paragraph": "convert", "jetpack_module:photon": "drop",
		"jetpack_module:custom-thing": "drop", "theme_hook:the_content:acme_toc": "replicate_theme", "plugin:redirection": "drop",
		"iframe_host:www.youtube.com": "embed", "iframe_host:forms.example.org": "raw_html", "element:script": "raw_html",
		"taxonomy:genre": "drop", "post_type:event": "skip", "marker:nextpage": "merge", "empty_paragraphs:empty_paragraphs": "drop",
		"permalink_structure:/%author%/%postname%/": "change", "category_base:category": "change",
		"missing_media:gone.png": "keep_src", "password_protected:all": "private", "oembed:www.flickr.com": "link",
	}
	got := map[string]*decisions.Item{}
	for _, it := range inv.Decisions.Items {
		got[it.ID] = it
	}
	for id, d := range want {
		if it := got[id]; it == nil || it.Proposed != d {
			t.Errorf("%s: got %+v, want proposed %s", id, it, d)
		}
	}
	if it := got["shortcode:pullquote"]; it != nil && strings.Contains(strings.Join(it.Allowed, ","), "convert") {
		t.Errorf("convert must not be offered without a converter: %v", it.Allowed)
	}
	if got["permalink_structure:/%author%/%postname%/"].OptionString("pattern") == "" {
		t.Errorf("permalink change needs a proposed pattern")
	}
	md := inv.Markdown(inv.Decisions).String()
	for _, s := range []string{"Decision table", "shortcode:pullquote", "Files that will NOT be imported"} {
		if !strings.Contains(md, s) {
			t.Errorf("inventory.md lacks %q", s)
		}
	}
}
