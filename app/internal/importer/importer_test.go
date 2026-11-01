package importer

import (
  "context"
  "io"
  "os"
  "path/filepath"
  "strings"
  "sync"
  "testing"

  "github.com/jackc/pgx/v5/pgxpool"

  "afsc/internal/theme"
)

const sampleDir = "../../testdata/bundle-sample"

func loadTheme(t *testing.T) *theme.Manifest {
  t.Helper()
  tm, err := theme.LoadManifest("../../themes/default")
  if err != nil {
    t.Fatal(err)
  }
  return tm
}

func validateDir(t *testing.T, dir string) *Report {
  t.Helper()
  rep := newReport(true)
  m, err := readManifest(dir)
  if err != nil {
    t.Fatal(err)
  }
  checkManifest(m, rep)
  tm := loadTheme(t)
  validate(dir, m, tm.AllowedClasses(), map[string]bool{"primary": true, "footer": true}, rep)
  return rep
}

func TestSampleBundleIsValid(t *testing.T) {
  rep := validateDir(t, sampleDir)
  if len(rep.Errors) > 0 {
    t.Fatalf("sample bundle has errors: %+v", rep.Errors)
  }
  wantWarnings := []string{"same file as wp:post:456", `class "evil"`}
  for _, w := range wantWarnings {
    found := false
    for _, got := range rep.Warnings {
      found = found || strings.Contains(got.Message, w)
    }
    if !found {
      t.Errorf("expected a warning containing %q, got %+v", w, rep.Warnings)
    }
  }
  if len(rep.Removals) == 0 {
    t.Errorf("expected sanitizer removals for the rawHtml block")
  }
}

// corrupt copies the sample bundle and replaces old with new in one of its files.
func corrupt(t *testing.T, file, old, new string) string {
  t.Helper()
  dir := t.TempDir()
  if err := os.CopyFS(dir, os.DirFS(sampleDir)); err != nil {
    t.Fatal(err)
  }
  path := filepath.Join(dir, file)
  b, err := os.ReadFile(path)
  if err != nil {
    t.Fatal(err)
  }
  if !strings.Contains(string(b), old) {
    t.Fatalf("%s does not contain %q", file, old)
  }
  if err := os.WriteFile(path, []byte(strings.Replace(string(b), old, new, 1)), 0o644); err != nil {
    t.Fatal(err)
  }
  return dir
}

func TestValidationErrors(t *testing.T) {
  cases := []struct {
    name, file, old, new, key, field string
  }{
    {"broken reference", "posts.ndjson", `"featured_media":"wp:post:456"`, `"featured_media":"wp:post:999999"`, "wp:post:123", "featured_media"},
    {"duplicate legacy path", "media.ndjson", `/wp-content/uploads/2020/05/portrait.png`, `/wp-content/uploads/2020/05/chart.png`, "wp:post:457", "legacy_paths[0]"},
    {"bad node", "posts.ndjson", `"type":"horizontalRule"`, `"type":"marquee"`, "wp:post:123", "content.content[6]"},
    {"bad media ref in content", "posts.ndjson", `"mediaId":"wp:post:460"`, `"mediaId":"wp:post:1"`, "wp:post:123", "content.content[18].attrs.mediaId"},
    {"path traversal", "media.ndjson", `"file":"files/`, `"file":"files/../../`, "wp:post:456", "file"},
    {"wrong source prefix", "users.ndjson", `"legacy_key":"wp:user:3"`, `"legacy_key":"other:user:3"`, "other:user:3", "legacy_key"},
    {"unknown field", "terms.ndjson", `"description":"Market news"`, `"descripton":"Market news"`, "", ""},
    {"slug clash", "posts.ndjson", `"slug":"team"`, `"slug":"intro"`, "wp:post:5", "slug"},
    {"unknown settings key", "settings.json", `"site"`, `"sight"`, "", "settings"},
    {"parent cycle", "terms.ndjson", `"slug":"markets","name":"Markets","description":"Market news","parent":null`, `"slug":"markets","name":"Markets","description":"Market news","parent":"wp:term:6"`, "*", "parent"},
  }
  for _, c := range cases {
    t.Run(c.name, func(t *testing.T) {
      rep := validateDir(t, corrupt(t, c.file, c.old, c.new))
      for _, e := range rep.Errors {
        if c.key == "" && c.field == "" {
          return
        }
        if (c.key == "*" || e.LegacyKey == c.key) && strings.HasPrefix(e.Field, c.field) {
          return
        }
      }
      t.Errorf("want an error for %s %s, got %+v", c.key, c.field, rep.Errors)
    })
  }
}

