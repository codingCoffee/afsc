package verify

import (
	"context"
	"fmt"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"

	"afsc/migrators/afsc-wp/internal/crawl"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/fidelity"
	"afsc/migrators/afsc-wp/internal/htmlmeta"
	"afsc/migrators/afsc-wp/internal/redirects"
	"afsc/migrators/afsc-wp/internal/report"
	"afsc/migrators/afsc-wp/internal/transform"
)

// urls asserts every url-map entry: status, a single hop, and a 200 after the hop (§8.1).
func (r *runner) urls(ctx context.Context) (Result, error) {
	m, err := extract.ReadNDJSON[transform.URLExpect](filepath.Join(r.o.WorkDir, "url-map.ndjson"))
	if err != nil {
		return Result{}, err
	}
	var bad [][]string
	unknown := 0
	for _, e := range m {
		got, err := r.fetch(ctx, "GET", e.Path)
		if err != nil {
			bad = append(bad, []string{e.Path, e.Expect, "error: " + err.Error()})
			continue
		}
		problem := ""
		switch e.Expect {
		case "200":
			if got.Status != 200 {
				problem = fmt.Sprintf("status %d", got.Status)
			}
		case "301":
			loc := r.relPath(got.Location)
			switch {
			case got.Status != 301:
				problem = fmt.Sprintf("status %d, want 301", got.Status)
			case redirects.Canonical(loc, "keep") != redirects.Canonical(r.relPath(e.Target), "keep"):
				problem = "redirects to " + loc + ", want " + e.Target
			default:
				next, err := r.fetch(ctx, "GET", got.Location)
				switch {
				case err != nil:
					problem = "after hop: " + err.Error()
				case next.Status >= 300 && next.Status < 400:
					problem = "redirect chain: second hop to " + next.Location
				case next.Status != 200 && !strings.Contains(e.Target, "://"):
					problem = fmt.Sprintf("target returns %d", next.Status)
				}
			}
		case "410":
			if got.Status != 410 {
				problem = fmt.Sprintf("status %d, want 410", got.Status)
			}
		case "404-approved":
			if got.Status != 404 && got.Status != 410 {
				problem = fmt.Sprintf("status %d, want 404 (approved)", got.Status)
			}
		default:
			unknown++
			problem = fmt.Sprintf("no expected outcome (AFSC returns %d); add a redirect or approve the 404", got.Status)
		}
		if problem != "" {
			bad = append(bad, []string{e.Path, e.Expect + " " + e.Target, problem})
		}
	}
	d := report.New("URL parity")
	d.P("%d URLs checked against %s; %d mismatches (%d without an expected outcome).", len(m), r.o.Target, len(bad), unknown)
	d.Table([]string{"Path", "Expected", "Problem"}, bad)
	return Result{Pass: len(bad) == 0, Summary: fmt.Sprintf("%d/%d URLs match", len(m)-len(bad), len(m))},
		d.Write(filepath.Join(r.o.ReportsDir, "url-parity.md"))
}

// routeKind groups URLs for the SEO diff by their shape.
func routeKind(p string) string {
	switch {
	case p == "/" || p == "":
		return "home"
	case regexp.MustCompile(`/page/\d+/?$`).MatchString(p):
		return "paginated archive"
	case strings.Contains(p, "/feed"):
		return "feed"
	case regexp.MustCompile(`^/\d{4}(/\d{2})?/?$`).MatchString(p):
		return "date archive"
	case strings.HasPrefix(p, "/author/"):
		return "author"
	case strings.HasPrefix(p, "/category/"), strings.HasPrefix(p, "/tag/"):
		return strings.Split(strings.Trim(p, "/"), "/")[0]
	}
	return "post or page"
}

type seoDiff struct {
	path, field, wp, af string
}

