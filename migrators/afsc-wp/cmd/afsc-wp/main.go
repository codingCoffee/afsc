// Command afsc-wp migrates a WordPress site to AFSC: inventory, crawl, extract, transform, bundle, verify and
// theme-scaffold. See README.md.
package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/urfave/cli/v3"

	"afsc/internal/theme"

	"afsc/migrators/afsc-wp/internal/bundle"
	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/crawl"
	"afsc/migrators/afsc-wp/internal/decisions"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/filter"
	"afsc/migrators/afsc-wp/internal/inventory"
	"afsc/migrators/afsc-wp/internal/plugins"
	"afsc/migrators/afsc-wp/internal/report"
	"afsc/migrators/afsc-wp/internal/scaffold"
	"afsc/migrators/afsc-wp/internal/transform"
	"afsc/migrators/afsc-wp/internal/uploads"
	"afsc/migrators/afsc-wp/internal/verify"
	"afsc/migrators/afsc-wp/internal/wpdb"
)

func logf(format string, args ...any) { fmt.Fprintf(os.Stderr, format+"\n", args...) }

func main() {
	app := &cli.Command{
		Name:  "afsc-wp",
		Usage: "migrate a WordPress site to AFSC",
		Flags: []cli.Flag{&cli.StringFlag{Name: "config", Aliases: []string{"c"}, Value: "afsc-wp.json", Usage: "site config (docs/config.md)"}},
		Commands: []*cli.Command{
			{Name: "inventory", Usage: "scan the WordPress restore, uploads and site code; write reports/inventory.* and propose decisions.json",
				Flags:  []cli.Flag{&cli.BoolFlag{Name: "from-extract", Usage: "use work/extract instead of reading MySQL"}},
				Action: runInventory},
			{Name: "crawl", Usage: "crawl the WordPress site into baseline/ (the human runs this against production)", Action: runCrawl},
			{Name: "extract", Usage: "read MySQL and uploads into work/extract", Action: runExtract},
			{Name: "transform", Usage: "convert work/extract into work/transform (needs every decision confirmed)", Action: runTransform},
			{Name: "bundle", Usage: "write the AFSC import bundle from work/transform",
				Flags: []cli.Flag{&cli.StringFlag{Name: "out", Usage: "bundle directory (default bundle_dir)"}}, Action: runBundle},
			{Name: "verify", Usage: "check an imported AFSC (local or staging) against the baseline",
				Flags: []cli.Flag{
					&cli.StringFlag{Name: "target", Usage: "AFSC base URL (default afsc.target_base_url)"},
					&cli.StringFlag{Name: "checks", Value: strings.Join(verify.Checks, ","), Usage: "comma-separated: " + strings.Join(verify.Checks, ",")},
					&cli.StringFlag{Name: "import-report", Usage: "afsc import --report output, for the counts check"},
				},
				Action: runVerify},
			{Name: "theme-scaffold", Usage: "write a starter AFSC theme from the inventory",
				Flags:  []cli.Flag{&cli.StringFlag{Name: "out", Usage: "output directory (default themes/<stylesheet>)"}},
				Action: runScaffold},
			{Name: "all", Usage: "extract → transform → bundle → afsc import --dry-run, timed",
				Flags: []cli.Flag{&cli.StringFlag{Name: "out", Usage: "bundle directory (default bundle_dir)"},
					&cli.BoolFlag{Name: "skip-extract", Usage: "reuse work/extract"}},
				Action: runAll},
		},
	}
	if err := app.Run(context.Background(), os.Args); err != nil {
		logf("afsc-wp: %v", err)
		os.Exit(1)
	}
}

func loadConfig(cmd *cli.Command) (*config.Config, error) {
	return config.Load(cmd.String("config"))
}

