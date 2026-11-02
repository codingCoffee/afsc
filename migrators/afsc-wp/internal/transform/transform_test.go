package transform_test

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"afsc/internal/importer"
	"afsc/internal/settings"
	"afsc/internal/theme"

	"afsc/migrators/afsc-wp/internal/bundle"
	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/decisions"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/filter"
	"afsc/migrators/afsc-wp/internal/plugins"
	"afsc/migrators/afsc-wp/internal/transform"
	"afsc/migrators/afsc-wp/internal/uploads"
)

type echoFilter struct{ html map[string]string }

func (e echoFilter) Run(_ context.Context, req filter.Request) (map[string]string, error) {
	out := map[string]string{}
	for _, it := range req.Items {
		if h, ok := e.html[req.Tag+"|"+it.ID]; ok {
			out[it.ID] = h
		} else {
			out[it.ID] = "<p>" + it.Content + "</p>\n"
		}
	}
	return out, nil
}

func pngFile(t *testing.T, p string, w, h int) {
	t.Helper()
	os.MkdirAll(filepath.Dir(p), 0o755)
	var b bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	img.Pix[0] = byte(w)
	png.Encode(&b, img)
	if err := os.WriteFile(p, b.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func setup(t *testing.T) (*config.Config, *extract.Dataset, *decisions.File) {
	dir := t.TempDir()
	up := filepath.Join(dir, "uploads")
	pngFile(t, filepath.Join(up, "2022/03/a.png"), 8, 4)
	pngFile(t, filepath.Join(up, "2022/03/a-4x2.png"), 4, 2)
	pngFile(t, filepath.Join(up, "2019/01/sig.png"), 3, 3)
	os.WriteFile(filepath.Join(up, "2022/03/dump.sql"), []byte("x"), 0o644)
	cfgPath := filepath.Join(dir, "afsc-wp.json")
	os.WriteFile(cfgPath, []byte(`{"source":"wp","site":{"base_url":"https://blog.example.com","old_hosts":["old.example.com"]},
		"uploads_dir":"uploads","generated_at":"2023-01-01T00:00:00Z"}`), 0o600)
	cfg, err := config.Load(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	files, err := uploads.Scan(up)
	if err != nil {
		t.Fatal(err)
	}
	meta := `a:3:{s:5:"width";i:8;s:6:"height";i:4;s:5:"sizes";a:1:{s:6:"medium";a:3:{s:4:"file";s:9:"a-4x2.png";s:5:"width";i:4;s:6:"height";i:2;}}}`
	ds := &extract.Dataset{Prefix: "wp_", Files: files, Options: map[string]any{
		"siteurl": "https://blog.example.com", "home": "https://blog.example.com", "blogname": "Example Blog",
		"permalink_structure": "/%year%/%monthnum%/%postname%/", "timezone_string": "Asia/Kolkata", "date_format": "F j, Y",
		"show_on_front": "page", "page_on_front": int64(20), "sticky_posts": []any{int64(10)}, "posts_per_page": "10",
		"active_plugins": []any{"wordpress-seo/wp-seo.php"},
		"wpseo_titles":   map[string]any{"separator": "sc-pipe", "title-post": "%%title%% %%sep%% %%sitename%%", "disable-attachment": true},
		"stylesheet":     "acme", "theme_mods_acme": map[string]any{"nav_menu_locations": map[string]any{"primary": int64(7)}},
	}}
	ds.Users = []*extract.User{{ID: 1, Login: "admin", Nicename: "admin-a", Email: "Admin.A@Example.com", DisplayName: "Admin A",
		Meta: extract.Meta{"wp_capabilities": {`a:1:{s:13:"administrator";b:1;}`}}}}
	ds.Terms = []*extract.Term{{TermTaxID: 2, TermID: 2, Name: "News", RawSlug: "news", Slug: "news", Taxonomy: "category"},
		{TermTaxID: 7, TermID: 7, Name: "Main", RawSlug: "main", Slug: "main", Taxonomy: "nav_menu"}}
	ds.Posts = []*extract.Post{
		{ID: 10, Author: 1, Type: "post", Status: "publish", Name: "hello", Slug: "hello", Title: "Hello", Date: "2022-03-01 10:30:00",
			DateGMT: "2022-03-01 05:00:00", ModifiedGMT: "2022-03-02 05:00:00", GUID: "http://blog.example.com/?p=10", TermTaxIDs: []int64{2},
			Content: `See <a href="https://old.example.com/?p=20">home</a>. [caption id="attachment_30" align="alignleft"]<img class="wp-image-30" src="https://blog.example.com/wp-content/uploads/2022/03/a-4x2.png" alt="A"> Cap[/caption]`,
			Meta:    extract.Meta{"_wp_old_slug": {"hi"}, "_thumbnail_id": {"30"}, "_yoast_wpseo_meta-robots-noindex": {"1"}}},
		{ID: 20, Author: 1, Type: "page", Status: "publish", Name: "home", Slug: "home", Title: "Home", Date: "2022-01-01 10:00:00",
			DateGMT: "2022-01-01 04:30:00", Content: "Welcome"},
		{ID: 21, Author: 1, Type: "post", Status: "draft", Name: "", Title: "Draft idea", Date: "2022-06-01 10:00:00", DateGMT: "0000-00-00 00:00:00", Content: "Later"},
		{ID: 30, Author: 1, Type: "attachment", Status: "inherit", Name: "a", Parent: 10, MimeType: "image/png", Title: "A",
			DateGMT: "2022-03-01 05:00:00", Meta: extract.Meta{"_wp_attached_file": {"2022/03/a.png"}, "_wp_attachment_metadata": {meta}}},
		{ID: 50, Type: "nav_menu_item", Status: "publish", MenuOrder: 1, TermTaxIDs: []int64{7}, Meta: extract.Meta{
			"_menu_item_type": {"post_type"}, "_menu_item_object": {"page"}, "_menu_item_object_id": {"20"}, "_menu_item_classes": {`a:1:{i:0;s:0:"";}`}}},
	}
	ds.Comments = []*extract.Comment{{ID: 5, PostID: 10, Author: "Commenter A", AuthorEmail: "user@example.com", AuthorIP: "192.0.2.1",
		DateGMT: "2022-03-02 00:00:00", Content: "Nice <a href=\"https://example.org\">site</a>", Approved: "1"},
		{ID: 6, PostID: 10, Content: "ping", Approved: "1", Type: "pingback", DateGMT: "2022-03-02 00:00:00"}}
	ds.Stats.PostMetaKeys = []extract.Count{{Key: "_yoast_wpseo_meta-robots-noindex", Count: 1}}
	ds.Sort()
	dec := &decisions.File{Items: []*decisions.Item{
		{ID: "shortcode:caption", Kind: "shortcode", Name: "caption", Decision: "convert", Status: decisions.StatusConfirmed},
	}}
	return cfg, ds, dec
}

func run(t *testing.T, cfg *config.Config, ds *extract.Dataset, dec *decisions.File, out string) *transform.Output {
	t.Helper()
	tm, err := theme.LoadManifest("../../../../app/themes/default")
	if err != nil {
		t.Fatal(err)
	}
	set := plugins.Resolve(cfg, []string{"wordpress-seo/wp-seo.php"}, ds.Stats.PostMetaKeys)
	res, err := transform.Run(context.Background(), transform.Options{Cfg: cfg, DS: ds, Plugins: set, Decisions: dec,
		Filter: echoFilter{}, Theme: tm})
	if err != nil {
		t.Fatal(err)
	}
	if err := res.Save(cfg.Work(), cfg.Reports(), cfg.Source, tm.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Write(cfg.Work(), cfg.Path(cfg.UploadsDir), out); err != nil {
		t.Fatal(err)
	}
	return res
}

func strictLines[T any](t *testing.T, path string) []T {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var out []T
	for _, line := range bytes.Split(bytes.TrimSpace(b), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		var v T
		if err := dec.Decode(&v); err != nil {
			t.Fatalf("%s: %v\n%s", path, err, line)
		}
		out = append(out, v)
	}
	return out
}

func TestTransformAndBundle(t *testing.T) {
	cfg, ds, dec := setup(t)
	out := filepath.Join(t.TempDir(), "bundle")
	res := run(t, cfg, ds, dec, out)

	posts := strictLines[importer.PostRec](t, filepath.Join(out, "posts.ndjson"))
	if len(posts) != 3 {
		t.Fatalf("posts %d", len(posts))
	}
	p10 := posts[0]
	if p10.LegacyKey != "wp:post:10" || p10.Status != "published" || !p10.Sticky || *p10.FeaturedMedia != "wp:post:30" ||
		p10.SEO == nil || p10.SEO.RobotsNoindex == nil || !*p10.SEO.RobotsNoindex || *p10.FeedGUID != "http://blog.example.com/?p=10" {
		t.Errorf("post 10: %+v", p10)
	}
	c := string(p10.Content)
	for _, want := range []string{`"href":"/"`, `"type":"figure"`, `"mediaId":"wp:post:30"`, `"size":"medium"`} {
		if !strings.Contains(c, want) {
			t.Errorf("content lacks %s: %s", want, c)
		}
	}
	if d := posts[2]; d.Slug != "draft-idea" || d.Status != "draft" || d.PublishedAt != nil || d.CreatedAt.IsZero() {
		t.Errorf("draft: %+v", d)
	}
	users := strictLines[importer.UserRec](t, filepath.Join(out, "users.ndjson"))
	if len(users) != 1 || users[0].Email != "admin.a@example.com" || users[0].Role != "admin" {
		t.Errorf("users %+v", users)
	}
	media := strictLines[importer.MediaRec](t, filepath.Join(out, "media.ndjson"))
	if len(media) != 2 {
		t.Fatalf("media %+v", media)
	}
	comments := strictLines[importer.CommentRec](t, filepath.Join(out, "comments.ndjson"))
	if len(comments) != 1 || comments[0].Body != "Nice site (https://example.org)" {
		t.Errorf("comments %+v", comments)
	}
	menus := strictLines[importer.MenuRec](t, filepath.Join(out, "menus.ndjson"))
	if len(menus) != 1 || *menus[0].Location != "primary" || menus[0].Items[0].Label != "Home" {
		t.Errorf("menus %+v", menus)
	}
	redirects := strictLines[importer.RedirectRec](t, filepath.Join(out, "redirects.ndjson"))
	got := map[string]string{}
	for _, r := range redirects {
		got[r.Source] = *r.Target
	}
	if got["/2022/03/hi/"] != "/2022/03/hello/" || got["/2022/03/hello/a/"] != "/wp-content/uploads/2022/03/a.png" {
		t.Errorf("redirects %v", got)
	}
	strictLines[importer.AuthorRec](t, filepath.Join(out, "authors.ndjson"))
	strictLines[importer.TermRec](t, filepath.Join(out, "terms.ndjson"))
	strictLines[importer.LegacyRec](t, filepath.Join(out, "legacy.ndjson"))
	b, _ := os.ReadFile(filepath.Join(out, "settings.json"))
	all, _, err := settings.Decode(b)
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	if all.Reading.Home.LegacyKey != "wp:post:20" || all.SEO.Separator != "|" || all.Permalinks.PostPattern != "/{year}/{month}/{slug}/" {
		t.Errorf("settings %+v", all)
	}
	var m importer.Manifest
	extract.ReadJSON(filepath.Join(out, "manifest.json"), &m)
	if m.GeneratedAt.Format("2006") != "2023" || m.Theme != "default" {
		t.Errorf("manifest %+v", m)
	}
	for _, f := range res.Report.Fidelity {
		if f.Class == "lossy" {
			t.Errorf("%s is lossy: %v", f.Key, f.Diffs)
		}
	}
	if len(res.Report.Media.NotImported) != 1 {
		t.Errorf("non-media files must be reported: %+v", res.Report.Media.NotImported)
	}
}

func TestBundleIsDeterministic(t *testing.T) {
	cfg, ds, dec := setup(t)
	a, b := filepath.Join(t.TempDir(), "a"), filepath.Join(t.TempDir(), "b")
	run(t, cfg, ds, dec, a)
	run(t, cfg, ds, dec, b)
	read := func(dir string) map[string][]byte {
		out := map[string][]byte{}
		filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
			if err == nil && !d.IsDir() {
				rel, _ := filepath.Rel(dir, p)
				out[rel], _ = os.ReadFile(p)
			}
			return nil
		})
		return out
	}
	ta, tb := read(a), read(b)
	if len(ta) != len(tb) {
		t.Fatalf("file counts differ")
	}
	for k, v := range ta {
		if !bytes.Equal(v, tb[k]) {
			t.Errorf("%s differs between runs", k)
		}
	}
}

func TestUnconfirmedDecisionsFailClosed(t *testing.T) {
	cfg, ds, dec := setup(t)
	dec.Items[0].Status = decisions.StatusProposed
	_, err := transform.Run(context.Background(), transform.Options{Cfg: cfg, DS: ds, Plugins: plugins.Resolve(cfg, nil, nil),
		Decisions: dec, Filter: echoFilter{}})
	if err == nil || !strings.Contains(err.Error(), "not confirmed") {
		t.Fatalf("want fail-closed error, got %v", err)
	}
}

func TestSlugify(t *testing.T) {
	cases := map[string]string{"Hello World!": "hello-world", "  Café — Culture ": "café-culture", "नमस्ते": "नमस-त", "": ""}
	for in, want := range cases {
		if got := transform.Slugify(in); got != want {
			t.Errorf("%q: %q want %q", in, got, want)
		}
	}
}
