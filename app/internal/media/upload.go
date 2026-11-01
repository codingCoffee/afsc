package media

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"net/http"
	"path"
	"strings"
	"unicode"
	"unicode/utf8"

	"afsc/internal/settings"
	"afsc/internal/storage"
)

// Class groups the allowed upload types (§7.4); each class has its own size limit.
type Class string

const (
	ClassImage Class = "image"
	ClassVideo Class = "video"
	ClassAudio Class = "audio"
	ClassPDF   Class = "pdf"
)

const svgType = "image/svg+xml"

// allowed maps every uploadable type to its class and canonical extension.
var allowed = map[string]struct {
	class Class
	ext   string
}{
	"image/jpeg": {ClassImage, ".jpg"}, "image/png": {ClassImage, ".png"}, "image/gif": {ClassImage, ".gif"},
	"image/webp": {ClassImage, ".webp"}, "image/avif": {ClassImage, ".avif"}, svgType: {ClassImage, ".svg"},
	"video/mp4": {ClassVideo, ".mp4"}, "video/webm": {ClassVideo, ".webm"}, "audio/mpeg": {ClassAudio, ".mp3"},
	"application/pdf": {ClassPDF, ".pdf"},
}

// ClassOf is the class of an allowed type, or "" for any other.
func ClassOf(mime string) Class { return allowed[mime].class }

// Sniff returns the allowlisted type of a file from its first 512 bytes, or "" when the content isn't one of them.
// The declared Content-Type and the file name are never trusted.
func Sniff(head []byte) string {
	if len(head) > 512 {
		head = head[:512]
	}
	switch {
	case len(head) >= 12 && string(head[4:8]) == "ftyp":
		return ftypType(head)
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return "image/webp"
	case bytes.HasPrefix(head, []byte{0x1A, 0x45, 0xDF, 0xA3}):
		if bytes.Contains(head, []byte("webm")) {
			return "video/webm"
		}
		return ""
	case bytes.HasPrefix(head, []byte("ID3")), mp3Frame(head):
		return "audio/mpeg"
	case isSVG(head):
		return svgType
	}
	ct, _, _ := strings.Cut(http.DetectContentType(head), ";")
	switch ct {
	case "image/jpeg", "image/png", "image/gif", "application/pdf":
		return ct
	}
	return ""
}

// mp3Frame matches an MPEG audio Layer III frame header with no ID3 tag in front.
func mp3Frame(b []byte) bool {
	return len(b) >= 4 && b[0] == 0xFF && b[1]&0xE0 == 0xE0 && (b[1]>>1)&3 == 1 && (b[1]>>3)&3 != 1 && b[2]>>4 != 0xF
}

// ftypType tells AVIF from MP4 by the ISO-BMFF brands; QuickTime, HEIC, M4A and other brands are refused.
func ftypType(b []byte) string {
	major := string(b[8:12])
	switch major {
	case "qt  ", "M4A ", "M4B ", "heic", "heix":
		return ""
	}
	end := min(int(binary.BigEndian.Uint32(b)), len(b))
	brands := []string{major}
	for i := 16; i+4 <= end; i += 4 {
		brands = append(brands, string(b[i:i+4]))
	}
	mp4 := false
	for _, br := range brands {
		switch br {
		case "avif", "avis":
			return "image/avif"
		case "isom", "iso2", "iso4", "iso5", "iso6", "mp41", "mp42", "avc1", "dash", "M4V ", "mmp4":
			mp4 = true
		}
	}
	if mp4 {
		return "video/mp4"
	}
	return ""
}

// isSVG reports whether the document's root element is <svg>, skipping an XML declaration, comments and a doctype.
// HTML with inline SVG is not an SVG file.
func isSVG(b []byte) bool {
	b = bytes.TrimPrefix(b, []byte("\xef\xbb\xbf"))
	for {
		b = bytes.TrimLeft(b, " \t\r\n")
		var end []byte
		switch {
		case bytes.HasPrefix(b, []byte("<?")):
			end = []byte("?>")
		case bytes.HasPrefix(b, []byte("<!--")):
			end = []byte("-->")
		case len(b) >= 9 && strings.EqualFold(string(b[:9]), "<!doctype"):
			end = []byte(">")
		default:
			return len(b) > 4 && strings.EqualFold(string(b[:4]), "<svg") && strings.IndexByte(" \t\r\n>/", b[4]) >= 0
		}
		i := bytes.Index(b, end)
		if i < 0 {
			return false
		}
		b = b[i+len(end):]
	}
}

