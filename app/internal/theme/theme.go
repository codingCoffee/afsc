package theme

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash/fnv"
	"html/template"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"afsc/internal/content"
	"afsc/internal/settings"
)

// AssetPrefix is the public URL prefix of hashed theme assets.
const AssetPrefix = "/_theme/"

// Backend gives template funcs access to site state.
type Backend interface {
	Settings() *settings.All
	BaseURL() string
	Menu(location string) ([]*MenuItem, error)
}

// StaticBackend serves fixed values; used by `afsc theme check` and the importer.
type StaticBackend struct {
	S     *settings.All
	Base  string
	Menus map[string][]*MenuItem
}

func (b *StaticBackend) Settings() *settings.All { return b.S }
func (b *StaticBackend) BaseURL() string         { return b.Base }
func (b *StaticBackend) Menu(loc string) ([]*MenuItem, error) {
	return b.Menus[loc], nil
}

type Options struct {
	Dev      bool // re-read the theme when its files change
	Backend  Backend
	OnReload func() // called after a successful dev reload
}

type Theme struct {
	dir    string
	opts   Options
	assets *os.Root
	state  atomic.Pointer[state]

	mu      sync.Mutex
	checked time.Time
	stamp   uint64
	loadErr error
	zones   sync.Map
}

type state struct {
	manifest *Manifest
	pages    map[string]*template.Template
	hasBase  bool
	nodes    *template.Template
	hasNode  map[string]bool
	assets   map[string]string // path under assets/ -> content hash
	strings  map[string]string
}

var defaultStrings = map[string]string{
	"pagination.prev":    "« Previous",
	"pagination.next":    "Next »",
	"breadcrumbs.home":   "Home",
	"breadcrumbs.search": "Search results for",
	"breadcrumbs.404":    "Page not found",
	"excerpt.more":       " […]",
	"feed.label":         "Feed",
}

func Load(dir string, opts Options) (*Theme, error) {
	if opts.Backend == nil {
		return nil, errors.New("theme: a backend is required")
	}
	t := &Theme{dir: dir, opts: opts}
	if root, err := os.OpenRoot(filepath.Join(dir, "assets")); err == nil {
		t.assets = root
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	st, err := t.build()
	if err != nil {
		return nil, err
	}
	t.state.Store(st)
	t.stamp, _ = treeStamp(dir)
	t.checked = time.Now()
	return t, nil
}

func (t *Theme) Manifest() *Manifest {
	st, _ := t.current()
	return st.manifest
}

// current returns the last good state and, in dev mode, the error of a failed reload.
func (t *Theme) current() (*state, error) {
	if !t.opts.Dev {
		return t.state.Load(), nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.checked) < 500*time.Millisecond {
		return t.state.Load(), t.loadErr
	}
	t.checked = time.Now()
	stamp, err := treeStamp(t.dir)
	if err != nil || stamp == t.stamp {
		return t.state.Load(), t.loadErr
	}
	t.stamp = stamp
	st, err := t.build()
	if err != nil {
		t.loadErr = err
		return t.state.Load(), err
	}
	t.loadErr = nil
	t.state.Store(st)
	if t.opts.OnReload != nil {
		t.opts.OnReload()
	}
	return st, nil
}

func treeStamp(dir string) (uint64, error) {
	h := fnv.New64a()
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s|%d|%d\n", p, info.Size(), info.ModTime().UnixNano())
		return nil
	})
	return h.Sum64(), err
}

func (t *Theme) build() (*state, error) {
	m, err := LoadManifest(t.dir)
	if err != nil {
		return nil, err
	}
	st := &state{manifest: m, pages: map[string]*template.Template{}, hasNode: map[string]bool{}, strings: maps.Clone(defaultStrings)}
	maps.Copy(st.strings, m.Strings)
	if st.assets, err = t.hashAssets(); err != nil {
		return nil, err
	}
	for _, a := range m.Assets {
		if _, ok := st.assets[a.Path]; !ok {
			return nil, fmt.Errorf("theme.json: asset %q not found under assets/", a.Path)
		}
	}

	tdir := filepath.Join(t.dir, "templates")
	common := template.New("_").Funcs(t.funcMap(st)).Option("missingkey=error")
	if src, err := os.ReadFile(filepath.Join(tdir, "base.html")); err == nil {
		if _, err := common.New("base.html").Parse(string(src)); err != nil {
			return nil, err
		}
		st.hasBase = true
	} else if !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	if err := parseDir(common, tdir, "parts"); err != nil {
		return nil, err
	}

	nodes, err := common.Clone()
	if err != nil {
		return nil, err
	}
	names, err := htmlFiles(filepath.Join(tdir, "nodes"))
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		node := strings.TrimSuffix(name, ".html")
		if spec, ok := content.Nodes[node]; !ok || node == "doc" || spec.Group == "inline" {
			return nil, fmt.Errorf("templates/nodes/%s: %q is not an overridable content node", name, node)
		}
		if err := parseFile(nodes, filepath.Join(tdir, "nodes", name), "nodes/"+name); err != nil {
			return nil, err
		}
		st.hasNode[node] = true
	}
	st.nodes = nodes

	names, err = htmlFiles(tdir)
	if err != nil {
		return nil, err
	}
	for _, name := range names {
		if name == "base.html" {
			continue
		}
		set, err := common.Clone()
		if err != nil {
			return nil, err
		}
		if err := parseFile(set, filepath.Join(tdir, name), name); err != nil {
			return nil, err
		}
		st.pages[name] = set
	}
	if st.pages["index.html"] == nil {
		return nil, errors.New("templates/index.html is required")
	}
	return st, nil
}

