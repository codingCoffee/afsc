// Command golden regenerates testdata/filter-golden.ndjson: it runs a transform with the real wp-filter.php
// (filter.command) and prints every filter and render result as replay records.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"afsc/migrators/afsc-wp/internal/config"
	"afsc/migrators/afsc-wp/internal/decisions"
	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/filter"
	"afsc/migrators/afsc-wp/internal/plugins"
	"afsc/migrators/afsc-wp/internal/transform"
)

func main() {
	path := flag.String("c", "testdata/afsc-wp.fixture.json", "config with filter.command set")
	flag.Parse()
	if err := run(*path); err != nil {
		fmt.Fprintln(os.Stderr, "golden:", err)
		os.Exit(1)
	}
}

func run(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	if len(cfg.Filter.Command) == 0 {
		return fmt.Errorf("filter.command is empty; golden output must come from real WordPress")
	}
	ds, err := extract.Load(cfg.Work("extract"))
	if err != nil {
		return err
	}
	dec, err := decisions.Load(cfg.Path(cfg.DecisionsFile))
	if err != nil {
		return err
	}
	var active []string
	if l, ok := ds.Option("active_plugins").([]any); ok {
		for _, v := range l {
			if s, ok := v.(string); ok {
				active = append(active, s)
			}
		}
	}
	set := plugins.Resolve(cfg, active, append(append([]extract.Count{}, ds.Stats.PostMetaKeys...), ds.Stats.UserMetaKeys...))
	rec := &filter.Recorder{Next: &filter.Command{Argv: cfg.Filter.Command, Timeout: 10 * time.Minute}}
	if _, err := transform.Run(context.Background(), transform.Options{Cfg: cfg, DS: ds, Plugins: set, Decisions: dec, Filter: rec}); err != nil {
		return err
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	for _, r := range rec.Records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}
