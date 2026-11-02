package verify

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/redirects"
	"afsc/migrators/afsc-wp/internal/report"
	"afsc/migrators/afsc-wp/internal/transform"
	"afsc/migrators/afsc-wp/internal/uploads"
)

// mediaCheck GETs every legacy upload path (200, no redirect, extension's Content-Type, original bytes or
// resize dimensions) and HEADs every media URL seen on rendered pages (§8.6).
func (r *runner) mediaCheck(ctx context.Context) (Result, error) {
	var bad [][]string
	n := 0
	for _, m := range r.media.media {
		for _, lp := range m.LegacyPaths {
			n++
			got, err := r.fetch(ctx, "GET", lp.Path)
			if err != nil {
				bad = append(bad, []string{lp.Path, err.Error()})
				continue
			}
			want := uploads.MediaTypes[strings.ToLower(path.Ext(lp.Path))]
			ct, _, _ := mime.ParseMediaType(got.Header.Get("Content-Type"))
			switch {
			case got.Status != 200:
				bad = append(bad, []string{lp.Path, fmt.Sprintf("status %d (no redirect allowed)", got.Status)})
			case want != "" && ct != want:
				bad = append(bad, []string{lp.Path, "Content-Type " + ct + ", want " + want})
			case lp.Variant == "orig":
				h := sha256.Sum256(got.Body)
				if hex.EncodeToString(h[:]) != m.SHA256 {
					bad = append(bad, []string{lp.Path, "bytes differ from the original"})
				}
			case lp.Variant == "resize" && lp.Width != nil:
				w, h := uploads.Dimensions(bytes.NewReader(got.Body))
				if w != *lp.Width || h != *lp.Height {
					bad = append(bad, []string{lp.Path, fmt.Sprintf("%dx%d, want %dx%d", w, h, *lp.Width, *lp.Height)})
				}
			}
		}
	}
	seen := uniqueSorted(r.images)
	for _, img := range seen {
		got, err := r.fetch(ctx, "HEAD", img)
		if err != nil || got.Status != 200 {
			bad = append(bad, []string{img, "rendered page image: " + statusOf(got, err)})
		}
	}
	d := report.New("Media")
	d.P("%d legacy upload paths and %d image URLs from rendered pages checked; %d problems.", n, len(seen), len(bad))
	if len(seen) == 0 {
		d.P("No rendered-page images were collected; run the seo or content check in the same verify run.")
	}
	d.Table([]string{"URL", "Problem"}, bad)
	return Result{Pass: len(bad) == 0, Summary: fmt.Sprintf("%d problems", len(bad))}, d.Write(filepath.Join(r.o.ReportsDir, "media.md"))
}

type rss struct {
	XMLName xml.Name
	Version string `xml:"version,attr"`
	Channel struct {
		Title       string `xml:"title"`
		Link        string `xml:"link"`
		Description string `xml:"description"`
		Items       []struct {
			Title       string `xml:"title"`
			Link        string `xml:"link"`
			GUID        string `xml:"guid"`
			Description string `xml:"description"`
		} `xml:"item"`
	} `xml:"channel"`
}

