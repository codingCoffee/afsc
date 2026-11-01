package main

import (
	"context"
	"errors"
	"io/fs"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"github.com/urfave/cli/v3"
)

func main() {
	if err := godotenv.Load(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		log.Fatalf("failed to load .env: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cmd := &cli.Command{
		Name:     "afsc",
		Usage:    "afsc server and tooling",
		Commands: []*cli.Command{serveCmd(), migrateCmd(), importCmd(), themeCmd(), userCmd(), contentCmd(), workerCmd()},
	}

	if err := cmd.Run(ctx, os.Args); err != nil {
		stop()
		log.Fatal(err)
	}
}