// seo compares the head of every baseline 200 page with AFSC after a host swap (§8.2).
func (r *runner) seo(ctx context.Context) (Result, error) {
	base, err := crawl.Load(r.o.BaselineDir)
	if err != nil {
		return Result{}, err
	}
	var diffs []seoDiff
	checked := 0
	for _, rec := range base {
		if rec.Status != 200 || rec.Meta == nil {
			continue
		}
		p := r.relPath(rec.URL)
		got, err := r.fetch(ctx, "GET", p)
		if err != nil || got.Status != 200 {
			continue
		}
		af, err := htmlmeta.Parse(got.Body, r.o.ContentSelector)
		if err != nil {
			continue
		}
		for _, img := range af.Images {
			r.images[img] = true
		}
		checked++
		wp := rec.Meta
		cmp := func(field, a, b string) {
			if a != b {
				diffs = append(diffs, seoDiff{p, field, a, b})
			}
		}
		cmp("title", wp.Title, af.Title)
		cmp("description", wp.Description, af.Description)
		cmp("canonical", r.relPath(wp.Canonical), r.relPath(af.Canonical))
		cmp("robots", strings.Join(htmlmeta.RobotsSet(wp.Robots), ", "), strings.Join(htmlmeta.RobotsSet(af.Robots), ", "))
		cmp("og:title", wp.OG["og:title"], af.OG["og:title"])
		cmp("og:description", wp.OG["og:description"], af.OG["og:description"])
		cmp("og:image present", strconv.FormatBool(wp.OG["og:image"] != ""), strconv.FormatBool(af.OG["og:image"] != ""))
		cmp("json-ld types", strings.Join(wp.Types(), " "), strings.Join(af.Types(), " "))
		cmp("json-ld headline", wp.Headline(), af.Headline())
		cmp("h1", strings.Join(wp.H1, " | "), strings.Join(af.H1, " | "))
	}
	groups := map[string][]seoDiff{}
	for _, d := range diffs {
		k := routeKind(d.path) + " — " + d.field
		groups[k] = append(groups[k], d)
	}
	d := report.New("SEO diff")
	d.P("%d pages compared; %d differences in %d groups. Every difference must be fixed or signed off here.", checked, len(diffs), len(groups))
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return len(groups[keys[i]]) > len(groups[keys[j]]) || (len(groups[keys[i]]) == len(groups[keys[j]]) && keys[i] < keys[j])
	})
	for _, k := range keys {
		var rows [][]string
		for i, x := range groups[k] {
			if i == 10 {
				rows = append(rows, []string{fmt.Sprintf("… %d more", len(groups[k])-10), "", ""})
				break
			}
			rows = append(rows, []string{x.path, x.wp, x.af})
		}
		d.H2(fmt.Sprintf("%s (%d)", k, len(groups[k]))).Table([]string{"Path", "WordPress", "AFSC"}, rows)
	}
	return Result{Pass: len(diffs) == 0, Summary: fmt.Sprintf("%d differences on %d pages", len(diffs), checked)},
		d.Write(filepath.Join(r.o.ReportsDir, "seo-diff.md"))
}

// content compares each published post's body: baseline crawl body, else the transform's reference (§8.3).
func (r *runner) content(ctx context.Context) (Result, error) {
	paths, err := extract.ReadNDJSON[transform.PathRec](filepath.Join(r.o.WorkDir, "paths.ndjson"))
	if err != nil {
		return Result{}, err
	}
	refs, err := extract.ReadNDJSON[transform.FilteredPost](filepath.Join(r.o.WorkDir, "filtered", "reference.ndjson"))
	if err != nil {
		return Result{}, err
	}
	ref := map[string]string{}
	for _, f := range refs {
		ref[r.o.Source+":post:"+strconv.FormatInt(f.ID, 10)] = f.HTML
	}
	bodies := map[string]string{}
	if base, err := crawl.Load(r.o.BaselineDir); err == nil {
		for _, b := range base {
			if b.Status == 200 && b.Meta != nil && b.Meta.Body != "" {
				bodies[redirects.Canonical(r.relPath(b.URL), "keep")] = b.Meta.Body
			}
		}
	}
	links := map[string]string{}
	if lm, err := extract.ReadNDJSON[transform.LinkRewrite](filepath.Join(r.o.WorkDir, "link-map.ndjson")); err == nil {
		for _, l := range lm {
			links[l.From] = l.To
		}
	}
	link := func(h string) string {
		if to, ok := links[strings.TrimSpace(h)]; ok {
			return to
		}
		return h
	}
	wpSide := fidelity.Side{Hosts: r.o.Hosts, Media: r.media.wp(r.o.Source), Link: link}
	afSide := fidelity.Side{Hosts: r.o.Hosts, Media: r.media.afsc}
	classes := map[string]int{}
	var rows [][]string
	for _, p := range paths {
		if p.Status != "published" || p.AFSCPath == "" {
			continue
		}
		wpHTML, src := bodies[redirects.Canonical(p.WPPath, "keep")], "crawl"
		if wpHTML == "" {
			wpHTML, src = ref[p.Key], "filtered"
		}
		got, err := r.fetch(ctx, "GET", p.AFSCPath)
		if err != nil || got.Status != 200 {
			classes["lossy"]++
			rows = append(rows, []string{p.Key, "lossy", src, fmt.Sprintf("AFSC page not available (%v)", statusOf(got, err))})
			continue
		}
		af, _ := htmlmeta.Parse(got.Body, r.o.ContentSelector)
		for _, img := range af.Images {
			r.images[img] = true
		}
		class, diffs := fidelity.Compare(fidelity.Analyze(wpHTML, wpSide), fidelity.Analyze(af.Body, afSide), p.RawHTML)
		classes[class]++
		if class == fidelity.Lossy || class == fidelity.Fallback {
			rows = append(rows, []string{p.Key, class, src, report.Samples(diffs, 4)})
		}
	}
	d := report.New("Content fidelity")
	var cr [][]string
	for _, kv := range report.SortedCounts(classes) {
		cr = append(cr, []string{kv[0], kv[1]})
	}
	d.Table([]string{"Class", "Posts"}, cr)
	d.P("Zero `lossy` posts are allowed at cutover unless each is signed off here. `fallback` posts use rawHtml and are listed for review.")
	d.H2("Lossy and fallback posts").Table([]string{"Post", "Class", "WordPress side", "Differences"}, rows)
	return Result{Pass: classes["lossy"] == 0, Summary: fmt.Sprintf("%v", classes)}, d.Write(filepath.Join(r.o.ReportsDir, "content-fidelity.md"))
}

