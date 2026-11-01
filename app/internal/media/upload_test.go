package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"testing"
)

func encoded(t *testing.T, format string, w, h int) []byte {
	t.Helper()
	img := image.NewPaletted(image.Rect(0, 0, w, h), color.Palette{color.Black, color.White})
	var b bytes.Buffer
	var err error
	switch format {
	case "png":
		err = png.Encode(&b, img)
	case "jpeg":
		err = jpeg.Encode(&b, img, nil)
	case "gif":
		err = gif.Encode(&b, img, nil)
	}
	if err != nil {
		t.Fatal(err)
	}
	return b.Bytes()
}

// isoBox builds an ISO-BMFF box.
func isoBox(typ string, payload ...[]byte) []byte {
	body := bytes.Join(payload, nil)
	out := binary.BigEndian.AppendUint32(nil, uint32(8+len(body)))
	return append(append(out, typ...), body...)
}

func u32(vs ...uint32) []byte {
	var out []byte
	for _, v := range vs {
		out = binary.BigEndian.AppendUint32(out, v)
	}
	return out
}

// avifFixture is the box skeleton of an AVIF file: ftyp, then meta/iprp/ipco/ispe with the image size.
func avifFixture(w, h uint32) []byte {
	ftyp := isoBox("ftyp", []byte("avif"), u32(0), []byte("mif1avifmiaf"))
	ispe := isoBox("ispe", u32(0, w, h))
	meta := isoBox("meta", u32(0), isoBox("hdlr", u32(0, 0), []byte("pict"), u32(0, 0, 0), []byte{0}),
		isoBox("iprp", isoBox("ipco", isoBox("pixi", u32(0), []byte{3, 8, 8, 8}), ispe)))
	return append(append(ftyp, meta...), isoBox("mdat", []byte{1, 2, 3})...)
}

// webpFixture is a lossless WebP header: the VP8L signature, then 14-bit width-1 and height-1.
func webpFixture(w, h uint32) []byte {
	bits := binary.LittleEndian.AppendUint32(nil, (w-1)|(h-1)<<14)
	chunk := append(append([]byte("VP8L"), binary.LittleEndian.AppendUint32(nil, 5)...), append([]byte{0x2f}, bits...)...)
	chunk = append(chunk, 0) // pad to an even length
	body := append([]byte("WEBP"), chunk...)
	return append(append([]byte("RIFF"), binary.LittleEndian.AppendUint32(nil, uint32(len(body)))...), body...)
}

func TestSniff(t *testing.T) {
	mp4 := isoBox("ftyp", []byte("isom"), u32(0x200), []byte("isomiso2avc1mp41"))
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"jpeg", encoded(t, "jpeg", 4, 3), "image/jpeg"},
		{"png", encoded(t, "png", 4, 3), "image/png"},
		{"gif", encoded(t, "gif", 4, 3), "image/gif"},
		{"webp", webpFixture(3, 2), "image/webp"},
		{"avif", avifFixture(640, 480), "image/avif"},
		{"mp4", mp4, "video/mp4"},
		{"webm", append([]byte{0x1A, 0x45, 0xDF, 0xA3, 0x9F, 0x42, 0x82, 0x84}, "webm"...), "video/webm"},
		{"mp3 with id3", []byte("ID3\x04\x00\x00\x00\x00\x00\x00"), "audio/mpeg"},
		{"mp3 frame", []byte{0xFF, 0xFB, 0x90, 0x64, 0, 0}, "audio/mpeg"},
		{"pdf", []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n"), "application/pdf"},
		{"svg", []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 1 1"/>`), "image/svg+xml"},
		{"svg with prolog", []byte("\xef\xbb\xbf<?xml version=\"1.0\"?>\n<!-- logo -->\n<!DOCTYPE svg>\n<svg>"), "image/svg+xml"},
		{"html", []byte("<!DOCTYPE html><html><body><svg></svg></body></html>"), ""},
		{"svg-ish html", []byte("<svgx>"), ""},
		{"plain text", []byte("hello"), ""},
		{"matroska", append([]byte{0x1A, 0x45, 0xDF, 0xA3}, "matroska"...), ""},
		{"quicktime", isoBox("ftyp", []byte("qt  "), u32(0), []byte("qt  ")), ""},
		{"heic", isoBox("ftyp", []byte("heic"), u32(0), []byte("mif1heic")), ""},
		{"m4a", isoBox("ftyp", []byte("M4A "), u32(0), []byte("M4A mp42isom")), ""},
		{"zip", []byte("PK\x03\x04"), ""},
		// a GIF that is also an HTML page is stored, and later served, as a GIF
		{"gif/html polyglot", append(encoded(t, "gif", 1, 1), "<html><script>alert(1)</script>"...), "image/gif"},
		// an HTML page renamed .jpg is still HTML
		{"html named jpg", []byte("<html><img src=x onerror=alert(1)>"), ""},
	}
	for _, c := range cases {
		if got := Sniff(c.head); got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, got, c.want)
		}
	}
}

func TestCleanFilename(t *testing.T) {
	cases := []struct{ name, mime, want string }{
		{"photo.jpg", "image/jpeg", "photo.jpg"},
		{"Photo.JPEG", "image/jpeg", "Photo.JPEG"},
		{"photo.jpg", "image/png", "photo.png"},
		{"page.html", "image/gif", "page.gif"},
		{"../../etc/passwd", "application/pdf", "passwd.pdf"},
		{`C:\Users\a\report.pdf`, "application/pdf", "report.pdf"},
		{"", "image/png", "file.png"},
		{".htaccess", "image/png", "file.png"},
		{"a\x00b\nc.mp3", "audio/mpeg", "abc.mp3"},
	}
	for _, c := range cases {
		if got := CleanFilename(c.name, c.mime); got != c.want {
			t.Errorf("CleanFilename(%q, %s) = %q, want %q", c.name, c.mime, got, c.want)
		}
	}
}

func TestDimensions(t *testing.T) {
	cases := []struct {
		mime string
		head []byte
		w, h int
	}{
		{"image/jpeg", encoded(t, "jpeg", 40, 30), 40, 30},
		{"image/png", encoded(t, "png", 41, 31), 41, 31},
		{"image/gif", encoded(t, "gif", 42, 32), 42, 32},
		{"image/webp", webpFixture(43, 33), 43, 33},
		{"image/avif", avifFixture(1920, 1080), 1920, 1080},
	}
	for _, c := range cases {
		w, h, ok := Dimensions(c.mime, c.head)
		if !ok || w != c.w || h != c.h {
			t.Errorf("%s: %dx%d %v, want %dx%d", c.mime, w, h, ok, c.w, c.h)
		}
	}
	if _, _, ok := Dimensions("image/avif", avifFixture(1, 1)[:30]); ok {
		t.Error("truncated avif: got dimensions")
	}
	if _, _, ok := Dimensions("application/pdf", []byte("%PDF-1.7")); ok {
		t.Error("pdf: got dimensions")
	}
}