// feeds validates /feed/ as RSS 2.0 and checks guids equal the baseline feed's for the same items.
func (r *runner) feeds(ctx context.Context) (Result, error) {
	got, err := r.fetch(ctx, "GET", "/feed/")
	if err != nil {
		return Result{}, err
	}
	var problems []string
	var af rss
	if got.Status != 200 {
		problems = append(problems, fmt.Sprintf("/feed/ returns %d", got.Status))
	} else if err := xml.Unmarshal(got.Body, &af); err != nil {
		problems = append(problems, "not XML: "+err.Error())
	}
	if af.XMLName.Local != "rss" || af.Version != "2.0" {
		problems = append(problems, "root is not <rss version=\"2.0\">")
	}
	if af.Channel.Title == "" || af.Channel.Link == "" {
		problems = append(problems, "channel title or link missing")
	}
	for i, it := range af.Channel.Items {
		if it.Title == "" && it.Description == "" {
			problems = append(problems, fmt.Sprintf("item %d has neither title nor description", i))
		}
		if it.GUID == "" {
			problems = append(problems, fmt.Sprintf("item %d has no guid", i))
		}
	}
	matched := 0
	if b, err := os.ReadFile(filepath.Join(r.o.BaselineDir, "feed.xml")); err == nil {
		var wp rss
		if xml.Unmarshal(b, &wp) == nil {
			guid := map[string]string{}
			for _, it := range af.Channel.Items {
				guid[redirects.Canonical(r.relPath(it.Link), "keep")] = it.GUID
			}
			for _, it := range wp.Channel.Items {
				p := redirects.Canonical(r.relPath(it.Link), "keep")
				g, ok := guid[p]
				if !ok {
					continue
				}
				matched++
				if g != it.GUID {
					problems = append(problems, fmt.Sprintf("%s: guid %q, WordPress had %q", p, g, it.GUID))
				}
			}
		}
	} else {
		problems = append(problems, "no baseline/feed.xml to compare guids with (run crawl)")
	}
	d := report.New("Feeds")
	d.P("%d items in the AFSC feed; %d matched baseline items by link.", len(af.Channel.Items), matched)
	d.List(problems)
	return Result{Pass: len(problems) == 0, Summary: fmt.Sprintf("%d problems", len(problems))}, d.Write(filepath.Join(r.o.ReportsDir, "feeds.md"))
}

type urlset struct {
	Sitemaps []struct {
		Loc string `xml:"loc"`
	} `xml:"sitemap"`
	URLs []struct {
		Loc string `xml:"loc"`
	} `xml:"url"`
}

