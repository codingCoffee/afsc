package media

import (
  "path"
  "strconv"
  "strings"
)

// formats maps an output extension to its Content-Type.
var formats = map[string]string{
  "jpg": "image/jpeg", "png": "image/png", "gif": "image/gif", "webp": "image/webp", "avif": "image/avif",
}

// extTypes is the fixed extension table for legacy paths; it doesn't depend on the host's mime database.
var extTypes = map[string]string{
  ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".jpe": "image/jpeg", ".png": "image/png", ".gif": "image/gif",
  ".webp": "image/webp", ".avif": "image/avif", ".svg": "image/svg+xml", ".pdf": "application/pdf",
  ".mp4": "video/mp4", ".m4v": "video/mp4", ".webm": "video/webm", ".mp3": "audio/mpeg",
}

// sourceExt is the imgproxy output extension that keeps a raster mime's format.
func sourceExt(mime string) string {
  switch mime {
  case "image/jpeg":
    return "jpg"
  case "image/png":
    return "png"
  case "image/gif":
    return "gif"
  case "image/webp":
    return "webp"
  case "image/avif":
    return "avif"
  }
  return ""
}

// extFormat is the imgproxy output extension for a legacy path's extension ("" if it isn't a raster format).
func extFormat(p string) string {
  ct := extTypes[strings.ToLower(path.Ext(p))]
  return sourceExt(ct)
}

// typeForPath is the Content-Type a legacy path must be served with, falling back to the stored mime.
func typeForPath(p, mime string) string {
  if ct, ok := extTypes[strings.ToLower(path.Ext(p))]; ok {
    return ct
  }
  return mime
}

// Negotiate picks the output format for a raster source: AVIF, then WebP, then the source format.
// GIFs only go to WebP so animation survives. A WebP or AVIF source the client can't take becomes PNG.
func Negotiate(accept, mime string) string {
  src := sourceExt(mime)
  if src != "gif" && accepts(accept, "image/avif") {
    return "avif"
  }
  if accepts(accept, "image/webp") {
    return "webp"
  }
  if src == "webp" || src == "avif" {
    return "png"
  }
  return src
}

// accepts reports whether the Accept header names typ explicitly with q > 0. Wildcards don't count:
// browsers send */* without being able to decode AVIF.
func accepts(header, typ string) bool {
  for _, part := range strings.Split(header, ",") {
    fields := strings.Split(part, ";")
    if !strings.EqualFold(strings.TrimSpace(fields[0]), typ) {
      continue
    }
    q := 1.0
    for _, f := range fields[1:] {
      k, v, _ := strings.Cut(strings.TrimSpace(f), "=")
      if strings.EqualFold(k, "q") {
        n, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
        if err != nil {
          n = 0
        }
        q = n
      }
    }
    return q > 0
  }
  return false
}
