package uploads

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var b bytes.Buffer
	if err := png.Encode(&b, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

func TestScan(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{
		"2021/03/photo.png":         pngBytes(t, 4, 3),
		"2021/03/disguised.jpg":     pngBytes(t, 2, 2),
		"2021/03/shell.php":         []byte("<?php echo 1;"),
		"backup/db.sql":             []byte("-- dump"),
		".htaccess":                 []byte("deny"),
		"2021/03/clip.mov":          []byte("moov"),
		"2021/03/notes.pdf":         []byte("%PDF-1.4\n"),
		"cache/.hidden/x.png":       pngBytes(t, 1, 1),
		"2021/03/text-as-image.png": []byte("hello world, not an image"),
	}
	for p, b := range files {
		full := filepath.Join(root, filepath.FromSlash(p))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := Scan(root)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]bool{}
	for _, f := range got {
		by[f.Path] = true
		switch f.Path {
		case "2021/03/photo.png":
			if !f.Media || f.Width != 4 || f.Height != 3 || f.Mime != "image/png" || len(f.SHA256) != 64 {
				t.Errorf("photo: %+v", f)
			}
		case "2021/03/disguised.jpg":
			if !f.Media || f.Mime != "image/png" || f.Reason != ReasonMimeChanged {
				t.Errorf("disguised: %+v", f)
			}
		case "2021/03/notes.pdf":
			if !f.Media || f.Mime != "application/pdf" {
				t.Errorf("pdf: %+v", f)
			}
		case "2021/03/shell.php", "backup/db.sql":
			if f.Media || f.Reason != ReasonNotMedia || f.SHA256 != "" {
				t.Errorf("non-media: %+v", f)
			}
		case ".htaccess", "cache/.hidden/x.png":
			if f.Media || f.Reason != ReasonHidden {
				t.Errorf("hidden: %+v", f)
			}
		case "2021/03/clip.mov":
			if f.Media || f.Reason != ReasonUnsupported {
				t.Errorf("mov: %+v", f)
			}
		case "2021/03/text-as-image.png":
			if f.Media || f.Reason != ReasonMismatch {
				t.Errorf("mismatch: %+v", f)
			}
		}
	}
	if len(by) != len(files) {
		t.Fatalf("scanned %d files, want %d", len(by), len(files))
	}
}
