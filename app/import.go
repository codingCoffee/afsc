package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/urfave/cli/v3"

	"afsc/internal/content"
	"afsc/internal/importer"
	"afsc/internal/settings"
	"afsc/internal/site"
	"afsc/internal/storage"
	"afsc/internal/theme"
)

func importCmd() *cli.Command {
	return &cli.Command{
		Name:  "import",
		Usage: "import a migration bundle (see docs/import-format.md)",
		Flags: []cli.Flag{
			&cli.StringFlag{Name: "bundle", Usage: "bundle directory", Required: true},
			&cli.BoolFlag{Name: "dry-run", Usage: "validate and simulate the import, then roll everything back"},
			&cli.StringFlag{Name: "report", Usage: "write the JSON report to this file"},
			&cli.BoolFlag{Name: "prune", Usage: "delete records from earlier imports of this source that are not in the bundle"},
			&cli.BoolFlag{Name: "yes", Usage: "don't ask before pruning"},
			&cli.StringFlag{Name: "theme", Usage: "theme directory for the content class allowlist", Sources: cli.EnvVars("AFSC_THEME_DIR")},
		},
		Action: runImport,
	}
}

func runImport(ctx context.Context, cmd *cli.Command) error {
	dbURL := os.Getenv("AFSC_DATABASE_URL")
	if dbURL == "" {
		return errors.New("AFSC_DATABASE_URL is not set")
	}
	pool, err := pgxpool.New(ctx, dbURL)
	if err != nil {
		return fmt.Errorf("invalid AFSC_DATABASE_URL: %w", err)
	}
	defer pool.Close()

	var tm *theme.Manifest
	var parts content.Partials
	if dir := cmd.String("theme"); dir != "" {
		defaults := settings.Defaults()
		th, err := theme.Load(dir, theme.Options{Backend: &theme.StaticBackend{S: &defaults}})
		if err != nil {
			return err
		}
		tm = th.Manifest()
		if th.HasPartials() {
			parts = th
		}
	}

	dryRun := cmd.Bool("dry-run")
	var store storage.Storage
	if !dryRun {
		cfg, err := storage.S3ConfigFromEnv()
		if err != nil {
			return err
		}
		s3, err := storage.NewS3(ctx, cfg)
		if err != nil {
			return err
		}
		if err := s3.Check(ctx); err != nil {
			return err
		}
		store = s3
	}

	rep, runErr := importer.Run(ctx, importer.Options{
		BundleDir: cmd.String("bundle"),
		DryRun:    dryRun,
		Prune:     cmd.Bool("prune"),
		Confirm:   func(plan []importer.PruneItem) bool { return cmd.Bool("yes") || confirmPrune(plan) },
		Theme:     tm,
		Partials:  parts,
		Pool:      pool,
		Storage:   store,
		Logf:      log.Printf,
	})

	if path := cmd.String("report"); path != "" && rep != nil {
		b, err := json.MarshalIndent(rep, "", "  ")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path, append(b, '\n'), 0o600); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	if rep != nil {
		printReport(rep)
	}
	// a failed import may have written part of the bundle, so purge unless nothing could have been written
	if !dryRun && !errors.Is(runErr, importer.ErrInvalid) {
		if err := site.Notify(ctx, pool, site.Event{Refresh: true, All: true}); err != nil {
			log.Printf("warning: page cache not purged, it expires within one TTL: %v", err)
		}
	}

	switch {
	case errors.Is(runErr, importer.ErrInvalid):
		return cli.Exit(fmt.Sprintf("validation failed with %d error(s)", len(rep.Errors)), 1)
	case runErr != nil:
		return cli.Exit(fmt.Sprintf("import failed: %v", runErr), 2)
	}
	return nil
}

func printReport(rep *importer.Report) {
	mode := ""
	if rep.DryRun {
		mode = " (dry run, nothing was written)"
	}
	fmt.Printf("import of source %q%s\n", rep.Source, mode)
	for _, l := range rep.Summary() {
		fmt.Println("  " + l)
	}
	const shown = 20
	for i, e := range rep.Errors {
		if i == shown {
			fmt.Printf("  ... %d more errors, see --report\n", len(rep.Errors)-shown)
			break
		}
		fmt.Printf("  error   %s %s: %s\n", e.LegacyKey, e.Field, e.Message)
	}
	fmt.Printf("  %d warning(s), %d sanitizer removal(s), %d user(s) to invite\n", len(rep.Warnings), len(rep.Removals), len(rep.Invites))
	for _, p := range rep.Prune {
		fmt.Printf("  prune   %s: %d record(s)\n", p.Table, p.Count)
	}
}

func confirmPrune(plan []importer.PruneItem) bool {
	fmt.Println("--prune will permanently delete these records, which earlier imports created but this bundle no longer has:")
	for _, p := range plan {
		fmt.Printf("  %-15s %6d  e.g. %s\n", p.Table, p.Count, strings.Join(p.Sample, ", "))
	}
	fmt.Print("Take a database backup first. Type yes to continue: ")
	line, _ := bufio.NewReader(os.Stdin).ReadString('\n')
	return strings.TrimSpace(line) == "yes"
}