// sitemaps compares the AFSC sitemap URL set with the baseline's, minus intentional removals (§8.6).
func (r *runner) sitemaps(ctx context.Context) (Result, error) {
	wp := map[string]bool{}
	files, _ := filepath.Glob(filepath.Join(r.o.BaselineDir, "sitemaps", "*.xml"))
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var u urlset
		if xml.Unmarshal(b, &u) == nil {
			for _, x := range u.URLs {
				wp[redirects.Canonical(r.relPath(strings.TrimSpace(x.Loc)), "keep")] = true
			}
		}
	}
	af := map[string]bool{}
	queue := []string{"/sitemap_index.xml"}
	done := map[string]bool{}
	for len(queue) > 0 {
		p := queue[0]
		queue = queue[1:]
		if done[p] {
			continue
		}
		done[p] = true
		got, err := r.fetch(ctx, "GET", p)
		if err != nil || got.Status != 200 {
			continue
		}
		var u urlset
		if xml.Unmarshal(got.Body, &u) != nil {
			continue
		}
		for _, s := range u.Sitemaps {
			queue = append(queue, r.relPath(strings.TrimSpace(s.Loc)))
		}
		for _, x := range u.URLs {
			af[redirects.Canonical(r.relPath(strings.TrimSpace(x.Loc)), "keep")] = true
		}
	}
	removed := map[string]bool{}
	if m, err := extract.ReadNDJSON[transform.URLExpect](filepath.Join(r.o.WorkDir, "url-map.ndjson")); err == nil {
		for _, e := range m {
			if e.Expect != "200" {
				removed[redirects.Canonical(r.relPath(e.URL), "keep")] = true
			}
		}
	}
	var missing, extra []string
	for p := range wp {
		if !af[p] && !removed[p] {
			missing = append(missing, p)
		}
	}
	for p := range af {
		if !wp[p] {
			extra = append(extra, p)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	d := report.New("Sitemaps")
	d.P("WordPress sitemaps: %d URLs (%d files in baseline/sitemaps). AFSC sitemaps: %d URLs.", len(wp), len(files), len(af))
	d.H2("In WordPress, missing from AFSC (not intentionally removed)").List(missing)
	d.H2("In AFSC, not in WordPress").List(extra)
	return Result{Pass: len(missing) == 0 && len(extra) == 0 && len(files) > 0,
		Summary: fmt.Sprintf("%d missing, %d extra", len(missing), len(extra))}, d.Write(filepath.Join(r.o.ReportsDir, "sitemaps.md"))
}

// visual diffs screenshot pairs captured elsewhere: <dir>/wp/<name>.png against <dir>/afsc/<name>.png (§8.5).
func (r *runner) visual(_ context.Context) (Result, error) {
	out := filepath.Join(r.o.ReportsDir, "visual")
	if err := os.MkdirAll(out, 0o700); err != nil {
		return Result{}, err
	}
	if err := r.sampleList(out); err != nil {
		return Result{}, err
	}
	if r.o.ScreenshotsDir == "" {
		return Result{Pass: false, Summary: "no screenshots_dir; wrote reports/visual/sample-urls.txt for capture"}, nil
	}
	wpShots, _ := filepath.Glob(filepath.Join(r.o.ScreenshotsDir, "wp", "*.png"))
	sort.Strings(wpShots)
	var rows [][]string
	var cards strings.Builder
	pass := len(wpShots) > 0
	for _, w := range wpShots {
		name := filepath.Base(w)
		a := filepath.Join(r.o.ScreenshotsDir, "afsc", name)
		ratio, diffImg, err := DiffFiles(w, a)
		if err != nil {
			rows = append(rows, []string{name, "error", err.Error()})
			pass = false
			continue
		}
		dp := filepath.Join(out, strings.TrimSuffix(name, ".png")+"-diff.png")
		if f, err := os.Create(dp); err == nil {
			png.Encode(f, diffImg)
			f.Close()
		}
		ok := ratio <= r.o.Threshold
		pass = pass && ok
		status := "ok"
		if !ok {
			status = "over threshold"
		}
		rows = append(rows, []string{name, fmt.Sprintf("%.3f%%", ratio*100), status})
		relW, _ := filepath.Rel(out, w)
		relA, _ := filepath.Rel(out, a)
		fmt.Fprintf(&cards, `<section><h2>%s — %.3f%%</h2><div class="row"><img src="%s" alt="WordPress"><img src="%s" alt="AFSC"><img src="%s" alt="diff"></div></section>`,
			name, ratio*100, relW, relA, filepath.Base(dp))
	}
	index := `<!doctype html><meta charset="utf-8"><title>Visual parity</title><style>body{font:14px system-ui;margin:16px}` +
		`.row{display:grid;grid-template-columns:repeat(3,1fr);gap:8px}img{width:100%;border:1px solid #ccc}</style>` +
		`<h1>Visual parity</h1><p>WordPress · AFSC · diff (red pixels differ)</p>` + cards.String()
	if err := os.WriteFile(filepath.Join(out, "index.html"), []byte(index), 0o600); err != nil {
		return Result{}, err
	}
	d := report.New("Visual parity")
	d.P("Threshold %.2f%% of pixels. Side-by-side pages: reports/visual/index.html.", r.o.Threshold*100)
	d.Table([]string{"Screenshot", "Different pixels", "Status"}, rows)
	return Result{Pass: pass, Summary: fmt.Sprintf("%d pairs", len(rows))}, d.Write(filepath.Join(out, "visual.md"))
}

// sampleList writes the visual sample set: top URLs plus three of each route kind, for an external capture tool.
func (r *runner) sampleList(out string) error {
	var urls []string
	seen := map[string]bool{}
	add := func(p string) {
		if !seen[p] {
			seen[p] = true
			urls = append(urls, p)
		}
	}
	if r.o.TopURLsFile != "" {
		if b, err := os.ReadFile(r.o.TopURLsFile); err == nil {
			for i, l := range strings.Split(string(b), "\n") {
				if l = strings.TrimSpace(strings.Split(l, ",")[0]); i < 150 && (strings.HasPrefix(l, "/") || strings.HasPrefix(l, "http")) {
					add(r.relPath(l))
				}
			}
		}
	}
	perKind := map[string]int{}
	if m, err := extract.ReadNDJSON[transform.URLExpect](filepath.Join(r.o.WorkDir, "url-map.ndjson")); err == nil {
		for _, e := range m {
			k := routeKind(e.Path)
			if e.Expect == "200" && perKind[k] < 3 {
				perKind[k]++
				add(e.Path)
			}
		}
	}
	add("/search/?q=the")
	add("/this-page-does-not-exist-afsc-wp/")
	var b strings.Builder
	b.WriteString("# One path per line. Capture each at 375, 768 and 1440 px, full page, on WordPress (wp/) and AFSC (afsc/),\n")
	b.WriteString("# named <slug>-<width>.png where slug is the path with / replaced by _ (\"_\" for the home page).\n")
	for _, u := range urls {
		b.WriteString(u + "\n")
	}
	return os.WriteFile(filepath.Join(out, "sample-urls.txt"), []byte(b.String()), 0o600)
}

func decodePNG(p string) (image.Image, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return png.Decode(f)
}

// DiffFiles compares two screenshots with a YIQ perceptual distance (as pixelmatch does). Pixels outside the
// smaller image count as different. It returns the share of differing pixels and a diff image.
func DiffFiles(a, b string) (float64, *image.RGBA, error) {
	ia, err := decodePNG(a)
	if err != nil {
		return 0, nil, err
	}
	ib, err := decodePNG(b)
	if err != nil {
		return 0, nil, err
	}
	return Diff(ia, ib)
}

func Diff(a, b image.Image) (float64, *image.RGBA, error) {
	ra, rb := a.Bounds(), b.Bounds()
	w, h := max(ra.Dx(), rb.Dx()), max(ra.Dy(), rb.Dy())
	out := image.NewRGBA(image.Rect(0, 0, w, h))
	if w == 0 || h == 0 {
		return 0, out, nil
	}
	const maxDelta = 35215.0
	threshold := 0.1 * 0.1 * maxDelta
	diff := 0
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			in := x < ra.Dx() && y < ra.Dy() && x < rb.Dx() && y < rb.Dy()
			if !in {
				diff++
				out.Set(x, y, color.RGBA{255, 0, 0, 255})
				continue
			}
			ca, cb := a.At(ra.Min.X+x, ra.Min.Y+y), b.At(rb.Min.X+x, rb.Min.Y+y)
			if yiqDelta(ca, cb) > threshold {
				diff++
				out.Set(x, y, color.RGBA{255, 0, 0, 255})
			} else {
				g := gray(ca)
				out.Set(x, y, color.RGBA{g, g, g, 255})
			}
		}
	}
	return float64(diff) / float64(w*h), out, nil
}

func rgb(c color.Color) (float64, float64, float64) {
	r, g, b, a := c.RGBA()
	af := float64(a) / 0xffff
	blend := func(v uint32) float64 { return 255 + (float64(v)/257-255)*af }
	if a == 0 {
		return 255, 255, 255
	}
	return blend(r), blend(g), blend(b)
}

func yiqDelta(c1, c2 color.Color) float64 {
	r1, g1, b1 := rgb(c1)
	r2, g2, b2 := rgb(c2)
	y := (r1-r2)*0.29889531 + (g1-g2)*0.58662247 + (b1-b2)*0.11448223
	i := (r1-r2)*0.59597799 - (g1-g2)*0.27417610 - (b1-b2)*0.32180189
	q := (r1-r2)*0.21147017 - (g1-g2)*0.52261711 + (b1-b2)*0.31114694
	return 0.5053*y*y + 0.299*i*i + 0.1957*q*q
}

func gray(c color.Color) uint8 {
	r, g, b := rgb(c)
	v := 0.299*r + 0.587*g + 0.114*b
	return uint8(255 - (255-v)*0.1)
}