func TestCorruptedFileHash(t *testing.T) {
  dir := corrupt(t, "media.ndjson", `"filename":"pixel.gif"`, `"filename":"pixel.gif"`)
  matches, _ := filepath.Glob(filepath.Join(dir, "files", "69", "*.gif"))
  if len(matches) != 1 {
    t.Fatalf("gif fixture not found")
  }
  if err := os.WriteFile(matches[0], []byte("GIF89a tampered"), 0o644); err != nil {
    t.Fatal(err)
  }
  rep := validateDir(t, dir)
  for _, e := range rep.Errors {
    if e.LegacyKey == "wp:post:462" && e.Field == "sha256" {
      return
    }
  }
  t.Errorf("want a sha256 error, got %+v", rep.Errors)
}

type memStorage struct {
  mu      sync.Mutex
  objects map[string][]byte
}

func (m *memStorage) Put(_ context.Context, key string, body io.ReadSeeker, _ int64, _ string) error {
  b, err := io.ReadAll(body)
  if err != nil {
    return err
  }
  m.mu.Lock()
  defer m.mu.Unlock()
  m.objects[key] = b
  return nil
}

var snapshotTables = []string{
  "users", "media", "media_legacy_paths", "authors", "terms", "posts", "post_authors", "post_terms", "seo",
  "post_videos", "comments", "menus", "menu_items", "menu_locations", "redirects", "settings", "legacy_records",
}

// snapshot hashes every row of every imported table, so two snapshots are equal only if nothing changed.
func snapshot(t *testing.T, pool *pgxpool.Pool) map[string]string {
  t.Helper()
  out := map[string]string{}
  for _, table := range snapshotTables {
    var sum *string
    q := "SELECT md5(string_agg(t::text, '|' ORDER BY t::text)) FROM " + table + " t"
    if err := pool.QueryRow(context.Background(), q).Scan(&sum); err != nil {
      t.Fatalf("%s: %v", table, err)
    }
    if sum != nil {
      out[table] = *sum
    }
  }
  return out
}

func TestImportTwiceIsIdempotent(t *testing.T) {
  url := os.Getenv("AFSC_TEST_DATABASE_URL")
  if url == "" {
    t.Skip("AFSC_TEST_DATABASE_URL not set")
  }
  ctx := context.Background()
  pool, err := pgxpool.New(ctx, url)
  if err != nil {
    t.Fatal(err)
  }
  defer pool.Close()

  store := &memStorage{objects: map[string][]byte{}}
  opts := Options{BundleDir: sampleDir, Theme: loadTheme(t), Pool: pool, Storage: store, Logf: t.Logf}

  before := snapshot(t, pool)
  dry := opts
  dry.DryRun = true
  if _, err := Run(ctx, dry); err != nil {
    t.Fatalf("dry run: %v", err)
  }
  if after := snapshot(t, pool); !equalMaps(before, after) {
    t.Fatalf("dry run changed the database")
  }

  first, err := Run(ctx, opts)
  if err != nil {
    t.Fatalf("first import: %v (errors %+v)", err, first.Errors)
  }
  s1 := snapshot(t, pool)

  second, err := Run(ctx, opts)
  if err != nil {
    t.Fatalf("second import: %v", err)
  }
  for entity, c := range second.Counts {
    if c.Created != 0 || c.Updated != 0 || c.Deleted != 0 {
      t.Errorf("second import changed %s: %+v", entity, *c)
    }
  }
  if s2 := snapshot(t, pool); !equalMaps(s1, s2) {
    for table := range s1 {
      if s1[table] != s2[table] {
        t.Errorf("table %s changed on re-import", table)
      }
    }
  }
  if len(first.Invites) == 0 {
    t.Errorf("expected invites for imported users")
  }
}

func equalMaps(a, b map[string]string) bool {
  if len(a) != len(b) {
    return false
  }
  for k, v := range a {
    if b[k] != v {
      return false
    }
  }
  return true
}
