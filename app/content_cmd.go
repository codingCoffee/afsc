package main

import (
	"context"
	"fmt"
	"os"

	"github.com/urfave/cli/v3"

	"afsc/internal/content"
)

const contentSchemaPath = "client/app/editor/schema.json"

func contentCmd() *cli.Command {
	return &cli.Command{
		Name:  "content",
		Usage: "content schema tools",
		Commands: []*cli.Command{{
			Name:   "schema",
			Usage:  "write the content schema JSON that the admin editor is checked against",
			Flags:  []cli.Flag{&cli.StringFlag{Name: "out", Value: contentSchemaPath, Usage: "output path"}},
			Action: runContentSchema,
		}},
	}
}

func contentSchemaFile() ([]byte, error) {
	b, err := content.SchemaJSON()
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func runContentSchema(_ context.Context, cmd *cli.Command) error {
	b, err := contentSchemaFile()
	if err != nil {
		return err
	}
	out := cmd.String("out")
	if err := os.WriteFile(out, b, 0o644); err != nil {
		return err
	}
	fmt.Printf("wrote %s\n", out)
	return nil
}
