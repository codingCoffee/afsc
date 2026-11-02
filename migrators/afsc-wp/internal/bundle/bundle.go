// Package bundle writes the AFSC import bundle (docs/import-format.md) from work/transform. The same input
// gives a byte-identical bundle: records are sorted, JSON has no timestamps of its own, files are named by hash.
package bundle

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"afsc/migrators/afsc-wp/internal/extract"
	"afsc/migrators/afsc-wp/internal/mediaidx"
	"afsc/migrators/afsc-wp/internal/records"
	"afsc/migrators/afsc-wp/internal/transform"
)

// Marker identifies a directory afsc-wp owns, so a re-run may replace its contents.
const Marker = ".afsc-wp-bundle"

var ndjsonFiles = []string{"users.ndjson", "media.ndjson", "authors.ndjson", "terms.ndjson", "posts.ndjson",
	"comments.ndjson", "menus.ndjson", "redirects.ndjson", "legacy.ndjson"}

// prepare refuses to write into a non-empty directory afsc-wp didn't create.
func prepare(out string) error {
	entries, err := os.ReadDir(out)
	if errors.Is(err, fs.ErrNotExist) {
		return os.MkdirAll(out, 0o700)
	}
	if err != nil {
		return err
	}
	if len(entries) == 0 {
		return nil
	}
	if _, err := os.Stat(filepath.Join(out, Marker)); err != nil {
		return fmt.Errorf("%s is not empty and was not written by afsc-wp; choose another bundle_dir", out)
	}
	return nil
}

// Write builds the bundle in out from the transform in workDir, copying originals from uploadsDir.
func Write(workDir, uploadsDir, out string) (map[string]int, error) {
	l, err := transform.Load(workDir)
	if err != nil {
		return nil, fmt.Errorf("read work/transform (run afsc-wp transform first): %w", err)
	}
	if err := prepare(out); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(out, Marker), []byte("written by afsc-wp\n"), 0o600); err != nil {
		return nil, err
	}
	keep := map[string]bool{}
	for _, m := range l.Media {
		src, ok := l.Sources[m.LegacyKey]
		if !ok {
			return nil, fmt.Errorf("%s: no source file recorded", m.LegacyKey)
		}
		ext := strings.ToLower(path.Ext(src))
		rel := "files/" + m.SHA256[:2] + "/" + m.SHA256 + ext
		if err := copyVerified(filepath.Join(uploadsDir, filepath.FromSlash(src)), filepath.Join(out, filepath.FromSlash(rel)), m.SHA256); err != nil {
			return nil, fmt.Errorf("%s: %w", m.LegacyKey, err)
		}
		m.File = rel
		keep[rel] = true
	}
	if err := prune(out, keep); err != nil {
		return nil, err
	}
	sortAll(l)
	counts := map[string]int{"users": len(l.Users), "media": len(l.Media), "authors": len(l.Authors), "terms": len(l.Terms),
		"posts": len(l.Posts), "comments": len(l.Comments), "menus": len(l.Menus), "redirects": len(l.Redirects), "legacy": len(l.Legacy)}
	writes := map[string]func(string) error{
		"users.ndjson":     func(p string) error { return extract.WriteNDJSON(p, l.Users) },
		"media.ndjson":     func(p string) error { return extract.WriteNDJSON(p, l.Media) },
		"authors.ndjson":   func(p string) error { return extract.WriteNDJSON(p, l.Authors) },
		"terms.ndjson":     func(p string) error { return extract.WriteNDJSON(p, l.Terms) },
		"posts.ndjson":     func(p string) error { return extract.WriteNDJSON(p, l.Posts) },
		"comments.ndjson":  func(p string) error { return extract.WriteNDJSON(p, l.Comments) },
		"menus.ndjson":     func(p string) error { return extract.WriteNDJSON(p, l.Menus) },
		"redirects.ndjson": func(p string) error { return extract.WriteNDJSON(p, l.Redirects) },
		"legacy.ndjson":    func(p string) error { return extract.WriteNDJSON(p, l.Legacy) },
	}
	for _, name := range ndjsonFiles {
		if err := writes[name](filepath.Join(out, name)); err != nil {
			return nil, err
		}
	}
	if err := os.WriteFile(filepath.Join(out, "settings.json"), l.Settings, 0o600); err != nil {
		return nil, err
	}
	m := records.Manifest{FormatVersion: 1, Source: l.Meta.Source, GeneratedAt: l.Meta.GeneratedAt, Counts: counts, Theme: l.Meta.Theme}
	return counts, extract.WriteJSON(filepath.Join(out, "manifest.json"), m)
}

func sortAll(l *transform.Loaded) {
	less := mediaidx.KeyLess
	sort.Slice(l.Users, func(i, j int) bool { return less(l.Users[i].LegacyKey, l.Users[j].LegacyKey) })
	sort.Slice(l.Media, func(i, j int) bool { return less(l.Media[i].LegacyKey, l.Media[j].LegacyKey) })
	sort.Slice(l.Authors, func(i, j int) bool { return less(l.Authors[i].LegacyKey, l.Authors[j].LegacyKey) })
	sort.Slice(l.Terms, func(i, j int) bool { return less(l.Terms[i].LegacyKey, l.Terms[j].LegacyKey) })
	sort.Slice(l.Posts, func(i, j int) bool { return less(l.Posts[i].LegacyKey, l.Posts[j].LegacyKey) })
	sort.Slice(l.Comments, func(i, j int) bool { return less(l.Comments[i].LegacyKey, l.Comments[j].LegacyKey) })
	sort.Slice(l.Menus, func(i, j int) bool { return less(l.Menus[i].LegacyKey, l.Menus[j].LegacyKey) })
	sort.Slice(l.Redirects, func(i, j int) bool { return l.Redirects[i].LegacyKey < l.Redirects[j].LegacyKey })
	sort.Slice(l.Legacy, func(i, j int) bool { return less(l.Legacy[i].LegacyKey, l.Legacy[j].LegacyKey) })
}

// copyVerified copies src to dst unless dst already holds the same bytes, and checks the hash either way.
func copyVerified(src, dst, want string) error {
	if got, err := hashFile(dst); err == nil && got == want {
		return nil
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return err
	}
	tmp := dst + ".tmp"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(out, h), in); err != nil {
		out.Close()
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		os.Remove(tmp)
		return fmt.Errorf("%s changed since extract (sha256 %s, want %s); re-run extract", src, got, want)
	}
	return os.Rename(tmp, dst)
}

func hashFile(p string) (string, error) {
	f, err := os.Open(p)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// prune removes files under out/files that this bundle no longer references. Nothing outside files/ is touched.
func prune(out string, keep map[string]bool) error {
	root := filepath.Join(out, "files")
	return filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil || d.IsDir() {
			return err
		}
		rel, _ := filepath.Rel(out, p)
		if !keep[filepath.ToSlash(rel)] {
			return os.Remove(p)
		}
		return nil
	})
}
