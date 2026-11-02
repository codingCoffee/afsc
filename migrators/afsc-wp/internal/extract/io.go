package extract

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// WriteNDJSON writes one JSON object per line without HTML escaping, so output is stable and readable.
func WriteNDJSON[T any](path string, recs []T) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	for _, r := range recs {
		if err := enc.Encode(r); err != nil {
			return fmt.Errorf("%s: %w", path, err)
		}
	}
	return os.WriteFile(path, b.Bytes(), 0o600)
}

// ReadNDJSON reads a file written by WriteNDJSON. A missing file reads as empty.
func ReadNDJSON[T any](path string) ([]T, error) {
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []T
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 256<<20)
	line := 0
	for sc.Scan() {
		line++
		if len(bytes.TrimSpace(sc.Bytes())) == 0 {
			continue
		}
		var v T
		if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
			return nil, fmt.Errorf("%s:%d: %w", path, line, err)
		}
		out = append(out, v)
	}
	return out, sc.Err()
}

// WriteJSON writes v indented, without HTML escaping.
func WriteJSON(path string, v any) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return err
	}
	return os.WriteFile(path, b.Bytes(), 0o600)
}

func ReadJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

type meta struct {
	Prefix string `json:"prefix"`
}

// Save writes the data set under dir (work/extract).
func (d *Dataset) Save(dir string) error {
	d.Sort()
	steps := []func() error{
		func() error { return WriteNDJSON(filepath.Join(dir, "posts.ndjson"), d.Posts) },
		func() error { return WriteNDJSON(filepath.Join(dir, "users.ndjson"), d.Users) },
		func() error { return WriteNDJSON(filepath.Join(dir, "terms.ndjson"), d.Terms) },
		func() error { return WriteNDJSON(filepath.Join(dir, "comments.ndjson"), d.Comments) },
		func() error { return WriteNDJSON(filepath.Join(dir, "files.ndjson"), d.Files) },
		func() error { return WriteNDJSON(filepath.Join(dir, "fixes.ndjson"), d.Fixes) },
		func() error { return WriteJSON(filepath.Join(dir, "options.json"), d.Options) },
		func() error { return WriteJSON(filepath.Join(dir, "tables.json"), d.Tables) },
		func() error { return WriteJSON(filepath.Join(dir, "stats.json"), d.Stats) },
		func() error { return WriteJSON(filepath.Join(dir, "meta.json"), meta{Prefix: d.Prefix}) },
	}
	for _, s := range steps {
		if err := s(); err != nil {
			return err
		}
	}
	return nil
}

// Load reads a data set written by Save.
func Load(dir string) (*Dataset, error) {
	d := &Dataset{Options: map[string]any{}}
	var err error
	if _, serr := os.Stat(filepath.Join(dir, "meta.json")); serr != nil {
		return nil, fmt.Errorf("no extract in %s (run afsc-wp extract): %w", dir, serr)
	}
	if d.Posts, err = ReadNDJSON[*Post](filepath.Join(dir, "posts.ndjson")); err != nil {
		return nil, err
	}
	if d.Users, err = ReadNDJSON[*User](filepath.Join(dir, "users.ndjson")); err != nil {
		return nil, err
	}
	if d.Terms, err = ReadNDJSON[*Term](filepath.Join(dir, "terms.ndjson")); err != nil {
		return nil, err
	}
	if d.Comments, err = ReadNDJSON[*Comment](filepath.Join(dir, "comments.ndjson")); err != nil {
		return nil, err
	}
	if d.Files, err = ReadNDJSON[*File](filepath.Join(dir, "files.ndjson")); err != nil {
		return nil, err
	}
	if d.Fixes, err = ReadNDJSON[Fix](filepath.Join(dir, "fixes.ndjson")); err != nil {
		return nil, err
	}
	var m meta
	for name, v := range map[string]any{"options.json": &d.Options, "tables.json": &d.Tables, "stats.json": &d.Stats, "meta.json": &m} {
		if err := ReadJSON(filepath.Join(dir, name), v); err != nil {
			return nil, err
		}
	}
	d.Prefix = m.Prefix
	normalizeNumbers(d.Options)
	d.Sort()
	return d, nil
}

// normalizeNumbers turns JSON float64 integers back into int64, matching what phpser produces.
func normalizeNumbers(m map[string]any) {
	for k, v := range m {
		m[k] = normalize(v)
	}
}

func normalize(v any) any {
	switch x := v.(type) {
	case float64:
		if x == float64(int64(x)) {
			return int64(x)
		}
	case map[string]any:
		normalizeNumbers(x)
	case []any:
		for i := range x {
			x[i] = normalize(x[i])
		}
	}
	return v
}