func htmlFiles(dir string) ([]string, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []string
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".html") {
			out = append(out, e.Name())
		}
	}
	return out, nil
}

func parseDir(set *template.Template, tdir, sub string) error {
	names, err := htmlFiles(filepath.Join(tdir, sub))
	if err != nil {
		return err
	}
	for _, name := range names {
		if err := parseFile(set, filepath.Join(tdir, sub, name), sub+"/"+name); err != nil {
			return err
		}
	}
	return nil
}

func parseFile(set *template.Template, file, name string) error {
	src, err := os.ReadFile(file)
	if err != nil {
		return err
	}
	if _, err := set.New(name).Parse(string(src)); err != nil {
		return fmt.Errorf("templates/%s: %w", name, err)
	}
	return nil
}

func (t *Theme) hashAssets() (map[string]string, error) {
	out := map[string]string{}
	if t.assets == nil {
		return out, nil
	}
	fsys := t.assets.FS()
	err := fs.WalkDir(fsys, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		f, err := fsys.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err != nil {
			return err
		}
		out[p] = hex.EncodeToString(h.Sum(nil))[:12]
		return nil
	})
	return out, err
}

// Page renders the most specific template for d and returns its name.
func (t *Theme) Page(w io.Writer, d *PageData) (string, error) {
	st, err := t.current()
	if err != nil {
		return "", err
	}
	for _, name := range Candidates(d) {
		if set := st.pages[name]; set != nil {
			return name, st.exec(w, set, name, d)
		}
	}
	return "", fmt.Errorf("no template for %s", d.Kind)
}

func (st *state) exec(w io.Writer, set *template.Template, name string, d *PageData) error {
	if st.hasBase {
		name = "base.html"
	}
	return set.ExecuteTemplate(w, name, d)
}

// Has and Render implement content.Partials with the theme's nodes/*.html.
func (t *Theme) Has(node string) bool {
	st, _ := t.current()
	return st.hasNode[node]
}

func (t *Theme) Render(node string, d content.NodeData) (string, error) {
	st, _ := t.current()
	var b strings.Builder
	err := st.nodes.ExecuteTemplate(&b, "nodes/"+node+".html", d)
	return b.String(), err
}

// HasPartials reports whether the theme overrides any content node.
func (t *Theme) HasPartials() bool {
	st, _ := t.current()
	return len(st.hasNode) > 0
}

func (st *state) assetURL(p string) (string, error) {
	h, ok := st.assets[p]
	if !ok {
		return "", fmt.Errorf("asset %q not found under assets/", p)
	}
	return AssetPrefix + h + "/" + (&url.URL{Path: p}).EscapedPath(), nil
}

// AssetHandler serves AssetPrefix/{hash}/{path}. A stale hash still gets the current file, briefly cached,
// so pages rendered before a deploy keep their styles.
func (t *Theme) AssetHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rest := strings.TrimPrefix(r.URL.Path, AssetPrefix)
		hash, p, ok := strings.Cut(rest, "/")
		st, _ := t.current()
		current, known := st.assets[p]
		if !ok || t.assets == nil || !known || !fs.ValidPath(p) {
			http.NotFound(w, r)
			return
		}
		f, err := t.assets.Open(p)
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer f.Close()
		info, err := f.Stat()
		if err != nil {
			http.Error(w, "asset error", http.StatusInternalServerError)
			return
		}
		if hash == current {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "public, max-age=60")
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		http.ServeContent(w, r, info.Name(), info.ModTime(), f)
	})
}

// Templates lists the page template files, sorted.
func (t *Theme) Templates() []string {
	st, _ := t.current()
	return slices.Sorted(maps.Keys(st.pages))
}

// Nodes lists the overridden content nodes, sorted.
func (t *Theme) Nodes() []string {
	st, _ := t.current()
	return slices.Sorted(maps.Keys(st.hasNode))
}