// limitBytes is the size limit for a type from the media settings.
func limitBytes(mime string, l settings.Media) int64 {
	mb := 0
	switch ClassOf(mime) {
	case ClassImage:
		mb = l.MaxImageMB
	case ClassVideo:
		mb = l.MaxVideoMB
	case ClassAudio:
		mb = l.MaxAudioMB
	case ClassPDF:
		mb = l.MaxPDFMB
	}
	return int64(mb) << 20
}

// CleanFilename keeps the last path segment of an uploaded name, drops control characters, and gives it the
// extension of the sniffed type when its own doesn't match.
func CleanFilename(name, mime string) string {
	name = path.Base(strings.ReplaceAll(name, `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || r == utf8.RuneError {
			return -1
		}
		return r
	}, strings.TrimSpace(name))
	if name == "." || name == "/" {
		name = ""
	}
	if name == "" || strings.HasPrefix(name, ".") {
		name = "file" + name
	}
	ext := path.Ext(name)
	if extTypes[strings.ToLower(ext)] != mime {
		name = strings.TrimSuffix(name, ext) + allowed[mime].ext
	}
	if utf8.RuneCountInString(name) > 200 {
		r := []rune(strings.TrimSuffix(name, path.Ext(name)))
		name = string(r[:190]) + path.Ext(name)
	}
	return name
}

// Upload is one file sent as a raw request body of known size.
type Upload struct {
	Body     io.Reader
	Size     int64
	Filename string
	AllowSVG bool // content.raw_html holders only (§7.4)
	Limits   settings.Media
}

// object is an upload that is now in storage under Key.
type object struct {
	Key      string
	Filename string
	Mime     string
	Bytes    int64
	SHA256   []byte
	Width    int
	Height   int
}

type counter struct{ n int64 }

func (c *counter) Write(p []byte) (int, error) {
	c.n += int64(len(p))
	return len(p), nil
}

// put sniffs and checks an upload, then streams it to storage while hashing it. Nothing touches disk.
func (a *Admin) put(ctx context.Context, up Upload) (*object, error) {
	if up.Size <= 0 {
		return nil, errf(http.StatusBadRequest, "empty", "the file is empty")
	}
	br := bufio.NewReaderSize(up.Body, dimsPeek)
	head, err := br.Peek(512)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	mime := Sniff(head)
	switch {
	case mime == "":
		return nil, errf(http.StatusUnsupportedMediaType, "type",
			"this file type isn't allowed: upload JPEG, PNG, GIF, WebP, AVIF, MP4, WebM, MP3 or PDF")
	case mime == svgType && !up.AllowSVG:
		return nil, errf(http.StatusForbidden, "capability", "only admins can upload SVG files")
	case up.Size > limitBytes(mime, up.Limits):
		return nil, errf(http.StatusRequestEntityTooLarge, "too_large", "the file is larger than the %d MB limit for %s files",
			limitBytes(mime, up.Limits)>>20, ClassOf(mime))
	}
	o := &object{Filename: CleanFilename(up.Filename, mime), Mime: mime, Bytes: up.Size}
	if ClassOf(mime) == ClassImage {
		peek, _ := br.Peek(dimsPeek)
		o.Width, o.Height, _ = Dimensions(mime, peek)
	}
	if o.Key, err = storage.NewKey(); err != nil {
		return nil, err
	}
	h, n := sha256.New(), &counter{}
	body := io.TeeReader(io.LimitReader(br, up.Size), io.MultiWriter(h, n))
	if err := a.Storage.PutStream(ctx, o.Key, body, up.Size, mime); err != nil {
		return nil, err
	}
	if n.n != up.Size {
		a.remove(ctx, o.Key)
		return nil, errf(http.StatusBadRequest, "truncated", "the upload ended early")
	}
	o.SHA256 = h.Sum(nil)
	return o, nil
}

// remove deletes a stored object that no row refers to; a failure only leaves an orphan behind.
func (a *Admin) remove(ctx context.Context, key string) {
	if err := a.Storage.Delete(context.WithoutCancel(ctx), key); err != nil {
		a.logf("media: deleting object %s: %v", key, err)
	}
}
