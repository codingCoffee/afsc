package main

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"testing"

	"afsc/internal/content"
)

func TestContentSchemaClientCopy(t *testing.T) {
	want, err := contentSchemaFile()
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(contentSchemaPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Errorf("%s is out of date; run `afsc content schema` and commit the result", contentSchemaPath)
	}
}

// The editor's bun test writes the getJSON output of its fixtures here; skipped when it hasn't run.
const contentEditorRoundtripPath = "client/.cache/editor-roundtrip.json"

func TestContentEditorOutputValidates(t *testing.T) {
	b, err := os.ReadFile(contentEditorRoundtripPath)
	if errors.Is(err, fs.ErrNotExist) {
		t.Skipf("%s not found; run `bun test` in client/ first", contentEditorRoundtripPath)
	}
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		AllowedClasses []string                   `json:"allowed_classes"`
		Docs           map[string]json.RawMessage `json:"docs"`
	}
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Docs) == 0 {
		t.Fatal("no documents in the editor output")
	}
	allowed := map[string]bool{}
	for _, c := range out.AllowedClasses {
		allowed[c] = true
	}
	for name, raw := range out.Docs {
		doc, err := content.Parse(raw)
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		res := content.Validate(doc, content.Options{AllowedClasses: allowed, AllowRawHTML: true})
		for _, e := range res.Errors {
			t.Errorf("%s: %s: %s", name, e.Path, e.Message)
		}
	}
}
