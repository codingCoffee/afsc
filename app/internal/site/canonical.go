package site

import (
  "net/url"
  "regexp"
  "strings"
)

var multiSlash = regexp.MustCompile(`/{2,}`)

// Canonical returns the canonical form of an unescaped request path: single slashes, no index.php prefix,
// lowercase, and the trailing-slash policy ("always" leaves file-like paths such as /robots.txt alone).
func Canonical(path, trailing string) string {
  c := multiSlash.ReplaceAllString(path, "/")
  if c == "" || c == "/index.php" {
    c = "/"
  } else if strings.HasPrefix(c, "/index.php/") {
    c = strings.TrimPrefix(c, "/index.php")
  }
  c = strings.ToLower(c)
  last := c[strings.LastIndexByte(c, '/')+1:]
  switch trailing {
  case "always":
    if !strings.HasSuffix(c, "/") && !strings.Contains(last, ".") {
      c += "/"
    }
  case "never":
    if len(c) > 1 {
      c = strings.TrimSuffix(c, "/")
    }
  }
  return c
}

// location escapes an unescaped path for a Location header, keeping the raw query.
func location(path, rawQuery string) string {
  return (&url.URL{Path: path, RawQuery: rawQuery}).String()
}
