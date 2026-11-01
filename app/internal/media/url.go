// Package media serves /m/ URLs and legacy upload paths (build plan §9, §9.1).
package media

import (
	"net/url"
	"path"
	"regexp"
	"strconv"
	"strings"
)

// Prefix starts every versioned media URL.
const Prefix = "/m/"

// Path is a parsed /m/{id}/{v}/{variant}/{filename} URL. Filename is unescaped.
type Path struct {
	ID       int64
	V        string
	Variant  string
	Filename string
}

var (
	idRe      = regexp.MustCompile(`^[1-9][0-9]{0,18}$`)
	versionRe = regexp.MustCompile(`^[0-9a-f]{8}$`)
	variantRe = regexp.MustCompile(`^(orig|w[1-9][0-9]{0,4}|c[1-9][0-9]{0,4}x[1-9][0-9]{0,4})$`)
)

// ParsePath parses the escaped request path, so a %2F inside the filename stays in the filename.
func ParsePath(escaped string) (Path, bool) {
	rest, ok := strings.CutPrefix(escaped, Prefix)
	if !ok {
		return Path{}, false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 4 || !idRe.MatchString(parts[0]) || !versionRe.MatchString(parts[1]) || !variantRe.MatchString(parts[2]) {
		return Path{}, false
	}
	name, err := url.PathUnescape(parts[3])
	if err != nil || name == "" {
		return Path{}, false
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return Path{}, false
	}
	return Path{ID: id, V: parts[1], Variant: parts[2], Filename: name}, true
}

// fileLike reports whether the last path segment has an extension; only those are looked up as legacy paths.
func fileLike(p string) bool {
	ext := path.Ext(p)
	return len(ext) > 1 && len(ext) <= 6 && !strings.HasSuffix(p, "/")
}
