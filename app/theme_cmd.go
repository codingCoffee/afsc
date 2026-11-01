package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"afsc/internal/content"
	"afsc/internal/seo"
	"afsc/internal/settings"
	"afsc/internal/site"
	"afsc/internal/theme"
)

func themeCmd() *cli.Command {
	return &cli.Command{
		Name:  "theme",
		Usage: "theme tools",
		Commands: []*cli.Command{{
			Name:      "check",
			Usage:     "parse every template and render it against fixture data",
			ArgsUsage: "<theme dir>",
			Flags: []cli.Flag{
				&cli.BoolFlag{Name: "rerender", Usage: "then re-render stored post HTML with this theme's node partials and image sizes (needs AFSC_DATABASE_URL)"},
			},
			Action: runThemeCheck,
		}},
	}
}

func runThemeCheck(ctx context.Context, cmd *cli.Command) error {
	dir := cmd.Args().First()
	if dir == "" {
		return cli.Exit("usage: afsc theme check <theme dir>", 2)
	}
	defaults := settings.Defaults()
	defaults.Site.Title, defaults.Site.Tagline = "Example Blog", "Just another example site"
	results, err := theme.Check(dir, func(t *theme.Theme, d *theme.PageData) {
		d.Head, _ = seo.Build(seo.Input{Page: d, Settings: &defaults, Crumbs: t.Breadcrumbs(d), Now: time.Now()})
	})
	if err != nil {
		return cli.Exit(fmt.Sprintf("FAIL %s: %v", dir, err), 1)
	}
	failed := 0
	for _, r := range results {
		if r.Err != nil {
			failed++
			fmt.Printf("FAIL %-28s %-22s %v\n", r.Template, r.Fixture, r.Err)
		} else {
			fmt.Printf("ok   %-28s %s\n", r.Template, r.Fixture)
		}
	}
	if failed > 0 {
		return cli.Exit(fmt.Sprintf("%d of %d checks failed", failed, len(results)), 1)
	}
	fmt.Printf("%d checks passed\n", len(results))
	if !cmd.Bool("rerender") {
		return nil
	}

	dbURL := os.Getenv("AFSC_DATABASE_URL")
	if dbURL == "" {
		return errors.New("AFSC_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("invalid AFSC_DATABASE_URL: %w", err)
	}
	defer pool.Close()
	th, err := theme.Load(dir, theme.Options{Backend: &theme.StaticBackend{S: &defaults}})
	if err != nil {
		return err
	}
	var parts content.Partials
	if th.HasPartials() {
		parts = th
	}
	n, err := site.RerenderAll(ctx, pool, parts, th.Manifest().Images())
	if err != nil {
		return fmt.Errorf("rerender: %w", err)
	}
	fmt.Printf("re-rendered content: %d post(s) changed\n", n)
	if n > 0 {
		if err := site.Notify(ctx, pool, site.Event{All: true}); err != nil {
			return fmt.Errorf("purge page cache: %w", err)
		}
	}
	return nil
}