func statusOf(r *response, err error) string {
	if err != nil {
		return err.Error()
	}
	return strconv.Itoa(r.Status)
}

// counts reconciles source → bundle (from transform) → AFSC (from the import report) per entity (§8.4).
func (r *runner) counts(_ context.Context) (Result, error) {
	var tr transform.Report
	if err := extract.ReadJSON(filepath.Join(r.o.ReportsDir, "transform.json"), &tr); err != nil {
		return Result{}, fmt.Errorf("reports/transform.json: %w", err)
	}
	var imp struct {
		Counts map[string]map[string]int `json:"counts"`
	}
	haveImport := r.o.ImportReport != ""
	if haveImport {
		if err := extract.ReadJSON(r.o.ImportReport, &imp); err != nil {
			return Result{}, fmt.Errorf("import report: %w", err)
		}
	}
	entity := map[string]string{"users": "users", "authors": "authors", "terms": "terms", "posts": "posts", "media": "media",
		"comments": "comments", "menus": "menus", "menu items": "menu_items", "redirects": "redirects", "legacy": "legacy_records"}
	pass := true
	var rows [][]string
	for _, c := range tr.Counts {
		var sk []string
		total := 0
		for _, kv := range report.SortedCounts(c.Skipped) {
			sk = append(sk, kv[0]+": "+kv[1])
			n, _ := strconv.Atoi(kv[1])
			total += n
		}
		afsc := "—"
		for prefix, key := range entity {
			if strings.HasPrefix(c.Entity, prefix) && haveImport {
				if m, ok := imp.Counts[key]; ok {
					n := m["created"] + m["updated"] + m["unchanged"] + m["skipped"]
					afsc = strconv.Itoa(n)
					if n != c.Bundle {
						pass = false
						afsc += " ✗"
					}
				}
			}
		}
		rows = append(rows, []string{c.Entity, strconv.Itoa(c.Source), strings.Join(sk, "; "), strconv.Itoa(c.Bundle), afsc})
	}
	d := report.New("Counts")
	d.P("WordPress source rows → skipped (by reason) → bundle records → AFSC records (created + updated + unchanged + skipped, from the import report).")
	if !haveImport {
		d.P("No import report given (`--import-report` or verify.import_report); the AFSC column is empty.")
	}
	d.Table([]string{"Entity", "Source", "Skipped", "Bundle", "AFSC"}, rows)
	return Result{Pass: pass && haveImport, Summary: fmt.Sprintf("%d entities", len(rows))}, d.Write(filepath.Join(r.o.ReportsDir, "counts.md"))
}

func uniqueSorted(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}
