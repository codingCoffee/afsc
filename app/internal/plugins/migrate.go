package plugins

import (
	"fmt"
	"io/fs"
	"regexp"
	"strings"
)

var (
	migFileRe = regexp.MustCompile(`^[0-9]+_[a-z0-9_]+\.(up|down)\.sql$`)
	// objects a migration names: CREATE/ALTER/DROP [UNIQUE] TABLE|INDEX|SEQUENCE|TYPE [CONCURRENTLY] [IF [NOT] EXISTS] name
	objectRe = regexp.MustCompile(`(?is)\b(?:create|alter|drop)\s+(?:unique\s+)?(?:table|index|sequence|type)\s+` +
		`(?:concurrently\s+)?(?:if\s+(?:not\s+)?exists\s+)?(?:only\s+)?("?[a-z0-9_.]+"?)`)
)

// CheckMigrations makes sure every migration file is named for golang-migrate and every object it creates,
// alters or drops is prefixed plg_<id>_. It reads the embedded files only.
func CheckMigrations(id string, fsys fs.FS) error {
	prefix := "plg_" + id + "_"
	files, err := fs.ReadDir(fsys, ".")
	if err != nil {
		return fmt.Errorf("migrations: %w", err)
	}
	n := 0
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".sql") {
			continue
		}
		if !migFileRe.MatchString(f.Name()) {
			return fmt.Errorf("migrations: %s is not named NNNN_name.up.sql or .down.sql", f.Name())
		}
		b, err := fs.ReadFile(fsys, f.Name())
		if err != nil {
			return fmt.Errorf("migrations: %w", err)
		}
		for _, m := range objectRe.FindAllStringSubmatch(stripComments(string(b)), -1) {
			name := strings.ToLower(strings.Trim(m[1], `"`))
			if i := strings.LastIndexByte(name, '.'); i >= 0 {
				name = name[i+1:]
			}
			if !strings.HasPrefix(name, prefix) {
				return fmt.Errorf("migrations: %s: %q must be named %s…", f.Name(), name, prefix)
			}
		}
		n++
	}
	if n == 0 {
		return fmt.Errorf("migrations: no .sql files")
	}
	return nil
}

func stripComments(sql string) string {
	var b strings.Builder
	for line := range strings.SplitSeq(sql, "\n") {
		if i := strings.Index(line, "--"); i >= 0 {
			line = line[:i]
		}
		b.WriteString(line + "\n")
	}
	return b.String()
}
