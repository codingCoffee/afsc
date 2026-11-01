package media

import (
	"bytes"
	"encoding/binary"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"

	"golang.org/x/image/webp"
)

// dimsPeek is how much of the file is read for dimensions. A JPEG whose metadata pushes the frame header past it
// gets no dimensions.
const dimsPeek = 64 << 10

// Dimensions reads an image's width and height from the start of the file. ok is false for other types and for
// headers it can't parse.
func Dimensions(mime string, head []byte) (w, h int, ok bool) {
	var cfg image.Config
	var err error
	r := bytes.NewReader(head)
	switch mime {
	case "image/jpeg":
		cfg, err = jpeg.DecodeConfig(r)
	case "image/png":
		cfg, err = png.DecodeConfig(r)
	case "image/gif":
		cfg, err = gif.DecodeConfig(r)
	case "image/webp":
		cfg, err = webp.DecodeConfig(r)
	case "image/avif":
		w, h = avifDims(head)
		return w, h, w > 0 && h > 0
	default:
		return 0, 0, false
	}
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return 0, 0, false
	}
	return cfg.Width, cfg.Height, true
}

// avifDims finds the first ispe (image spatial extent) property: meta → iprp → ipco → ispe. The first one belongs
// to the primary image in files written by every common encoder.
func avifDims(b []byte) (int, int) {
	var walk func(b []byte, path []string) (int, int)
	walk = func(b []byte, path []string) (int, int) {
		for len(b) >= 8 {
			size := int64(binary.BigEndian.Uint32(b))
			typ := string(b[4:8])
			hdr := int64(8)
			switch size {
			case 0:
				size = int64(len(b))
			case 1:
				if len(b) < 16 {
					return 0, 0
				}
				size, hdr = int64(binary.BigEndian.Uint64(b[8:16])), 16
			}
			if size < hdr || size > int64(len(b)) {
				return 0, 0
			}
			body := b[hdr:size]
			switch {
			case len(path) == 0 && typ == "meta" && len(body) >= 4:
				// meta is a full box: skip version and flags
				if w, h := walk(body[4:], []string{"meta"}); w > 0 {
					return w, h
				}
			case len(path) == 1 && typ == "iprp", len(path) == 2 && typ == "ipco":
				if w, h := walk(body, append(path, typ)); w > 0 {
					return w, h
				}
			case len(path) == 3 && typ == "ispe" && len(body) >= 12:
				return int(binary.BigEndian.Uint32(body[4:8])), int(binary.BigEndian.Uint32(body[8:12]))
			}
			b = b[size:]
		}
		return 0, 0
	}
	return walk(b, nil)
}