func activePlugins(ds *extract.Dataset) []string {
	var out []string
	if l, ok := ds.Option("active_plugins").([]any); ok {
		for _, v := range l {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}

func pluginSet(cfg *config.Config, ds *extract.Dataset) *plugins.Set {
	keys := append(append([]extract.Count{}, ds.Stats.PostMetaKeys...), ds.Stats.UserMetaKeys...)
	return plugins.Resolve(cfg, activePlugins(ds), keys)
}

func codeDirs(cfg *config.Config) []string {
	var out []string
	for _, d := range cfg.WPCodeDirs {
		out = append(out, cfg.Path(d))
	}
	return out
}

// readDataset reads MySQL and scans uploads.
func readDataset(ctx context.Context, cfg *config.Config) (*extract.Dataset, *plugins.Set, error) {
	logf("reading MySQL (read-only)…")
	ds, set, err := wpdb.Load(ctx, cfg)
	if err != nil {
		return nil, nil, err
	}
	if cfg.UploadsDir == "" {
		return nil, nil, fmt.Errorf("uploads_dir is not set")
	}
	logf("scanning %s…", cfg.Path(cfg.UploadsDir))
	if ds.Files, err = uploads.Scan(cfg.Path(cfg.UploadsDir)); err != nil {
		return nil, nil, fmt.Errorf("scan uploads: %w", err)
	}
	ds.Sort()
	return ds, set, nil
}

func runInventory(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	var ds *extract.Dataset
	var set *plugins.Set
	if cmd.Bool("from-extract") {
		if ds, err = extract.Load(cfg.Work("extract")); err != nil {
			return err
		}
		set = pluginSet(cfg, ds)
	} else if ds, set, err = readDataset(ctx, cfg); err != nil {
		return err
	}
	inv, err := inventory.Run(cfg, ds, set, codeDirs(cfg))
	if err != nil {
		return err
	}
	path := cfg.Path(cfg.DecisionsFile)
	old, _ := decisions.Load(path)
	merged := decisions.Merge(old, inv.Decisions)
	if err := merged.Save(path); err != nil {
		return err
	}
	if err := extract.WriteJSON(cfg.Reports("inventory.json"), inv); err != nil {
		return err
	}
	if err := inv.Markdown(merged).Write(cfg.Reports("inventory.md")); err != nil {
		return err
	}
	pending := 0
	for _, it := range merged.Items {
		if !it.Stale && it.Status != decisions.StatusConfirmed {
			pending++
		}
	}
	logf("wrote reports/inventory.md, reports/inventory.json and %s (%d items, %d to confirm)", cfg.DecisionsFile, len(merged.Items), pending)
	return nil
}

func runExtract(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	return extractStep(ctx, cfg)
}

func extractStep(ctx context.Context, cfg *config.Config) error {
	ds, _, err := readDataset(ctx, cfg)
	if err != nil {
		return err
	}
	if err := ds.Save(cfg.Work("extract")); err != nil {
		return err
	}
	logf("extracted %d posts, %d users, %d terms, %d comments, %d files, %d charset fixes", len(ds.Posts), len(ds.Users),
		len(ds.Terms), len(ds.Comments), len(ds.Files), len(ds.Fixes))
	return nil
}

func filterRunner(cfg *config.Config) (filter.Runner, error) {
	var next filter.Runner
	switch {
	case cfg.Filter.ReplayFile != "":
		next = &filter.Replay{Path: cfg.Path(cfg.Filter.ReplayFile)}
	case len(cfg.Filter.Command) > 0:
		next = &filter.Command{Argv: cfg.Filter.Command, Timeout: time.Duration(cfg.Filter.TimeoutSeconds) * time.Second, Batch: cfg.Filter.BatchSize}
	default:
		return nil, fmt.Errorf("set filter.command (tools/wp-filter.php) or filter.replay_file in %s", "afsc-wp.json")
	}
	return &filter.Cached{Next: next, Dir: cfg.Work("filtered")}, nil
}

func runTransform(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	return transformStep(ctx, cfg)
}

// freshness fails closed when the data now holds items the decision table has never seen.
func freshness(cfg *config.Config, ds *extract.Dataset, set *plugins.Set, dec *decisions.File) error {
	inv, err := inventory.Run(cfg, ds, set, codeDirs(cfg))
	if err != nil {
		return err
	}
	var missing []string
	for _, it := range inv.Decisions.Items {
		if d := dec.Get(it.Kind, it.Name); d == nil || d.Stale {
			missing = append(missing, it.ID)
		}
	}
	if len(missing) > 0 {
		return fmt.Errorf("the data has %d items with no decision; re-run `afsc-wp inventory` and confirm them:\n  %s",
			len(missing), report.Samples(missing, 20))
	}
	return nil
}

func transformStep(ctx context.Context, cfg *config.Config) error {
	ds, err := extract.Load(cfg.Work("extract"))
	if err != nil {
		return err
	}
	set := pluginSet(cfg, ds)
	dec, err := decisions.Load(cfg.Path(cfg.DecisionsFile))
	if err != nil {
		return err
	}
	if err := dec.Check(); err != nil {
		return err
	}
	if err := freshness(cfg, ds, set, dec); err != nil {
		return err
	}
	runner, err := filterRunner(cfg)
	if err != nil {
		return err
	}
	var tm *theme.Manifest
	themeName := ""
	if cfg.ThemeDir != "" {
		if tm, err = theme.LoadManifest(cfg.Path(cfg.ThemeDir)); err != nil {
			return fmt.Errorf("theme_dir: %w", err)
		}
		themeName = tm.Name
	}
	var baseline []string
	if recs, err := crawl.Load(cfg.Path(cfg.BaselineDir)); err == nil {
		for _, r := range recs {
			baseline = append(baseline, r.URL)
		}
	}
	out, err := transform.Run(ctx, transform.Options{Cfg: cfg, DS: ds, Plugins: set, Decisions: dec, Filter: runner, Theme: tm, Baseline: baseline})
	if err != nil {
		return err
	}
	if err := out.Save(cfg.Work(), cfg.Reports(), cfg.Source, themeName); err != nil {
		return err
	}
	classes := map[string]int{}
	for _, f := range out.Report.Fidelity {
		classes[f.Class]++
	}
	logf("transformed %d posts, %d media, %d redirects; pre-import fidelity %v (reports/transform.md)", len(out.Posts), len(out.Media), len(out.Redirects), classes)
	return nil
}

func bundleDir(cfg *config.Config, cmd *cli.Command) string {
	if o := cmd.String("out"); o != "" {
		return o
	}
	return cfg.Path(cfg.BundleDir)
}

func runBundle(_ context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	counts, err := bundle.Write(cfg.Work(), cfg.Path(cfg.UploadsDir), bundleDir(cfg, cmd))
	if err != nil {
		return err
	}
	logf("bundle written to %s: %v", bundleDir(cfg, cmd), counts)
	return nil
}

func runCrawl(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	var seeds []string
	for _, f := range cfg.Crawl.ExtraURLFiles {
		u, err := crawl.ReadURLFile(cfg.Path(f))
		if err != nil {
			return err
		}
		seeds = append(seeds, u...)
	}
	if ds, err := extract.Load(cfg.Work("extract")); err == nil {
		seeds = append(seeds, routeSamples(ds)...)
	} else {
		logf("no work/extract; crawling without WordPress route samples")
	}
	start := time.Now()
	recs, err := crawl.Run(ctx, crawl.Options{
		Base: cfg.Crawl.BaseURL, Hosts: cfg.SiteHosts(), UserAgent: cfg.Crawl.UserAgent,
		Delay: time.Duration(cfg.Crawl.DelayMS) * time.Millisecond, Timeout: time.Duration(cfg.Crawl.TimeoutSeconds) * time.Second,
		MaxDepth: cfg.Crawl.MaxDepth, MaxURLs: cfg.Crawl.MaxURLs, Selector: cfg.ContentSelector, Seeds: seeds,
		OutDir: cfg.Path(cfg.BaselineDir), Logf: logf,
	})
	if err != nil {
		return err
	}
	logf("crawled %d URLs in %s into %s", len(recs), time.Since(start).Round(time.Second), cfg.BaselineDir)
	return nil
}

// routeSamples adds one URL per WordPress route type the link graph may not reach (§4).
func routeSamples(ds *extract.Dataset) []string {
	out := []string{"/feed/", "/comments/feed/", "/page/2/", "/?s=the", "/feed/atom/", "/?feed=rss2"}
	seen := map[string]bool{}
	for i := len(ds.Posts) - 1; i >= 0; i-- {
		p := ds.Posts[i]
		if p.Status != "publish" && p.Type != "attachment" {
			continue
		}
		id := strconv.FormatInt(p.ID, 10)
		switch {
		case p.Type == "post" && !seen["post"]:
			seen["post"] = true
			out = append(out, "/?p="+id)
			if len(p.Date) >= 10 {
				y, m, d := p.Date[:4], p.Date[5:7], p.Date[8:10]
				out = append(out, "/"+y+"/", "/"+y+"/"+m+"/", "/"+y+"/"+m+"/"+d+"/", "/"+y+"/page/2/")
			}
		case p.Type == "page" && !seen["page"]:
			seen["page"] = true
			out = append(out, "/?page_id="+id)
		case p.Type == "attachment" && !seen["attachment"]:
			seen["attachment"] = true
			out = append(out, "/?attachment_id="+id)
		}
	}
	for _, t := range ds.Terms {
		if (t.Taxonomy == "category" || t.Taxonomy == "post_tag") && !seen[t.Taxonomy] && t.Count > 0 {
			seen[t.Taxonomy] = true
			out = append(out, "/?cat="+strconv.FormatInt(t.TermID, 10))
		}
	}
	if len(ds.Users) > 0 {
		u := ds.Users[0]
		out = append(out, "/?author="+strconv.FormatInt(u.ID, 10), "/author/"+u.Nicename+"/", "/author/"+u.Nicename+"/page/2/")
	}
	return out
}

func runVerify(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	target := cmd.String("target")
	if target == "" {
		target = cfg.AFSC.TargetBaseURL
	}
	if target == "" {
		return fmt.Errorf("no target: pass --target or set afsc.target_base_url (local or staging AFSC, never production)")
	}
	hosts := map[string]bool{}
	for _, h := range cfg.SiteHosts() {
		hosts[h] = true
	}
	imp := cmd.String("import-report")
	if imp == "" && cfg.Verify.ImportReport != "" {
		imp = cfg.Path(cfg.Verify.ImportReport)
	}
	var checks []string
	for _, c := range strings.Split(cmd.String("checks"), ",") {
		if c = strings.TrimSpace(c); c != "" {
			if !slices.Contains(verify.Checks, c) {
				return fmt.Errorf("unknown check %q (known: %s)", c, strings.Join(verify.Checks, ", "))
			}
			checks = append(checks, c)
		}
	}
	res, err := verify.Run(ctx, verify.Options{
		Source: cfg.Source, Target: strings.TrimSuffix(target, "/"), WPBase: cfg.Site.BaseURL, Hosts: hosts,
		ContentSelector: cfg.Verify.AFSCContentSelector, WorkDir: cfg.Work(), BaselineDir: cfg.Path(cfg.BaselineDir),
		ReportsDir: cfg.Reports(), BundleDir: cfg.Path(cfg.BundleDir), ImportReport: imp,
		ScreenshotsDir: cfg.Path(cfg.Verify.ScreenshotsDir), Threshold: cfg.Verify.VisualThreshold,
		TopURLsFile: cfg.Path(cfg.Verify.TopURLsFile), Timeout: 30 * time.Second,
	}, checks)
	failed := 0
	for _, r := range res {
		mark := "PASS"
		if !r.Pass {
			mark, failed = "FAIL", failed+1
		}
		logf("%-8s %s  %s", r.Check, mark, r.Summary)
	}
	if err != nil {
		return err
	}
	if failed > 0 {
		return fmt.Errorf("%d of %d gates failed; see reports/", failed, len(res))
	}
	return nil
}

func runScaffold(_ context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	ds, err := extract.Load(cfg.Work("extract"))
	if err != nil {
		return err
	}
	var inv struct {
		Content struct {
			Classes map[string]int `json:"classes"`
		} `json:"content"`
	}
	if err := extract.ReadJSON(cfg.Reports("inventory.json"), &inv); err != nil {
		return fmt.Errorf("reports/inventory.json (run inventory first): %w", err)
	}
	out := cmd.String("out")
	if out == "" {
		out = filepath.Join("themes", ds.OptionString("stylesheet"))
	}
	res, err := scaffold.Run(scaffold.Input{DS: ds, Classes: inv.Content.Classes, CodeDirs: codeDirs(cfg), Selector: cfg.ContentSelector, Out: out})
	if err != nil {
		return err
	}
	logf("theme scaffold %q written to %s: %d allowed classes, %d menu locations, %d widget partials; set theme_dir to it",
		res.Manifest.Name, out, len(res.Manifest.Content.AllowedClasses), len(res.Manifest.MenuLocations), len(res.Widgets))
	return nil
}

func runAll(ctx context.Context, cmd *cli.Command) error {
	cfg, err := loadConfig(cmd)
	if err != nil {
		return err
	}
	type step struct {
		name string
		took time.Duration
	}
	var steps []step
	timed := func(name string, fn func() error) error {
		start := time.Now()
		logf("== %s", name)
		err := fn()
		steps = append(steps, step{name, time.Since(start)})
		return err
	}
	if !cmd.Bool("skip-extract") {
		if err := timed("extract", func() error { return extractStep(ctx, cfg) }); err != nil {
			return err
		}
	}
	if err := timed("transform", func() error { return transformStep(ctx, cfg) }); err != nil {
		return err
	}
	out := bundleDir(cfg, cmd)
	if err := timed("bundle", func() error {
		_, err := bundle.Write(cfg.Work(), cfg.Path(cfg.UploadsDir), out)
		return err
	}); err != nil {
		return err
	}
	reportPath := cfg.Work("import-dry-run.json")
	if err := timed("afsc import --dry-run", func() error {
		args := append(append([]string{}, cfg.AFSC.Command[1:]...), "import", "--bundle", out, "--dry-run", "--report", reportPath)
		if cfg.ThemeDir != "" {
			args = append(args, "--theme", cfg.Path(cfg.ThemeDir))
		}
		c := exec.CommandContext(ctx, cfg.AFSC.Command[0], args...)
		c.Stdout, c.Stderr = os.Stderr, os.Stderr
		return c.Run()
	}); err != nil {
		return fmt.Errorf("dry run failed (see %s): %w", reportPath, err)
	}
	d := report.New("afsc-wp all timing")
	var rows [][]string
	var total time.Duration
	for _, s := range steps {
		rows = append(rows, []string{s.name, s.took.Round(time.Millisecond).String()})
		total += s.took
	}
	rows = append(rows, []string{"total", total.Round(time.Millisecond).String()})
	d.Table([]string{"Step", "Time"}, rows)
	d.P("The cutover budget for the whole pipeline is one hour (migration plan §0.4).")
	if err := d.Write(cfg.Reports("all-timing.md")); err != nil {
		return err
	}
	logf("done in %s; bundle at %s, dry-run report at %s", total.Round(time.Second), out, reportPath)
	return nil
}
