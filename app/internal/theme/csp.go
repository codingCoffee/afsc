package theme

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/net/html"
)

// InlineScriptHashes returns 'sha256-…' CSP sources for every executable inline <script> in an HTML fragment.
func InlineScriptHashes(doc []byte) []string {
	z := html.NewTokenizer(bytes.NewReader(doc))
	var out []string
	inScript := false
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken:
			name, hasAttr := z.TagName()
			if string(name) != "script" {
				continue
			}
			inScript = true
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				switch string(k) {
				case "src":
					inScript = false
				case "type":
					if t := strings.ToLower(string(v)); t != "" && t != "module" && t != "text/javascript" {
						inScript = false // data blocks such as application/ld+json never execute
					}
				}
			}
		case html.TextToken:
			if inScript {
				sum := sha256.Sum256(z.Raw())
				out = append(out, "'sha256-"+base64.StdEncoding.EncodeToString(sum[:])+"'")
			}
		case html.EndTagToken:
			inScript = false
		}
	}
}

// templateScriptHashes hashes the inline scripts of every template under tdir. Only static script bodies match
// what the browser sees, so templates must not put actions inside an inline script.
func templateScriptHashes(tdir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(tdir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".html") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		out = append(out, InlineScriptHashes(b)...)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	slices.Sort(out)
	return slices.Compact(out), nil
}

// InlineScripts returns the current manifest and the CSP hashes of the inline scripts in the templates. The
// manifest pointer changes whenever the theme reloads.
func (t *Theme) InlineScripts() (*Manifest, []string) {
	st, _ := t.current()
	return st.manifest, st.scripts
}
