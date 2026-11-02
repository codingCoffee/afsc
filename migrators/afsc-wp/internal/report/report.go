// Package report writes the Markdown reports under reports/. Reports never contain emails, IPs or hashes;
// records are referenced by legacy key.
package report

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type Doc struct {
	b strings.Builder
}

func New(title string) *Doc {
	d := &Doc{}
	d.b.WriteString("# " + title + "\n\n")
	return d
}

func (d *Doc) H2(s string) *Doc { d.b.WriteString("## " + s + "\n\n"); return d }

func (d *Doc) H3(s string) *Doc { d.b.WriteString("### " + s + "\n\n"); return d }

func (d *Doc) P(format string, args ...any) *Doc {
	d.b.WriteString(fmt.Sprintf(format, args...) + "\n\n")
	return d
}

func (d *Doc) List(items []string) *Doc {
	if len(items) == 0 {
		d.b.WriteString("_None._\n\n")
		return d
	}
	for _, it := range items {
		d.b.WriteString("- " + it + "\n")
	}
	d.b.WriteString("\n")
	return d
}

// Cell escapes a table cell.
func Cell(s string) string {
	s = strings.ReplaceAll(s, "|", `\|`)
	return strings.ReplaceAll(s, "\n", " ")
}

func (d *Doc) Table(head []string, rows [][]string) *Doc {
	if len(rows) == 0 {
		d.b.WriteString("_None._\n\n")
		return d
	}
	d.b.WriteString("| " + strings.Join(head, " | ") + " |\n|")
	for range head {
		d.b.WriteString("---|")
	}
	d.b.WriteString("\n")
	for _, r := range rows {
		cells := make([]string, len(r))
		for i, c := range r {
			cells[i] = Cell(c)
		}
		d.b.WriteString("| " + strings.Join(cells, " | ") + " |\n")
	}
	d.b.WriteString("\n")
	return d
}

func (d *Doc) String() string { return d.b.String() }

func (d *Doc) Write(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(d.b.String()), 0o600)
}

// Samples returns at most n items, with a "+k more" marker.
func Samples(items []string, n int) string {
	if len(items) <= n {
		return strings.Join(items, ", ")
	}
	return strings.Join(items[:n], ", ") + fmt.Sprintf(" (+%d more)", len(items)-n)
}

// SortedCounts returns map entries sorted by count desc, then key.
func SortedCounts(m map[string]int) [][2]string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if m[keys[i]] != m[keys[j]] {
			return m[keys[i]] > m[keys[j]]
		}
		return keys[i] < keys[j]
	})
	out := make([][2]string, len(keys))
	for i, k := range keys {
		out[i] = [2]string{k, fmt.Sprint(m[k])}
	}
	return out
}
