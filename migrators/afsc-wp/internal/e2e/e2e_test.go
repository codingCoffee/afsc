// Package e2e runs the whole pipeline on the synthetic fixture: MySQL → extract → transform → bundle → AFSC's
// importer (twice) → AFSC's rendered content_html → content-fidelity classes.
package e2e

import (
	"bytes"
	"context"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"

	"afsc/internal/importer"
	"afsc/internal/theme"

	"afsc/migrators/afsc-wp/internal/bundle"
	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/decisions"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/fidelity"
	"afsc/migrators/afsc-wp/internal/filter"
	"afsc/migrators/afsc-wp/internal/inventory"
	"afsc/migrators/afsc-wp/internal/plugins"
	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/transform"
	"afsc/migrators/afsc-wp/internal/uploads"
	"afsc/migrators/afsc-wp/internal/wpdb"
)

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

func pipeline(t *testing.T, cfg *config.Config, ds *extract.Dataset, set *plugins.Set, dec *decisions.File, tm *theme.Manifest, out string) *transform.Output {
	t.Helper()
	res, err := transform.Run(context.Background(), transform.Options{Cfg: cfg, DS: ds, Plugins: set, Decisions: dec,
		Filter: &filter.Replay{Path: cfg.Path(cfg.Filter.ReplayFile)}, Theme: tm})
	if err != nil {
		t.Fatalf("transform: %v", err)
	}
	if err := res.Save(cfg.Work(), cfg.Reports(), cfg.Source, tm.Name); err != nil {
		t.Fatal(err)
	}
	if _, err := bundle.Write(cfg.Work(), cfg.Path(cfg.UploadsDir), out); err != nil {
		t.Fatalf("bundle: %v", err)
	}
	return res
}

func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	out := map[string][]byte{}
	filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			b, _ := os.ReadFile(p)
			rel, _ := filepath.Rel(dir, p)
			out[rel] = b
		}
		return nil
	})
	return out
}

