// Package verify checks an imported AFSC (local or staging, never production) against the WordPress baseline
// (migration plan §8). Each check writes a report under reports/ and returns whether its gate passed.
package verify

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/records"
)

var Checks = []string{"urls", "seo", "content", "counts", "visual", "media", "feeds", "sitemaps"}

type Options struct {
	Source          string // bundle source prefix ("wp")
	Target          string // AFSC base URL
	WPBase          string // the WordPress site's base URL (for host swaps)
	Hosts           map[string]bool
	ContentSelector string
	WorkDir         string
	BaselineDir     string
	ReportsDir      string
	BundleDir       string
	ImportReport    string
	ScreenshotsDir  string
	Threshold       float64
	TopURLsFile     string
	Timeout         time.Duration
	Client          *http.Client
}

type Result struct {
	Check   string `json:"check"`
	Pass    bool   `json:"pass"`
	Summary string `json:"summary"`
}

type runner struct {
	o      Options
	target *url.URL
	client *http.Client
	media  *mediaMap
	images map[string]bool // media URLs seen on rendered AFSC pages
}

// Run executes the selected checks in a stable order.
func Run(ctx context.Context, o Options, checks []string) ([]Result, error) {
	t, err := url.Parse(o.Target)
	if err != nil || t.Host == "" {
		return nil, fmt.Errorf("verify target %q is not an absolute URL", o.Target)
	}
	r := &runner{o: o, target: t, images: map[string]bool{}}
	c := o.Client
	if c == nil {
		c = &http.Client{}
	}
	cl := *c
	cl.Timeout = o.Timeout
	cl.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	r.client = &cl
	if r.media, err = loadMediaMap(o.BundleDir, o.ReportsDir); err != nil {
		return nil, err
	}
	fns := map[string]func(context.Context) (Result, error){
		"urls": r.urls, "seo": r.seo, "content": r.content, "counts": r.counts, "visual": r.visual,
		"media": r.mediaCheck, "feeds": r.feeds, "sitemaps": r.sitemaps,
	}
	var out []Result
	for _, name := range Checks {
		if !slices.Contains(checks, name) {
			continue
		}
		res, err := fns[name](ctx)
		if err != nil {
			return out, fmt.Errorf("%s: %w", name, err)
		}
		res.Check = name
		out = append(out, res)
	}
	return out, nil
}

type response struct {
	Status   int
	Location string
	Header   http.Header
	Body     []byte
}

// fetch does one request without following redirects.
func (r *runner) fetch(ctx context.Context, method, pathOrURL string) (*response, error) {
	u := r.abs(pathOrURL)
	req, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "afsc-wp-verify/1.0")
	resp, err := r.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 64<<20))
	if err != nil {
		return nil, err
	}
	return &response{Status: resp.StatusCode, Location: resp.Header.Get("Location"), Header: resp.Header, Body: body}, nil
}

// abs resolves a path, or a URL on any site host, against the AFSC target.
func (r *runner) abs(p string) string {
	u, err := url.Parse(p)
	if err != nil {
		return r.o.Target + p
	}
	return r.target.ResolveReference(&url.URL{Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}).String()
}

// relPath reduces a URL on the WordPress site or the AFSC target to path?query.
func (r *runner) relPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return raw
	}
	if u.Host != "" && !r.o.Hosts[strings.ToLower(u.Hostname())] && !strings.EqualFold(u.Host, r.target.Host) {
		return raw
	}
	p := u.EscapedPath()
	if dec, err := url.PathUnescape(p); err == nil {
		p = dec
	}
	if p == "" {
		p = "/"
	}
	if u.RawQuery != "" {
		p += "?" + u.RawQuery
	}
	return p
}

// mediaMap resolves media references from both sides to bundle legacy keys.
type mediaMap struct {
	byPath  map[string]string // legacy path → key
	bySHA8  map[string]string // first 8 hex of sha256 (AFSC /m/{id}/{v}/ URLs) → key
	aliases map[string]string
	media   []*records.Media
}

func loadMediaMap(bundleDir, reportsDir string) (*mediaMap, error) {
	m := &mediaMap{byPath: map[string]string{}, bySHA8: map[string]string{}, aliases: map[string]string{}}
	media, err := extract.ReadNDJSON[*records.Media](filepath.Join(bundleDir, "media.ndjson"))
	if err != nil {
		return nil, err
	}
	m.media = media
	for _, x := range media {
		m.bySHA8[x.SHA256[:8]] = x.LegacyKey
		for _, lp := range x.LegacyPaths {
			m.byPath[lp.Path] = x.LegacyKey
		}
	}
	var rep struct {
		Media struct {
			Aliases map[string]string `json:"aliases"`
		} `json:"media"`
	}
	if err := extract.ReadJSON(filepath.Join(reportsDir, "transform.json"), &rep); err == nil {
		m.aliases = rep.Media.Aliases
	}
	return m, nil
}

var (
	afscMediaRe = regexp.MustCompile(`/m/\d+/([0-9a-f]{8})/`)
	wpImageRe   = regexp.MustCompile(`^wp-image-(\d+)$`)
	photonRe    = regexp.MustCompile(`^https?://i[0-3]\.wp\.com/[^/]+(/.*)$`)
	sizedRe     = regexp.MustCompile(`^(.*)-\d+x\d+(\.[A-Za-z0-9]+)$`)
)

func (m *mediaMap) canon(k string) string {
	if a, ok := m.aliases[k]; ok {
		return a
	}
	return k
}

// wp resolves WordPress markup: wp-image-N, then the legacy path index (any host, Photon, size variants).
func (m *mediaMap) wp(source string) func(string, []string) (string, bool) {
	return func(u string, cls []string) (string, bool) {
		for _, c := range cls {
			if s := wpImageRe.FindStringSubmatch(c); s != nil {
				return m.canon(source + ":post:" + s[1]), true
			}
		}
		if s := photonRe.FindStringSubmatch(u); s != nil {
			u = s[1]
		}
		pu, err := url.Parse(u)
		if err != nil {
			return "", false
		}
		p := pu.Path
		if dec, err := url.PathUnescape(p); err == nil {
			p = dec
		}
		if k, ok := m.byPath[p]; ok {
			return k, true
		}
		if s := sizedRe.FindStringSubmatch(p); s != nil {
			if k, ok := m.byPath[s[1]+s[2]]; ok {
				return k, true
			}
		}
		return "", false
	}
}

// afsc resolves AFSC markup: /m/{id}/{v}/ URLs by content hash, legacy paths as a fallback.
func (m *mediaMap) afsc(u string, _ []string) (string, bool) {
	if s := afscMediaRe.FindStringSubmatch(u); s != nil {
		k, ok := m.bySHA8[s[1]]
		return k, ok
	}
	return m.wp("")(u, nil)
}

func pct(n, d int) string {
	if d == 0 {
		return "0"
	}
	return strconv.FormatFloat(100*float64(n)/float64(d), 'f', 1, 64) + "%"
}
