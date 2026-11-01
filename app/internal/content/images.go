package content

import (
	"math"
	"slices"
	"strconv"
	"strings"
)

// ImageSize is a theme.json image_sizes entry: a bounding box (0 = unconstrained), or exact dimensions when Crop.
type ImageSize struct {
	Width  int  `json:"width"`
	Height int  `json:"height"`
	Crop   bool `json:"crop"`
}

// ImageSizes are the theme's image sizes and srcset widths. They decide which /m/ variants exist.
type ImageSizes struct {
	Sizes  map[string]ImageSize
	Widths []int
}

// Resizable reports whether imgproxy may process a file of this mime type. SVG is never processed.
func Resizable(mime string) bool {
	switch mime {
	case "image/jpeg", "image/png", "image/gif", "image/webp", "image/avif":
		return true
	}
	return false
}

// Variant names the /m/ variant for a size on m and its output dimensions. "full", "" and sizes the image
// is too small for give "orig". ok is false for an undeclared size.
func (s *ImageSizes) Variant(m MediaInfo, size string) (variant string, w, h int, ok bool) {
	if size == "" || size == "full" {
		return "orig", m.Width, m.Height, true
	}
	sz, ok := s.Sizes[size]
	if !ok {
		return "orig", m.Width, m.Height, false
	}
	if !Resizable(m.Mime) || m.Width <= 0 || m.Height <= 0 {
		return "orig", m.Width, m.Height, true
	}
	if sz.Crop {
		w, h = cropDims(m, sz)
		return "c" + strconv.Itoa(sz.Width) + "x" + strconv.Itoa(sz.Height), w, h, true
	}
	w, h = fitDims(m, sz)
	if w >= m.Width {
		return "orig", m.Width, m.Height, true
	}
	return "w" + strconv.Itoa(w), w, h, true
}

// fitDims scales m down to fit the box, never up.
func fitDims(m MediaInfo, sz ImageSize) (int, int) {
	f := 1.0
	if sz.Width > 0 {
		f = min(f, float64(sz.Width)/float64(m.Width))
	}
	if sz.Height > 0 {
		f = min(f, float64(sz.Height)/float64(m.Height))
	}
	return scaled(m.Width, m.Height, f)
}

// cropDims is the crop box, shrunk at the same aspect ratio when the original is smaller.
func cropDims(m MediaInfo, sz ImageSize) (int, int) {
	f := min(1, float64(m.Width)/float64(sz.Width), float64(m.Height)/float64(sz.Height))
	return max(1, int(math.Round(float64(sz.Width)*f))), max(1, int(math.Round(float64(sz.Height)*f)))
}

func scaled(w, h int, f float64) (int, int) {
	if f >= 1 {
		return w, h
	}
	return max(1, int(math.Round(float64(w)*f))), max(1, int(math.Round(float64(h)*f)))
}

// ScaledHeight is the height of m resized to width w.
func ScaledHeight(m MediaInfo, w int) int {
	_, h := scaled(m.Width, m.Height, float64(w)/float64(m.Width))
	return h
}

// Allowed reports whether variant may be requested for m. It is how /m/ refuses arbitrary sizes.
func (s *ImageSizes) Allowed(m MediaInfo, variant string) bool {
	if variant == "orig" {
		return true
	}
	if !Resizable(m.Mime) {
		return false
	}
	if w, h, crop, ok := ParseVariant(variant); ok && crop {
		for _, sz := range s.Sizes {
			if sz.Crop && sz.Width == w && sz.Height == h {
				return true
			}
		}
		return false
	} else if ok {
		if slices.Contains(s.Widths, w) {
			return true
		}
		if m.Width <= 0 || m.Height <= 0 {
			return false
		}
		for _, sz := range s.Sizes {
			if fw, _ := fitDims(m, sz); !sz.Crop && fw == w {
				return true
			}
		}
	}
	return false
}

// ParseVariant parses w{W} and c{W}x{H}. It rejects leading zeros so each variant has one spelling.
func ParseVariant(v string) (w, h int, crop, ok bool) {
	num := func(s string) (int, bool) {
		if s == "" || len(s) > 5 || s[0] == '0' {
			return 0, false
		}
		n, err := strconv.Atoi(s)
		return n, err == nil && n > 0
	}
	switch {
	case strings.HasPrefix(v, "w"):
		w, ok = num(v[1:])
		return w, 0, false, ok
	case strings.HasPrefix(v, "c"):
		ws, hs, found := strings.Cut(v[1:], "x")
		if !found {
			return 0, 0, false, false
		}
		var okW, okH bool
		w, okW = num(ws)
		h, okH = num(hs)
		return w, h, true, okW && okH
	}
	return 0, 0, false, false
}

// Srcset lists the width variants of m for an image shown as variant: srcset_widths below the original's
// width, the variant's own width and the original. Cropped variants and non-raster files get none.
func (s *ImageSizes) Srcset(m MediaInfo, variant string) string {
	if !Resizable(m.Mime) || m.Width <= 0 || m.Height <= 0 || strings.HasPrefix(variant, "c") {
		return ""
	}
	widths := []int{m.Width}
	for _, w := range s.Widths {
		if w < m.Width {
			widths = append(widths, w)
		}
	}
	if w, _, _, ok := ParseVariant(variant); ok {
		widths = append(widths, w)
	}
	slices.Sort(widths)
	widths = slices.Compact(widths)
	if len(widths) < 2 {
		return ""
	}
	parts := make([]string, len(widths))
	for i, w := range widths {
		v := "w" + strconv.Itoa(w)
		if w == m.Width {
			v = "orig"
		}
		parts[i] = MediaURL(m, v) + " " + strconv.Itoa(w) + "w"
	}
	return strings.Join(parts, ", ")
}

// SizesAttr is WordPress's default sizes attribute for an image rendered w pixels wide.
func SizesAttr(w int) string {
	if w <= 0 {
		return ""
	}
	n := strconv.Itoa(w)
	return "(max-width: " + n + "px) 100vw, " + n + "px"
}