func TestFixtureEndToEnd(t *testing.T) {
	dbURL := os.Getenv("AFSC_TEST_DATABASE_URL")
	if os.Getenv("AFSC_WP_TEST_MYSQL_DSN") == "" || dbURL == "" {
		t.Skip("needs AFSC_WP_TEST_MYSQL_DSN (fixture loaded) and AFSC_TEST_DATABASE_URL (migrated AFSC test database)")
	}
	ctx := context.Background()
	cfg, err := config.Load("../../testdata/afsc-wp.fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	cfg.WorkDir, cfg.ReportsDir = filepath.Join(tmp, "work"), filepath.Join(tmp, "reports")

	ds, set, err := wpdb.Load(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if ds.Files, err = uploads.Scan(cfg.Path(cfg.UploadsDir)); err != nil {
		t.Fatal(err)
	}
	ds.Sort()
	dec, err := decisions.Load(cfg.Path(cfg.DecisionsFile))
	if err != nil {
		t.Fatal(err)
	}
	inv, err := inventory.Run(cfg, ds, set, []string{cfg.Path("wp-code/acme")})
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range inv.Decisions.Items {
		if dec.Get(it.Kind, it.Name) == nil {
			t.Errorf("decisions.fixture.json lacks %s (proposed %s)", it.ID, it.Proposed)
		}
	}
	tm, err := theme.LoadManifest(cfg.Path(cfg.ThemeDir))
	if err != nil {
		t.Fatal(err)
	}

	b1, b2 := filepath.Join(tmp, "bundle1"), filepath.Join(tmp, "bundle2")
	res := pipeline(t, cfg, ds, set, dec, tm, b1)
	pipeline(t, cfg, ds, set, dec, tm, b2)
	t1, t2 := readTree(t, b1), readTree(t, b2)
	if len(t1) != len(t2) {
		t.Fatalf("bundles differ in file count: %d vs %d", len(t1), len(t2))
	}
	for name, b := range t1 {
		if !bytes.Equal(b, t2[name]) {
			t.Errorf("bundle is not deterministic: %s differs", name)
		}
	}

	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	store := &memStorage{objects: map[string][]byte{}}
	opts := importer.Options{BundleDir: b1, Theme: tm, Pool: pool, Storage: store, Logf: t.Logf}
	first, err := importer.Run(ctx, opts)
	if err != nil {
		t.Fatalf("first import: %v\nerrors: %+v", err, first.Errors)
	}
	second, err := importer.Run(ctx, opts)
	if err != nil {
		t.Fatalf("second import: %v", err)
	}
	for entity, c := range second.Counts {
		if c.Created != 0 || c.Updated != 0 || c.Deleted != 0 {
			t.Errorf("second import changed %s: %+v", entity, *c)
		}
	}

	media, err := extract.ReadNDJSON[*records.Media](filepath.Join(b1, "media.ndjson"))
	if err != nil {
		t.Fatal(err)
	}
	bySHA8, byPath := map[string]string{}, map[string]string{}
	for _, m := range media {
		bySHA8[m.SHA256[:8]] = m.LegacyKey
		for _, lp := range m.LegacyPaths {
			byPath[lp.Path] = m.LegacyKey
		}
	}
	aliases := res.Report.Media.Aliases
	hosts := map[string]bool{}
	for _, h := range cfg.SiteHosts("blog.example.com") {
		hosts[h] = true
	}
	mPath := regexp.MustCompile(`/m/\d+/([0-9a-f]{8})/`)
	photon := regexp.MustCompile(`^https?://i[0-3]\.wp\.com/[^/]+(/.*)$`)
	wpImg := regexp.MustCompile(`^wp-image-(\d+)$`)
	resolve := func(u string, cls []string) (string, bool) {
		for _, c := range cls {
			if s := wpImg.FindStringSubmatch(c); s != nil {
				k := cfg.Source + ":post:" + s[1]
				if a, ok := aliases[k]; ok {
					k = a
				}
				return k, true
			}
		}
		if s := mPath.FindStringSubmatch(u); s != nil {
			k, ok := bySHA8[s[1]]
			return k, ok
		}
		if s := photon.FindStringSubmatch(u); s != nil {
			u = s[1]
		}
		u = strings.TrimPrefix(strings.TrimPrefix(u, "https://blog.example.com"), "http://blog.example.com")
		if i := strings.IndexByte(u, '?'); i >= 0 {
			u = u[:i]
		}
		k, ok := byPath[u]
		return k, ok
	}
	link := func(h string) string {
		if to, ok := res.LinkMap[strings.TrimSpace(h)]; ok {
			return to
		}
		return h
	}
	wpSide := fidelity.Side{Hosts: hosts, Media: resolve, Link: link}
	afSide := fidelity.Side{Hosts: hosts, Media: func(u string, _ []string) (string, bool) { return resolve(u, nil) }}

	var want []string
	if err := extract.ReadJSON("../../testdata/expected-fallback.json", &want); err != nil {
		t.Fatal(err)
	}
	raw := map[string]bool{}
	for _, p := range res.Paths {
		raw[p.Key] = p.RawHTML
	}
	rows, err := pool.Query(ctx, `SELECT legacy_key, content_html FROM posts WHERE legacy_key LIKE $1 AND status = 'published' ORDER BY legacy_key`, cfg.Source+":post:%")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	checked := 0
	for rows.Next() {
		var key, h string
		if err := rows.Scan(&key, &h); err != nil {
			t.Fatal(err)
		}
		id := strings.TrimPrefix(key, cfg.Source+":post:")
		wpHTML, ok := res.Reference[id]
		if !ok {
			t.Errorf("%s: no WordPress reference", key)
			continue
		}
		checked++
		class, diffs := fidelity.Compare(fidelity.Analyze(wpHTML, wpSide), fidelity.Analyze(h, afSide), raw[key])
		switch {
		case slices.Contains(want, key) && class != fidelity.Fallback:
			t.Errorf("%s: want fallback, got %s %v", key, class, diffs)
		case !slices.Contains(want, key) && class != fidelity.Exact && class != fidelity.Minor:
			t.Errorf("%s: want exact or minor, got %s %v\nwp:   %s\nafsc: %s", key, class, diffs, wpHTML, h)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if checked < 8 {
		t.Errorf("only %d published posts checked", checked)
	}
	for _, k := range want {
		if _, err := strconv.Atoi(strings.TrimPrefix(k, cfg.Source+":post:")); err != nil {
			t.Errorf("bad key in expected-fallback.json: %s", k)
		}
	}
}
