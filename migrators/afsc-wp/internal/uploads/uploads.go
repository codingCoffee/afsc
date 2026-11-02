// Package uploads scans a copy of wp-content/uploads. Only media types AFSC accepts are hashed and imported;
// everything else is listed for the human, because it may be publicly reachable on production today.
package uploads

import (
	"crypto/sha256"
	"encoding/hex"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	_ "golang.org/x/image/webp"

	"afsc/migrators/afsc-wp/internal/extract"
)

// MediaTypes are the extensions AFSC's importer accepts, with the MIME type each must sniff as.
var MediaTypes = map[string]string{
	".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".jpe": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
	".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml", ".mp4": "video/mp4", ".m4v": "video/mp4",
	".webm": "video/webm", ".pdf": "application/pdf", ".mp3": "audio/mpeg",
}

// unsupportedMedia are media files AFSC can't store yet; they are reported separately from non-media.
var unsupportedMedia = map[string]bool{
	".mov": true, ".ogg": true, ".ogv": true, ".oga": true, ".wav": true, ".m4a": true, ".wmv": true, ".flv": true,
	".bmp": true, ".tif": true, ".tiff": true, ".ico": true, ".heic": true, ".aac": true, ".flac": true, ".3gp": true,
}

const (
	ReasonNotMedia    = "not a media file"
	ReasonUnsupported = "media type AFSC does not accept"
	ReasonHidden      = "dotfile or hidden directory"
	ReasonSymlink     = "symlink (not followed)"
	ReasonMimeChanged = "content does not match extension; using sniffed type"
	ReasonMismatch    = "content does not match an accepted media type"
)

// Scan walks root and returns every regular file, sorted by path.
func Scan(root string) ([]*extract.File, error) {
	var out []*extract.File
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if p == root {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			return nil
		}
		f := &extract.File{Path: rel}
		info, err := d.Info()
		if err != nil {
			return err
		}
		f.Size = info.Size()
		switch {
		case d.Type()&fs.ModeSymlink != 0:
			f.Reason = ReasonSymlink
		case !d.Type().IsRegular():
			f.Reason = ReasonNotMedia
		case hidden(rel):
			f.Reason = ReasonHidden
		default:
			classify(p, f)
		}
		out = append(out, f)
		return nil
	})
	return out, err
}

func hidden(rel string) bool {
	for _, seg := range strings.Split(rel, "/") {
		if strings.HasPrefix(seg, ".") {
			return true
		}
	}
	return false
}

func classify(full string, f *extract.File) {
	ext := strings.ToLower(path.Ext(f.Path))
	mime, ok := MediaTypes[ext]
	if !ok {
		f.Reason = ReasonNotMedia
		if unsupportedMedia[ext] {
			f.Reason = ReasonUnsupported
		}
		return
	}
	fh, err := os.Open(full)
	if err != nil {
		f.Reason = "unreadable: " + err.Error()
		return
	}
	defer fh.Close()
	head := make([]byte, 512)
	n, _ := io.ReadFull(fh, head)
	sniffed, _, _ := strings.Cut(http.DetectContentType(head[:n]), ";")
	switch {
	case sniffed == mime:
	case isAccepted(sniffed):
		f.Reason = ReasonMimeChanged
		mime = sniffed
	case mime == "image/svg+xml" || mime == "image/avif" || mime == "video/webm":
		// DetectContentType can't identify these reliably; the importer only rejects accepted-but-different types.
	default:
		f.Reason = ReasonMismatch
		return
	}
	h := sha256.New()
	h.Write(head[:n])
	if _, err := io.Copy(h, fh); err != nil {
		f.Reason = "unreadable: " + err.Error()
		return
	}
	f.Media, f.Mime, f.SHA256 = true, mime, hex.EncodeToString(h.Sum(nil))
	if strings.HasPrefix(mime, "image/") && mime != "image/svg+xml" && mime != "image/avif" {
		if _, err := fh.Seek(0, io.SeekStart); err == nil {
			if cfg, _, err := image.DecodeConfig(fh); err == nil {
				f.Width, f.Height = cfg.Width, cfg.Height
			}
		}
	}
}

func isAccepted(mime string) bool {
	for _, m := range MediaTypes {
		if m == mime {
			return true
		}
	}
	return false
}

// Dimensions decodes the size of an image file, 0, 0 when it can't.
func Dimensions(r io.Reader) (int, int) {
	cfg, _, err := image.DecodeConfig(r)
	if err != nil {
		return 0, 0
	}
	return cfg.Width, cfg.Height
}
